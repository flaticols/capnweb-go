package capnweb

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamCollector is a test service that reads all chunks from a stream.
type streamCollector struct {
	testService
}

func (s *streamCollector) Collect(_ context.Context, reader *StreamReader) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := reader.Read(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		sb.WriteString(chunk.(string))
	}
	return sb.String(), nil
}

func TestPipeWriteRead(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &streamCollector{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	writer, readable, err := client.CreatePipe(ctx)
	if err != nil {
		t.Fatalf("CreatePipe: %v", err)
	}

	// Call Collect with the readable end.
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := client.callWithTag(ctx, "import", 0, "Collect", readable)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result.(string)
	}()

	// Write chunks.
	for _, chunk := range []string{"Hello", ", ", "World", "!"} {
		if err := writer.Write(ctx, chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := writer.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case result := <-resultCh:
		if result != "Hello, World!" {
			t.Fatalf("result = %v; want 'Hello, World!'", result)
		}
	case err := <-errCh:
		t.Fatalf("Call: %v", err)
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestPipeAbort(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &streamCollector{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	writer, readable, err := client.CreatePipe(ctx)
	if err != nil {
		t.Fatalf("CreatePipe: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := client.callWithTag(ctx, "import", 0, "Collect", readable)
		errCh <- err
	}()

	// Write one chunk then abort.
	if err := writer.Write(ctx, "partial"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Abort(ctx, fmt.Errorf("test abort")); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from abort")
		}
		if !strings.Contains(err.Error(), "test abort") {
			t.Fatalf("error = %v; want 'test abort'", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestMultipleConcurrentStreams(t *testing.T) {
	clientTr, serverTr := newChanTransportPair()
	server := NewSession(serverTr, &streamCollector{})
	client := NewSession(clientTr, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() { _ = server.Run(ctx) }()
	go func() { _ = client.Run(ctx) }()

	const numStreams = 5
	var wg sync.WaitGroup
	results := make([]string, numStreams)
	errs := make([]error, numStreams)

	for i := range numStreams {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			writer, readable, err := client.CreatePipe(ctx)
			if err != nil {
				errs[idx] = err
				return
			}

			resultCh := make(chan string, 1)
			errCh := make(chan error, 1)
			go func() {
				result, err := client.callWithTag(ctx, "import", 0, "Collect", readable)
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- result.(string)
			}()

			msg := fmt.Sprintf("stream-%d", idx)
			if err := writer.Write(ctx, msg); err != nil {
				errs[idx] = err
				return
			}
			if err := writer.Close(ctx); err != nil {
				errs[idx] = err
				return
			}

			select {
			case r := <-resultCh:
				results[idx] = r
			case err := <-errCh:
				errs[idx] = err
			case <-ctx.Done():
				errs[idx] = ctx.Err()
			}
		}(i)
	}

	wg.Wait()

	for i := range numStreams {
		if errs[i] != nil {
			t.Fatalf("stream %d: %v", i, errs[i])
		}
		want := fmt.Sprintf("stream-%d", i)
		if results[i] != want {
			t.Fatalf("stream %d: got %v; want %v", i, results[i], want)
		}
	}
}
