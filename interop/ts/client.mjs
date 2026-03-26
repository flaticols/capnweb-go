// Interop test client: connects to the Go WebSocket server and runs
// basic RPC calls using raw capnweb protocol messages.

import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import WebSocket from "ws";

const SERVER_URL = process.env.CAPNWEB_SERVER_URL || "ws://localhost:8089/ws";

/** Send a message without waiting. */
function send(ws, msg) {
  ws.send(JSON.stringify(msg));
}

/** Wait for the next message. */
function recv(ws) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("timeout")), 5000);
    ws.once("message", (data) => {
      clearTimeout(timeout);
      resolve(JSON.parse(data.toString()));
    });
  });
}

/** Open a WebSocket and wait for connection. */
function connect(url) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    ws.on("open", () => resolve(ws));
    ws.on("error", (err) => reject(err));
  });
}

describe("Go server interop", () => {
  let ws;

  before(async () => {
    ws = await connect(SERVER_URL);
  });

  after(() => {
    if (ws) ws.close();
  });

  it("calls Greet and receives resolve", async () => {
    // push: call main.Greet("World") — assigned import 1
    send(ws, ["push", ["import", 0, "Greet", ["World"]]]);
    send(ws, ["pull", 1]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], 1);
    assert.equal(msg[2], "Hello, World!");

    // release
    send(ws, ["release", 1, 1]);
  });

  it("calls Add and receives numeric result", async () => {
    // push: call main.Add(10, 32) — assigned import 2
    send(ws, ["push", ["import", 0, "Add", [10, 32]]]);
    send(ws, ["pull", 2]);

    const msg = await recv(ws);
    assert.equal(msg[0], "resolve");
    assert.equal(msg[1], 2);
    assert.equal(msg[2], 42);

    send(ws, ["release", 2, 1]);
  });

  it("calls Echo with various types", async () => {
    const testCases = [
      { input: "hello", expected: "hello" },
      { input: 42, expected: 42 },
      { input: true, expected: true },
      { input: null, expected: null },
      { input: { key: "value" }, expected: { key: "value" } },
    ];

    let importId = 3;
    for (const { input, expected } of testCases) {
      send(ws, ["push", ["import", 0, "Echo", [input]]]);
      send(ws, ["pull", importId]);

      const msg = await recv(ws);
      assert.equal(msg[0], "resolve", `expected resolve for input ${JSON.stringify(input)}`);
      assert.equal(msg[1], importId);
      assert.deepEqual(msg[2], expected);

      send(ws, ["release", importId, 1]);
      importId++;
    }
  });

  it("receives reject for method that fails", async () => {
    const importId = 8;
    send(ws, ["push", ["import", 0, "Fail", []]]);
    send(ws, ["pull", importId]);

    const msg = await recv(ws);
    assert.equal(msg[0], "reject");
    assert.equal(msg[1], importId);
    // Error expression: ["error", type, message]
    assert.equal(msg[2][0], "error");
    assert.ok(msg[2][2].includes("intentional error"));

    send(ws, ["release", importId, 1]);
  });

  it("receives reject for unknown method", async () => {
    const importId = 9;
    send(ws, ["push", ["import", 0, "DoesNotExist", []]]);
    send(ws, ["pull", importId]);

    const msg = await recv(ws);
    assert.equal(msg[0], "reject");
    assert.equal(msg[1], importId);

    send(ws, ["release", importId, 1]);
  });
});
