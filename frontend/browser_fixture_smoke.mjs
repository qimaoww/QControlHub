import assert from "node:assert/strict";
import { installAgentFixture } from "./browser/agent-fixture.mjs";
import { testConfigInboundsRuntime } from "./config_inbounds_browser_runtime.mjs";
import { testConfigInboundsRuntime as inboundsScenario } from "./browser/config-inbounds.mjs";
import { testUsersRuntime } from "./users_browser_runtime.mjs";
import { testUsersRuntime as allocationsScenario } from "./browser/users-allocations.mjs";

assert.equal(testConfigInboundsRuntime, inboundsScenario, "configuration browser entrypoint must retain its scenario API");
assert.equal(testUsersRuntime, allocationsScenario, "user browser entrypoint must retain its scenario API");

const originals = new Map(["window", "location"].map((key) => [
  key, Object.getOwnPropertyDescriptor(globalThis, key),
]));
try {
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { fetch: async () => new Response("catalog") },
  });
  Object.defineProperty(globalThis, "location", {
    configurable: true,
    value: { href: "https://panel.example.invalid/", hash: "#node-settings" },
  });
  const first = installAgentFixture("admin");
  const request = {
    method: "POST",
    headers: { "X-QControlHub-CSRF": "browser-test-csrf" },
    body: JSON.stringify({ name: "fixture-node" }),
  };
  const send = (options = request) => window.fetch("/api/v1/enrollment-tokens", options);
  assert.equal((await send()).status, 200);
  assert.equal(first.testAPI.lastEnrollmentRequest.name, "fixture-node");
  await assert.rejects(send({ ...request, headers: {} }), /缺少 CSRF 头/, "the extracted fixture must still enforce mutation authentication");
  first.testAPI.enrollmentFailure = true;
  const failed = await send();
  assert.equal(failed.status, 503, "injected failures must remain HTTP responses rather than fixture exceptions");
  assert.equal((await failed.json()).error, "temporary enrollment failure");

  const second = installAgentFixture("admin");
  assert.notEqual(second.testAPI, first.testAPI);
  assert.notEqual(second.populatedAgents, first.populatedAgents);
  assert.equal(second.testAPI.enrollmentFailure, false, "each scenario must have independent fixture state");
  assert.deepEqual(second.testAPI.calls, []);
  assert.equal((await send()).status, 200);
  assert.equal(second.testAPI.calls.length, 1);
  assert.equal(await (await window.fetch("/assets/preset-plans.json")).text(), "catalog");
} finally {
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }
}

console.log("Browser fixture isolation and CSRF smoke passed");
