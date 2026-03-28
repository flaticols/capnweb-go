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

// testService is the Go bootstrap object, mirroring the TS TestService.
type testService struct{}

func (s *testService) Echo(_ context.Context, val any) (any, error)          { return val, nil }
func (s *testService) Add(_ context.Context, a, b float64) (float64, error)  { return a + b, nil }
func (s *testService) Greet(_ context.Context, name string) (string, error)  { return "Hello, " + name + "!", nil }
func (s *testService) Fail(_ context.Context) (any, error)                   { return nil, errors.New("intentional error") }
func (s *testService) GetChild(_ context.Context) (*childService, error)     { return &childService{}, nil }

// childService is an RpcTarget returned by reference.
type childService struct {
	capnweb.RpcTargetBase
}

func (c *childService) ChildMethod(_ context.Context) (string, error) { return "from child", nil }

// --- Test matrix ---
//
//   Server  │  Client  │  What it proves
//   ────────┼──────────┼──────────────────────────────
//   TS      │  TS      │  Baseline (reference behavior)
//   Go      │  TS      │  Go server behaves like TS server
//   TS      │  Go      │  Go client behaves like TS client

// TestTSServerTSClient is the baseline: TS server ↔ TS client.
// If this fails, the reference implementation is broken (not us).
func TestTSServerTSClient(t *testing.T) {
	requireInterop(t)
	tsServer := startTSServer(t)
	defer tsServer.stop()

	runTSClient(t, tsServer.wsURL(), "lower")
}

// TestGoServerTSClient validates the Go server produces identical
// responses to the TS server.
func TestGoServerTSClient(t *testing.T) {
	requireInterop(t)
	goServer := startGoServer(t, "127.0.0.1:8089")
	defer goServer.stop()

	runTSClient(t, "ws://127.0.0.1:8089/ws", "upper")
}

// TestTSServerGoClient validates the Go client interprets TS server
// responses identically to the TS client.
func TestTSServerGoClient(t *testing.T) {
	requireInterop(t)
	tsServer := startTSServer(t)
	defer tsServer.stop()

	runGoClient(t, tsServer.wsURL())
}

// --- TS server ---

type tsServerProc struct {
	cmd  *exec.Cmd
	port string
}

func (s *tsServerProc) wsURL() string { return fmt.Sprintf("ws://127.0.0.1:%s", s.port) }
func (s *tsServerProc) stop() {
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
}

func startTSServer(t *testing.T) *tsServerProc {
	t.Helper()
	capnwebPath := os.Getenv("CAPNWEB_PATH")
	if capnwebPath == "" {
		t.Skip("CAPNWEB_PATH not set")
	}

	npmInstall(t, "ts")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, "npx", "tsx", "server.mjs")
	cmd.Dir = "ts"
	cmd.Env = append(os.Environ(),
		"CAPNWEB_PATH="+capnwebPath,
		"PORT=0", // let OS pick a port — we'll parse from READY line
	)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start TS server: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	var port string
	for scanner.Scan() {
		if p, ok := strings.CutPrefix(scanner.Text(), "READY:"); ok {
			port = p
			break
		}
	}
	if port == "" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("TS server did not become ready")
	}

	return &tsServerProc{cmd: cmd, port: port}
}

// --- Go server ---

type goServerHandle struct {
	server *http.Server
}

func (h *goServerHandle) stop() {
	_ = h.server.Shutdown(context.Background())
}

func startGoServer(t *testing.T, addr string) *goServerHandle {
	t.Helper()
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

	server := &http.Server{Addr: addr, Handler: mux}
	go func() { _ = server.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)
	return &goServerHandle{server: server}
}

// --- TS client runner ---

func runTSClient(t *testing.T, serverURL, methodCase string) {
	t.Helper()
	npmInstall(t, "ts")

	cmd := exec.Command("node", "--test", "client.mjs")
	cmd.Dir = "ts"
	cmd.Env = append(os.Environ(),
		"CAPNWEB_SERVER_URL="+serverURL,
		"METHOD_CASE="+methodCase,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("TS client tests failed: %v", err)
	}
}

// --- Go client runner ---

func runGoClient(t *testing.T, serverURL string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tr, err := capnweb.WSDial(ctx, serverURL, nil)
	if err != nil {
		t.Fatalf("WSDial: %v", err)
	}

	client := capnweb.NewSession(tr, nil)
	go func() { _ = client.Run(ctx) }()
	defer func() { _ = client.Close() }()

	// TS server uses lowercase method names.
	main := client.Main()

	t.Run("greet", func(t *testing.T) {
		result, err := capnweb.Call[string](ctx, main, "greet", "World")
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result != "Hello, World!" {
			t.Fatalf("got %v; want Hello, World!", result)
		}
	})

	t.Run("add", func(t *testing.T) {
		result, err := capnweb.Call[float64](ctx, main, "add", 10.0, 32.0)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result != 42.0 {
			t.Fatalf("got %v; want 42", result)
		}
	})

	t.Run("echo", func(t *testing.T) {
		result, err := capnweb.Call[string](ctx, main, "echo", "test")
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result != "test" {
			t.Fatalf("got %v; want test", result)
		}
	})

	t.Run("fail", func(t *testing.T) {
		_, err := main.Call(ctx, "fail")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "intentional error") {
			t.Fatalf("got %v; want 'intentional error'", err)
		}
	})

	t.Run("getChild", func(t *testing.T) {
		child, err := capnweb.Call[*capnweb.Stub](ctx, main, "getChild")
		if err != nil {
			t.Fatalf("Call: %v", err)
		}

		childResult, err := capnweb.Call[string](ctx, child, "childMethod")
		if err != nil {
			t.Fatalf("childMethod: %v", err)
		}
		if childResult != "from child" {
			t.Fatalf("got %v; want 'from child'", childResult)
		}

		_ = child.Release(ctx)
	})
}

// --- helpers ---

func requireInterop(t *testing.T) {
	t.Helper()
	if os.Getenv("CAPNWEB_INTEROP") == "" {
		t.Skip("set CAPNWEB_INTEROP=1 to run")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
}

var npmInstalled bool

func npmInstall(t *testing.T, dir string) {
	t.Helper()
	if npmInstalled {
		return
	}
	install := exec.Command("npm", "install")
	install.Dir = dir
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		t.Fatalf("npm install failed: %v", err)
	}
	npmInstalled = true
}
