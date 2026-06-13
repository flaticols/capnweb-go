# Cross-language interop (go-js / js-go)

This directory is the runnable cross-language example and conformance harness for
`capnweb-go`. It exercises the Cap'n Web protocol in **both directions** against
the reference TypeScript implementation ([cloudflare/capnweb](https://github.com/cloudflare/capnweb)),
proving the Go implementation is wire-compatible.

## What it covers

| Server | Client | What it proves |
| ------ | ------ | -------------- |
| TS     | TS     | Baseline — reference behavior |
| Go     | TS     | Go server encodes like the TS server (**go → js**) |
| TS     | Go     | Go client decodes like the TS client (**js → go**) |

The service surface (defined identically in Go `testService` and TS
`TestService`) covers: `echo`, `add`, `greet`, `fail`/`failTyped`, `getChild`
(pass-by-reference), `collect` (streaming), plus the capnweb **0.8.0** features:

- **`makeBlob`** — a `Blob` round-trip, asserting MIME type + bytes survive the
  streaming pipe (`["blob", type, ["readable", id]]`).
- **`failWithProps`** — an `Error` carrying custom properties (`code`, `detail`)
  and a `cause`, asserting they propagate (`["error", name, msg, null, props]`).
- **`getInvalidDate`** — an invalid `Date`, asserting it serializes as
  `["date", null]` and decodes back to an invalid/zero date.

## Running locally

You need Go, Node 22+, and a checkout of the reference implementation:

```sh
git clone https://github.com/cloudflare/capnweb /tmp/capnweb-ref
cd interop/ts && npm install && cd ../..

CAPNWEB_INTEROP=1 CAPNWEB_PATH=/tmp/capnweb-ref \
  go test -race -v -timeout 120s ./interop/...
```

Without `CAPNWEB_INTEROP=1` the tests skip, so the suite is a no-op in
environments that lack Node.

## CI

- **`ci.yml`** runs this suite on every PR against a **pinned** capnweb version
  (`v0.8.0`) for deterministic results.
- **`nightly.yml`** runs it against **`capnweb@latest`** as a drift alarm: a red
  nightly means upstream changed the wire protocol and the Go implementation
  needs to be re-aligned.
