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

## Status

Early development. See the [issue tracker](https://github.com/flaticols/capnweb-go/issues) for the implementation roadmap.

## License

MIT
