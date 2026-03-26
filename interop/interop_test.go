package interop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
// against it, and reports the results (wire format validation).
func TestInteropTSClient(t *testing.T) {
	if os.Getenv("CAPNWEB_INTEROP") == "" {
		t.Skip("skipping interop test (set CAPNWEB_INTEROP=1 to run)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}

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
	time.Sleep(100 * time.Millisecond)

	npmInstall(t, "ts")

	cmd := exec.Command("node", "--test", "client.mjs")
	cmd.Dir = "ts"
	cmd.Env = append(os.Environ(), "CAPNWEB_SERVER_URL=ws://127.0.0.1:8089/ws")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("TS client tests failed: %v", err)
	}
}

// TestInteropGoClient starts a TS server (using the reference capnweb
// implementation) and connects a Go client to it (behavioral validation).
func TestInteropGoClient(t *testing.T) {
	if os.Getenv("CAPNWEB_INTEROP") == "" {
		t.Skip("skipping interop test (set CAPNWEB_INTEROP=1 to run)")
	}
	capnwebPath := os.Getenv("CAPNWEB_PATH")
	if capnwebPath == "" {
		t.Skip("CAPNWEB_PATH not set (should point to cloudflare/capnweb checkout)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}

	npmInstall(t, "ts")

	// Start the TS server and wait for the READY signal.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", "server.mjs")
	cmd.Dir = "ts"
	cmd.Env = append(os.Environ(),
		"CAPNWEB_PATH="+capnwebPath,
		"PORT=8090",
	)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start TS server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Wait for "READY:<port>" line.
	scanner := bufio.NewScanner(stdout)
	var port string
	for scanner.Scan() {
		line := scanner.Text()
		if p, ok := strings.CutPrefix(line, "READY:"); ok {
			port = p
			break
		}
	}
	if port == "" {
		t.Fatal("TS server did not become ready")
	}

	// Connect Go client to TS server.
	wsURL := fmt.Sprintf("ws://127.0.0.1:%s", port)
	tr, err := capnweb.WSDial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WSDial: %v", err)
	}

	client := capnweb.NewSession(tr, nil)
	go func() { _ = client.Run(ctx) }()
	defer client.Close()

	// Test: Greet
	t.Run("Greet", func(t *testing.T) {
		result, err := client.Call(ctx, 0, "greet", "World")
		if err != nil {
			t.Fatalf("Call greet: %v", err)
		}
		if result != "Hello, World!" {
			t.Fatalf("greet = %v; want Hello, World!", result)
		}
	})

	// Test: Add
	t.Run("Add", func(t *testing.T) {
		result, err := client.Call(ctx, 0, "add", 10.0, 32.0)
		if err != nil {
			t.Fatalf("Call add: %v", err)
		}
		if result != 42.0 {
			t.Fatalf("add = %v; want 42", result)
		}
	})

	// Test: Echo
	t.Run("Echo", func(t *testing.T) {
		result, err := client.Call(ctx, 0, "echo", "test")
		if err != nil {
			t.Fatalf("Call echo: %v", err)
		}
		if result != "test" {
			t.Fatalf("echo = %v; want test", result)
		}
	})

	// Test: Fail (expect rejection)
	t.Run("Fail", func(t *testing.T) {
		_, err := client.Call(ctx, 0, "fail")
		if err == nil {
			t.Fatal("expected error from fail")
		}
		if !strings.Contains(err.Error(), "intentional error") {
			t.Fatalf("error = %v; want 'intentional error'", err)
		}
	})
}

func npmInstall(t *testing.T, dir string) {
	t.Helper()
	install := exec.Command("npm", "install")
	install.Dir = dir
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		t.Fatalf("npm install failed: %v", err)
	}
}
