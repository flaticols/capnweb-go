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

// RpcTarget is implemented by types that should be passed by reference over
// the wire. Instead of being serialized as JSON, RpcTarget values are exported
// into the session's export table and sent as ["export", id] expressions.
//
// Embed [RpcTargetBase] in your struct to implement this interface:
//
//	type MyService struct {
//	    capnweb.RpcTargetBase
//	}
type RpcTarget interface {
	IsRpcTarget()
}

// RpcTargetBase is embedded in structs to mark them as pass-by-reference
// RPC targets. This is the Go equivalent of extending the RpcTarget class
// in the TypeScript reference implementation.
type RpcTargetBase struct{}

// IsRpcTarget implements [RpcTarget].
func (RpcTargetBase) IsRpcTarget() {}
