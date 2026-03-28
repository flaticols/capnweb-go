package capnweb

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"time"
)

// Stub represents a remote object accessible through a session.
// It wraps an import table entry and provides methods to call remote
// methods and release the reference.
//
// Stubs are created via [Session.Main] (for the bootstrap interface) or
// returned from [Stub.Call] when the remote returns a pass-by-reference
// object.
//
// When done with a stub, call [Stub.Release] to send a release message
// to the remote. A runtime finalizer is set as a safety net, but explicit
// release is preferred for deterministic cleanup.
type Stub struct {
	session  *Session
	importID int64

	mu       sync.Mutex
	released bool
}

func newStub(session *Session, importID int64) *Stub {
	s := &Stub{
		session:  session,
		importID: importID,
	}
	runtime.SetFinalizer(s, finalizeStub)
	return s
}

func finalizeStub(s *Stub) {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return
	}
	s.released = true
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.session.Release(ctx, s.importID, 1)
}

// Call invokes a method on the remote object and blocks until the result
// is available. If the remote returns a pass-by-reference object, it is
// automatically wrapped as a *Stub.
func (s *Stub) Call(ctx context.Context, method string, args ...any) (any, error) {
	result, err := s.session.Call(ctx, s.importID, method, args...)
	if err != nil {
		return nil, err
	}
	return s.session.wrapImportEntry(result), nil
}

// Release sends a release message for this stub's import and marks it as
// released. Subsequent calls to Release are no-ops.
func (s *Stub) Release(ctx context.Context) error {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return nil
	}
	s.released = true
	s.mu.Unlock()

	runtime.SetFinalizer(s, nil)
	return s.session.Release(ctx, s.importID, 1)
}

// ID returns the import ID of the remote object.
func (s *Stub) ID() int64 {
	return s.importID
}

// String implements fmt.Stringer.
func (s *Stub) String() string {
	return fmt.Sprintf("Stub(%d)", s.importID)
}

// Call invokes a method on the stub and converts the result to type T.
// JSON numbers (float64) are automatically coerced to the target numeric type.
func Call[T any](ctx context.Context, stub *Stub, method string, args ...any) (T, error) {
	result, err := stub.Call(ctx, method, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	var zero T
	if result == nil {
		return zero, nil
	}
	if typed, ok := result.(T); ok {
		return typed, nil
	}
	rv := reflect.ValueOf(result)
	rt := reflect.TypeFor[T]()
	if rv.Type().ConvertibleTo(rt) {
		return rv.Convert(rt).Interface().(T), nil
	}
	return zero, fmt.Errorf("capnweb: result type %T is not assignable to %s", result, rt)
}
