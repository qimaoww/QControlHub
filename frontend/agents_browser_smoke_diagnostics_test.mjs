import assert from "node:assert/strict";
import { test } from "node:test";
import { setImmediate } from "node:timers/promises";
import { runInNewContext } from "node:vm";
import { observeSmokeResult, smokeFailure } from "./agents_browser_smoke.mjs";

const url = "http://127.0.0.1:1234/agents-browser-smoke.html?mode=logs#core-logs";
const timeoutMessage = "等待浏览器 smoke 完成标记超时";
const flush = () => setImmediate();

function fakeCDP(t, options = {}) {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"], now: 0 });
  const original = globalThis.WebSocket;
  let socket;
  const page = {
    location: { href: url },
    document: {
      readyState: "interactive", visibilityState: "visible", hasFocus: () => true,
      documentElement: { dataset: { browserSmoke: options.result?.status || "", motionCoveragePages: "logs,traffic" } },
      body: { textContent: "Core logs still loading" },
      activeElement: { outerHTML: '<button id="log-switch">Logs</button>' },
      querySelector: selector => selector === "#browser-smoke-result"
        ? { textContent: options.result?.detail || "" }
        : { className: "workspace-main loading", getAttribute: () => "true" },
      getAnimations: () => [],
    },
    window: {},
  };
  options.changePage?.(page);

  class FakeSocket extends EventTarget {
    static OPEN = 1;
    readyState = 0;
    requests = [];
    closes = 0;
    snapshots = [];

    constructor() {
      super();
      if (options.constructorError) throw new Error(options.constructorError);
      socket = this;
      queueMicrotask(() => {
        if (options.beforeOpen) { options.beforeOpen(this); return; }
        this.readyState = FakeSocket.OPEN;
        this.dispatchEvent(new Event("open"));
        options.afterOpen?.(this);
      });
    }

    emit(message) {
      this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(message) }));
    }

    reply(request, result) {
      queueMicrotask(() => this.emit({ id: request.id, result }));
    }

    send(payload) {
      const request = JSON.parse(payload);
      this.requests.push({ ...request, at: Date.now() });
      if (request.method.endsWith(".enable") && options.stallSubscriptions) return;
      if (request.method !== "Runtime.evaluate") { this.reply(request, {}); return; }
      if (request.params.awaitPromise) {
        options.onObservation?.(this);
        if (options.result) this.reply(request, { result: { value: options.result } });
        return; // No result models a page that never publishes its completion marker.
      }
      this.snapshots.push({ at: Date.now(), closed: this.closes > 0 });
      if (options.stallSnapshot) return;
      const value = runInNewContext(request.params.expression, page);
      this.reply(request, { result: { value } });
    }

    failClose(code = 1011, reason = "renderer disconnected") {
      this.readyState = 3;
      this.dispatchEvent(Object.assign(new Event("close"), { code, reason }));
    }

    close() {
      this.closes++;
      this.failClose(1000, "runner cleanup");
    }
  }

  globalThis.WebSocket = FakeSocket;
  t.after(() => {
    globalThis.WebSocket = original;
    t.mock.timers.reset();
  });
  return { socket: () => socket, clock: t.mock.timers };
}

function observe(options = {}) {
  const diagnostics = { browser: { exitCode: null, signalCode: null } };
  let settled = false;
  const outcome = observeSmokeResult(
    "ws://fake/page", options.navigateURL, false, false, Boolean(options.navigateURL), false, diagnostics,
  ).then(
    result => { settled = true; return { result }; },
    error => { settled = true; return { error }; },
  );
  return { diagnostics, outcome, settled: () => settled };
}

const runtimeException = (description = "fixture exploded") => ({
  method: "Runtime.exceptionThrown",
  params: { exceptionDetails: {
    text: "Uncaught", exception: { description }, url: "/assets/browser/logs.mjs",
    lineNumber: 17, columnNumber: 4,
    stackTrace: { callFrames: [{ functionName: "renderLogs", url: "/assets/browser/logs.mjs", lineNumber: 17, columnNumber: 4 }] },
  } },
});

test("unanswered diagnostic subscriptions do not move the 45s verdict; live evidence precedes cleanup", async t => {
  const fixture = fakeCDP(t, {
    stallSubscriptions: true,
    onObservation: socket => {
      socket.emit(runtimeException());
      socket.emit({ method: "Runtime.consoleAPICalled", params: { type: "error", args: [{ value: "fetch failed" }] } });
    },
  });
  const observed = observe();
  await flush();
  assert.equal(fixture.socket().requests.find(request => request.params.awaitPromise)?.at, 0);
  assert.equal(observed.diagnostics.cdp.subscriptions["Runtime.enable"], "pending");
  fixture.clock.tick(1500);
  await flush();
  assert.match(observed.diagnostics.cdp.subscriptions["Runtime.enable"], /exceeded 1500ms/);
  assert.match(observed.diagnostics.cdp.subscriptions["Inspector.enable"], /exceeded 1500ms/);
  fixture.clock.tick(43499);
  await flush();
  assert.equal(observed.settled(), false);
  assert.equal(fixture.socket().snapshots.length, 0);
  fixture.clock.tick(1);
  await flush();
  const { error } = await observed.outcome;
  assert.equal(error.message, timeoutMessage);
  assert.deepEqual(fixture.socket().snapshots, [{ at: 45000, closed: false }]);
  assert.equal(fixture.socket().closes, 1);
  assert.equal(observed.diagnostics.page.url, url);
  assert.equal(observed.diagnostics.page.marker, null);
  assert.equal(observed.diagnostics.page.body, "Core logs still loading");
  assert.equal(observed.diagnostics.runtimeExceptions[0].frames[0].line, 17);
  assert.deepEqual(observed.diagnostics.consoleErrors, ["fetch failed"]);
  const stderr = "old-noise" + "s".repeat(9000) + "renderer stderr tail";
  const failure = smokeFailure("logs", url, error, observed.diagnostics, stderr);
  assert.equal(failure.cause, error);
  assert.match(failure.message, /Chrome logs smoke failed/);
  assert.ok(failure.message.includes(url));
  assert.ok(failure.message.includes("fixture exploded"));
  assert.ok(failure.message.includes('"marker":null'));
  assert.equal(failure.message.split("Chrome stderr tail (last 8192 chars):\n")[1], stderr.slice(-8192));
  assert.ok(!failure.message.includes("old-noise"));
});

test("popup marker observation keeps the original 60s boundary", async t => {
  const fixture = fakeCDP(t, { stallSubscriptions: true });
  const observed = observe({ navigateURL: url.replace("mode=logs", "mode=motion-popup-mobile-reduced") });
  await flush();
  assert.equal(fixture.socket().requests.find(request => request.params.awaitPromise)?.at, 0);
  fixture.clock.tick(59999);
  await flush();
  assert.equal(observed.settled(), false);
  fixture.clock.tick(1);
  await flush();
  assert.equal((await observed.outcome).error.message, timeoutMessage);
  assert.deepEqual(fixture.socket().snapshots, [{ at: 60000, closed: false }]);
});

test("a stuck snapshot has only 1500ms after the original failure, with no retry", async t => {
  const fixture = fakeCDP(t, { stallSnapshot: true });
  const observed = observe();
  await flush();
  fixture.clock.tick(45000);
  await flush();
  assert.deepEqual(fixture.socket().snapshots, [{ at: 45000, closed: false }]);
  assert.equal(observed.settled(), false);
  fixture.clock.tick(1499);
  await flush();
  assert.equal(observed.settled(), false);
  fixture.clock.tick(1);
  await flush();
  assert.equal((await observed.outcome).error.message, timeoutMessage);
  assert.match(observed.diagnostics.page.unavailable, /Runtime.evaluate exceeded 1500ms/);
  assert.equal(fixture.socket().snapshots.length, 1);
  assert.equal(fixture.socket().closes, 1);
});

test("an already-closed CDP socket preserves the close cause and does not wait for a snapshot", async t => {
  const fixture = fakeCDP(t, { onObservation: socket => socket.failClose(1011, "renderer disconnected") });
  const observed = observe();
  await flush();
  assert.match((await observed.outcome).error.message, /CDP socket closed \(1011\)/);
  assert.deepEqual(observed.diagnostics.cdp.close, { code: 1011, reason: "renderer disconnected" });
  assert.equal(observed.diagnostics.cdp.state, "closed");
  assert.match(observed.diagnostics.page.unavailable, /socket state 3/);
  assert.equal(fixture.socket().snapshots.length, 0);
});

for (const event of ["Inspector.targetCrashed", "Inspector.detached"]) {
  test(`${event} before any pending command is remembered and fails promptly`, async t => {
    const fixture = fakeCDP(t, { afterOpen: socket => socket.emit({ method: event, params: { reason: "target gone" } }) });
    const observed = observe();
    await flush();
    assert.equal((await observed.outcome).error.message, event);
    assert.deepEqual(observed.diagnostics.cdp.failure, { event, reason: "target gone" });
    assert.equal(observed.diagnostics.page.unavailable, event);
    assert.equal(fixture.socket().snapshots.length, 0);
    assert.equal(Date.now(), 0);
  });
}

test("a socket closing before open is diagnosed without waiting for the connection deadline", async t => {
  fakeCDP(t, { beforeOpen: socket => socket.failClose(1006, "browser quit") });
  const observed = observe();
  await flush();
  assert.match((await observed.outcome).error.message, /closed before opening/);
  assert.deepEqual(observed.diagnostics.cdp.close, { code: 1006, reason: "browser quit" });
  assert.match(observed.diagnostics.page.unavailable, /socket state 3/);
  assert.equal(Date.now(), 0);
});

test("a WebSocket construction error is available to startup failure formatting", async t => {
  fakeCDP(t, { constructorError: "invalid debugger URL" });
  const observed = observe();
  const { error } = await observed.outcome;
  assert.equal(observed.diagnostics.cdp.error, "invalid debugger URL");
  assert.match(observed.diagnostics.page.unavailable, /not established/);
  const failure = smokeFailure("motion-popup", url, error, observed.diagnostics, "Chrome exited");
  assert.ok(failure.message.includes(`Chrome motion-popup smoke failed (${url})`));
  assert.ok(failure.message.includes("Chrome exited"));
});

test("expected runtime errors do not turn a passed fixture into a failed smoke", async t => {
  const fixture = fakeCDP(t, {
    result: { status: "passed", detail: "expected error handling passed" },
    onObservation: socket => socket.emit(runtimeException("expected network failure")),
  });
  const observed = observe();
  assert.deepEqual((await observed.outcome).result, { status: "passed", detail: "expected error handling passed" });
  assert.equal(observed.diagnostics.runtimeExceptions[0].description, "expected network failure");
  assert.equal(fixture.socket().snapshots.length, 0);
  assert.equal(fixture.socket().closes, 1);
});

test("failed page assertions are preserved and snapshot/event output stays bounded", async t => {
  const fixture = fakeCDP(t, {
    result: { status: "failed", detail: "dynamic menu reveal missing" },
    changePage: page => {
      page.document.body.textContent = "b".repeat(8000);
      page.document.documentElement.dataset.motionCoveragePages = "r".repeat(5000);
      page.document.getAnimations = () => Array.from({ length: 30 }, () => ({
        id: "i".repeat(300), animationName: "n".repeat(300), playState: "running", currentTime: 42,
      }));
    },
    onObservation: socket => {
      for (let index = 0; index < 20; index++) {
        socket.emit(runtimeException(`${index}:` + "e".repeat(8000)));
        socket.emit({ method: "Runtime.consoleAPICalled", params: { type: "error", args: [{ value: "c".repeat(8000) }] } });
      }
    },
  });
  const observed = observe();
  const { result } = await observed.outcome;
  assert.equal(result.status, "failed");
  assert.equal(result.detail, "dynamic menu reveal missing");
  assert.equal(observed.diagnostics.page.marker, "failed");
  assert.equal(observed.diagnostics.page.result, result.detail);
  assert.deepEqual(fixture.socket().snapshots, [{ at: 0, closed: false }]);
  assert.equal(observed.diagnostics.page.body.length, 2048);
  assert.equal(observed.diagnostics.page.routeCoverage.length, 1024);
  assert.equal(observed.diagnostics.page.animations.length, 12);
  assert.equal(observed.diagnostics.page.animations[0].name.length, 128);
  assert.equal(observed.diagnostics.runtimeExceptions.length, 8);
  assert.ok(observed.diagnostics.runtimeExceptions[0].description.startsWith("12:"));
  assert.equal(observed.diagnostics.runtimeExceptions[0].description.length, 2048);
  assert.equal(observed.diagnostics.consoleErrors.length, 8);
  assert.equal(observed.diagnostics.consoleErrors[0].length, 512);
});
