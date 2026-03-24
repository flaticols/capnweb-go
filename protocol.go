// Package capnweb implements Cloudflare's Cap'n Web RPC protocol in Go.
//
// Cap'n Web is a JSON-based, bidirectional RPC protocol that supports promise
// pipelining, pass-by-reference objects with automatic reference counting,
// multiplexed streaming, and server-side remap expressions.
//
// A session is established over a [Transport] (WebSocket, HTTP batch, or custom).
// Local objects implementing exported methods are served via [Target], and remote
// objects are called through [Stub].
package capnweb
