// Interop test client using raw protocol messages.
// Validates wire format correctness against any server.
//
// Set METHOD_CASE=lower for TS servers (greet), upper for Go servers (Greet).
// Default: upper (Go).

import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import WebSocket from "ws";

const SERVER_URL = process.env.CAPNWEB_SERVER_URL || "ws://localhost:8089/ws";
const LOWER = process.env.METHOD_CASE === "lower";

const methods = {
  greet: LOWER ? "greet" : "Greet",
  add: LOWER ? "add" : "Add",
  echo: LOWER ? "echo" : "Echo",
  fail: LOWER ? "fail" : "Fail",
  doesNotExist: LOWER ? "doesNotExist" : "DoesNotExist",
  getChild: LOWER ? "getChild" : "GetChild",
  childMethod: LOWER ? "childMethod" : "ChildMethod",
  failTyped: LOWER ? "failTyped" : "FailTyped",
  collect: LOWER ? "collect" : "Collect",
  makeBlob: LOWER ? "makeBlob" : "MakeBlob",
  failWithProps: LOWER ? "failWithProps" : "FailWithProps",
  getInvalidDate: LOWER ? "getInvalidDate" : "GetInvalidDate",
  getNumbers: LOWER ? "getNumbers" : "GetNumbers",
  getPeople: LOWER ? "getPeople" : "GetPeople",
  double: LOWER ? "double" : "Double",
  bigNumber: LOWER ? "bigNumber" : "BigNumber",
  getBytes: LOWER ? "getBytes" : "GetBytes",
  getHeaders: LOWER ? "getHeaders" : "GetHeaders",
  getSpecialFloats: LOWER ? "getSpecialFloats" : "GetSpecialFloats",
  getEmptyHeaders: LOWER ? "getEmptyHeaders" : "GetEmptyHeaders",
};

function send(ws, msg) {
  ws.send(JSON.stringify(msg));
}

function recv(ws) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("timeout")), 5000);
    ws.once("message", (data) => {
      clearTimeout(timeout);
      resolve(JSON.parse(data.toString()));
    });
  });
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    ws.on("open", () => resolve(ws));
    ws.on("error", (err) => reject(err));
  });
}

describe("server interop", () => {
  let ws;
  let nextId = 1;

  before(async () => {
    ws = await connect(SERVER_URL);
  });

  after(() => {
    if (ws) ws.close();
  });

  it("greet returns greeting string", async () => {
    const id = nextId++;
    send(ws, ["push", ["import", 0, [methods.greet], ["World"]]]);
    send(ws, ["pull", id]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], id);
    assert.equal(msg[2], "Hello, World!");

    send(ws, ["release", id, 1]);
  });

  it("add returns numeric sum", async () => {
    const id = nextId++;
    send(ws, ["push", ["import", 0, [methods.add], [10, 32]]]);
    send(ws, ["pull", id]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], id);
    assert.equal(msg[2], 42);

    send(ws, ["release", id, 1]);
  });

  it("echo returns various types unchanged", async () => {
    const cases = [
      { input: "hello", expected: "hello" },
      { input: 42, expected: 42 },
      { input: true, expected: true },
      { input: null, expected: null },
      { input: { key: "value" }, expected: { key: "value" } },
    ];

    for (const { input, expected } of cases) {
      const id = nextId++;
      send(ws, ["push", ["import", 0, [methods.echo], [input]]]);
      send(ws, ["pull", id]);

      const msg = await recv(ws);
      assert.equal(msg[0], "resolve", `expected resolve for ${JSON.stringify(input)}`);
      assert.equal(msg[1], id);
      assert.deepEqual(msg[2], expected);

      send(ws, ["release", id, 1]);
    }
  });

  it("fail returns reject with error", async () => {
    const id = nextId++;
    send(ws, ["push", ["import", 0, [methods.fail], []]]);
    send(ws, ["pull", id]);

    const msg = await recv(ws);
    assert.equal(msg[0], "reject");
    assert.equal(msg[1], id);
    assert.equal(msg[2][0], "error");
    assert.ok(msg[2][2].includes("intentional error"));

    send(ws, ["release", id, 1]);
  });

  it("getChild returns export and child method works", async () => {
    const getChildId = nextId++;
    send(ws, ["push", ["import", 0, [methods.getChild], []]]);
    send(ws, ["pull", getChildId]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], getChildId);
    assert.ok(Array.isArray(msg[2]), "expected array expression");
    assert.equal(msg[2][0], "export");
    const childExportId = msg[2][1];
    assert.ok(childExportId < 0, "export ID should be negative");

    // Call childMethod on the exported child.
    const childMethodId = nextId++;
    send(ws, ["push", ["import", childExportId, [methods.childMethod], []]]);
    send(ws, ["pull", childMethodId]);

    const childMsg = await recv(ws);
    assert.equal(childMsg[0], "resolve");
    assert.equal(childMsg[1], childMethodId);
    assert.equal(childMsg[2], "from child");

    send(ws, ["release", childMethodId, 1]);
    send(ws, ["release", getChildId, 1]);
    send(ws, ["release", childExportId, 1]);
  });

  it("pipeline: getChild then childMethod in one batch", async () => {
    const getChildId = nextId++;
    const childMethodId = nextId++;

    // Send both pushes before any pull — true pipelining.
    send(ws, ["push", ["import", 0, [methods.getChild], []]]);
    send(ws, ["push", ["pipeline", getChildId, [methods.childMethod], []]]);
    send(ws, ["pull", childMethodId]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], childMethodId);
    assert.equal(msg[2], "from child");

    send(ws, ["release", childMethodId, 1]);
    send(ws, ["release", getChildId, 1]);
  });

  it("pipeline error propagation", async () => {
    const failId = nextId++;
    const childId = nextId++;

    // Pipeline: fail → anyMethod. First stage fails, second should inherit error.
    send(ws, ["push", ["import", 0, [methods.fail], []]]);
    send(ws, ["push", ["pipeline", failId, ["anyMethod"], []]]);
    send(ws, ["pull", childId]);

    const msg = await recv(ws);
    assert.equal(msg[0], "reject");
    assert.equal(msg[1], childId);
    assert.equal(msg[2][0], "error");
    assert.ok(msg[2][2].includes("intentional error"));

    send(ws, ["release", childId, 1]);
    send(ws, ["release", failId, 1]);
  });

  it("concurrent calls return correct results", async () => {
    const n = 10;
    const ids = [];

    // Send all pushes and pulls at once.
    for (let i = 0; i < n; i++) {
      const id = nextId++;
      ids.push(id);
      send(ws, ["push", ["import", 0, [methods.add], [i, 1]]]);
      send(ws, ["pull", id]);
    }

    // Collect all responses with a single timeout for the whole batch.
    const results = new Map();
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("timeout waiting for concurrent results")), 10000);
      const handler = (data) => {
        const msg = JSON.parse(data.toString());
        if (msg[0] === "resolve") {
          results.set(msg[1], msg[2]);
        }
        if (results.size >= n) {
          clearTimeout(timeout);
          ws.removeListener("message", handler);
          resolve();
        }
      };
      ws.on("message", handler);
    });

    // Verify each result.
    for (let i = 0; i < n; i++) {
      const val = results.get(ids[i]);
      assert.equal(val, i + 1, `call ${i}: expected ${i + 1}, got ${val}`);
    }

    // Release all.
    for (const id of ids) {
      send(ws, ["release", id, 1]);
    }
  });

  it("streaming: pipe write read close via capnweb client", async () => {
    // Use the capnweb npm package for streaming (handles pipe/ID tracking).
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));

    const stub = newWebSocketRpcSession(clientWs);

    // Create a ReadableStream with chunks.
    const chunks = ["Hello", ", ", "World", "!"];
    const readable = new ReadableStream({
      start(controller) {
        for (const chunk of chunks) {
          controller.enqueue(chunk);
        }
        controller.close();
      },
    });

    const result = await stub[methods.collect](readable);
    assert.equal(result, "Hello, World!");

    clientWs.close();
  });

  it("makeBlob returns a blob with preserved type and bytes", async () => {
    // Use the capnweb client so the blob's readable pipe is handled for us.
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));

    const stub = newWebSocketRpcSession(clientWs);
    const blob = await stub[methods.makeBlob]();
    assert.equal(blob.type, "text/plain");
    assert.equal(await blob.text(), "blob payload");

    clientWs.close();
  });

  it("failWithProps preserves error properties over the wire", async () => {
    const id = nextId++;
    send(ws, ["push", ["import", 0, [methods.failWithProps], []]]);
    send(ws, ["pull", id]);

    const msg = await recv(ws);
    assert.equal(msg[0], "reject");
    assert.equal(msg[1], id);
    const err = msg[2];
    assert.equal(err[0], "error");
    // 0.8.0 form: ["error", name, message, stack-or-null, props].
    assert.ok(err.length >= 5, `expected 5-element error, got ${JSON.stringify(err)}`);
    const props = err[4];
    assert.equal(props.code, 42);
    assert.equal(props.detail, "extra");
    // cause is itself a devalued error expression.
    assert.ok(Array.isArray(props.cause) && props.cause[0] === "error");

    send(ws, ["release", id, 1]);
  });

  it("getInvalidDate serializes as [\"date\", null]", async () => {
    const id = nextId++;
    send(ws, ["push", ["import", 0, [methods.getInvalidDate], []]]);
    send(ws, ["pull", id]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], id);
    assert.deepEqual(msg[2], ["date", null]);

    send(ws, ["release", id, 1]);
  });

  it("echo round-trips nested arrays and objects (array escaping)", async () => {
    // Use the capnweb client so array escaping / object recursion is handled
    // by the reference implementation; this validates the Go server's codec.
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const arr = [1, "two", [3, 4]];
    assert.deepEqual(await stub[methods.echo](arr), arr);

    const obj = { nums: [1, 2], label: "x", nested: { deep: [5] } };
    assert.deepEqual(await stub[methods.echo](obj), obj);

    clientWs.close();
  });

  it("map() with object-literal instruction (remap nested refs)", async () => {
    // .map() generates a remap; returning an object literal exercises nested
    // pipeline-ref resolution inside object instructions on the Go server.
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const people = stub[methods.getPeople]();
    const result = await people.map((p) => ({ greeting: p.name }));
    assert.deepEqual(result, [{ greeting: "Alice" }, { greeting: "Bob" }]);

    clientWs.close();
  });

  it("map() capturing a stub and calling it (remap capture)", async () => {
    // The mapper captures `stub` (the bootstrap) and calls a method on it —
    // exercises remap capture resolution against the receiver's export table.
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const nums = stub[methods.getNumbers]();
    const result = await nums.map((n) => stub[methods.double](n));
    assert.deepEqual(result, [2, 4, 6]);

    clientWs.close();
  });

  it("bigNumber returns a precise BigInt from the Go server", async () => {
    // Use the capnweb client so ["bigint",...] is reconstructed as a BigInt;
    // validates the Go server encodes *big.Int instead of a lossy number.
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const n = await stub[methods.bigNumber]();
    assert.equal(typeof n, "bigint");
    assert.equal(n, 123456789012345678901234567890n);

    clientWs.close();
  });

  it("getBytes returns the exact bytes from the Go server (base64)", async () => {
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const b = await stub[methods.getBytes]();
    assert.ok(b instanceof Uint8Array, `expected Uint8Array, got ${typeof b}`);
    assert.deepEqual([...b], [0xde, 0xad]);

    clientWs.close();
  });

  it("getHeaders combines duplicate field values from the Go server", async () => {
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const h = await stub[methods.getHeaders]();
    assert.equal(h.get("x-multi"), "a, b");

    clientWs.close();
  });

  it("getSpecialFloats round-trips Infinity/-Infinity/NaN from the Go server", async () => {
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const arr = await stub[methods.getSpecialFloats]();
    assert.equal(arr[0], Infinity);
    assert.equal(arr[1], -Infinity);
    assert.ok(Number.isNaN(arr[2]));

    clientWs.close();
  });

  it("getEmptyHeaders encodes as [] and is accepted by the reference", async () => {
    const { newWebSocketRpcSession } = await import("capnweb");
    const clientWs = new WebSocket(SERVER_URL);
    await new Promise((resolve) => clientWs.on("open", resolve));
    const stub = newWebSocketRpcSession(clientWs);

    const h = await stub[methods.getEmptyHeaders]();
    assert.equal([...h].length, 0);

    clientWs.close();
  });

  it("unknown method returns reject", async () => {
    const id = nextId++;
    send(ws, ["push", ["import", 0, [methods.doesNotExist], []]]);
    send(ws, ["pull", id]);

    const msg = await recv(ws);
    assert.equal(msg[0], "reject");
    assert.equal(msg[1], id);

    send(ws, ["release", id, 1]);
  });
});
