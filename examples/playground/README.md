# Playground — browser UI + Go backend

A minimal end-to-end demo: a Go [Cap'n Web](https://github.com/cloudflare/capnweb)
server called from the browser with the reference `capnweb` client over
WebSocket. It exercises the headline features of the protocol.

## Run

```bash
go run ./examples/playground
# open http://127.0.0.1:8088
```

No frontend build step — `web/app.js` is a native ES module that imports the
`capnweb` client from a CDN (`esm.sh`), so an internet connection is needed the
first time the page loads. The Go server serves the embedded UI and the
WebSocket RPC endpoint at `/ws`.

## What it shows

- **`greet` / `add`** — plain RPC calls with typed arguments and return values.
- **`counter`** — `NewCounter()` returns a live server-side object **by
  reference**; each `Increment()` is a call on that remote stub, and its state
  lives on the server.
- **pipelining** — `api.NewCounter().Add(10)` pipes the counter promise straight
  into `.Add` without awaiting it, so both calls travel in a single round trip.

## Layout

- `main.go` — the `Playground` bootstrap service (`Greet`, `Add`, `NewCounter`),
  the `Counter` RpcTarget, and an HTTP server that embeds and serves `web/`.
- `web/index.html`, `web/app.js` — the browser UI (`// @ts-check` + JSDoc for
  editor type-checking without a compiler).
