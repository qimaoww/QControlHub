import assert from "node:assert/strict";

import { installAgents } from "../modules/agents.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
// Runtime smoke: the real metrics patch path (pollAgentMetrics ->
// updateAgentMetrics -> updatePublicIPDisplays) updates the card rows and
// copy-button state instead of only the direct helper.
{
  const previousDocument = globalThis.document;
  const previousCSS = globalThis.CSS;
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  globalThis.setTimeout = () => 0;
  globalThis.clearTimeout = () => {};
  globalThis.CSS = { escape: (value) => String(value) };
  const metricState = {
    route: "node-settings",
    navigationEpoch: 1,
    data: {},
  };
  // Pre-populate with a stale address so the refresh must update every field
  // (text, title, copy dataset, copy aria, and the detail source) in place.
  const cardCodeV4 = { textContent: "2400:cb00::1", title: "2400:cb00::1" };
  const copyV4 = {
    dataset: { copyIp: "2400:cb00::1" },
    hidden: false,
    title: "复制 IPv4 地址",
    attrs: {},
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
  };
  const cardLineV4 = {
    hidden: false,
    dataset: {},
    querySelector(sel) {
      if (sel === "code") return cardCodeV4;
      if (sel === "[data-copy-ip]") return copyV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const cardContainer = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? cardLineV4 : null;
    },
  };
  const publicCodeV4 = { textContent: "", title: "" };
  const publicSmallV4 = { textContent: "" };
  const publicLineV4 = {
    hidden: false,
    dataset: {},
    querySelector(sel) {
      if (sel === "code") return publicCodeV4;
      if (sel === "small") return publicSmallV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const publicContainer = {
    classList: { contains(cls) { return cls === "node-public-ips"; } },
    querySelector(sel) {
      return sel === '.public-ip-row[data-ip-family="v4"]' ? publicLineV4 : null;
    },
  };
  const connectionNote = { textContent: "旧手动地址", hidden: false };
  const root = {
    dataset: { available: "0" },
    querySelector() { return null; },
    querySelectorAll(sel) {
      if (sel === ".node-card-ips, .node-public-ips") {
        return [cardContainer, publicContainer];
      }
      if (sel === "[data-node-connection-address]") return [connectionNote];
      return [];
    },
  };
  globalThis.document = {
    hidden: false,
    querySelector(sel) {
      return sel.includes("data-agent-metrics") ? root : null;
    },
    querySelectorAll() { return []; },
  };
  try {
    const { updateAgentMetrics } = installAgents(
      new Proxy(
        {
          state: metricState,
          engines: [],
          api: async () => [],
          optionalAPI: async () => null,
          can: () => true,
          esc: (value) => String(value ?? ""),
          engineName: (value) => value,
          serviceStatusName: (value) => value,
          statusTone: (value) => value,
          conciseVersion: (_engine, value) => value,
          ago: () => "刚刚",
          heartbeat: () => "刚刚",
          bytes: (value) => `${value || 0} B`,
          percent: (used, limit) => (limit ? Number(used || 0) / limit : 0),
          rate: (value) => `${value || 0} B/s`,
          actionName: (value) => value,
          serviceActionDisabled: () => false,
          trafficChart: () => "",
          renderConfigDiff: () => "",
          notify: () => {},
          confirmAction: () => {},
          short: (value) => value,
          date: (value) => value,
        },
        { get: (target, key) => target[key] ?? noop },
      ),
    );
    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "198.35.26.96", public_ipv6: "" },
      labels: { client_address: "93.184.216.34" },
      features: ["public-ip-probe-v1"],
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    // The one in-place refresh must update the card text and hover title, the
    // copy target/title/aria, and the detail source, without replacing the DOM.
    assert.equal(cardCodeV4.textContent, "93.184.216.34");
    assert.equal(cardLineV4.hidden, false);
    assert.equal(cardCodeV4.title, "93.184.216.34");
    assert.equal(cardLineV4.dataset.ipSource, "手动设置");
    assert.equal(copyV4.dataset.copyIp, "93.184.216.34");
    assert.equal(copyV4.title, "复制 IPv4 地址");
    assert.equal(copyV4.attrs["aria-label"], "复制 IPv4 公网地址 93.184.216.34");
    assert.equal(copyV4.hidden, false);
    assert.equal(publicCodeV4.textContent, "93.184.216.34");
    assert.equal(publicLineV4.hidden, false);
    assert.equal(publicCodeV4.title, "93.184.216.34");
    assert.equal(publicLineV4.dataset.ipSource, "手动设置");
    assert.equal(publicSmallV4.textContent, "手动设置");
    assert.equal(connectionNote.textContent, "");
    assert.equal(connectionNote.hidden, true);
    assert.equal(cardContainer.querySelector('.card-ip-row[data-ip-family="v4"]'), cardLineV4);
    assert.equal(publicContainer.querySelector('.public-ip-row[data-ip-family="v4"]'), publicLineV4);

    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "198.35.26.96", public_ipv6: "" },
      labels: {},
      features: ["public-ip-probe-v1"],
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    assert.equal(cardCodeV4.textContent, "198.35.26.96");
    assert.equal(cardLineV4.dataset.ipSource, "Agent 本地直连探测");
    assert.equal(publicSmallV4.textContent, "Agent 本地直连探测");
    assert.equal(connectionNote.textContent, "");
    assert.equal(connectionNote.hidden, true);

    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "::ffff:0101:0101", public_ipv6: "" },
      labels: {},
      features: ["public-ip-probe-v1"],
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    assert.equal(cardCodeV4.textContent, "1.1.1.1");
    assert.equal(cardLineV4.dataset.ipSource, "Agent 本地直连探测");
    assert.equal(copyV4.dataset.copyIp, "1.1.1.1");
    assert.equal(publicCodeV4.textContent, "1.1.1.1");
    assert.equal(publicSmallV4.textContent, "Agent 本地直连探测");
    assert.equal(cardContainer.querySelector('.card-ip-row[data-ip-family="v4"]'), cardLineV4);
    assert.equal(publicContainer.querySelector('.public-ip-row[data-ip-family="v4"]'), publicLineV4);
  } finally {
    if (previousSetTimeout === undefined) delete globalThis.setTimeout;
    else globalThis.setTimeout = previousSetTimeout;
    if (previousClearTimeout === undefined) delete globalThis.clearTimeout;
    else globalThis.clearTimeout = previousClearTimeout;
    if (previousCSS === undefined) delete globalThis.CSS;
    else globalThis.CSS = previousCSS;
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}

}
