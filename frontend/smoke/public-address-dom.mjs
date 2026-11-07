import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { formatHostPort } from "../modules/agent-addresses.js";
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

// Live address updates, source labels, copy targets and relay filtering run
// on the native page and details in browser/agent-overview.mjs.
}
