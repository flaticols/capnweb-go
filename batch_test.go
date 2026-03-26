package capnweb

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type batchTestService struct{}

func (s *batchTestService) Greet(_ context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}

func (s *batchTestService) Add(_ context.Context, a, b float64) (float64, error) {
	return a + b, nil
}

func TestBatchHandlerSingleCall(t *testing.T) {
	handler := BatchHandler(&batchTestService{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `["push",["import",0,"Greet",["World"]]]` + "\n" +
		`["pull",1]` + "\n"

	resp, err := http.Post(srv.URL, "application/x-ndjson", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}

	msgs, err := ReadNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	// Should have a resolve message.
	found := false
	for _, msg := range msgs {
		if rm, ok := msg.(ResolveMsg); ok {
			var val string
			_ = json.Unmarshal(rm.Expr, &val)
			if val == "Hello, World!" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected resolve with 'Hello, World!', got %d messages", len(msgs))
	}
}

func TestBatchHandlerPipelinedCalls(t *testing.T) {
	handler := BatchHandler(&batchTestService{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Two calls in one batch.
	body := `["push",["import",0,"Greet",["Alice"]]]` + "\n" +
		`["pull",1]` + "\n" +
		`["push",["import",0,"Add",[3,4]]]` + "\n" +
		`["pull",2]` + "\n"

	resp, err := http.Post(srv.URL, "application/x-ndjson", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	msgs, err := ReadNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	resolves := 0
	for _, msg := range msgs {
		if _, ok := msg.(ResolveMsg); ok {
			resolves++
		}
	}
	if resolves < 2 {
		t.Fatalf("expected at least 2 resolves, got %d (total msgs: %d)", resolves, len(msgs))
	}
}

func TestBatchHandlerEmptyBody(t *testing.T) {
	handler := BatchHandler(&batchTestService{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/x-ndjson", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) != 0 {
		t.Fatalf("expected empty response body, got %q", body)
	}
}

func TestBatchHandlerMethodNotAllowed(t *testing.T) {
	handler := BatchHandler(&batchTestService{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", resp.StatusCode)
	}
}

func TestBatchClient(t *testing.T) {
	handler := BatchHandler(&batchTestService{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := &BatchClient{URL: srv.URL}

	msgs := []Message{
		PushMsg{Expr: json.RawMessage(`["import",0,"Greet",["Bob"]]`)},
		PullMsg{ImportID: 1},
	}

	resp, err := client.Do(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range resp {
		if rm, ok := msg.(ResolveMsg); ok {
			var val string
			_ = json.Unmarshal(rm.Expr, &val)
			if val == "Hello, Bob!" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected resolve with 'Hello, Bob!', got %d messages", len(resp))
	}
}

func TestNDJSONRoundTrip(t *testing.T) {
	msgs := []Message{
		PushMsg{Expr: json.RawMessage(`42`)},
		PullMsg{ImportID: 1},
		ResolveMsg{ExportID: 1, Expr: json.RawMessage(`"ok"`)},
	}

	var buf bytes.Buffer
	if err := WriteNDJSON(&buf, msgs); err != nil {
		t.Fatal(err)
	}

	got, err := ReadNDJSON(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(msgs) {
		t.Fatalf("got %d messages; want %d", len(got), len(msgs))
	}

	for i := range msgs {
		wantJSON, _ := MarshalMessage(msgs[i])
		gotJSON, _ := MarshalMessage(got[i])
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("message %d: %s != %s", i, gotJSON, wantJSON)
		}
	}
}
