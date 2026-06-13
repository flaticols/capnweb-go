package capnweb

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// pipe is an internal bidirectional channel created by the ["pipe"] message.
// The writing end calls Write/Close/Abort; the reading end uses a StreamReader.
type pipe struct {
	chunks chan any
	mu     sync.Mutex
	closed bool
	err    error
}

func newPipe() *pipe {
	return &pipe{chunks: make(chan any, 64)}
}

// Write pushes a chunk into the pipe. Called via stream message dispatch.
func (p *pipe) Write(_ context.Context, chunk any) (any, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("write to closed pipe")
	}
	p.mu.Unlock()
	p.chunks <- chunk
	return nil, nil
}

// Close closes the pipe. The reader will receive io.EOF.
func (p *pipe) Close(_ context.Context) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.chunks)
	}
	return nil, nil
}

// Abort closes the pipe with an error. The reader will receive the error.
func (p *pipe) Abort(_ context.Context, reason string) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.err = fmt.Errorf("%s", reason)
		close(p.chunks)
	}
	return nil, nil
}

// StreamReader reads chunks from a pipe. Obtained from a ReadableExpr
// when a method receives a stream argument.
type StreamReader struct {
	pipe *pipe
}

// Read returns the next chunk from the stream. Returns io.EOF when the
// writer closes the stream, or an error if the writer aborts.
func (r *StreamReader) Read(ctx context.Context) (any, error) {
	select {
	case chunk, ok := <-r.pipe.chunks:
		if !ok {
			if r.pipe.err != nil {
				return nil, r.pipe.err
			}
			return nil, io.EOF
		}
		return chunk, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// StreamWriter writes chunks to a remote pipe via stream messages.
// Each write sends a ["stream", ...] message and waits for the resolve
// (backpressure).
type StreamWriter struct {
	session  *Session
	importID int64
}

// Write sends a chunk to the remote pipe. Blocks until the remote
// acknowledges the write (backpressure).
func (w *StreamWriter) Write(ctx context.Context, chunk any) error {
	return w.streamCall(ctx, "write", chunk)
}

// Close signals the end of the stream. The remote reader will receive io.EOF.
func (w *StreamWriter) Close(ctx context.Context) error {
	return w.streamCall(ctx, "close")
}

// Abort terminates the stream with an error.
func (w *StreamWriter) Abort(ctx context.Context, reason error) error {
	return w.streamCall(ctx, "abort", reason.Error())
}

func (w *StreamWriter) streamCall(ctx context.Context, method string, args ...any) error {
	expr, err := w.session.buildCallExpr("import", w.importID, method, args)
	if err != nil {
		return err
	}

	f := NewFuture()

	w.session.sendMu.Lock()
	entry := w.session.imports.Allocate()
	w.session.mu.Lock()
	w.session.pending[entry.ID] = &pendingCall{future: f}
	w.session.mu.Unlock()
	sendErr := w.session.transport.Send(ctx, StreamMsg{Expr: expr})
	w.session.sendMu.Unlock()

	if sendErr != nil {
		return fmt.Errorf("capnweb: send stream: %w", sendErr)
	}

	// Wait for acknowledge (backpressure).
	_, err = f.Await(ctx)

	// Auto-release.
	w.session.imports.Remove(entry.ID)

	return err
}
