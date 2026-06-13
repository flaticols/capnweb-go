package capnweb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// chanTransport is a simple in-memory Transport for testing.
type chanTransport struct {
	send chan Message
	recv chan Message
	done chan struct{}
	once sync.Once
}

func newChanTransportPair() (*chanTransport, *chanTransport) {
	ab := make(chan Message, 64)
	ba := make(chan Message, 64)
	a := &chanTransport{send: ab, recv: ba, done: make(chan struct{})}
	b := &chanTransport{send: ba, recv: ab, done: make(chan struct{})}
	return a, b
}

func (t *chanTransport) Send(ctx context.Context, msg Message) error {
	select {
	case t.send <- msg:
		return nil
	case <-t.done:
		return errors.New("transport closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *chanTransport) Recv(ctx context.Context) (Message, error) {
	select {
	case msg := <-t.recv:
		return msg, nil
	case <-t.done:
		return nil, errors.New("transport closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *chanTransport) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}

// testService is a bootstrap object for testing.
type testService struct{}

func (s *testService) Echo(_ context.Context, val any) (any, error) {
	return val, nil
}

func (s *testService) Add(_ context.Context, a, b float64) (float64, error) {
	return a + b, nil
}

func (s *testService) Greet(_ context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}

func (s *testService) Fail(_ context.Context) (any, error) {
	return nil, errors.New("intentional error")
}

func (s *testService) FailTyped(_ context.Context) (any, error) {
	return nil, NewTypeError("bad argument")
}

// childService is an RpcTarget returned by reference.
type childService struct {
	RpcTargetBase
}

func (c *childService) ChildMethod(_ context.Context) (string, error) {
	return "from child", nil
}

// parentService extends testService with a method that returns an RpcTarget.
type parentService struct {
	testService
}

func (s *parentService) GetChild(_ context.Context) (*childService, error) {
	return &childService{}, nil
}

// remapService provides a collection and a transformation method for remap tests.
type remapService struct {
	testService
}

func (s *remapService) GetPeople(_ context.Context) ([]any, error) {
	return []any{
		map[string]any{"name": "Alice"},
		map[string]any{"name": "Bob"},
	}, nil
}

func TestSessionCallResolve(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	result, err := client.Call(ctx, 0, "Greet", "World")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != "Hello, World!" {
		t.Fatalf("result = %v; want Hello, World!", result)
	}
}

func TestSessionCallAdd(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	result, err := client.Call(ctx, 0, "Add", 3.0, 4.0)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != 7.0 {
		t.Fatalf("result = %v; want 7", result)
	}
}

func TestSessionCallReject(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	_, err := client.Call(ctx, 0, "Fail")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "intentional error") {
		t.Fatalf("error = %v; want 'intentional error'", err)
	}
}

func TestSessionCallMethodNotFound(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	_, err := client.Call(ctx, 0, "NonExistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v; want 'not found'", err)
	}
}

func TestSessionMultipleConcurrentCalls(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([]any, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = client.Call(ctx, 0, "Add", float64(idx), 1.0)
		}(i)
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		want := float64(i) + 1.0
		if results[i] != want {
			t.Fatalf("call %d: result = %v; want %v", i, results[i], want)
		}
	}
}

func TestSessionAbort(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	// Wait for sessions to start.
	time.Sleep(10 * time.Millisecond)

	_ = server.Abort(errors.New("fatal error"))

	// Client's Run should exit due to the abort message.
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("client session did not terminate after abort")
	}
}

func TestSessionContextCancel(t *testing.T) {
	_, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx) }()

	// Give Run a moment to start.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-errCh:
		// Good — Run returned.
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestSessionStream(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()

	// Send a stream message directly (auto-pulled, auto-released).
	expr, _ := json.Marshal([]any{"import", 0, "Echo", []any{42}})
	_ = clientTr.Send(ctx, StreamMsg{Expr: expr})

	// Server should send a resolve back.
	msg, err := clientTr.Recv(ctx)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	resolve, ok := msg.(ResolveMsg)
	if !ok {
		t.Fatalf("expected ResolveMsg, got %T", msg)
	}
	var val any
	json.Unmarshal(resolve.Expr, &val)
	if val != 42.0 {
		t.Fatalf("result = %v; want 42", val)
	}
}

func TestSessionGetChildRpcTarget(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &parentService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	// Call GetChild — should return a *Stub (pass-by-reference).
	child, err := Call[*Stub](ctx, main, "GetChild")
	if err != nil {
		t.Fatalf("GetChild: %v", err)
	}
	if child.ID() >= 0 {
		t.Fatalf("expected negative import ID, got %d", child.ID())
	}

	// Call ChildMethod on the returned child.
	childResult, err := Call[string](ctx, child, "ChildMethod")
	if err != nil {
		t.Fatalf("ChildMethod: %v", err)
	}
	if childResult != "from child" {
		t.Fatalf("ChildMethod = %v; want 'from child'", childResult)
	}

	// Release the child.
	if err := child.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestSessionErrorTypePreservation(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	_, err := client.Call(ctx, 0, "FailTyped")
	if err == nil {
		t.Fatal("expected error")
	}

	var errExpr *ErrorExpr
	if !errors.As(err, &errExpr) {
		t.Fatalf("errors.As failed: got %T", err)
	}
	if errExpr.Type != "TypeError" {
		t.Fatalf("Type = %q; want TypeError", errExpr.Type)
	}
	if errExpr.Message != "bad argument" {
		t.Fatalf("Message = %q; want 'bad argument'", errExpr.Message)
	}
}

func TestSessionMethodNotFoundIsTypeError(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	_, err := client.Call(ctx, 0, "NonExistent")
	if err == nil {
		t.Fatal("expected error")
	}

	var errExpr *ErrorExpr
	if !errors.As(err, &errExpr) {
		t.Fatalf("errors.As failed: got %T", err)
	}
	if errExpr.Type != "TypeError" {
		t.Fatalf("Type = %q; want TypeError", errExpr.Type)
	}
}

func TestPipelineTwoStage(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &parentService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	// Pipeline: GetChild → ChildMethod without waiting for GetChild.
	child, err := main.Pipeline(ctx, "GetChild")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	defer child.Release(ctx)

	result, err := Call[string](ctx, child, "ChildMethod")
	if err != nil {
		t.Fatalf("ChildMethod: %v", err)
	}
	if result != "from child" {
		t.Fatalf("result = %v; want 'from child'", result)
	}
}

func TestPipelineErrorPropagation(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	// Pipeline on a method that fails — the second stage should get the error.
	failing, err := main.Pipeline(ctx, "Fail")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	defer failing.Release(ctx)

	_, err = failing.Call(ctx, "AnyMethod")
	if err == nil {
		t.Fatal("expected error from pipeline on failed stage")
	}
	if !strings.Contains(err.Error(), "intentional error") {
		t.Fatalf("error = %v; want 'intentional error'", err)
	}
}

func TestRemapPropertyExtraction(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &remapService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	// Remap: GetPeople().map(x => x.name)
	remapExpr := RemapExpr{
		ImportID:     0,
		Path:         []string{"GetPeople"},
		Captures:     []Expr{},
		Instructions: []Expr{ImportExpr{ImportID: 0, Path: []string{"name"}}},
	}
	encoded, err := EncodeExpr(remapExpr)
	if err != nil {
		t.Fatalf("EncodeExpr: %v", err)
	}

	f := NewFuture()
	client.sendMu.Lock()
	entry := client.imports.Allocate()
	client.mu.Lock()
	client.pending[entry.ID] = &pendingCall{future: f}
	client.mu.Unlock()
	_ = client.transport.Send(ctx, PushMsg{Expr: encoded})
	_ = client.transport.Send(ctx, PullMsg{ImportID: entry.ID})
	client.sendMu.Unlock()

	val, err := f.Await(ctx)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}

	names, ok := val.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T: %v", val, val)
	}
	if len(names) != 2 {
		t.Fatalf("got %d results; want 2", len(names))
	}
	if names[0] != "Alice" || names[1] != "Bob" {
		t.Fatalf("got %v; want [Alice, Bob]", names)
	}
}

func TestRemapWithCapture(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &remapService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	// Remap: GetPeople().map(x => service.Greet(x.name))
	// Capture: the bootstrap service. It lives on the receiver's export table,
	// so per spec it is referenced as ["import", 0] (an entry on our exports),
	// not ["export", 0] (which would import a new stub).
	// Instructions:
	//   1. Get x.name → result 1
	//   2. Call capture[0].Greet(result1) → result 2
	remapExpr := RemapExpr{
		ImportID: 0,
		Path:     []string{"GetPeople"},
		Captures: []Expr{ImportExpr{ImportID: 0}},
		Instructions: []Expr{
			ImportExpr{ImportID: 0, Path: []string{"name"}},
			PipelineExpr{ImportID: -1, Path: []string{"Greet"}, Args: []Expr{
				ImportExpr{ImportID: 1},
			}},
		},
	}
	encoded, err := EncodeExpr(remapExpr)
	if err != nil {
		t.Fatalf("EncodeExpr: %v", err)
	}

	f := NewFuture()
	client.sendMu.Lock()
	entry := client.imports.Allocate()
	client.mu.Lock()
	client.pending[entry.ID] = &pendingCall{future: f}
	client.mu.Unlock()
	_ = client.transport.Send(ctx, PushMsg{Expr: encoded})
	_ = client.transport.Send(ctx, PullMsg{ImportID: entry.ID})
	client.sendMu.Unlock()

	val, err := f.Await(ctx)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}

	results, ok := val.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T: %v", val, val)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results; want 2", len(results))
	}
	if results[0] != "Hello, Alice!" || results[1] != "Hello, Bob!" {
		t.Fatalf("got %v; want [Hello, Alice!, Hello, Bob!]", results)
	}
}
