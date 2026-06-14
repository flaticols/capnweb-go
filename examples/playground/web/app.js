// @ts-check
//
// Browser client for the capnweb-go playground. No build step: this is a native
// ES module that imports the reference capnweb client straight from a CDN.
// `// @ts-check` + JSDoc give editor type-checking without a compiler.

import { newWebSocketRpcSession } from "https://esm.sh/capnweb@0.8.0";

/** @param {string} id */
const $ = (id) => /** @type {HTMLElement} */ (document.getElementById(id));

const wsUrl = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;

// The session's main stub is the Go `Playground` bootstrap. Method calls return
// promises; objects returned by reference (Counter) become callable stubs.
const api = newWebSocketRpcSession(wsUrl);
$("status").textContent = "connected";

/**
 * Run an async action and render its result (or error) into an output element.
 * @param {string} outId
 * @param {() => Promise<unknown>} fn
 */
async function show(outId, fn) {
  const out = $(outId);
  out.classList.remove("err");
  out.textContent = "…";
  try {
    const result = await fn();
    out.textContent = typeof result === "string" ? result : JSON.stringify(result);
  } catch (err) {
    out.classList.add("err");
    out.textContent = String(err);
  }
}

// greet
$("greet-btn").addEventListener("click", () => {
  const name = /** @type {HTMLInputElement} */ ($("greet-name")).value;
  show("greet-out", () => api.Greet(name));
});

// add
$("add-btn").addEventListener("click", () => {
  const a = Number(/** @type {HTMLInputElement} */ ($("add-a")).value);
  const b = Number(/** @type {HTMLInputElement} */ ($("add-b")).value);
  show("add-out", async () => `${a} + ${b} = ${await api.Add(a, b)}`);
});

// counter — pass-by-reference
/** @type {any} */
let counter = null;

$("counter-new").addEventListener("click", () => {
  show("counter-out", async () => {
    counter?.[Symbol.dispose]?.(); // release the previous one
    counter = api.NewCounter();
    return "new counter created (value 0)";
  });
});

$("counter-inc").addEventListener("click", () => {
  show("counter-out", async () => {
    if (!counter) throw new Error("create a counter first");
    return `value = ${await counter.Increment()}`;
  });
});

$("counter-val").addEventListener("click", () => {
  show("counter-out", async () => {
    if (!counter) throw new Error("create a counter first");
    return `value = ${await counter.Value()}`;
  });
});

// pipelining — NewCounter() is not awaited; .Add(10) pipes onto its result.
$("pipe-btn").addEventListener("click", () => {
  show("pipe-out", async () => {
    const value = await api.NewCounter().Add(10);
    return `NewCounter().Add(10) = ${value}  (single round trip)`;
  });
});
