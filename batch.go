package capnweb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// BatchHandler returns an http.Handler that processes batched RPC requests.
// Each HTTP request creates an ephemeral session: the request body is NDJSON
// (one message per line), all messages are processed in order, and outbound
// messages are written back as NDJSON.
func BatchHandler(main any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse inbound messages.
		inbound, err := ReadNDJSON(r.Body)
		if err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Create an ephemeral session backed by a collector transport.
		ct := &collectorTransport{inbox: inbound}
		sess := NewSession(ct, main)

		// Run processes all inbox messages then returns when Recv returns EOF.
		_ = sess.Run(r.Context())

		// Write outbound messages as NDJSON.
		w.Header().Set("Content-Type", "application/x-ndjson")
		if len(ct.outbox) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := WriteNDJSON(w, ct.outbox); err != nil {
			// Headers already sent — can't change status code.
			return
		}
	})
}

// BatchClient sends batches of messages to an HTTP batch endpoint.
// BatchClient sends batches of RPC messages to an HTTP batch endpoint.
// Set URL to the batch endpoint. HTTPClient is optional (defaults to
// http.DefaultClient).
type BatchClient struct {
	URL        string
	HTTPClient *http.Client
}

// Do sends a batch of messages and returns the response messages.
func (c *BatchClient) Do(ctx context.Context, messages []Message) ([]Message, error) {
	var buf bytes.Buffer
	if err := WriteNDJSON(&buf, messages); err != nil {
		return nil, fmt.Errorf("capnweb: batch encode: %w", err)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, &buf)
	if err != nil {
		return nil, fmt.Errorf("capnweb: batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("capnweb: batch send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("capnweb: batch: server returned %d: %s", resp.StatusCode, body)
	}

	return ReadNDJSON(resp.Body)
}

// ReadNDJSON reads newline-delimited JSON messages from a reader.
func ReadNDJSON(r io.Reader) ([]Message, error) {
	var msgs []Message
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		msg, err := UnmarshalMessage(line)
		if err != nil {
			return nil, fmt.Errorf("capnweb: ndjson line: %w", err)
		}
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("capnweb: ndjson read: %w", err)
	}
	return msgs, nil
}

// WriteNDJSON writes messages as newline-delimited JSON to a writer.
func WriteNDJSON(w io.Writer, msgs []Message) error {
	for _, msg := range msgs {
		data, err := MarshalMessage(msg)
		if err != nil {
			return fmt.Errorf("capnweb: ndjson marshal: %w", err)
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

// collectorTransport is an in-memory transport used by ephemeral batch
// sessions. It reads from a pre-filled inbox and collects outbound messages.
type collectorTransport struct {
	inbox  []Message
	pos    int
	mu     sync.Mutex
	outbox []Message
}

func (t *collectorTransport) Send(_ context.Context, msg Message) error {
	t.mu.Lock()
	t.outbox = append(t.outbox, msg)
	t.mu.Unlock()
	return nil
}

func (t *collectorTransport) Recv(_ context.Context) (Message, error) {
	if t.pos >= len(t.inbox) {
		return nil, io.EOF
	}
	msg := t.inbox[t.pos]
	t.pos++
	return msg, nil
}

func (t *collectorTransport) Close() error {
	return nil
}
