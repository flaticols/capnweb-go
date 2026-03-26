package capnweb

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// WSTransport implements Transport over a WebSocket connection.
// One WebSocket text message = one capnweb message.
type WSTransport struct {
	conn *websocket.Conn
	mu   sync.Mutex // serializes concurrent Send calls
}

// WSDialOptions configures a client WebSocket connection.
type WSDialOptions struct {
	HTTPClient *http.Client
}

// WSDial creates a client-side WebSocket transport by connecting to the
// given URL.
func WSDial(ctx context.Context, url string, opts *WSDialOptions) (*WSTransport, error) {
	var dialOpts *websocket.DialOptions
	if opts != nil && opts.HTTPClient != nil {
		dialOpts = &websocket.DialOptions{HTTPClient: opts.HTTPClient}
	}
	conn, _, err := websocket.Dial(ctx, url, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("capnweb: ws dial: %w", err)
	}
	return &WSTransport{conn: conn}, nil
}

// WSAcceptOptions configures server-side WebSocket upgrade.
type WSAcceptOptions struct {
	Origins []string // allowed origins; nil allows any
}

// WSAccept upgrades an HTTP request to a WebSocket connection and returns
// a transport.
func WSAccept(w http.ResponseWriter, r *http.Request, opts *WSAcceptOptions) (*WSTransport, error) {
	var acceptOpts *websocket.AcceptOptions
	if opts != nil && opts.Origins != nil {
		acceptOpts = &websocket.AcceptOptions{OriginPatterns: opts.Origins}
	}
	conn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		return nil, fmt.Errorf("capnweb: ws accept: %w", err)
	}
	return &WSTransport{conn: conn}, nil
}

// NewWSTransport wraps an existing websocket.Conn as a Transport.
func NewWSTransport(conn *websocket.Conn) *WSTransport {
	return &WSTransport{conn: conn}
}

// Send sends a message as a WebSocket text message. Safe for concurrent use.
func (t *WSTransport) Send(ctx context.Context, msg Message) error {
	data, err := MarshalMessage(msg)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.Write(ctx, websocket.MessageText, data)
}

// Recv reads the next WebSocket text message and decodes it as a Message.
func (t *WSTransport) Recv(ctx context.Context) (Message, error) {
	typ, data, err := t.conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("capnweb: ws recv: %w", err)
	}
	if typ != websocket.MessageText {
		return nil, fmt.Errorf("capnweb: ws recv: expected text message, got %v", typ)
	}
	return UnmarshalMessage(data)
}

// Close sends a normal close frame and closes the connection.
func (t *WSTransport) Close() error {
	return t.conn.Close(websocket.StatusNormalClosure, "")
}
