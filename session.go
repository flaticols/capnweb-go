package capnweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"
)

// Session manages a single capnweb RPC connection. It is fully bidirectional —
// either side can call methods on objects exported by the other.
type Session struct {
	transport Transport
	imports   *ImportTable
	exports   *ExportTable

	mu             sync.Mutex
	pending        map[int64]*pendingCall
	remoteImportID int64 // mirrors the remote's import ID allocation
	err            error
	wg             sync.WaitGroup // tracks in-flight push goroutines

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
		done:      make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Main returns a Stub for the remote's bootstrap (main) interface.
// This is the entry point for calling methods on the remote endpoint.
func (s *Session) Main() *Stub {
	return newStub(s, 0)
}

// wrapImportEntry converts *ImportEntry values to *Stub. If val is not
// an *ImportEntry, it is returned unchanged.
func (s *Session) wrapImportEntry(val any) any {
	if entry, ok := val.(*ImportEntry); ok {
		return newStub(s, entry.ID)
	}
	if slice, ok := val.([]any); ok {
		for i, elem := range slice {
			if entry, ok := elem.(*ImportEntry); ok {
				slice[i] = newStub(s, entry.ID)
			}
		}
	}
	return val
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
			// Wait for in-flight goroutines before terminating
			// (e.g., batch transport returns EOF after all messages).
			s.wg.Wait()
			s.terminateAll(fmt.Errorf("capnweb: transport: %w", err))
			return err
		}
		if err := s.handleMessage(msg); err != nil {
			s.wg.Wait()
			s.terminateAll(err)
			return err
		}
	}
}

// Call sends a method call to a remote object and blocks until the result
// is available. targetID is the import ID of the remote object (0 for the
// bootstrap interface).
func (s *Session) Call(ctx context.Context, targetID int64, method string, args ...any) (any, error) {
	return s.callWithTag(ctx, "import", targetID, method, args...)
}

func (s *Session) callWithTag(ctx context.Context, tag string, targetID int64, method string, args ...any) (any, error) {
	expr, err := s.buildCallExpr(tag, targetID, method, args)
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

// push sends a push message without pull. Returns the allocated import ID.
// Used for promise pipelining where the result is consumed by a subsequent call.
func (s *Session) push(ctx context.Context, tag string, targetID int64, method string, args []any) (int64, error) {
	expr, err := s.buildCallExpr(tag, targetID, method, args)
	if err != nil {
		return 0, err
	}

	s.sendMu.Lock()
	entry := s.imports.Allocate()
	pushErr := s.transport.Send(ctx, PushMsg{Expr: expr})
	s.sendMu.Unlock()

	if pushErr != nil {
		return 0, fmt.Errorf("capnweb: send push: %w", pushErr)
	}
	return entry.ID, nil
}

// Abort sends an abort message and terminates the session.
func (s *Session) Abort(reason error) error {
	expr, _ := EncodeExpr(errorToExpr(reason))
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
		return s.handlePipe(m)
	case AbortMsg:
		return s.handleAbort(m)
	default:
		return fmt.Errorf("capnweb: unknown message type %T", msg)
	}
}

func (s *Session) handlePush(m PushMsg) error {
	exportID := s.nextRemoteImportID()
	dr := &deferredResult{future: NewFuture()}
	s.exports.ExportWithID(exportID, dr)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		result, err := s.evaluateAndCall(m.Expr)
		if err != nil {
			dr.future.Reject(err)
		} else {
			dr.future.Resolve(result)
		}
	}()
	return nil
}

func (s *Session) handlePull(m PullMsg) error {
	exportID := m.ImportID

	e := s.exports.Get(exportID)
	if e == nil {
		return nil
	}
	if dr, ok := e.Target.(*deferredResult); ok {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			val, err := dr.future.Await(s.getCtx())
			_ = s.sendResult(exportID, val, err)
		}()
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

func (s *Session) handlePipe(_ PipeMsg) error {
	exportID := s.nextRemoteImportID()
	p := newPipe()
	s.exports.ExportWithID(exportID, p)
	return nil
}

// CreatePipe sends a ["pipe"] message to the remote, creating a pipe.
// Returns a StreamWriter for sending chunks and a ReadableExpr that can
// be passed as a method argument so the remote can read from the pipe.
func (s *Session) CreatePipe(ctx context.Context) (*StreamWriter, Expr, error) {
	s.sendMu.Lock()
	entry := s.imports.Allocate()
	pipeErr := s.transport.Send(ctx, PipeMsg{})
	s.sendMu.Unlock()

	if pipeErr != nil {
		return nil, nil, fmt.Errorf("capnweb: send pipe: %w", pipeErr)
	}

	writer := &StreamWriter{session: s, importID: entry.ID}
	readable := ReadableExpr{ImportID: entry.ID}
	return writer, readable, nil
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
		encoded, _ := EncodeExpr(errorToExpr(err))
		return s.transport.Send(s.getCtx(), RejectMsg{ExportID: exportID, Expr: encoded})
	}
	expr, convErr := s.valueToExpr(val)
	if convErr != nil {
		encoded, _ := EncodeExpr(errorToExpr(convErr))
		return s.transport.Send(s.getCtx(), RejectMsg{ExportID: exportID, Expr: encoded})
	}
	encoded, encErr := EncodeExpr(expr)
	if encErr != nil {
		encoded, _ = EncodeExpr(errorToExpr(encErr))
		return s.transport.Send(s.getCtx(), RejectMsg{ExportID: exportID, Expr: encoded})
	}
	return s.transport.Send(s.getCtx(), ResolveMsg{ExportID: exportID, Expr: encoded})
}

// errorToExpr converts a Go error to an ErrorExpr. If the error is already
// an *ErrorExpr (via errors.As), the type and stack are preserved. Otherwise
// it is wrapped as a generic "Error".
func errorToExpr(err error) ErrorExpr {
	var errExpr *ErrorExpr
	if errors.As(err, &errExpr) {
		return *errExpr
	}
	return ErrorExpr{Type: "Error", Message: err.Error()}
}

// Release sends a release message for the given import ID and removes it
// from the import table. Used when the caller is done with a remote object
// obtained via pass-by-reference.
func (s *Session) Release(ctx context.Context, importID, refCount int64) error {
	err := s.transport.Send(ctx, ReleaseMsg{ImportID: importID, RefCount: refCount})
	s.imports.Remove(importID)
	return err
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
	case RemapExpr:
		return s.evaluateRemap(&e)
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
	// Unwrap deferred results from prior pipeline stages.
	if dr, ok := target.(*deferredResult); ok {
		val, err := dr.future.Await(s.getCtx())
		if err != nil {
			return nil, err
		}
		target = val
	}
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
		return nil, NewTypeError(fmt.Sprintf("method %q not found on %T", methodName, target))
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

// valueToExpr converts a Go value to an Expr for wire encoding.
// RpcTarget values are exported as ["export", id]; plain values are
// wrapped as LiteralExpr.
func (s *Session) valueToExpr(val any) (Expr, error) {
	if val == nil {
		return LiteralExpr{Value: nil}, nil
	}
	switch b := val.(type) {
	case Expr:
		// Already an expression (e.g. a method returned a DateExpr/BytesExpr) —
		// pass it through rather than double-wrapping it in a LiteralExpr.
		return b, nil
	case *Blob:
		return s.blobToExpr(b)
	case Blob:
		return s.blobToExpr(&b)
	case time.Time:
		return DateExpr{Time: b}, nil
	}
	if _, ok := val.(RpcTarget); ok {
		entry := s.exports.Export(val)
		return ExportExpr{ExportID: entry.ID}, nil
	}
	// []byte is a scalar (base64), not a slice to recurse into.
	if _, ok := val.([]byte); ok {
		return LiteralExpr{Value: val}, nil
	}
	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		// Every literal array is recursively devalued and escaped as [[...]].
		elems := make([]Expr, rv.Len())
		for i := range rv.Len() {
			e, err := s.valueToExpr(rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			elems[i] = e
		}
		return ArrayExpr{Elements: elems}, nil
	case reflect.Map:
		// Object property values are themselves expressions.
		if rv.Type().Key().Kind() != reflect.String {
			break // non-string keys can't form a JSON object; fall through
		}
		fields := make(map[string]Expr, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			e, err := s.valueToExpr(iter.Value().Interface())
			if err != nil {
				return nil, err
			}
			fields[iter.Key().String()] = e
		}
		return ObjectExpr{Fields: fields}, nil
	}
	return LiteralExpr{Value: val}, nil
}

// blobToExpr streams a Blob's bytes through a new pipe and returns the
// ["blob", type, ["readable", id]] expression referencing it. Mirrors the JS
// reference: even small blobs are streamed because the readable is the only
// way to carry the bytes within the synchronously-serialized message.
func (s *Session) blobToExpr(b *Blob) (Expr, error) {
	// Blobs stream their bytes through a pipe, which needs the live ack
	// back-channel of a streaming transport. Over a one-shot batch transport
	// the write would block forever, so fail fast with a clear error instead.
	if _, ok := s.transport.(interface{ nonStreaming() }); ok {
		return nil, fmt.Errorf("capnweb: Blob requires a streaming transport (e.g. WebSocket), not HTTP batch")
	}
	ctx := s.getCtx()
	writer, readable, err := s.CreatePipe(ctx)
	if err != nil {
		return nil, fmt.Errorf("capnweb: blob pipe: %w", err)
	}
	data := b.data
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if len(data) > 0 {
			_ = writer.Write(ctx, BytesExpr{Data: data})
		}
		_ = writer.Close(ctx)
	}()
	return BlobExpr{Type: b.Type, Body: readable}, nil
}

// blobFromExpr builds a lazily-read Blob from a BlobExpr, backed by the
// readable end of the pipe carrying its bytes.
func (s *Session) blobFromExpr(v BlobExpr) *Blob {
	blob := &Blob{Type: v.Type}
	if r, ok := s.exprToValue(v.Body).(*StreamReader); ok {
		blob.reader = r
	}
	return blob
}

// readerFromExpr resolves a ReadableExpr to the StreamReader for its pipe.
func (s *Session) readerFromExpr(v ReadableExpr) any {
	entry := s.exports.Get(v.ImportID)
	if entry == nil {
		return nil
	}
	if p, ok := entry.Target.(*pipe); ok {
		return &StreamReader{pipe: p}
	}
	return nil
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
	case BlobExpr:
		return s.blobFromExpr(v)
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
	case ReadableExpr:
		return s.readerFromExpr(v)
	case WritableExpr:
		return &StreamWriter{session: s, importID: v.ExportID}
	case ArrayExpr:
		return s.arrayToValue(v)
	case ObjectExpr:
		return s.objectToValue(v)
	default:
		return nil
	}
}

func (s *Session) arrayToValue(v ArrayExpr) []any {
	out := make([]any, len(v.Elements))
	for i, el := range v.Elements {
		out[i] = s.exprToValue(el)
	}
	return out
}

func (s *Session) objectToValue(v ObjectExpr) map[string]any {
	out := make(map[string]any, len(v.Fields))
	for k, el := range v.Fields {
		out[k] = s.exprToValue(el)
	}
	return out
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

// evaluateRemap evaluates a remap expression — server-side .map() over a
// collection. Uses a scoped import table where negative IDs reference
// captures, 0 is the current element, and positive IDs are previous
// instruction results.
func (s *Session) evaluateRemap(r *RemapExpr) (any, error) {
	// Resolve the collection.
	collection, err := s.dispatchCall(r.ImportID, r.Path, nil)
	if err != nil {
		return nil, err
	}

	items, ok := collection.([]any)
	if !ok {
		return nil, NewTypeError(fmt.Sprintf("remap: expected array, got %T", collection))
	}

	// Resolve captures.
	captures := make([]any, len(r.Captures))
	for i, cap := range r.Captures {
		captures[i] = s.resolveCapture(cap)
	}

	// Map over elements.
	results := make([]any, len(items))
	for i, element := range items {
		val, err := s.evaluateRemapElement(element, captures, r.Instructions)
		if err != nil {
			return nil, fmt.Errorf("remap element %d: %w", i, err)
		}
		results[i] = val
	}
	return results, nil
}

func (s *Session) evaluateRemapElement(element any, captures []any, instructions []Expr) (any, error) {
	// Internal import table: negative → captures, 0 → element, positive → results
	instrResults := make([]any, 0, len(instructions))

	for _, instr := range instructions {
		val, err := s.evaluateRemapInstr(instr, element, captures, instrResults)
		if err != nil {
			return nil, err
		}
		instrResults = append(instrResults, val)
	}

	if len(instrResults) == 0 {
		return element, nil
	}
	return instrResults[len(instrResults)-1], nil
}

// resolveCapture resolves a remap capture reference. Per the spec, a capture is
// ["import", id] — an entry on our own exports table (the peer is importing it)
// — or ["export", id] — a new object the peer exported, which we import as a
// stub. Anything else is treated as a plain value.
func (s *Session) resolveCapture(capture Expr) any {
	switch c := capture.(type) {
	case ImportExpr:
		if entry := s.exports.Get(c.ImportID); entry != nil {
			return entry.Target
		}
		return nil
	case ExportExpr:
		entry := s.imports.Insert(c.ExportID)
		return newStub(s, entry.ID)
	default:
		return s.exprToValue(capture)
	}
}

func (s *Session) evaluateRemapInstr(instr Expr, element any, captures, results []any) (any, error) {
	switch e := instr.(type) {
	case ImportExpr:
		target := s.remapResolveID(e.ImportID, element, captures, results)
		return s.remapEvalRef(target, e.Path, e.Args, element, captures, results)
	case PipelineExpr:
		target := s.remapResolveID(e.ImportID, element, captures, results)
		return s.remapEvalRef(target, e.Path, e.Args, element, captures, results)
	case ArrayExpr:
		// Array literal instruction — resolve each element in the remap scope
		// so nested pipeline/import refs are substituted, not left as raw refs.
		out := make([]any, len(e.Elements))
		for i, el := range e.Elements {
			v, err := s.evaluateRemapInstr(el, element, captures, results)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case ObjectExpr:
		// Object literal instruction — same recursive resolution per field.
		out := make(map[string]any, len(e.Fields))
		for k, el := range e.Fields {
			v, err := s.evaluateRemapInstr(el, element, captures, results)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	default:
		return s.exprToValue(instr), nil
	}
}

func (s *Session) remapEvalRef(target any, path []string, args []Expr, element any, captures, results []any) (any, error) {
	if len(path) == 0 && args == nil {
		return target, nil
	}
	if len(path) > 0 && args == nil {
		return accessPath(target, path), nil
	}
	resolvedArgs := make([]Expr, len(args))
	for i, arg := range args {
		val, err := s.evaluateRemapInstr(arg, element, captures, results)
		if err != nil {
			return nil, err
		}
		resolvedArgs[i] = LiteralExpr{Value: val}
	}
	return s.dispatchCallOnValue(target, path, resolvedArgs)
}

func (s *Session) remapResolveID(id int64, element any, captures, results []any) any {
	switch {
	case id == 0:
		return element
	case id < 0:
		idx := int(-id - 1)
		if idx < len(captures) {
			return captures[idx]
		}
		return nil
	default:
		idx := int(id - 1)
		if idx < len(results) {
			return results[idx]
		}
		return nil
	}
}

func accessPath(val any, path []string) any {
	for _, key := range path {
		m, ok := val.(map[string]any)
		if !ok {
			return nil
		}
		val = m[key]
	}
	return val
}

// dispatchCallOnValue calls a method on a Go value directly (without export
// table lookup). Used by remap to call methods on resolved capture values.
func (s *Session) dispatchCallOnValue(target any, path []string, args []Expr) (any, error) {
	if target == nil {
		return nil, NewTypeError("remap: nil target")
	}

	// If target is an ImportEntry (from a capture), look up the export.
	if entry, ok := target.(*ImportEntry); ok {
		return s.dispatchCall(entry.ID, path, args)
	}

	// If target is an ExportEntry, use its target.
	if entry, ok := target.(*ExportEntry); ok {
		target = entry.Target
	}

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
		return nil, NewTypeError(fmt.Sprintf("remap: method %q not found on %T", methodName, target))
	}

	mt := m.Type()
	callArgs := make([]reflect.Value, 0, mt.NumIn())

	paramIdx := 0
	if mt.NumIn() > 0 && mt.In(0).Implements(reflect.TypeFor[context.Context]()) {
		callArgs = append(callArgs, reflect.ValueOf(s.getCtx()))
		paramIdx = 1
	}

	for i, arg := range args {
		goVal := s.exprToValue(arg)
		idx := paramIdx + i
		if idx < mt.NumIn() {
			callArgs = append(callArgs, coerceArg(goVal, mt.In(idx)))
		} else {
			callArgs = append(callArgs, reflect.ValueOf(goVal))
		}
	}

	results := m.Call(callArgs)
	return interpretResults(results)
}

type deferredResult struct {
	future *Future
}

func (s *Session) buildCallExpr(tag string, targetID int64, method string, args []any) (json.RawMessage, error) {
	if method != "" {
		encodedArgs, err := s.encodeArgs(args)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{tag, targetID, []string{method}, encodedArgs})
	}
	return json.Marshal([]any{tag, targetID})
}

// encodeArgs encodes call arguments. Each argument is devalued through
// valueToExpr (so arrays are escaped, objects recurse, and typed values like
// bytes/dates/stubs are encoded as expressions) and then serialized.
func (s *Session) encodeArgs(args []any) ([]json.RawMessage, error) {
	if len(args) == 0 {
		return []json.RawMessage{}, nil
	}
	out := make([]json.RawMessage, len(args))
	for i, arg := range args {
		expr, err := s.valueToExpr(arg)
		if err != nil {
			return nil, err
		}
		encoded, err := EncodeExpr(expr)
		if err != nil {
			return nil, err
		}
		out[i] = encoded
	}
	return out, nil
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
