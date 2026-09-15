// Public-address selection and incremental dual-stack display updates.

function parseCanonicalIPv4(value) {
  const match = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(value);
  if (!match) return null;
  // Match netip.ParseAddr: dotted-quad octets are canonical decimal, so a
  // leading zero is invalid even when its numeric value fits in one byte.
  const octets = match.slice(1);
  if (octets.some((octet) => octet.length > 1 && octet.startsWith("0")))
    return null;
  const parsed = octets.map(Number);
  if (parsed.some((octet) => octet > 255)) return null;
  return parsed;
}

function isGloballyRoutableIPv4(value) {
  const parsed = parseCanonicalIPv4(value);
  if (!parsed) return false;
  const [a, b, c] = parsed;
  if (a === 0 || a === 10 || a === 127 || a === 255) return false;
  if (a === 100 && b >= 64 && b <= 127) return false;
  if (a === 169 && b === 254) return false;
  if (a === 172 && b >= 16 && b <= 31) return false;
  if (a === 192 && b === 0 && c === 0) return false;
  if (a === 192 && b === 0 && c === 2) return false;
  if (a === 192 && b === 31 && c === 196) return false;
  if (a === 192 && b === 52 && c === 193) return false;
  if (a === 192 && b === 88 && c === 99) return false;
  if (a === 192 && b === 168) return false;
  if (a === 192 && b === 175 && c === 48) return false;
  if (a === 198 && (b === 18 || b === 19)) return false;
  if (a === 198 && b === 51 && c === 100) return false;
  if (a === 203 && b === 0 && c === 113) return false;
  return a <= 223;
}

function parseIPv6Bytes(value) {
  const cleaned = value.toLowerCase();
  if (cleaned.includes("%")) return null;
  const parts = cleaned.split("::");
  if (parts.length > 2) return null;
  const parseGroups = (groups, allowDottedTail) => {
    const out = [];
    for (const [index, group] of groups.entries()) {
      if (group.includes(".")) {
        if (!allowDottedTail || index !== groups.length - 1) return null;
        const octets = parseCanonicalIPv4(group);
        if (!octets) return null;
        out.push(...octets);
        continue;
      }
      if (!group || group.length > 4 || !/^[0-9a-f]+$/.test(group)) return null;
      const hextet = parseInt(group, 16);
      out.push(hextet >> 8, hextet & 0xff);
    }
    return out;
  };
  if (parts.length === 1) {
    const full = parseGroups(parts[0].split(":"), true);
    return full && full.length === 16 ? full : null;
  }
  const left = parts[0] ? parseGroups(parts[0].split(":"), false) : [];
  if (!left) return null;
  const right = parts.length === 2 && parts[1]
    ? parseGroups(parts[1].split(":"), true)
    : [];
  if (right === null) return null;
  const missing = 16 - left.length - right.length;
  if (missing < 2 || missing % 2 !== 0) return null;
  return [...left, ...new Array(missing).fill(0), ...right];
}

function isIPv4Mapped(bytes) {
  return (
    bytes.length === 16 &&
    bytes.slice(0, 10).every((byte) => byte === 0) &&
    bytes[10] === 0xff &&
    bytes[11] === 0xff
  );
}

function normalizeInterfaceAddress(raw) {
  let value = String(raw || "").trim();
  if (!value) return "";
  if (value.startsWith("[") && value.endsWith("]")) value = value.slice(1, -1);
  // A zone identifier means the address is scoped, not a global unicast value
  // netpolicy would accept; fail closed rather than silently stripping it.
  if (value.indexOf("%") >= 0) return "";
  const ipv4 = parseCanonicalIPv4(value);
  if (ipv4) return `IPv4:${ipv4.join(".")}`;
  if (!value.includes(":")) return "";
  const bytes = parseIPv6Bytes(value);
  if (!bytes) return "";
  if (isIPv4Mapped(bytes)) return `IPv4:${bytes.slice(12).join(".")}`;
  return `IPv6:${value.toLowerCase()}`;
}

// Mirrors internal/netpolicy.IsPublicAddress. Prefixes are listed as significant
// bytes; mask entries narrow a final byte when the prefix length is not a whole
// byte boundary (everything else is masked at 0xff).
const ipv6SpecialPrefixes = [
  { bytes: [0x00, 0x64, 0xff, 0x9b, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00] }, // 64:ff9b::/96
  { bytes: [0x00, 0x64, 0xff, 0x9b, 0x00, 0x01] }, // 64:ff9b:1::/48
  { bytes: [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00] }, // 100::/64
  { bytes: [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01] }, // 100:0:0:1::/64
  { bytes: [0x20, 0x01, 0x00], mask: [0xff, 0xff, 0xfe] }, // 2001::/23
  { bytes: [0x20, 0x01, 0x0d, 0xb8] }, // 2001:db8::/32
  { bytes: [0x20, 0x02] }, // 2002::/16
  { bytes: [0x26, 0x20, 0x00, 0x4f, 0x80, 0x00] }, // 2620:4f:8000::/48
  { bytes: [0x3f, 0xff, 0x00], mask: [0xff, 0xff, 0xf0] }, // 3fff::/20
  { bytes: [0x5f, 0x00] }, // 5f00::/16
];

function ipv6PrefixMatches(bytes, prefix) {
  for (let index = 0; index < prefix.bytes.length; index++) {
    const mask = (prefix.mask && prefix.mask[index]) || 0xff;
    if ((bytes[index] & mask) !== (prefix.bytes[index] & mask)) return false;
  }
  return true;
}

// Cloudflare edge addresses are relay hops, not an Agent egress address. Keep
// this list aligned with the backend netpolicy CIDRs so stale observations are
// fail-closed in the dashboard as well as at ingestion time.
const cloudflareIPv4Prefixes = [
  { bytes: [103, 21, 244, 0], bits: 22 },
  { bytes: [103, 22, 200, 0], bits: 22 },
  { bytes: [103, 31, 4, 0], bits: 22 },
  { bytes: [104, 16, 0, 0], bits: 13 },
  { bytes: [104, 24, 0, 0], bits: 14 },
  { bytes: [108, 162, 192, 0], bits: 18 },
  { bytes: [131, 0, 72, 0], bits: 22 },
  { bytes: [141, 101, 64, 0], bits: 18 },
  { bytes: [162, 158, 0, 0], bits: 15 },
  { bytes: [172, 64, 0, 0], bits: 13 },
  { bytes: [173, 245, 48, 0], bits: 20 },
  { bytes: [188, 114, 96, 0], bits: 20 },
  { bytes: [190, 93, 240, 0], bits: 20 },
  { bytes: [197, 234, 240, 0], bits: 22 },
  { bytes: [198, 41, 128, 0], bits: 17 },
];

const cloudflareIPv6Prefixes = [
  { bytes: [0x24, 0x00, 0xcb, 0x00], bits: 32 },
  { bytes: [0x26, 0x06, 0x47, 0x00], bits: 32 },
  { bytes: [0x28, 0x03, 0xf8, 0x00], bits: 32 },
  { bytes: [0x24, 0x05, 0xb5, 0x00], bits: 32 },
  { bytes: [0x24, 0x05, 0x81, 0x00], bits: 32 },
  { bytes: [0x2a, 0x06, 0x98, 0xc0], bits: 29 },
  { bytes: [0x2c, 0x0f, 0xf2, 0x48], bits: 32 },
];

function bytePrefixMatches(bytes, prefix) {
  let remaining = prefix.bits;
  for (let index = 0; remaining > 0; index++) {
    const significant = Math.min(remaining, 8);
    const mask = (0xff << (8 - significant)) & 0xff;
    if ((bytes[index] & mask) !== (prefix.bytes[index] & mask)) return false;
    remaining -= significant;
  }
  return true;
}

function isCloudflareRelayNormalized(normalized) {
  if (normalized.startsWith("IPv4:")) {
    const bytes = parseCanonicalIPv4(normalized.slice(5));
    return Boolean(
      bytes && cloudflareIPv4Prefixes.some((prefix) => bytePrefixMatches(bytes, prefix)),
    );
  }
  if (!normalized.startsWith("IPv6:")) return false;
  const bytes = parseIPv6Bytes(normalized.slice(5));
  return Boolean(
    bytes && cloudflareIPv6Prefixes.some((prefix) => bytePrefixMatches(bytes, prefix)),
  );
}

function isGloballyRoutableIPv6(value) {
  const bytes = parseIPv6Bytes(value);
  if (!bytes) return false;
  // :: (unspecified) and ::1 (loopback).
  if (bytes.every((byte) => byte === 0)) return false;
  if (bytes.slice(0, 15).every((byte) => byte === 0) && bytes[15] === 1) return false;
  // fe80::/10 link-local unicast.
  if (bytes[0] === 0xfe && (bytes[1] & 0xc0) === 0x80) return false;
  // ff00::/8 multicast includes link-local and site-local multicast.
  if (bytes[0] === 0xff) return false;
  // fc00::/7 unique-local.
  if ((bytes[0] & 0xfe) === 0xfc) return false;
  for (const prefix of ipv6SpecialPrefixes) {
    if (ipv6PrefixMatches(bytes, prefix)) return false;
  }
  return true;
}

function isGloballyRoutableNormalized(normalized) {
  if (!normalized) return false;
  if (normalized.startsWith("IPv4:")) {
    return isGloballyRoutableIPv4(normalized.slice(5));
  }
  return isGloballyRoutableIPv6(normalized.slice(5));
}

const manualAddressLabels = [
  { key: "client_address", source: "手动设置" },
  { key: "public_host", source: "节点公网域名" },
  { key: "public_ip", source: "节点公网 IP" },
];

function normalizedPublicLiteral(raw) {
  const normalized = normalizeInterfaceAddress(raw);
  if (!normalized) return null;
  const isIPv4 = normalized.startsWith("IPv4:");
  const value = normalized.slice(5);
  if (!isGloballyRoutableNormalized(normalized) || isCloudflareRelayNormalized(normalized)) return null;
  return { value, isIPv4 };
}

function manualAddressEntries(labels) {
  if (!labels || typeof labels !== "object") return [];
  return manualAddressLabels.flatMap((item) => {
    const value = String(labels[item.key] || "").trim();
    return value ? [{ ...item, value }] : [];
  });
}

function manualPublicAddress(labels, wantIPv4) {
  for (const entry of manualAddressEntries(labels)) {
    const literal = normalizedPublicLiteral(entry.value);
    if (literal && literal.isIPv4 === wantIPv4)
      return { value: literal.value, source: entry.source };
  }
  return null;
}

// A manually managed hostname or non-public address remains a valid client
// connection setting, but it must never be presented as either IP family.
export function manualConnectionAddressNote(labels) {
  const first = manualAddressEntries(labels)[0];
  if (!first || normalizedPublicLiteral(first.value)) return "";
  return `手动连接地址：${first.value}`;
}

function publicAddressUnavailableSource(metrics, features) {
  const probeEnabled = Array.isArray(features) && features.includes("public-ip-probe-v1");
  if (!probeEnabled) return "公网探测未启用 · 可手动设置";
  if (!metrics?.collected_at) return "公网探测已启用 · 等待结果";
  return "无可验证公网地址 · 可手动设置";
}

function interfacePublicAddress(metrics, wantIPv4) {
  const interfaces = Array.isArray(metrics.network_interfaces) ? metrics.network_interfaces : [];
  for (const networkInterface of interfaces) {
    const addresses = Array.isArray(networkInterface.addresses) ? networkInterface.addresses : [];
    for (const raw of addresses) {
      const normalized = normalizeInterfaceAddress(raw);
      if (!normalized) continue;
      const isV4 = normalized.startsWith("IPv4:");
      if (isV4 !== wantIPv4) continue;
      if (!isGloballyRoutableNormalized(normalized)) continue;
      if (isCloudflareRelayNormalized(normalized)) continue;
      return { value: normalized.slice(5), name: networkInterface.name || "" };
    }
  }
  return null;
}

// Resolves the display rows for the node's dual-stack public addresses.
// Managed literal IP labels win, followed by probed egress addresses, then a
// default-route interface address of the same family, then the verified WSS
// connection source as a last resort. Hostnames and non-public labels remain
// connection settings and are never inferred into an IP family.
export function publicAddressRows(metrics = {}, labels = {}, features = []) {
  const observedCandidate = normalizeInterfaceAddress(metrics.observed_public_ip || "");
  const observed = isCloudflareRelayNormalized(observedCandidate)
    ? ""
    : observedCandidate;
  const observedIPv4 =
    observed.startsWith("IPv4:") && isGloballyRoutableNormalized(observed)
      ? observed.slice(5)
      : "";
  const observedIPv6 =
    observed.startsWith("IPv6:") && isGloballyRoutableNormalized(observed)
      ? observed.slice(5)
      : "";
  const families = [
    {
      label: "IPv4",
      cls: "v4",
      manualSource: manualPublicAddress(labels, true),
      probed: metrics.public_ipv4,
      probeSource: metrics.public_ipv4_source,
      interfaceSource: interfacePublicAddress(metrics, true),
      fallback: observedIPv4,
    },
    {
      label: "IPv6",
      cls: "v6",
      manualSource: manualPublicAddress(labels, false),
      probed: metrics.public_ipv6,
      probeSource: metrics.public_ipv6_source,
      interfaceSource: interfacePublicAddress(metrics, false),
      fallback: observedIPv6,
    },
  ];
  return families.map((family) => {
    if (family.manualSource) {
      return {
        ...family,
        value: family.manualSource.value,
        source: family.manualSource.source,
        ok: true,
      };
    }
    const probed = normalizeInterfaceAddress(family.probed || "");
    const probedIsIPv4 = probed.startsWith("IPv4:");
    const probeSourceValid = !family.probeSource || [
      "agent-config",
      "control-plane-config",
    ].includes(family.probeSource);
    if (
      probed &&
      probeSourceValid &&
      probedIsIPv4 === (family.label === "IPv4") &&
      isGloballyRoutableNormalized(probed) &&
      !isCloudflareRelayNormalized(probed)
    ) {
      const source = family.probeSource === "control-plane-config"
        ? "控制面配置的 Agent 直连探测"
        : "Agent 本地直连探测";
      return { ...family, value: probed.slice(5), source, ok: true };
    }
    if (family.interfaceSource) {
      return {
        ...family,
        value: family.interfaceSource.value,
        source: family.interfaceSource.name
          ? `默认路由接口 ${family.interfaceSource.name}`
          : "默认路由接口",
        ok: true,
      };
    }
    if (family.fallback) {
      return { ...family, value: family.fallback, source: "已验证连接来源", ok: true };
    }
    return {
      ...family,
      value: "",
      source: publicAddressUnavailableSource(metrics, features),
      ok: false,
    };
  });
}

// Builds a host:port display string. An IPv6 literal must be bracketed before
// appending the port; a raw "2606:...:443" is not a usable address preview.
export function formatHostPort(address, port) {
  const raw = String(address || "").trim();
  if (!raw) return "";
  let host = raw;
  if (raw.startsWith("[") && raw.endsWith("]")) {
    const inner = raw.slice(1, -1);
    if (inner.includes(":")) host = inner;
  }
  if (!port) return host;
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`;
}

export function updatePublicIPDisplays(root, metrics, labels = {}, features = []) {
  const rows = publicAddressRows(metrics || {}, labels || {}, features || []);
  const connectionNote = manualConnectionAddressNote(labels);
  for (const container of root.querySelectorAll(".node-card-ips, .node-public-ips")) {
    const isCard = container.classList.contains("node-card-ips");
    for (const row of rows) {
      const selector = isCard
        ? `.card-ip-row[data-ip-family="${row.cls}"]`
        : `.public-ip-row[data-ip-family="${row.cls}"]`;
      const line = container.querySelector(selector);
      if (!line) continue;
      line.hidden = !row.value;
      if (line.dataset) line.dataset.ipSource = row.source;
      const code = line.querySelector("code");
      if (code) {
        code.textContent = row.value || "未探测到";
        code.title = row.value || "";
      }
      line.classList.toggle("empty", !row.value);
      if (!isCard) {
        const source = line.querySelector("small");
        if (source) source.textContent = row.source;
        continue;
      }
      const copy = line.querySelector("[data-copy-ip]");
      if (copy) {
        copy.dataset.copyIp = row.value || "";
        copy.title = row.value ? `复制 ${row.label} 地址` : "暂无地址";
        copy.hidden = !row.value;
        copy.setAttribute("aria-label", `复制 ${row.label} 公网地址 ${row.value || ""}`);
      }
    }
  }
  for (const note of root.querySelectorAll("[data-node-connection-address]")) {
    note.textContent = connectionNote;
    note.hidden = !connectionNote;
  }
}
