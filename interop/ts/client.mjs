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
