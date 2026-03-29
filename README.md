[![Go Reference](https://pkg.go.dev/badge/GitHub.com/flaticols/capnweb-go.svg)](https://pkg.go.dev/GitHub.com/flaticols/capnweb-go)

# capnweb-go

Go implementation of Cloudflare's [Cap'n Web](https://github.com/nicolo-ribaudo/tc39-proposal-structs/issues/26#issuecomment-2579268997) RPC protocol.

## What is Cap'n Web?

Cap'n Web is a JSON-based, bidirectional RPC protocol designed for the web. It enables structured communication between endpoints over WebSockets, HTTP batch requests, or any message-passing transport. The protocol supports:

- **Bidirectional RPC** — either side can call the other; there is no fixed "client" or "server"
- **Promise pipelining** — chain dependent calls without waiting for intermediate results, collapsing multiple round trips into one
- **Pass-by-reference** — objects can be exported as stubs and called remotely, with automatic reference counting
- **Streaming** — multiplexed ReadableStream/WritableStream over a single connection
- **Server-side `.map()`** — send transformation recipes to run on remote collections without per-element round trips

The reference implementation is in TypeScript and runs on Cloudflare Workers.

## Cap'n Web vs Cap'n Proto

Both protocols were designed by [Kenton Varda](https://github.com/kentonv) — he created Cap'n Proto, then joined Cloudflare where he built the Workers runtime and designed Cap'n Web as its RPC layer. Despite the shared author and similar name, **Cap'n Web is not Cap'n Proto**. They share philosophical DNA — both draw from the [capability-based security](https://en.wikipedia.org/wiki/Capability-based_security) model and the idea of promise pipelining pioneered by [E language](https://en.wikipedia.org/wiki/E_(programming_language)) and [CapTP](http://erights.org/elib/distrib/captp/index.html) — but differ significantly in design:

| | Cap'n Proto | Cap'n Web |
|---|---|---|
| **Wire format** | Custom binary schema (zero-copy) | JSON arrays |
| **Schema** | Required `.capnp` schema files + code generation | Schema-free; methods discovered at runtime |
| **Target environment** | Systems programming (C++, Rust, Go) | Web platforms (browsers, Workers, Deno) |
| **Transport** | Custom TCP framing | WebSocket, HTTP batch, postMessage |
| **Serialization** | Schema-defined structs with pointer arithmetic | JSON with typed expression arrays (`["bytes", "..."]`, `["date", 123]`) |
| **Complexity** | High — full type system, generics, schema evolution | Moderate — intentionally simple wire format |
| **Ecosystem** | Mature, used in Sandstorm, Cloudflare internals | New, designed for Cloudflare Workers RPC |

**In short:** Cap'n Proto is a high-performance binary RPC system with mandatory schemas. Cap'n Web is a lightweight JSON-based RPC protocol that trades raw throughput for web-native simplicity and zero code generation.

Both support promise pipelining and capability-based object references — the features that make them more powerful than typical REST or gRPC patterns.

## Goals of this project

1. **Spec-complete Go implementation** of the Cap'n Web protocol, suitable for use in Go services that need to interoperate with Cloudflare Workers or any other Cap'n Web endpoint
2. **Idiomatic Go API** — no code generation required; export any struct with methods as an RPC target, call remote methods through typed stubs
3. **WebSocket and HTTP batch transports** out of the box, with a pluggable `Transport` interface for custom framing
4. **Interoperability** with the TypeScript reference implementation, validated by an automated cross-language test suite

## Install

```bash
go get github.com/flaticols/capnweb-go
```

## Examples

### Go Server (WebSocket)

```go
package main

import (
	"context"
	"net/http"

	capnweb "github.com/flaticols/capnweb-go"
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

## License

MIT
