import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { formatHostPort, updatePublicIPDisplays } from "../modules/agents.js";
export async function run() {
// The native node surface must keep address families upright and readable,
// wrap long IPv6 values, and reserve the adjacent copy control's hit target.
{
  const css = readFileSync(new URL("../analytics/theme.css", import.meta.url), "utf8");
  const nodes = readFileSync(new URL("../analytics/nodes.js", import.meta.url), "utf8");
  const ui = readFileSync(new URL("../analytics/ui.js", import.meta.url), "utf8");
  assert.match(nodes, /muted\(row.label\)/);
  assert.match(ui, /muted = \(message\) => node\("span"/);
  assert.match(css, /\.a-probe-address \{[^}]*font-size: 12px/);
  assert.match(css, /\.a-probe-address \.a-muted \{ font-size: 11px/);
  assert.match(css, /\.a-probe-address-value \{[^}]*grid-template-columns: minmax\(0,1fr\) auto/);
  assert.match(css, /\.a-probe-address-value > \[dir\] \{ min-width: 0; overflow-wrap: anywhere/);
  assert.match(css, /\.a-ip-copy.a-button \{ flex: none/);
  assert.match(css, /@media \(pointer: coarse\) \{ \.a-ip-copy.a-button \{ width: 44px; min-height: 44px/);
}

assert.equal(
  formatHostPort("2606:4700:4700::1111", "443"),
  "[2606:4700:4700::1111]:443",
);
assert.equal(
  formatHostPort("[2606:4700:4700::1111]", "443"),
  "[2606:4700:4700::1111]:443",
);
assert.equal(formatHostPort("[::1]", "443"), "[::1]:443");
assert.equal(formatHostPort("93.184.216.34", "443"), "93.184.216.34:443");
assert.equal(formatHostPort("node.example.com", "8443"), "node.example.com:8443");
assert.equal(formatHostPort("", "443"), "");

{
  const codeV4 = { textContent: "" };
  const copyV4 = {
    dataset: { copyIp: "" },
    hidden: true,
    title: "",
    setAttribute() {},
  };
  const lineV4 = {
    hidden: true,
    querySelector(sel) {
      if (sel === "code") return codeV4;
      if (sel === "[data-copy-ip]") return copyV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? lineV4 : null;
    },
  };
  const root = {
    dataset: {},
    querySelector() { return null; },
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, { public_ipv4: "198.35.26.10", public_ipv6: "" });
  assert.equal(lineV4.hidden, false);
  assert.equal(codeV4.textContent, "198.35.26.10");
  assert.equal(copyV4.dataset.copyIp, "198.35.26.10");
  assert.equal(copyV4.hidden, false);
}

{
  const code = { textContent: "" };
  const small = { textContent: "" };
  const line = {
    hidden: true,
    querySelector(sel) {
      if (sel === "code") return code;
      if (sel === "small") return small;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-public-ips"; } },
    querySelector(sel) {
      return sel === '.public-ip-row[data-ip-family="v6"]' ? line : null;
    },
  };
  const root = {
    dataset: {},
    querySelector() { return null; },
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, { public_ipv6: "2001:4860:4860::8888" });
  assert.equal(line.hidden, false);
  assert.equal(code.textContent, "2001:4860:4860::8888");
  assert.equal(small.textContent, "Agent 本地直连探测");
  updatePublicIPDisplays(root, { public_ipv6: "" });
  assert.equal(line.hidden, true, "undetected IPv6 row is hidden in place");
}

{
  const code = { textContent: "", title: "" };
  const copy = {
    dataset: { copyIp: "" },
    hidden: true,
    title: "",
    attrs: {},
    setAttribute(name, value) {
      this.attrs[name] = value;
    },
  };
  const line = {
    hidden: false,
    dataset: {},
    querySelector(sel) {
      if (sel === "code") return code;
      if (sel === "[data-copy-ip]") return copy;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? line : null;
    },
  };
  const root = {
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, {
    collected_at: "now",
    public_ipv4: "172.69.135.152",
    observed_public_ip: "93.184.216.34",
  });
  assert.equal(line.hidden, false, "relay probe falls back to verified WSS in place");
  assert.equal(code.textContent, "93.184.216.34");
  assert.equal(copy.dataset.copyIp, "93.184.216.34");
  assert.equal(copy.hidden, false);
  updatePublicIPDisplays(root, { collected_at: "now", public_ipv4: "104.22.17.83" });
  assert.equal(line.hidden, true, "relay-only probe hides the IPv4 row");
  assert.equal(copy.hidden, true, "relay-only probe removes the copy target");
  updatePublicIPDisplays(root, { public_ipv4: "198.35.26.96" });
  assert.equal(line.hidden, false, "a later genuine probe restores the same row");
  assert.equal(code.textContent, "198.35.26.96");
  assert.equal(copy.dataset.copyIp, "198.35.26.96");
  assert.equal(copy.hidden, false);
}

}
