package capnweb

import (
	"context"
	"testing"
	"time"
)

func TestSessionMain(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()
	if main == nil {
		t.Fatal("Main() returned nil")
	}
	if main.ID() != 0 {
		t.Fatalf("Main().ID() = %d; want 0", main.ID())
	}
}

func TestStubCall(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	result, err := main.Call(ctx, "Greet", "World")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != "Hello, World!" {
		t.Fatalf("result = %v; want Hello, World!", result)
	}
}

func TestStubCallGeneric(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	result, err := Call[string](ctx, main, "Greet", "World")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != "Hello, World!" {
		t.Fatalf("result = %v; want Hello, World!", result)
	}
}

func TestStubCallGenericNumericCoercion(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &testService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	// JSON numbers come back as float64; Call[int] should coerce.
	result, err := Call[int](ctx, main, "Add", 3.0, 4.0)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != 7 {
		t.Fatalf("result = %v; want 7", result)
	}
}

func TestStubCallReturnsStub(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &parentService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	child, err := Call[*Stub](ctx, main, "GetChild")
	if err != nil {
		t.Fatalf("GetChild: %v", err)
	}
	if child.ID() >= 0 {
		t.Fatalf("expected negative import ID, got %d", child.ID())
	}

	result, err := Call[string](ctx, child, "ChildMethod")
	if err != nil {
		t.Fatalf("ChildMethod: %v", err)
	}
	if result != "from child" {
		t.Fatalf("result = %v; want 'from child'", result)
	}

	if err := child.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestStubRelease(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &parentService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	child, err := Call[*Stub](ctx, main, "GetChild")
	if err != nil {
		t.Fatalf("GetChild: %v", err)
	}

	if err := child.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestStubDoubleRelease(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &parentService{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	main := client.Main()

	child, err := Call[*Stub](ctx, main, "GetChild")
	if err != nil {
		t.Fatalf("GetChild: %v", err)
	}

	if err := child.Release(ctx); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	// Second release should be a no-op.
	if err := child.Release(ctx); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestStubString(t *testing.T) {
	clientTr, _ := newChanTransportPair()
	client := NewSession(clientTr, nil)
	main := client.Main()
	if s := main.String(); s != "Stub(0)" {
		t.Fatalf("String() = %q; want Stub(0)", s)
	}
}
