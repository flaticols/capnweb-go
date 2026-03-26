package capnweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newWSPair creates a connected client/server WSTransport pair using a
// test HTTP server.
func newWSPair(t *testing.T) (client, server *WSTransport, cleanup func()) {
	t.Helper()

	serverReady := make(chan *WSTransport, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, err := WSAccept(w, r, &WSAcceptOptions{Origins: []string{"*"}})
		if err != nil {
			t.Errorf("WSAccept: %v", err)
			return
		}
		serverReady <- tr
	}))

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	c, err := WSDial(context.Background(), wsURL, nil)
	if err != nil {
		s.Close()
		t.Fatalf("WSDial: %v", err)
	}

	srv := <-serverReady
	return c, srv, func() {
		c.Close()
		srv.Close()
		s.Close()
	}
}

func TestWSTransportRoundTrip(t *testing.T) {
	client, server, cleanup := newWSPair(t)
	defer cleanup()
	ctx := context.Background()

	// Client → Server
	want := PushMsg{Expr: json.RawMessage(`["import",0,"greet",["hello"]]`)}
	if err := client.Send(ctx, want); err != nil {
		t.Fatalf("send: %v", err)
	}
	got, err := server.Recv(ctx)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	push, ok := got.(PushMsg)
	if !ok {
		t.Fatalf("expected PushMsg, got %T", got)
	}
	if string(push.Expr) != string(want.Expr) {
		t.Fatalf("expr = %s; want %s", push.Expr, want.Expr)
	}

	// Server → Client
	resp := ResolveMsg{ExportID: 1, Expr: json.RawMessage(`"hello back"`)}
	if err := server.Send(ctx, resp); err != nil {
		t.Fatalf("send: %v", err)
	}
	got2, err := client.Recv(ctx)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	resolve, ok := got2.(ResolveMsg)
	if !ok {
		t.Fatalf("expected ResolveMsg, got %T", got2)
	}
	if resolve.ExportID != 1 {
		t.Fatalf("exportID = %d; want 1", resolve.ExportID)
	}
}

func TestWSTransportAllMessageTypes(t *testing.T) {
	client, server, cleanup := newWSPair(t)
	defer cleanup()
	ctx := context.Background()

	messages := []Message{
		PushMsg{Expr: json.RawMessage(`42`)},
		PullMsg{ImportID: 1},
		ResolveMsg{ExportID: 1, Expr: json.RawMessage(`"ok"`)},
		RejectMsg{ExportID: 2, Expr: json.RawMessage(`["error","Error","fail"]`)},
		ReleaseMsg{ImportID: 3, RefCount: 1},
		StreamMsg{Expr: json.RawMessage(`"data"`)},
		PipeMsg{},
		AbortMsg{Expr: json.RawMessage(`["error","Error","bye"]`)},
	}

	for _, msg := range messages {
		if err := client.Send(ctx, msg); err != nil {
			t.Fatalf("send %T: %v", msg, err)
		}
	}

	for i, want := range messages {
		got, err := server.Recv(ctx)
		if err != nil {
			t.Fatalf("recv[%d]: %v", i, err)
		}
		// Re-marshal both and compare.
		wantJSON, _ := MarshalMessage(want)
		gotJSON, _ := MarshalMessage(got)
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("message[%d]:\n  want: %s\n  got:  %s", i, wantJSON, gotJSON)
		}
	}
}

func TestWSTransportConcurrentSend(t *testing.T) {
	client, server, cleanup := newWSPair(t)
	defer cleanup()
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := PullMsg{ImportID: int64(id)}
			if err := client.Send(ctx, msg); err != nil {
				t.Errorf("concurrent send %d: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	// Receive all — order may vary, just verify count and validity.
	seen := map[int64]bool{}
	for range n {
		got, err := server.Recv(ctx)
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		pull, ok := got.(PullMsg)
		if !ok {
			t.Fatalf("expected PullMsg, got %T", got)
		}
		seen[pull.ImportID] = true
	}
	if len(seen) != n {
		t.Fatalf("received %d unique messages; want %d", len(seen), n)
	}
}

func TestWSTransportCloseUnblocksRecv(t *testing.T) {
	client, server, cleanup := newWSPair(t)
	defer cleanup()

	// Close client, server recv should fail.
	client.Close()
	_, err := server.Recv(context.Background())
	if err == nil {
		t.Fatal("expected error after client close")
	}
}

func TestWSTransportInterface(t *testing.T) {
	// Compile-time check that WSTransport implements Transport.
	var _ Transport = (*WSTransport)(nil)
}
