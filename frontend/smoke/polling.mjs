import assert from "node:assert/strict";

import { poll } from "../analytics/live-dom.js";

import { mergeVisibleTrafficCardOrder, mergeTrafficPorts, orderTrafficItems } from "../modules/traffic-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const pollingDocument = globalThis.document;
globalThis.document = {
  hidden: false,
  activeElement: null,
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  // Log polling now runs against the native UI in browser/core-logs.mjs:
  // per-engine windows, bounded pagination, failure/recovery, unchanged DOM,
  // visibility, reading anchors, text selection and route disposal. Source
  // states and account/grant races run in its two focused browser suites.
  // Native traffic polling, readonly scope, applied endpoint refresh, stable
  // identities and quota reset run in browser/traffic-layout.mjs.
  assert.deepEqual(
    mergeTrafficPorts(
      [{ deployment_confirmed: true, is_current_user: true, id: "policy-a", agent_id: "alpha", engine: "xray", port: 443 }],
      [{ agent_id: "alpha", engine: "xray", port: 443, protocol: "tcp" }, { agent_id: "alpha", engine: "xray", port: 8443, protocol: "udp" }, { agent_id: "alpha", engine: "sing-box", port: 8443, protocol: "tcp" }],
    ).map((item) => item.kind),
    ["policy", "endpoint"],
    "configured ports merge with policies by the Agent-wide port identity",
  );
  assert.deepEqual(
    mergeTrafficPorts(
      [{ deployment_confirmed: true, is_current_user: true, id: "policy-hidden", agent_id: "alpha", engine: "xray", port: 443, monitoring_enabled: false }],
      [{ agent_id: "alpha", engine: "xray", port: 443, protocol: "tcp" }],
    ),
    [],
    "deleted monitoring stays hidden even while the configured endpoint is still discoverable",
  );
  assert.deepEqual(
    orderTrafficItems(
      [
        { policy: { agent_id: "alpha", port: 443 } },
        { endpoint: { agent_id: "beta", port: 8443 } },
        { policy: { agent_id: "gamma", port: 80 } },
      ],
      ["beta:8443", "alpha:443"],
    ).map((item) => (item.policy || item.endpoint).agent_id),
    ["beta", "alpha", "gamma"],
    "traffic card order appends newly discovered ports after persisted cards",
  );
  assert.deepEqual(
    mergeVisibleTrafficCardOrder(
      ["alpha:443", "beta:8443", "gamma:80"],
      ["gamma:80", "alpha:443"],
    ),
    ["gamma:80", "beta:8443", "alpha:443"],
    "filtered traffic reordering preserves hidden card positions",
  );

  // Exercise the shipped poller rather than a retired route's timer loop.
  const previousSetTimeout = globalThis.setTimeout, previousClearTimeout = globalThis.clearTimeout;
  const timers = new Map(); let timerID = 0;
  globalThis.setTimeout = callback => { timers.set(++timerID, callback); return timerID; };
  globalThis.clearTimeout = id => timers.delete(id);
  globalThis.document = Object.assign(new EventTarget(), { hidden: false });
  const controller = new AbortController();
  let calls = 0, release, fail = false, current = true;
  const errors = [];
  try {
    const tick = poll({ signal: controller.signal, current: () => current, onRefreshError: error => errors.push(error.message) }, async () => {
      calls++;
      if (fail) throw new Error('temporary native poll failure');
      await new Promise(resolve => { release = resolve; });
    });
    assert.equal(timers.size, 1, 'native polling starts with one timer');
    const active = tick(); await tick();
    assert.equal(calls, 1, 'concurrent native ticks duplicate a fleet request');
    release(); await active;
    assert.equal(timers.size, 1, 'native completion schedules more than one timer');
    fail = true; await tick();
    assert.deepEqual(errors, ['temporary native poll failure'], 'native polling does not report a transient failure');
    assert.equal(timers.size, 1, 'failed native polling cannot retry');
    fail = false; const retry = tick(); await Promise.resolve();
    assert.equal(calls, 3, 'native polling does not retry after failure');
    release(); await retry;
    globalThis.document.hidden = true; await tick();
    assert.equal(calls, 3, 'hidden native page still requests the fleet');
    assert.equal(timers.size, 1, 'hidden native polling loses its resume timer');
    globalThis.document.hidden = false;
    current = false; await tick();
    assert.equal(calls, 3, 'departed native route requests old fleet');
    assert.equal(timers.size, 0, 'departed native route retains a timer');
    current = true;
    const pending = tick(); await Promise.resolve(); current = false; controller.abort(); release(); await pending;
    assert.equal(timers.size, 0, 'disposing an in-flight native poll retains a timer');
  } finally {
    current = false; controller.abort();
    globalThis.setTimeout = previousSetTimeout; globalThis.clearTimeout = previousClearTimeout;
  }
} finally {
  if (pollingDocument === undefined) delete globalThis.document;
  else globalThis.document = pollingDocument;
}

}
