package interop

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	capnweb "github.com/flaticols/capnweb-go"
)

// testService is the shared bootstrap object for interop testing.
// Both Go and TS tests call these methods.
type testService struct{}

func (s *testService) Echo(_ context.Context, val any) (any, error) {
	return val, nil
}

func (s *testService) Add(_ context.Context, a, b float64) (float64, error) {
	return a + b, nil
}

func (s *testService) Greet(_ context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}

func (s *testService) Fail(_ context.Context) (any, error) {
	return nil, errors.New("intentional error")
}

// TestInteropTSClient starts a Go WebSocket server, runs the TS test client
// against it, and reports the results.
func TestInteropTSClient(t *testing.T) {
	if os.Getenv("CAPNWEB_INTEROP") == "" {
		t.Skip("skipping interop test (set CAPNWEB_INTEROP=1 to run)")
	}

	// Check node is available.
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found, skipping interop tests")
	}

	// Start Go WebSocket server.
	svc := &testService{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		tr, err := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		if err != nil {
			t.Errorf("WSAccept: %v", err)
			return
		}
		sess := capnweb.NewSession(tr, svc)
		_ = sess.Run(r.Context())
	})

	server := &http.Server{Addr: "127.0.0.1:8089", Handler: mux}
	go func() { _ = server.ListenAndServe() }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	// Give server time to start.
	time.Sleep(100 * time.Millisecond)

	// Install TS dependencies.
	install := exec.Command("npm", "install")
	install.Dir = "ts"
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		t.Fatalf("npm install failed: %v", err)
	}

	// Run TS test client.
	cmd := exec.Command("node", "--test", "client.mjs")
	cmd.Dir = "ts"
	cmd.Env = append(os.Environ(), "CAPNWEB_SERVER_URL=ws://127.0.0.1:8089/ws")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("TS interop tests failed: %v", err)
	}
}

// TestInteropBatchTSClient tests the HTTP batch endpoint from TS.
func TestInteropBatchTSClient(t *testing.T) {
	if os.Getenv("CAPNWEB_INTEROP") == "" {
		t.Skip("skipping interop test (set CAPNWEB_INTEROP=1 to run)")
	}

	// This will be expanded when batch client tests are added to TS.
	t.Skip("batch interop tests not yet implemented")
}
