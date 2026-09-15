import assert from "node:assert/strict";

import { manualConnectionAddressNote, publicAddressRows } from "../modules/agents.js";
export async function run() {
// Dual-stack public address display regressions.
const ipRows = publicAddressRows({
  observed_public_ip: "93.184.216.34",
  public_ipv4: "198.35.26.96",
  public_ipv6: "2001:4860:4860::8888",
});
assert.equal(ipRows.length, 2);
assert.equal(ipRows[0].value, "198.35.26.96");
assert.equal(ipRows[0].source, "Agent 本地直连探测");
assert.equal(ipRows[0].ok, true);
assert.equal(ipRows[1].value, "2001:4860:4860::8888");

const managedProbeRows = publicAddressRows({
  public_ipv4: "198.35.26.96",
  public_ipv4_source: "control-plane-config",
});
assert.equal(managedProbeRows[0].source, "控制面配置的 Agent 直连探测");
assert.equal(publicAddressRows({
  public_ipv4: "198.35.26.96",
  public_ipv4_source: "untrusted-source",
})[0].value, "", "unknown probe provenance must fail closed");

const fallbackRows = publicAddressRows({ observed_public_ip: "93.184.216.34" });
assert.equal(fallbackRows[0].value, "93.184.216.34");
assert.equal(fallbackRows[0].source, "已验证连接来源");
assert.equal(fallbackRows[1].value, "");
assert.equal(fallbackRows[1].ok, false);

const manualRows = publicAddressRows(
  {
    public_ipv4: "198.35.26.96",
    public_ipv6: "2001:4860:4860::8888",
  },
  {
    client_address: "198.35.26.10",
    public_ip: "93.184.216.34",
  },
  ["public-ip-probe-v1"],
);
assert.equal(manualRows[0].value, "198.35.26.10");
assert.equal(manualRows[0].source, "手动设置");
assert.equal(manualRows[1].value, "2001:4860:4860::8888");
assert.equal(manualRows[1].source, "Agent 本地直连探测");

// A hostname remains the highest-priority client connection setting, while a
// supported public_ip literal may still fill its own family without DNS
// inference or pretending that the hostname is an IP address.
const manualDomainRows = publicAddressRows(
  { public_ipv4: "198.35.26.96" },
  { client_address: "node.example.com", public_ip: "93.184.216.34" },
);
assert.equal(manualDomainRows[0].value, "93.184.216.34");
assert.equal(manualDomainRows[0].source, "节点公网 IP");
assert.equal(manualDomainRows[1].value, "");
assert.equal(manualDomainRows[1].source, "公网探测未启用 · 可手动设置");

assert.equal(
  publicAddressRows({}, {}, [])[0].source,
  "公网探测未启用 · 可手动设置",
);
assert.equal(
  publicAddressRows({}, {}, ["public-ip-probe-v1"])[0].source,
  "公网探测已启用 · 等待结果",
);
assert.equal(
  publicAddressRows({}, {}, ["managed-public-ip-probe-v1"])[0].source,
  "公网探测未启用 · 可手动设置",
);
assert.equal(
  publicAddressRows({ collected_at: "now" }, {}, ["public-ip-probe-v1"])[0].source,
  "无可验证公网地址 · 可手动设置",
);

const manualIPv6Rows = publicAddressRows(
  { public_ipv6: "2620:4f:8001::1" },
  { client_address: "[2001:4860:4860::8888]" },
);
assert.equal(manualIPv6Rows[1].value, "2001:4860:4860::8888");
assert.equal(manualIPv6Rows[1].source, "手动设置");

const invalidManualRows = publicAddressRows(
  { public_ipv4: "198.35.26.96" },
  {
    client_address: "100.64.0.8",
    public_ip: "198.19.0.1",
    public_host: "2606:4700:4700::1111%eth0",
  },
  ["public-ip-probe-v1"],
);
assert.equal(invalidManualRows[0].value, "198.35.26.96");
assert.equal(invalidManualRows[0].source, "Agent 本地直连探测");
assert.equal(invalidManualRows[1].value, "");
assert.equal(invalidManualRows[1].ok, false);

const leadingZeroRows = publicAddressRows(
  {},
  { client_address: "001.001.001.001", public_ip: "01.2.3.4" },
);
assert.equal(leadingZeroRows[0].value, "");
assert.equal(leadingZeroRows[0].ok, false);
assert.equal(leadingZeroRows[1].value, "");
assert.equal(
  manualConnectionAddressNote({ client_address: "001.001.001.001" }),
  "手动连接地址：001.001.001.001",
);
assert.equal(
  manualConnectionAddressNote({ public_ip: "01.2.3.4" }),
  "手动连接地址：01.2.3.4",
);

const mappedLeadingZeroRows = publicAddressRows(
  {},
  { client_address: "::ffff:001.001.001.001", public_ip: "::ffff:1.2.3.004" },
);
assert.equal(mappedLeadingZeroRows[0].value, "");
assert.equal(mappedLeadingZeroRows[0].ok, false);
assert.equal(
  manualConnectionAddressNote({ client_address: "::ffff:001.001.001.001" }),
  "手动连接地址：::ffff:001.001.001.001",
);

// IPv4-mapped IPv6 has the same family and IANA policy as its unmapped IPv4.
// Hexadecimal and dotted spellings must therefore converge before display or
// copy eligibility is decided.
const mappedHexRows = publicAddressRows(
  {},
  { client_address: "::ffff:c000:201", public_ip: "::ffff:0a00:1" },
);
assert.equal(mappedHexRows[0].value, "");
assert.equal(mappedHexRows[0].ok, false);
assert.equal(mappedHexRows[1].value, "");
assert.equal(mappedHexRows[1].ok, false);

const mappedGlobalRows = publicAddressRows(
  {},
  { client_address: "::ffff:0101:0101" },
);
assert.equal(mappedGlobalRows[0].value, "1.1.1.1");
assert.equal(mappedGlobalRows[0].source, "手动设置");
assert.equal(mappedGlobalRows[1].value, "");

const embeddedIPv4Rows = publicAddressRows(
  {},
  { client_address: "2001:4860::192.0.2.1" },
);
assert.equal(embeddedIPv4Rows[0].value, "");
assert.equal(embeddedIPv4Rows[1].value, "2001:4860::192.0.2.1");
assert.equal(embeddedIPv4Rows[1].source, "手动设置");
const embeddedLeadingZeroRows = publicAddressRows(
  {},
  { client_address: "2001:4860::192.0.2.001" },
);
assert.equal(embeddedLeadingZeroRows[0].value, "");
assert.equal(embeddedLeadingZeroRows[1].value, "");

const normalizedProbeRows = publicAddressRows(
  {
    public_ipv4: "::ffff:0101:0101",
    public_ipv6: "2001:4860::192.0.2.1",
  },
  {},
  ["public-ip-probe-v1"],
);
assert.equal(normalizedProbeRows[0].value, "1.1.1.1");
assert.equal(normalizedProbeRows[0].source, "Agent 本地直连探测");
assert.equal(normalizedProbeRows[1].value, "2001:4860::192.0.2.1");
assert.equal(normalizedProbeRows[1].source, "Agent 本地直连探测");

const normalizedObservedRows = publicAddressRows({
  observed_public_ip: "::ffff:0101:0101",
});
assert.equal(normalizedObservedRows[0].value, "1.1.1.1");
assert.equal(normalizedObservedRows[0].source, "已验证连接来源");
assert.equal(normalizedObservedRows[1].value, "");

const mappedInterfaceRows = publicAddressRows({
  network_interfaces: [
    { name: "eth0", addresses: ["::ffff:0101:0101", "2001:4860::192.0.2.1"] },
  ],
});
assert.equal(mappedInterfaceRows[0].value, "1.1.1.1");
assert.equal(mappedInterfaceRows[0].source, "默认路由接口 eth0");
assert.equal(mappedInterfaceRows[1].value, "2001:4860::192.0.2.1");
assert.equal(mappedInterfaceRows[1].source, "默认路由接口 eth0");

const canonicalManualRows = publicAddressRows(
  {},
  { client_address: "1.1.1.1" },
);
assert.equal(canonicalManualRows[0].value, "1.1.1.1");
assert.equal(canonicalManualRows[0].source, "手动设置");
assert.equal(manualConnectionAddressNote({ client_address: "1.1.1.1" }), "");

// An observed relay (e.g. a Cloudflare edge) must never beat a genuine
// default-route interface address that reports the same family.
const relayRows = publicAddressRows({
  observed_public_ip: "2400:cb00::1",
  network_interfaces: [{ name: "eth0", addresses: ["2001:4860:4860::8888"] }],
});
assert.equal(relayRows[0].value, "");
assert.equal(relayRows[0].source, "公网探测未启用 · 可手动设置");
assert.equal(relayRows[0].ok, false);
assert.equal(relayRows[1].value, "2001:4860:4860::8888");
assert.equal(relayRows[1].source, "默认路由接口 eth0");

for (const relayAddress of [
  "104.22.17.83",
  "172.69.135.152",
  "162.158.193.59",
  "172.64.217.32",
  "172.71.124.82",
  "172.68.225.178",
]) {
  const rows = publicAddressRows({ observed_public_ip: relayAddress });
  assert.equal(rows[0].value, "", `Cloudflare relay ${relayAddress} must not be displayed`);
  assert.equal(rows[0].ok, false, `Cloudflare relay ${relayAddress} must not be copyable`);
}
const mappedRelayRows = publicAddressRows({ observed_public_ip: "::ffff:172.69.135.152" });
assert.equal(mappedRelayRows[0].value, "", "mapped Cloudflare relay must be filtered");
const cloudflareIPv6Rows = publicAddressRows({ observed_public_ip: "2606:4700::1111" });
assert.equal(cloudflareIPv6Rows[1].value, "", "Cloudflare IPv6 relay must be filtered");
const cloudflareInterfaceRows = publicAddressRows({
  network_interfaces: [{ name: "eth0", addresses: ["104.22.17.83", "2606:4700::1111"] }],
});
assert.equal(cloudflareInterfaceRows[0].value, "", "Cloudflare IPv4 interface must be filtered");
assert.equal(cloudflareInterfaceRows[1].value, "", "Cloudflare IPv6 interface must be filtered");
const relayProbeFallbackRows = publicAddressRows({
  collected_at: "now",
  public_ipv4: "172.69.135.152",
  public_ipv6: "::ffff:172.69.135.152",
  network_interfaces: [
    { name: "eth0", addresses: ["198.35.26.96", "2001:4860:4860::8888"] },
  ],
});
assert.equal(relayProbeFallbackRows[0].value, "198.35.26.96");
assert.equal(relayProbeFallbackRows[0].source, "默认路由接口 eth0");
assert.equal(relayProbeFallbackRows[1].value, "2001:4860:4860::8888");
assert.equal(relayProbeFallbackRows[1].source, "默认路由接口 eth0");
const relayProbeVerifiedFallbackRows = publicAddressRows({
  collected_at: "now",
  public_ipv4: "104.22.17.83",
  observed_public_ip: "93.184.216.34",
});
assert.equal(relayProbeVerifiedFallbackRows[0].value, "93.184.216.34");
assert.equal(relayProbeVerifiedFallbackRows[0].source, "已验证连接来源");
const realObservedRows = publicAddressRows({ observed_public_ip: "93.184.216.34" });
assert.equal(realObservedRows[0].value, "93.184.216.34");
assert.equal(realObservedRows[0].source, "已验证连接来源");
assert.equal(relayRows[1].ok, true);

// Interface fallback is strictly filtered: private, CGNAT, documentation and
// link-local addresses are dropped while a truly routable address surfaces.
const filteredRows = publicAddressRows({
  network_interfaces: [
    { name: "tailscale0", addresses: ["100.64.0.8", "fd00::8"] },
    { name: "docker0", addresses: ["172.17.0.1"] },
    { name: "eth0", addresses: ["192.0.2.9", "198.35.26.96", "2001:db8::8", "2001:4860:4860::8888", "fe80::1%eth0"] },
  ],
});
assert.equal(filteredRows[0].value, "198.35.26.96");
assert.equal(filteredRows[0].source, "默认路由接口 eth0");
assert.equal(filteredRows[1].value, "2001:4860:4860::8888");
assert.equal(filteredRows[1].source, "默认路由接口 eth0");

// The frontend IANA special-purpose policy must stay equivalent to Go
// internal/netpolicy instead of the loose IsGlobalUnicast. These are exact
// boundary cases: the benchmark /15 covers both 198.18 and 198.19, the /48
// special range covers 2620:4f:8000, and a zoned global address is scoped.
const boundaryRows = publicAddressRows({
  network_interfaces: [
    { name: "eth0", addresses: ["198.19.0.1", "198.18.0.1", "2620:4f:8000::1", "2606:4700:4700::1111%eth0"] },
  ],
});
assert.equal(boundaryRows[0].value, "");
assert.equal(boundaryRows[0].source, "公网探测未启用 · 可手动设置");
assert.equal(boundaryRows[0].ok, false);
assert.equal(boundaryRows[1].value, "");
assert.equal(boundaryRows[1].source, "公网探测未启用 · 可手动设置");
assert.equal(boundaryRows[1].ok, false);

const outsideBoundaryRows = publicAddressRows({
  network_interfaces: [
    { name: "eth0", addresses: ["198.20.0.1", "2620:4f:8001::1"] },
  ],
});
assert.equal(outsideBoundaryRows[0].value, "198.20.0.1");
assert.equal(outsideBoundaryRows[0].source, "默认路由接口 eth0");
assert.equal(outsideBoundaryRows[0].ok, true);
assert.equal(outsideBoundaryRows[1].value, "2620:4f:8001::1");
assert.equal(outsideBoundaryRows[1].source, "默认路由接口 eth0");
assert.equal(outsideBoundaryRows[1].ok, true);

// The same denylist also guards probed and verified-WSS fallbacks, so a
// special-purpose or zoned value can never be shown or copied even if a stale
// value reaches the metrics payload.
const staleSourceRows = publicAddressRows({
  observed_public_ip: "2620:4f:8000::1",
  public_ipv4: "198.19.0.1",
  public_ipv6: "2606:4700:4700::1111%eth0",
});
assert.equal(staleSourceRows[0].value, "");
assert.equal(staleSourceRows[0].source, "公网探测未启用 · 可手动设置");
assert.equal(staleSourceRows[0].ok, false);
assert.equal(staleSourceRows[1].value, "");
assert.equal(staleSourceRows[1].source, "公网探测未启用 · 可手动设置");
assert.equal(staleSourceRows[1].ok, false);

}
