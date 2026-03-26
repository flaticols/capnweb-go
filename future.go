package capnweb

import (
	"context"
	"sync"
)

// Future represents a pending result that will be resolved or rejected.
type Future struct {
	mu   sync.Mutex
	done chan struct{}
	val  any
	err  error
}

// NewFuture creates a new unresolved future.
func NewFuture() *Future {
	return &Future{done: make(chan struct{})}
}

// Resolve sets the value and unblocks all waiters.
func (f *Future) Resolve(val any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		return // already settled
	default:
	}
	f.val = val
	close(f.done)
}

// Reject sets the error and unblocks all waiters.
func (f *Future) Reject(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		return // already settled
	default:
	}
	f.err = err
	close(f.done)
}

// Await blocks until the future is settled or the context is cancelled.
func (f *Future) Await(ctx context.Context) (any, error) {
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Done returns a channel that is closed when the future is settled.
func (f *Future) Done() <-chan struct{} {
	return f.done
}

// Settled reports whether the future has been resolved or rejected.
func (f *Future) Settled() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}
