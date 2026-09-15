import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { formatHostPort, updatePublicIPDisplays } from "../modules/agents.js";
export async function run() {
// CSS contract: the IPv4/IPv6 badge must override the <i> default italic and
// must not regress to the old tiny sizes; card and detail address text must
// keep the readability floor while staying on a single ellipsizing line.
{
  const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
  assert.equal(
    /font-size:[1-9]\d*(?:\.\d+)?px/.test(css),
    false,
    "positive pixel font sizes must use the shared typography scale",
  );
  assert.equal(
    /line-height:\d+(?:\.\d+)?px/.test(css),
    false,
    "pixel line heights must use the shared typography scale",
  );
  assert.equal(
    css.includes(".settings-section>.settings-hint{padding:0 20px 18px"),
    true,
    "settings helper text keeps the section's horizontal and bottom padding",
  );
  assert.equal(
    css.includes(".settings-section header p,.settings-hint{min-width:0;overflow-wrap:anywhere}"),
    true,
    "settings copy wraps instead of overflowing its card",
  );
  const badge = /\.ip-family\{[^}]*\}/.exec(css)?.[0] || "";
  assert.equal(
    badge.includes("font-style:normal"),
    true,
    ".ip-family must override the <i> default italic",
  );
  assert.equal(
    /\.ip-family\{[^}]*font-size:8px/.test(css),
    false,
    ".ip-family badge size must not regress to 8px",
  );
  const cardCode = /\.card-ip-row code\{[^}]*\}/.exec(css)?.[0] || "";
  const publicCode = /\.public-ip-row code\{[^}]*\}/.exec(css)?.[0] || "";
  assert.equal(
    /font-size:(?:11px|calc\(11px \* var\(--ui-font-scale\)\))/.test(cardCode),
    true,
    "card address text must be at least 11px before the shared scale",
  );
  assert.equal(
    /font-size:(?:12px|calc\(12px \* var\(--ui-font-scale\)\))/.test(publicCode),
    true,
    "detail address text must be at least 12px before the shared scale",
  );
  assert.equal(
    /\.card-ip-row code\{[^}]*font-size:9px/.test(css),
    false,
    "card address size must not regress to 9px",
  );
  assert.equal(
    css.includes(".card-ip-row code{min-width:0;max-width:calc(100% - 72px);flex:0 1 auto"),
    true,
    "card copy control must stay next to the address",
  );
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
