// Command playground is a small browser demo for capnweb-go: a Go capnweb
// server exposing a few RPC methods, plus a static TypeScript-flavored UI that
// talks to it over WebSocket using the reference `capnweb` client.
//
// Run it and open the printed URL:
//
//	go run ./examples/playground
//	# → http://127.0.0.1:8088
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	capnweb "go.flaticols.dev/capnweb-go"
)

//go:embed web
var webFS embed.FS

// Counter is a stateful object the server hands out by reference. Each one the
// browser creates keeps its own running total on the server — demonstrating
// pass-by-reference stubs.
type Counter struct {
	capnweb.RpcTargetBase
	n atomic.Int64
}

// Increment adds one and returns the new value.
func (c *Counter) Increment(_ context.Context) (float64, error) {
	return float64(c.n.Add(1)), nil
}

// Add adds delta and returns the new value.
func (c *Counter) Add(_ context.Context, delta float64) (float64, error) {
	return float64(c.n.Add(int64(delta))), nil
}

// Value returns the current total.
func (c *Counter) Value(_ context.Context) (float64, error) {
	return float64(c.n.Load()), nil
}

// Playground is the bootstrap service exported to the browser.
type Playground struct {
	capnweb.RpcTargetBase
}

// Greet returns a greeting.
func (p *Playground) Greet(_ context.Context, name string) (string, error) {
	if name == "" {
		name = "world"
	}
	return "Hello, " + name + "!", nil
}

// Add returns a + b.
func (p *Playground) Add(_ context.Context, a, b float64) (float64, error) {
	return a + b, nil
}

// NewCounter creates a fresh server-side Counter and returns it by reference.
func (p *Playground) NewCounter(_ context.Context) (*Counter, error) {
	return &Counter{}, nil
}

func main() {
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}

	mux := http.NewServeMux()

	// capnweb RPC over WebSocket. A fresh session per connection, all backed by
	// the same Playground bootstrap.
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		tr, err := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		if err != nil {
			log.Printf("ws accept: %v", err)
			return
		}
		sess := capnweb.NewSession(tr, &Playground{})
		if err := sess.Run(r.Context()); err != nil {
			log.Printf("session ended: %v", err)
		}
	})

	// Static UI.
	mux.Handle("/", http.FileServer(http.FS(web)))

	addr := "127.0.0.1:8088"
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("capnweb playground listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
