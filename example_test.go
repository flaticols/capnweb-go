package capnweb_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	capnweb "go.flaticols.dev/capnweb-go"
)

// A Greeter is a simple RPC service. Embed RpcTargetBase to mark it
// as pass-by-reference when returned from other methods.
type Greeter struct {
	capnweb.RpcTargetBase
}

func (g *Greeter) Greet(_ context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}

func (g *Greeter) Add(_ context.Context, a, b float64) (float64, error) {
	return a + b, nil
}

func (g *Greeter) Fail(_ context.Context) (any, error) {
	return nil, fmt.Errorf("something went wrong")
}

// ExampleNewSession demonstrates a basic RPC call over an in-process
// WebSocket connection.
func ExampleNewSession() {
	// Start a Go server with a Greeter service.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, _ := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		sess := capnweb.NewSession(tr, &Greeter{})
		sess.Run(r.Context())
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Connect a Go client.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, _ := capnweb.WSDial(ctx, wsURL, nil)
	client := capnweb.NewSession(tr, nil)
	go client.Run(ctx)
	defer client.Close()

	// Call the Greet method.
	main := client.Main()
	result, _ := capnweb.Call[string](ctx, main, "Greet", "World")
	fmt.Println(result)
	// Output: Hello, World!
}

// ExampleCall demonstrates the generic Call helper with type coercion.
func ExampleCall() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, _ := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		sess := capnweb.NewSession(tr, &Greeter{})
		sess.Run(r.Context())
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, _ := capnweb.WSDial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	client := capnweb.NewSession(tr, nil)
	go client.Run(ctx)
	defer client.Close()

	main := client.Main()

	// Call[int] automatically coerces JSON float64 to int.
	sum, _ := capnweb.Call[int](ctx, main, "Add", 3.0, 4.0)
	fmt.Println(sum)
	// Output: 7
}

// ExampleBatchHandler demonstrates the HTTP batch transport.
func ExampleBatchHandler() {
	handler := capnweb.BatchHandler(&Greeter{})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Send NDJSON batch request.
	body := `["push",["import",0,["Greet"],["World"]]]` + "\n" +
		`["pull",1]` + "\n"

	resp, err := http.Post(srv.URL, "application/x-ndjson", strings.NewReader(body))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	msgs, _ := capnweb.ReadNDJSON(resp.Body)
	for _, msg := range msgs {
		if rm, ok := msg.(capnweb.ResolveMsg); ok {
			fmt.Println(string(rm.Expr))
		}
	}
	// Output: "Hello, World!"
}

// Calculator is an RpcTarget returned by reference from MathService.
type Calculator struct {
	capnweb.RpcTargetBase
}

func (c *Calculator) Multiply(_ context.Context, a, b float64) (float64, error) {
	return a * b, nil
}

type MathService struct {
	capnweb.RpcTargetBase
}

func (s *MathService) GetCalculator(_ context.Context) (*Calculator, error) {
	return &Calculator{}, nil
}

// ExampleStub_Pipeline demonstrates promise pipelining — chaining calls
// without waiting for intermediate results.
func ExampleStub_Pipeline() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, _ := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		sess := capnweb.NewSession(tr, &MathService{})
		sess.Run(r.Context())
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, _ := capnweb.WSDial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	client := capnweb.NewSession(tr, nil)
	go client.Run(ctx)
	defer client.Close()

	main := client.Main()

	// Pipeline: GetCalculator returns a stub, then Multiply is called
	// on it — both calls are sent before waiting for any result.
	calc, _ := main.Pipeline(ctx, "GetCalculator")
	result, _ := capnweb.Call[float64](ctx, calc, "Multiply", 6.0, 7.0)
	calc.Release(ctx)

	fmt.Println(result)
	// Output: 42
}

// StreamService collects chunks from a stream into a single string.
type StreamService struct {
	capnweb.RpcTargetBase
}

func (s *StreamService) Collect(_ context.Context, reader *capnweb.StreamReader) (string, error) {
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

// ExampleSession_CreatePipe demonstrates streaming data from client to server.
func ExampleSession_CreatePipe() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, _ := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		sess := capnweb.NewSession(tr, &StreamService{})
		sess.Run(r.Context())
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tr, _ := capnweb.WSDial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	client := capnweb.NewSession(tr, nil)
	go client.Run(ctx)
	defer client.Close()

	// Create a pipe and pass the readable end to the server.
	writer, readable, _ := client.CreatePipe(ctx)

	resultCh := make(chan string, 1)
	go func() {
		main := client.Main()
		r, _ := capnweb.Call[string](ctx, main, "Collect", readable)
		resultCh <- r
	}()

	// Write chunks through the pipe.
	writer.Write(ctx, "Hello")
	writer.Write(ctx, ", ")
	writer.Write(ctx, "World!")
	writer.Close(ctx)

	fmt.Println(<-resultCh)
	// Output: Hello, World!
}
