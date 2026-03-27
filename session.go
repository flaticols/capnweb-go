package capnweb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
)

// Session manages a single capnweb RPC connection. It is fully bidirectional —
// either side can call methods on objects exported by the other.
type Session struct {
	transport Transport
	imports   *ImportTable
	exports   *ExportTable

	mu             sync.Mutex
	pending        map[int64]*pendingCall
	pulled         map[int64]bool // export IDs that have been pulled
	remoteImportID int64          // mirrors the remote's import ID allocation
	err            error

	// sendMu serializes outbound push/stream messages so that the implicit
	// import ID assignment (based on send order) stays in sync. Allocate must
	// happen inside this lock.
	sendMu sync.Mutex

	ctxMu  sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type pendingCall struct {
	future *Future
}

// NewSession creates a session. The main object is exported at ID 0 (the
// bootstrap interface). Pass nil if this endpoint has no bootstrap.
func NewSession(transport Transport, main any) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		transport: transport,
		imports:   NewImportTable(),
		exports:   NewExportTable(main),
		pending:   make(map[int64]*pendingCall),
		pulled:    make(map[int64]bool),
		done:      make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Run starts the message processing loop. It blocks until the context is
// cancelled, the transport closes, or an abort is received.
func (s *Session) Run(ctx context.Context) error {
	s.ctxMu.Lock()
	s.cancel() // cancel the background context from NewSession
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.ctxMu.Unlock()

	defer func() {
		s.cancel()
		close(s.done)
	}()

	for {
		msg, err := s.transport.Recv(s.getCtx())
		if err != nil {
			s.terminateAll(fmt.Errorf("capnweb: transport: %w", err))
			return err
		}
		if err := s.handleMessage(msg); err != nil {
			s.terminateAll(err)
			return err
		}
	}
}

// Call sends a method call to a remote object and blocks until the result
// is available. targetID is the import ID of the remote object (0 for the
// bootstrap interface).
func (s *Session) Call(ctx context.Context, targetID int64, method string, args ...any) (any, error) {
	// Build the expression: ["import", targetID, method, [args...]]
	expr, err := buildCallExpr(targetID, method, args)
	if err != nil {
		return nil, err
	}

	// Allocate + send must be atomic: the remote assigns implicit import IDs
	// in push-receive order, so our allocate order must equal our send order.
	f := NewFuture()

	s.sendMu.Lock()
	entry := s.imports.Allocate()
	s.mu.Lock()
	s.pending[entry.ID] = &pendingCall{future: f}
	s.mu.Unlock()
	pushErr := s.transport.Send(ctx, PushMsg{Expr: expr})
	pullErr := s.transport.Send(ctx, PullMsg{ImportID: entry.ID})
	s.sendMu.Unlock()

	if pushErr != nil {
		return nil, fmt.Errorf("capnweb: send push: %w", pushErr)
	}
	if pullErr != nil {
		return nil, fmt.Errorf("capnweb: send pull: %w", pullErr)
	}

	// Wait for resolution.
	val, err := f.Await(ctx)
	if err != nil {
		return nil, err
	}

	// Send release.
	_ = s.transport.Send(ctx, ReleaseMsg{ImportID: entry.ID, RefCount: entry.RefCount})
	s.imports.Remove(entry.ID)

	return val, nil
}

// Abort sends an abort message and terminates the session.
func (s *Session) Abort(reason error) error {
	msg := reason.Error()
	expr, _ := json.Marshal([]any{"error", "Error", msg})
	s.ctxMu.RLock()
	ctx := s.ctx
	cancel := s.cancel
	s.ctxMu.RUnlock()
	err := s.transport.Send(ctx, AbortMsg{Expr: expr})
	s.terminateAll(reason)
	cancel()
	return err
}

// Close gracefully shuts down the session.
func (s *Session) Close() error {
	s.terminateAll(fmt.Errorf("capnweb: session closed"))
	s.ctxMu.RLock()
	cancel := s.cancel
	s.ctxMu.RUnlock()
	cancel()
	return s.transport.Close()
}

func (s *Session) getCtx() context.Context {
	s.ctxMu.RLock()
	defer s.ctxMu.RUnlock()
	return s.ctx
}

// Done returns a channel that is closed when the session ends.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// Err returns the terminal error, if any.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// --- message handling ---

func (s *Session) handleMessage(msg Message) error {
	switch m := msg.(type) {
	case PushMsg:
		return s.handlePush(m)
	case PullMsg:
		return s.handlePull(m)
	case ResolveMsg:
		return s.handleResolve(m)
	case RejectMsg:
		return s.handleReject(m)
	case ReleaseMsg:
		return s.handleRelease(m)
	case StreamMsg:
		return s.handleStream(m)
	case PipeMsg:
		// TODO: implement pipe handling (#11)
		return nil
	case AbortMsg:
		return s.handleAbort(m)
	default:
		return fmt.Errorf("capnweb: unknown message type %T", msg)
	}
}

func (s *Session) handlePush(m PushMsg) error {
	exportID := s.nextRemoteImportID()
	result, err := s.evaluateAndCall(m.Expr)

	s.mu.Lock()
	pulled := s.pulled[exportID]
	s.mu.Unlock()

	if pulled {
		return s.sendResult(exportID, result, err)
	}

	// Not yet pulled — store for when pull arrives.
	s.exports.ExportWithID(exportID, &deferredResult{val: result, err: err})
	return nil
}

func (s *Session) handlePull(m PullMsg) error {
	exportID := m.ImportID

	s.mu.Lock()
	s.pulled[exportID] = true
	s.mu.Unlock()

	e := s.exports.Get(exportID)
	if e == nil {
		return nil
	}
	if dr, ok := e.Target.(*deferredResult); ok {
		return s.sendResult(exportID, dr.val, dr.err)
	}
	return nil
}

func (s *Session) handleResolve(m ResolveMsg) error {
	s.mu.Lock()
	pc, ok := s.pending[m.ExportID]
	s.mu.Unlock()
	if !ok {
		return nil
	}

	expr, err := DecodeExpr(m.Expr)
	if err != nil {
		pc.future.Reject(fmt.Errorf("capnweb: decode resolve: %w", err))
		return nil
	}
	pc.future.Resolve(s.exprToValue(expr))
	return nil
}

func (s *Session) handleReject(m RejectMsg) error {
	s.mu.Lock()
	pc, ok := s.pending[m.ExportID]
	s.mu.Unlock()
	if !ok {
		return nil
	}

	expr, err := DecodeExpr(m.Expr)
	if err != nil {
		pc.future.Reject(fmt.Errorf("capnweb: decode reject: %w", err))
		return nil
	}
	if errExpr, ok := expr.(ErrorExpr); ok {
		pc.future.Reject(&errExpr)
	} else {
		pc.future.Reject(fmt.Errorf("capnweb: rejected: %v", s.exprToValue(expr)))
	}
	return nil
}

func (s *Session) handleRelease(m ReleaseMsg) error {
	s.exports.HandleRelease(m.ImportID, m.RefCount)
	return nil
}

func (s *Session) handleStream(m StreamMsg) error {
	exportID := s.nextRemoteImportID()
	result, err := s.evaluateAndCall(m.Expr)
	return s.sendResult(exportID, result, err)
}

func (s *Session) handleAbort(m AbortMsg) error {
	expr, err := DecodeExpr(m.Expr)
	if err != nil {
		return fmt.Errorf("capnweb: abort: %w", err)
	}
	if errExpr, ok := expr.(ErrorExpr); ok {
		return &errExpr
	}
	return fmt.Errorf("capnweb: remote abort: %v", s.exprToValue(expr))
}

// --- internal helpers ---

func (s *Session) sendResult(exportID int64, val any, err error) error {
	if err != nil {
		errExpr, _ := json.Marshal([]any{"error", "Error", err.Error()})
		return s.transport.Send(s.getCtx(), RejectMsg{ExportID: exportID, Expr: errExpr})
	}
	encoded, marshalErr := json.Marshal(val)
	if marshalErr != nil {
		errExpr, _ := json.Marshal([]any{"error", "Error", marshalErr.Error()})
		return s.transport.Send(s.getCtx(), RejectMsg{ExportID: exportID, Expr: errExpr})
	}
	return s.transport.Send(s.getCtx(), ResolveMsg{ExportID: exportID, Expr: encoded})
}

func (s *Session) evaluateAndCall(raw json.RawMessage) (any, error) {
	expr, err := DecodeExpr(raw)
	if err != nil {
		return nil, fmt.Errorf("capnweb: decode: %w", err)
	}
	switch e := expr.(type) {
	case ImportExpr:
		return s.dispatchCall(e.ImportID, e.Path, e.Args)
	case PipelineExpr:
		return s.dispatchCall(e.ImportID, e.Path, e.Args)
	default:
		return s.exprToValue(expr), nil
	}
}

func (s *Session) dispatchCall(exportID int64, path []string, args []Expr) (any, error) {
	entry := s.exports.Get(exportID)
	if entry == nil {
		return nil, fmt.Errorf("capnweb: unknown export ID %d", exportID)
	}

	target := entry.Target
	if len(path) == 0 {
		return target, nil
	}

	methodName := path[len(path)-1]
	v := reflect.ValueOf(target)
	m := v.MethodByName(methodName)
	if !m.IsValid() {
		m = v.MethodByName(capitalize(methodName))
	}
	if !m.IsValid() {
		return nil, fmt.Errorf("capnweb: method %q not found on %T", methodName, target)
	}

	mt := m.Type()
	callArgs := make([]reflect.Value, 0, mt.NumIn())

	paramIdx := 0
	if mt.NumIn() > 0 && mt.In(0).Implements(reflect.TypeFor[context.Context]()) {
		callArgs = append(callArgs, reflect.ValueOf(s.getCtx()))
		paramIdx = 1
	}

	if args == nil {
		args = []Expr{}
	}
	for i, arg := range args {
		goVal := s.exprToValue(arg)
		idx := paramIdx + i
		switch {
		case idx < mt.NumIn():
			callArgs = append(callArgs, coerceArg(goVal, mt.In(idx)))
		case mt.IsVariadic():
			callArgs = append(callArgs, coerceArg(goVal, mt.In(mt.NumIn()-1).Elem()))
		default:
			callArgs = append(callArgs, reflect.ValueOf(goVal))
		}
	}

	results := m.Call(callArgs)
	return interpretResults(results)
}

func (s *Session) exprToValue(e Expr) any {
	switch v := e.(type) {
	case LiteralExpr:
		return v.Value
	case UndefinedExpr:
		return nil
	case InfExpr:
		return math.Inf(1)
	case NegInfExpr:
		return math.Inf(-1)
	case NaNExpr:
		return math.NaN()
	case BytesExpr:
		return v.Data
	case BigIntExpr:
		return v.Value
	case DateExpr:
		return v.Time
	case ErrorExpr:
		return &v
	case HeadersExpr:
		return v.Header
	case ExportExpr:
		entry := s.imports.Insert(v.ExportID)
		return entry
	case ArrayExpr:
		out := make([]any, len(v.Elements))
		for i, el := range v.Elements {
			out[i] = s.exprToValue(el)
		}
		return out
	default:
		return nil
	}
}

func (s *Session) terminateAll(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	for _, pc := range s.pending {
		pc.future.Reject(err)
	}
}

func (s *Session) nextRemoteImportID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remoteImportID++
	return s.remoteImportID
}

type deferredResult struct {
	val any
	err error
}

func buildCallExpr(targetID int64, method string, args []any) (json.RawMessage, error) {
	if method != "" {
		return json.Marshal([]any{"import", targetID, method, args})
	}
	return json.Marshal([]any{"import", targetID})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

func coerceArg(val any, target reflect.Type) reflect.Value {
	if val == nil {
		return reflect.Zero(target)
	}
	v := reflect.ValueOf(val)
	if v.Type().AssignableTo(target) {
		return v
	}
	if v.Type().ConvertibleTo(target) {
		return v.Convert(target)
	}
	return v
}

func interpretResults(results []reflect.Value) (any, error) {
	switch len(results) {
	case 0:
		return nil, nil
	case 1:
		v := results[0]
		if v.Type().Implements(reflect.TypeFor[error]()) {
			if v.IsNil() {
				return nil, nil
			}
			return nil, v.Interface().(error)
		}
		return v.Interface(), nil
	default:
		last := results[len(results)-1]
		if last.Type().Implements(reflect.TypeFor[error]()) {
			var err error
			if !last.IsNil() {
				err = last.Interface().(error)
			}
			if len(results) == 2 {
				return results[0].Interface(), err
			}
			vals := make([]any, len(results)-1)
			for i := range len(results) - 1 {
				vals[i] = results[i].Interface()
			}
			return vals, err
		}
		vals := make([]any, len(results))
		for i, r := range results {
			vals[i] = r.Interface()
		}
		return vals, nil
	}
}
