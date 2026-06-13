package interop

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	capnweb "go.flaticols.dev/capnweb-go"
)

// testService is the Go bootstrap object, mirroring the TS TestService.
type testService struct{}

func (s *testService) Echo(_ context.Context, val any) (any, error)         { return val, nil }
func (s *testService) Add(_ context.Context, a, b float64) (float64, error) { return a + b, nil }
func (s *testService) Greet(_ context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}
func (s *testService) Fail(_ context.Context) (any, error) {
	return nil, errors.New("intentional error")
}
func (s *testService) GetChild(_ context.Context) (*childService, error) { return &childService{}, nil }
func (s *testService) FailTyped(_ context.Context) (any, error) {
	return nil, capnweb.NewTypeError("bad argument")
}

func (s *testService) Collect(_ context.Context, reader *capnweb.StreamReader) (string, error) {
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

// --- 0.8.0 features ---

// MakeBlob returns a Blob; exercises Go's blob encode (streaming via pipe).
func (s *testService) MakeBlob(_ context.Context) (*capnweb.Blob, error) {
	return capnweb.NewBlob("text/plain", []byte("blob payload")), nil
}

// FailWithProps returns an error carrying custom properties and a cause;
// exercises Go's ["error", name, msg, null, props] encode.
func (s *testService) FailWithProps(_ context.Context) (any, error) {
	return nil, &capnweb.ErrorExpr{
		Type:    "Error",
		Message: "with props",
		Props: map[string]capnweb.Expr{
			"code":   capnweb.LiteralExpr{Value: float64(42)},
			"detail": capnweb.LiteralExpr{Value: "extra"},
			"cause":  capnweb.ErrorExpr{Type: "RangeError", Message: "the cause"},
		},
	}
}

// GetInvalidDate returns the zero time, which encodes as ["date", null].
func (s *testService) GetInvalidDate(_ context.Context) (time.Time, error) {
	return time.Time{}, nil
}

// GetNumbers / GetPeople / Double back the remap (.map()) interop tests.
func (s *testService) GetNumbers(_ context.Context) ([]any, error) {
	return []any{1.0, 2.0, 3.0}, nil
}

func (s *testService) GetPeople(_ context.Context) ([]any, error) {
	return []any{
		map[string]any{"name": "Alice"},
		map[string]any{"name": "Bob"},
	}, nil
}

func (s *testService) Double(_ context.Context, n float64) (float64, error) {
	return n * 2, nil
}

// BigNumber returns an integer beyond float64 precision; exercises ["bigint",...]
// encoding so the value survives instead of being truncated to a float.
func (s *testService) BigNumber(_ context.Context) (*big.Int, error) {
	n, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	return n, nil
}

// GetBytes returns a byte expression; exercises ["bytes", base64] (unpadded).
func (s *testService) GetBytes(_ context.Context) (capnweb.Expr, error) {
	return capnweb.BytesExpr{Data: []byte{0xDE, 0xAD}}, nil
}

// GetHeaders returns headers with a duplicated field; exercises comma-combining.
func (s *testService) GetHeaders(_ context.Context) (capnweb.Expr, error) {
	h := http.Header{}
	h.Add("X-Multi", "a")
	h.Add("X-Multi", "b")
	return capnweb.HeadersExpr{Header: h}, nil
}

// GetSpecialFloats returns the non-finite floats; exercises ["inf"]/["-inf"]/["nan"].
func (s *testService) GetSpecialFloats(_ context.Context) ([]any, error) {
	return []any{math.Inf(1), math.Inf(-1), math.NaN()}, nil
}

// GetEmptyHeaders returns an empty header set; must encode as ["headers",[]],
// which the reference accepts (it rejects the old ["headers",null] form).
func (s *testService) GetEmptyHeaders(_ context.Context) (capnweb.Expr, error) {
	return capnweb.HeadersExpr{Header: http.Header{}}, nil
}

// GetRequest returns a Request with a body; the reference's Request constructor
// throws unless init.duplex is present, so this exercises the duplex emission.
func (s *testService) GetRequest(_ context.Context) (capnweb.Expr, error) {
	return capnweb.RequestExpr{
		URL:    "https://example.com/",
		Method: "POST",
		Body:   capnweb.BytesExpr{Data: []byte("hello")},
	}, nil
}

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
	cmd.WaitDelay = 2 * time.Second

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
		var errExpr *capnweb.ErrorExpr
		if !errors.As(err, &errExpr) {
			t.Fatalf("errors.As failed: got %T", err)
		}
		if !strings.Contains(errExpr.Message, "intentional error") {
			t.Fatalf("got %v; want 'intentional error'", errExpr.Message)
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

	t.Run("pipeline", func(t *testing.T) {
		// Pipeline: getChild → childMethod without waiting for getChild.
		child, err := main.Pipeline(ctx, "getChild")
		if err != nil {
			t.Fatalf("Pipeline: %v", err)
		}
		defer child.Release(ctx)

		result, err := capnweb.Call[string](ctx, child, "childMethod")
		if err != nil {
			t.Fatalf("childMethod: %v", err)
		}
		if result != "from child" {
			t.Fatalf("got %v; want 'from child'", result)
		}
	})

	t.Run("pipelineErrorPropagation", func(t *testing.T) {
		failing, err := main.Pipeline(ctx, "fail")
		if err != nil {
			t.Fatalf("Pipeline: %v", err)
		}
		defer failing.Release(ctx)

		_, err = failing.Call(ctx, "anyMethod")
		if err == nil {
			t.Fatal("expected error from pipeline on failed stage")
		}
		if !strings.Contains(err.Error(), "intentional error") {
			t.Fatalf("error = %v; want 'intentional error'", err)
		}
	})

	t.Run("concurrentCalls", func(t *testing.T) {
		const n = 10
		var wg sync.WaitGroup
		errs := make([]error, n)
		results := make([]float64, n)

		for i := range n {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				r, err := capnweb.Call[float64](ctx, main, "add", float64(idx), 1.0)
				errs[idx] = err
				results[idx] = r
			}(i)
		}
		wg.Wait()

		for i := range n {
			if errs[i] != nil {
				t.Fatalf("call %d: %v", i, errs[i])
			}
			want := float64(i) + 1.0
			if results[i] != want {
				t.Fatalf("call %d: got %v; want %v", i, results[i], want)
			}
		}
	})

	t.Run("blobFromTS", func(t *testing.T) {
		// TS server returns a Blob; Go client receives and drains it.
		result, err := capnweb.Call[*capnweb.Blob](ctx, main, "makeBlob")
		if err != nil {
			t.Fatalf("makeBlob: %v", err)
		}
		if result.Type != "text/plain" {
			t.Errorf("blob type = %q; want text/plain", result.Type)
		}
		data, err := result.Bytes(ctx)
		if err != nil {
			t.Fatalf("blob bytes: %v", err)
		}
		if string(data) != "blob payload" {
			t.Errorf("blob = %q; want 'blob payload'", data)
		}
	})

	t.Run("errorProps", func(t *testing.T) {
		// TS server throws an Error with custom props + cause.
		_, err := main.Call(ctx, "failWithProps")
		if err == nil {
			t.Fatal("expected error")
		}
		var errExpr *capnweb.ErrorExpr
		if !errors.As(err, &errExpr) {
			t.Fatalf("errors.As failed: got %T", err)
		}
		if lit, ok := errExpr.Props["code"].(capnweb.LiteralExpr); !ok || lit.Value != float64(42) {
			t.Errorf("props[code] = %#v; want literal 42", errExpr.Props["code"])
		}
		if _, ok := errExpr.Props["cause"].(capnweb.ErrorExpr); !ok {
			t.Errorf("props[cause] = %#v; want nested ErrorExpr", errExpr.Props["cause"])
		}
	})

	t.Run("invalidDate", func(t *testing.T) {
		// TS server returns an invalid Date; Go decodes it to the zero time.
		result, err := capnweb.Call[time.Time](ctx, main, "getInvalidDate")
		if err != nil {
			t.Fatalf("getInvalidDate: %v", err)
		}
		if !result.IsZero() {
			t.Errorf("invalid date decoded to %v; want zero time", result)
		}
	})

	t.Run("echoArray", func(t *testing.T) {
		// Array escaping ([[...]]) + arg devaluation, both directions:
		// the arg is encoded by Go and the result decoded by Go.
		in := []any{1.0, "two", []any{3.0, 4.0}}
		result, err := capnweb.Call[[]any](ctx, main, "echo", in)
		if err != nil {
			t.Fatalf("echo array: %v", err)
		}
		if !reflect.DeepEqual(result, in) {
			t.Fatalf("echo array = %#v; want %#v", result, in)
		}
	})

	t.Run("echoNestedObject", func(t *testing.T) {
		// Object property values must be recursively (de)valued — a nested
		// array inside an object exercises both rules at once.
		in := map[string]any{"nums": []any{1.0, 2.0}, "label": "x"}
		result, err := capnweb.Call[map[string]any](ctx, main, "echo", in)
		if err != nil {
			t.Fatalf("echo object: %v", err)
		}
		if !reflect.DeepEqual(result, in) {
			t.Fatalf("echo object = %#v; want %#v", result, in)
		}
	})

	t.Run("bigint", func(t *testing.T) {
		// TS server returns a BigInt; Go must receive the exact *big.Int.
		result, err := capnweb.Call[*big.Int](ctx, main, "bigNumber")
		if err != nil {
			t.Fatalf("bigNumber: %v", err)
		}
		if result == nil || result.String() != "123456789012345678901234567890" {
			t.Fatalf("bigNumber = %v; want 123456789012345678901234567890", result)
		}
	})

	t.Run("bytes", func(t *testing.T) {
		// TS server returns a Uint8Array, encoded as unpadded base64; Go must
		// decode it rather than rejecting the missing padding.
		result, err := capnweb.Call[[]byte](ctx, main, "getBytes")
		if err != nil {
			t.Fatalf("getBytes: %v", err)
		}
		if !bytes.Equal(result, []byte{0xDE, 0xAD}) {
			t.Fatalf("getBytes = %v; want [222 173]", result)
		}
	})

	t.Run("headers", func(t *testing.T) {
		// TS server returns a Headers with a duplicated field; the reference
		// combines the values, and Go must receive the combined form.
		result, err := capnweb.Call[http.Header](ctx, main, "getHeaders")
		if err != nil {
			t.Fatalf("getHeaders: %v", err)
		}
		if got := result.Get("X-Multi"); got != "a, b" {
			t.Fatalf("X-Multi = %q; want \"a, b\"", got)
		}
	})

	t.Run("specialFloats", func(t *testing.T) {
		// TS server returns [Infinity, -Infinity, NaN] as ["inf"]/["-inf"]/["nan"].
		result, err := capnweb.Call[[]any](ctx, main, "getSpecialFloats")
		if err != nil {
			t.Fatalf("getSpecialFloats: %v", err)
		}
		if len(result) != 3 {
			t.Fatalf("got %d values; want 3", len(result))
		}
		if f, _ := result[0].(float64); !math.IsInf(f, 1) {
			t.Errorf("[0] = %v; want +Inf", result[0])
		}
		if f, _ := result[1].(float64); !math.IsInf(f, -1) {
			t.Errorf("[1] = %v; want -Inf", result[1])
		}
		if f, _ := result[2].(float64); !math.IsNaN(f) {
			t.Errorf("[2] = %v; want NaN", result[2])
		}
	})

	t.Run("streaming", func(t *testing.T) {
		writer, readable, err := client.CreatePipe(ctx)
		if err != nil {
			t.Fatalf("CreatePipe: %v", err)
		}

		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			r, err := capnweb.Call[string](ctx, main, "collect", readable)
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- r
		}()

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
				t.Fatalf("got %v; want 'Hello, World!'", result)
			}
		case err := <-errCh:
			t.Fatalf("collect: %v", err)
		case <-ctx.Done():
			t.Fatal("timeout")
		}
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
