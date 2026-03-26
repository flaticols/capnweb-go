package capnweb

import "context"

// Transport is a bidirectional message stream. Implementations handle framing
// and JSON serialization for a specific underlying protocol (WebSocket, HTTP
// batch, etc.).
type Transport interface {
	// Send sends a message to the remote endpoint.
	// Must be safe for concurrent use from multiple goroutines.
	Send(ctx context.Context, msg Message) error

	// Recv receives the next message from the remote endpoint.
	// Blocks until a message is available, the context is cancelled, or the
	// transport is closed.
	Recv(ctx context.Context) (Message, error)

	// Close closes the transport. Any blocked Recv call returns an error.
	Close() error
}
