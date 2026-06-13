package capnweb

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// blobService returns a Blob; used to exercise transport behavior.
type blobService struct{}

func (blobService) Make(_ context.Context) (*Blob, error) {
	return NewBlob("text/plain", []byte("hi")), nil
}

// TestBlobOverBatchFailsFast verifies that returning a Blob over the one-shot
// HTTP batch transport fails fast with a clear error instead of deadlocking
// (the stream-write ack can never arrive on a batch session).
func TestBlobOverBatchFailsFast(t *testing.T) {
	srv := httptest.NewServer(BatchHandler(blobService{}))
	defer srv.Close()

	// A call: push "make" then pull its result.
	push := PushMsg{Expr: []byte(`["import",0,["make"],[]]`)}
	pull := PullMsg{ImportID: 1}

	client := &BatchClient{URL: srv.URL}

	done := make(chan struct {
		msgs []Message
		err  error
	}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msgs, err := client.Do(ctx, []Message{push, pull})
		done <- struct {
			msgs []Message
			err  error
		}{msgs, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("batch Do failed: %v", res.err)
		}
		// Expect a reject carrying the streaming-transport error.
		var rejected bool
		for _, m := range res.msgs {
			if rej, ok := m.(RejectMsg); ok {
				rejected = true
				if !strings.Contains(string(rej.Expr), "streaming transport") {
					t.Errorf("reject = %s; want streaming-transport error", rej.Expr)
				}
			}
		}
		if !rejected {
			t.Errorf("expected a reject message, got %d messages: %v", len(res.msgs), res.msgs)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("batch request hung — Blob over batch deadlocked instead of failing fast")
	}
}
