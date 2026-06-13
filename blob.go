package capnweb

import (
	"context"
	"errors"
	"io"
	"sync"
)

// Blob is a binary value with an associated MIME type, transferred over RPC as
// a ["blob", type, ["readable", id]] expression. The bytes are streamed through
// a pipe (matching the JS reference), so a received Blob is read lazily: call
// Bytes to drain the stream.
//
// Construct an outgoing Blob with NewBlob. A Blob obtained from a remote call is
// backed by a stream and its bytes are only materialized when Bytes is called.
//
// Because the bytes stream through a pipe, sending a Blob requires a streaming
// transport with a live back-channel (e.g. WebSocket). Sending one over the
// one-shot HTTP batch transport fails fast with an error rather than blocking.
type Blob struct {
	// Type is the MIME type (blob.type in JS), e.g. "text/plain".
	Type string

	mu     sync.Mutex
	data   []byte
	reader *StreamReader // non-nil for received blobs not yet drained
	read   bool
}

// NewBlob creates a Blob carrying the given bytes and MIME type, ready to be
// returned from an RPC method or sent as a value.
func NewBlob(mimeType string, data []byte) *Blob {
	return &Blob{Type: mimeType, data: data}
}

// Bytes returns the blob's contents. For a received blob this drains the
// underlying stream on first call and caches the result; subsequent calls
// return the cached bytes.
func (b *Blob) Bytes(ctx context.Context) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.read || b.reader == nil {
		return b.data, nil
	}
	var buf []byte
	for {
		chunk, err := b.reader.Read(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch c := chunk.(type) {
		case []byte:
			buf = append(buf, c...)
		case BytesExpr:
			buf = append(buf, c.Data...)
		case string:
			buf = append(buf, c...)
		}
	}
	b.data = buf
	b.read = true
	b.reader = nil
	return buf, nil
}
