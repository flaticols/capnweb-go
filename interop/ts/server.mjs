// Interop test server: runs a capnweb RPC server using the reference
// TypeScript implementation. The Go client connects and tests behavioral
// compatibility.
//
// Usage: CAPNWEB_PATH=/path/to/cloudflare/capnweb node server.mjs

import http from "node:http";
import { WebSocketServer } from "ws";

const capnwebPath = process.env.CAPNWEB_PATH;
if (!capnwebPath) {
  console.error("CAPNWEB_PATH must point to a checkout of cloudflare/capnweb");
  process.exit(1);
}

const { RpcTarget, newWebSocketRpcSession } = await import(`${capnwebPath}/src/index.ts`);

class TestService extends RpcTarget {
  echo(val) {
    return val;
  }

  add(a, b) {
    return a + b;
  }

  greet(name) {
    return `Hello, ${name}!`;
  }

  fail() {
    throw new Error("intentional error");
  }

  getChild() {
    return new ChildService();
  }

  failTyped() {
    throw new TypeError("bad argument");
  }

  async collect(readable) {
    const reader = readable.getReader();
    let result = "";
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      result += value;
    }
    return result;
  }

  // --- 0.8.0 features ---

  // makeBlob returns a Blob with a MIME type; exercises blob streaming.
  makeBlob() {
    return new Blob(["blob payload"], { type: "text/plain" });
  }

  // echoBlob reads a blob argument and returns its text (for symmetry).
  async echoBlob(blob) {
    return await blob.text();
  }

  // failWithProps throws an Error carrying custom enumerable properties plus a
  // cause; exercises ["error", name, msg, stack?, props].
  failWithProps() {
    const err = new Error("with props");
    err.code = 42;
    err.detail = "extra";
    err.cause = new RangeError("the cause");
    throw err;
  }

  // getInvalidDate returns an invalid Date; exercises ["date", null].
  getInvalidDate() {
    return new Date(NaN);
  }

  // getNumbers / getPeople / double back the remap (.map()) interop tests.
  getNumbers() {
    return [1, 2, 3];
  }

  getPeople() {
    return [{ name: "Alice" }, { name: "Bob" }];
  }

  double(n) {
    return n * 2;
  }

  // bigNumber returns a BigInt beyond float64 precision.
  bigNumber() {
    return 123456789012345678901234567890n;
  }
}

class ChildService extends RpcTarget {
  childMethod() {
    return "from child";
  }
}

const PORT = parseInt(process.env.PORT || "0", 10);

const httpServer = http.createServer((_req, res) => {
  res.writeHead(404).end("Not Found");
});

const wsServer = new WebSocketServer({ server: httpServer });
wsServer.on("connection", (ws) => {
  newWebSocketRpcSession(ws, new TestService());
});

httpServer.listen(PORT, "127.0.0.1", () => {
  const actualPort = httpServer.address().port;
  console.log(`READY:${actualPort}`);
});
