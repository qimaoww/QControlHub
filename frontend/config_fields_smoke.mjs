import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { commonConfigFields } from "./modules/config-field-model.js";

const fields = [
  { key: "server", label: "监听地址", scope: "inbound", kind: "string" },
  { key: "mode", label: "转发模式", scope: "override", kind: "string", description: "TCP / UDP" },
  { key: "dns", label: "DNS", scope: "global", kind: "object" },
  { key: "outbound_udp_allow_fragmentation", label: "出站 UDP 分片", scope: "override", kind: "boolean" },
  { key: "servers", label: "服务端列表", scope: "structure", kind: "array" },
];

const structuralFields = ["inbounds", "endpoints", "outbounds", "listeners", "servers", "shadowsocks"]
  .map(key => ({key, label:key}));
for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
  assert.deepEqual(commonConfigFields(engine, [...fields, ...structuralFields]).map(field => field.key),
    ["mode", "dns", "outbound_udp_allow_fragmentation"], `${engine} common fields include inbound structures`);
}
assert.deepEqual(commonConfigFields("mihomo", [
  ...["port", "socks-port", "mixed-port", "redir-port", "tproxy-port", "tun", "tunnels", "tuic-server", "ss-config", "vmess-config"].map(key => ({key})),
  {key:"dns"}, {key:"log-level"},
]).map(field => field.key), ["dns", "log-level"], "native listeners must not be common-only edits");
assert.deepEqual(commonConfigFields("ss-rust", [{key:"unknown"}, {key:"dns", scope:"global"}]).map(field => field.key), ["dns"]);

const css = readFileSync(new URL("analytics/theme.css", import.meta.url), "utf8");
assert.match(css, /\.a-form-grid \{ display: grid; grid-template-columns: repeat\(3, minmax\(0, 1fr\)\)/);
assert.match(css, /\.a-field \{[^}]*min-width: 0/);
assert.match(css, /\.a-field textarea \{ resize: vertical/);
assert.match(css, /\.a-code \{ max-height: 55dvh; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; background: var\(--tint\)/);
assert.match(css, /\.a-code, \.a-editor textarea \{[^}]*font-size: var\(--text-label\)/);
assert.match(css, /@media \(max-width: 40rem\)/);
assert.match(css, /\.a-form-grid \{ grid-template-columns: minmax\(0, 1fr\)/);
assert.match(css, /\.a-button, \.a-nav-link, \.a-check, \.a-field input, \.a-field select \{ min-height: 44px/);

console.log("Config field studio smoke passed");
