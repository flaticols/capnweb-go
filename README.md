# capnweb-go

Go implementation of Cloudflare's [Cap'n Web](https://github.com/cloudflare/capnweb) RPC protocol — wire-compatible with the TypeScript reference, validated by a cross-language test suite against capnweb 0.8.0.

No code generation: export any Go struct with methods as an RPC target and call remote methods through typed stubs.

## Features

- **Bidirectional RPC** — either side can call the other; there is no fixed "client" or "server"
- **Promise pipelining** — chain dependent calls without waiting for intermediate results, collapsing multiple round trips into one
- **Pass-by-reference** — objects can be exported as stubs and called remotely, with automatic reference counting
- **Streaming** — multiplexed ReadableStream/WritableStream over a single connection
- **Server-side `.map()`** — send transformation recipes to run on remote collections without per-element round trips
- **WebSocket and HTTP batch transports** out of the box, with a pluggable `Transport` interface

## Install

```bash
go get go.flaticols.dev/capnweb-go
```

## Examples

### Go Server (WebSocket)

```go
package main

import (
	"context"
	"net/http"

	capnweb "go.flaticols.dev/capnweb-go"
)

type Greeter struct {
	capnweb.RpcTargetBase
}

func (g *Greeter) Greet(_ context.Context, name string) (string, error) {
	return "Hello, " + name + "!", nil
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		tr, _ := capnweb.WSAccept(w, r, &capnweb.WSAcceptOptions{Origins: []string{"*"}})
		sess := capnweb.NewSession(tr, &Greeter{})
		sess.Run(r.Context())
	})
	http.ListenAndServe(":8080", mux)
}
```

### Go Server (HTTP Batch)

```go
mux.Handle("/rpc", capnweb.BatchHandler(&Greeter{}))
```

Each HTTP POST carries a batch of NDJSON messages — all calls and their responses fit in one round trip.

### Go Client (WebSocket)

```go
ctx := context.Background()
tr, _ := capnweb.WSDial(ctx, "ws://localhost:8080/ws", nil)

client := capnweb.NewSession(tr, nil)
go client.Run(ctx)
defer client.Close()

main := client.Main()
result, _ := capnweb.Call[string](ctx, main, "Greet", "World")
// result == "Hello, World!"
```

### Go Client (HTTP Batch)

```go
bc := &capnweb.BatchClient{URL: "http://localhost:8080/rpc"}
msgs, _ := bc.Do(ctx, []capnweb.Message{
	capnweb.PushMsg{Expr: json.RawMessage(`["import",0,["Greet"],["World"]]`)},
	capnweb.PullMsg{ImportID: 1},
})
```

### TypeScript Client (WebSocket)

```typescript
import { newWebSocketRpcSession } from "capnweb";

const stub = newWebSocketRpcSession("ws://localhost:8080/ws");
const result = await stub.Greet("World");
// result === "Hello, World!"
```

### TypeScript Server + Go Client

```typescript
// server.ts
import { RpcTarget, newWebSocketRpcSession } from "capnweb";

class Greeter extends RpcTarget {
  greet(name: string) { return `Hello, ${name}!`; }
}

// ... set up WebSocketServer, call newWebSocketRpcSession(ws, new Greeter())
```

```go
// client.go
tr, _ := capnweb.WSDial(ctx, "ws://localhost:3000", nil)
client := capnweb.NewSession(tr, nil)
go client.Run(ctx)

main := client.Main()
result, _ := capnweb.Call[string](ctx, main, "greet", "World")
```

### Pass-by-Reference

Methods that return an `RpcTarget` are automatically passed by reference:

```go
type Calculator struct{ capnweb.RpcTargetBase }
func (c *Calculator) Add(_ context.Context, a, b float64) (float64, error) {
	return a + b, nil
}

type MathService struct{ capnweb.RpcTargetBase }
func (s *MathService) GetCalculator(_ context.Context) (*Calculator, error) {
	return &Calculator{}, nil
}
```

```go
// Client
main := client.Main()
calc, _ := capnweb.Call[*capnweb.Stub](ctx, main, "GetCalculator")
result, _ := capnweb.Call[float64](ctx, calc, "Add", 3.0, 4.0)
defer calc.Release(ctx)
```

### Promise Pipelining

Chain calls without waiting for intermediate results:

```go
main := client.Main()
calc, _ := main.Pipeline(ctx, "GetCalculator")   // push only, no wait
result, _ := capnweb.Call[float64](ctx, calc, "Add", 3.0, 4.0) // push + pull
defer calc.Release(ctx)
```

### Streaming

```go
// Client: create pipe, write chunks
writer, readable, _ := client.CreatePipe(ctx)
go func() {
	main := client.Main()
	capnweb.Call[string](ctx, main, "Collect", readable)
}()
writer.Write(ctx, "chunk1")
writer.Write(ctx, "chunk2")
writer.Close(ctx)

// Server: read stream
func (s *Service) Collect(_ context.Context, reader *capnweb.StreamReader) (string, error) {
	var buf strings.Builder
	for {
		chunk, err := reader.Read(context.Background())
		if err == io.EOF { break }
		buf.WriteString(chunk.(string))
	}
	return buf.String(), nil
}
```

## Status

All core and advanced protocol features are implemented. See the [issue tracker](https://github.com/flaticols/capnweb-go/issues) for details.

[![Go Reference](https://pkg.go.dev/badge/go.flaticols.dev/capnweb-go.svg)](https://pkg.go.dev/go.flaticols.dev/capnweb-go)
[![CI](https://github.com/flaticols/capnweb-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/flaticols/capnweb-go/actions/workflows/ci.yml)
[![Release](https://github.com/flaticols/capnweb-go/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/flaticols/capnweb-go/actions/workflows/release.yml)

## License

MIT
