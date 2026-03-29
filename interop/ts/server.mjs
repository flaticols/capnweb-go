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
