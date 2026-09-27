import { assert, waitFor } from "./assertions.mjs";

// Client exports shaped like a real fleet: several engines per node, long names,
// single-line YAML, an offline node and a node that still needs an address.
const ss = (host, port, name) => `ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206ZXhhbXBsZS1wYXNzd29yZA@${host}:${port}#${encodeURIComponent(name)}`;
const vless = (host, port, name) => `vless://7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b@${host}:${port}?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sid=6ba85179e30d4fc2&type=tcp#${encodeURIComponent(name)}`;
const field = (label, value, secret = false) => ({ label, value, secret });
const ssFields = (host, port) => [field("协议", "Shadowsocks 2022"), field("服务器", host), field("端口", String(port)), field("加密方法", "2022-blake3-aes-128-gcm"), field("密码", "ZXhhbXBsZS1wYXNzd29yZA==", true)];
const vlessFields = (host, port) => [field("协议", "VLESS"), field("服务器", host), field("端口", String(port)), field("UUID", "7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b", true), field("传输", "tcp"), field("传输安全", "reality"), field("TLS ServerName", "www.example.com"), field("Reality Public Key", "Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw"), field("Reality Short ID", "6ba85179e30d4fc2"), field("客户端指纹", "chrome")];
const profile = (tag, port, protocol, format, uri, fields, extra = {}) => {
  const value = (label) => fields.find((field) => field.label === label)?.value;
  const type = protocol.startsWith("Shadowsocks") ? "ss" : protocol.startsWith("VLESS") ? "vless" : protocol === "Hysteria 2" ? "hysteria2" : protocol === "TUIC v5" ? "tuic" : protocol.toLowerCase();
  const proxy = { name: extra.client_name || tag, type, server: value("服务器"), port };
  if (type === "ss") Object.assign(proxy, { cipher: value("加密方法"), password: value("密码") });
  if (type === "vless") Object.assign(proxy, { uuid: value("UUID"), tls: true, flow: "xtls-rprx-vision", servername: value("TLS ServerName"), "client-fingerprint": "chrome", "reality-opts": { "public-key": value("Reality Public Key"), "short-id": value("Reality Short ID") } });
  if (type === "mieru") Object.assign(proxy, { username: value("用户名"), password: value("用户密码"), transport: "TCP" });
  if (["trojan", "hysteria2", "anytls"].includes(type)) proxy.password = value("密码");
  if (type === "tuic") Object.assign(proxy, { uuid: "7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b", password: "example-password" });
  const mihomo_yaml = `{${Object.entries(proxy).map(([key, value]) => `${key}: ${JSON.stringify(value)}`).join(", ")}}`;
  return { tag, port, protocol, address_mode: "auto", ...extra, mihomo_yaml, profile: { format, uri, fields } };
};
const agent = (id, name, region_code, status = "online") => ({
  id, name, region_code, status, os: "Debian", arch: "amd64", version: "1.2.3", can_manage: true,
  capabilities: ["mihomo", "xray", "sing-box", "ss-rust"], supported_capabilities: ["mihomo", "xray", "sing-box", "ss-rust"],
  features: ["agent-self-upgrade-v1"], labels: {}, metrics: {}, runtime: {},
  last_seen: "2026-09-20T00:00:00Z", enrolled_at: "2026-08-24T00:00:00Z",
});
const entry = (node, engine, address, profiles, extra = {}) => ({
  agent_id: node.id, agent_name: node.name, agent_status: node.status, engine, address, source: "公网 IPv4",
  address_mode: "auto", profiles, ...extra,
});

export function clientLayoutFixture() {
  const agents = [
    agent("hk-01", "香港 · HK-01", "HK"),
    agent("lax-02", "洛杉矶 · LAX-02", "US"),
    agent("mad-03", "马德里 · MAD-03", "ES"),
    agent("sg-04", "新加坡 · SG-04", "SG"),
    agent("hk-05", "香港 · HK-05", "HK"),
    agent("sjc-06", "圣何塞 · SJC-06", "US"),
    agent("fra-07", "法兰克福 · Frankfurt-edge-with-a-very-long-node-name-07", "DE", "offline"),
    agent("jp-08", "东京 · JP-08", "JP"),
  ];
  const [hk, lax, mad, sg, hk5, sjc, fra, jp] = agents;
  const laxProfiles = (host) => [
    profile("ss-rust-1", 44667, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss(host, 44667, "LAX-02-v4"), ssFields(host, 44667), { client_name: "LAX-02-v4", address: host }),
    profile("ss-rust-2", 21026, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("[2001:db8::21]", 21026, "LAX-02-v6"), ssFields("2001:db8::21", 21026), { client_name: "LAX-02-v6", address: "[2001:db8::21]", address_mode: "ipv6" }),
  ];
  const entries = [
    entry(hk, "ss-rust", "203.0.113.17", [profile("ss-rust-1", 52717, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("203.0.113.17", 52717, "ss-rust-1"), ssFields("203.0.113.17", 52717))]),
    entry(lax, "ss-rust", "198.51.100.41", laxProfiles("198.51.100.41"), {
      address_options: [
        { family: "ipv4", address: "198.51.100.41", source: "公网 IPv4", profiles: laxProfiles("198.51.100.41") },
        { family: "ipv6", address: "2001:db8::21", source: "公网 IPv6", profiles: laxProfiles("[2001:db8::21]") },
      ],
    }),
    entry(mad, "ss-rust", "192.0.2.19", [profile("ss2022-b1ca1314", 31164, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("192.0.2.19", 31164, "ss2022-b1ca1314"), ssFields("192.0.2.19", 31164))]),
    entry(sg, "sing-box", "203.0.113.84", [profile("VLESS-REALITY-443.json", 443, "VLESS-Vision-uTLS-REALITY", "VLESS URI", vless("203.0.113.84", 443, "VLESS-REALITY-443.json"), vlessFields("203.0.113.84", 443))]),
    entry(sg, "ss-rust", "203.0.113.84", [profile("ss-rust-1", 38092, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("203.0.113.84", 38092, "ss-rust-1"), ssFields("203.0.113.84", 38092))]),
    entry(hk5, "xray", "198.51.100.197", [profile("HK-05-VLESS", 443, "VLESS-ENC-TCP-Vision-uTLS-REALITY", "VLESS URI", vless("198.51.100.197", 443, "HK-05-VLESS"), vlessFields("198.51.100.197", 443), { client_name: "香港 HK-05 · VLESS Reality 主线路" })]),
    entry(sjc, "mihomo", "192.0.2.30", [
      profile("vless-eeeafb5f", 443, "VLESS-Vision-uTLS-REALITY", "VLESS URI", vless("192.0.2.30", 443, "vless-eeeafb5f"), vlessFields("192.0.2.30", 443)),
      profile("mieru-in", 27015, "Mieru", "Mihomo Mieru YAML", "- name: mieru-in\n  type: mieru\n  server: 192.0.2.30\n  port: 27015\n  transport: TCP\n  username: client\n  password: example-password\n  multiplexing: MULTIPLEXING_LOW", [field("协议", "Mieru"), field("服务器", "192.0.2.30"), field("端口", "27015"), field("用户名", "client"), field("用户密码", "example-password", true), field("传输", "TCP")]),
    ]),
    entry(fra, "sing-box", "198.51.100.7", [
      profile("hy2-in", 8443, "Hysteria 2", "Hysteria 2 URI", "hysteria2://example-password@198.51.100.7:8443?sni=www.example.com#hy2-in", [field("协议", "Hysteria 2"), field("服务器", "198.51.100.7"), field("端口", "8443"), field("密码", "example-password", true)]),
      profile("tuic-in", 8444, "TUIC v5", "TUIC v5 URI", "tuic://7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b:example-password@198.51.100.7:8444?congestion_control=bbr&alpn=h3#tuic-in", [field("协议", "TUIC v5"), field("服务器", "198.51.100.7"), field("端口", "8444")]),
      profile("anytls-in", 8445, "AnyTLS", "AnyTLS URI", "anytls://example-password@198.51.100.7:8445?sni=www.example.com#anytls-in", [field("协议", "AnyTLS"), field("服务器", "198.51.100.7"), field("端口", "8445")]),
    ]),
    entry(fra, "xray", "198.51.100.7", [profile("trojan-in", 9443, "Trojan", "Trojan URI", "trojan://example-password@198.51.100.7:9443?security=tls&sni=www.example.com#trojan-in", [field("协议", "Trojan"), field("服务器", "198.51.100.7"), field("端口", "9443"), field("密码", "example-password", true)])]),
    entry(jp, "sing-box", "", [], { address_required: true, source: "" }),
  ];
  return { agents, entries };
}

export async function testClientLayoutRuntime({ testAPI }) {
  await waitFor(() => document.querySelectorAll(".client-access-node-card").length === 8, "client cards did not load");
  if (new URLSearchParams(location.search).has("preview")) return;
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "client page overflows the viewport");
  assert.ok(document.querySelector('#client-search [name="q"]').getBoundingClientRect().width >= 140, "search input must remain usable beside its button");
  const query = (selector) => document.querySelector(selector);
  const count = (selector) => document.querySelectorAll(selector).length;
  const selectFormat = (format) => {
    const select = query("[data-client-display-format]");
    select.value = format;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  };
  query('[data-filter-engine="mihomo"]').click();
  assert.equal(count(".client-profile-row"), 2, "engine filter must show only Mihomo profiles");
  assert.ok(query(".client-export-unavailable"), "native-only protocols must not be mislabeled as URL");
  selectFormat("mihomo");
  assert.equal(count(".client-export-control input"), 2, "format selector must keep every export on one line");
  const yaml = query(".client-export-control input");
  yaml.parentElement.querySelector("[data-secret-visibility]").click();
  assert.equal(yaml.type, "text", "YAML must be revealable");
  assert.ok(yaml.value.startsWith("{") && !/[\r\n]/.test(yaml.value), "YAML must be a single-line Sub-Store proxy map");
  yaml.parentElement.querySelector("[data-secret-visibility]").click();
  assert.equal(yaml.type, "password", "YAML must be maskable again");
  selectFormat("url");
  assert.equal(count("textarea.client-share-yaml"), 0, "switching back must restore URL values");
  query('[data-filter-engine=""]').click();
  query('#client-search [name="q"]').value = "LAX-02-v6";
  query('#client-search button[type="submit"]').click();
  assert.equal(count(".client-profile-row"), 1, "search must match a custom client name");
  assert.ok(query('.client-profile-row>header').textContent.includes("端口 21026"), "port metadata must stay visible");
  query("[data-client-parameter-open]").click();
  assert.ok(query(".client-parameter-dialog[open]"), "parameter details must open");
  query(".client-parameter-dialog[open] [data-client-parameter-close]").click();
  query("[data-client-display-open]").click();
  assert.ok(query(".client-display-dialog[open]"), "display settings must open");
  query(".client-display-dialog[open] [data-client-display-close]").click();
  query("[data-clear-search]").click();
  assert.equal(count(".client-access-node-card"), 8, "clearing search must restore every node");
  query('#client-search [name="q"]').value = "no-such-client";
  query("#client-search").requestSubmit();
  assert.equal(count(".client-profile-row"), 0, "unmatched search must show no profiles");
  query("[data-clear-client-filters]").click();
  assert.equal(count(".client-access-node-card"), 8, "empty-state reset must restore results");
  query("[data-refresh-client-access]").click();
  await waitFor(() => !query("[data-refresh-client-access]").disabled, "refresh must finish");
  selectFormat("mihomo");
  const missing = testAPI.clientAccessEntries[0].profiles[0];
  const savedYAML = missing.mihomo_yaml;
  missing.mihomo_yaml = "";
  missing.mihomo_error = "此协议不支持 Mihomo YAML";
  query("[data-refresh-client-access]").click();
  await waitFor(() => query(".client-export-unavailable"), "unsupported format must explain the missing export");
  assert.equal(query("[data-client-display-format]").value, "mihomo", "refresh must preserve display format");
  assert.equal(query(".client-export-unavailable").closest(".client-profile-row").querySelector(".client-export-control"), null, "unsupported YAML must not copy the URL");
  missing.mihomo_yaml = savedYAML;
  delete missing.mihomo_error;
  selectFormat("url");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "filtered client page overflows the viewport");
}
