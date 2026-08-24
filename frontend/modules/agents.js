import {
  bindEvent,
  createInteractionGate,
  createRefreshChannel,
} from "./refresh.js";
import { ConfigFormatError, formatConfigContent } from "./code-format.js";

export function developmentSourceVisible(engine, channel) {
  return engine === "mihomo" && channel === "development";
}

export function coreSourceForInstall(engine, channel, rawSource) {
  return developmentSourceVisible(engine, channel)
    ? rawSource || "official"
    : undefined;
}

export function batchAgentEligibility(agent, action, engine) {
  if (!agent || agent.status !== "online")
    return { eligible: false, reason: "节点离线，不能执行当前动作" };
  if (action === "upgrade-agent") {
    if (!(agent.features || []).includes("agent-self-upgrade-v1"))
      return {
        eligible: false,
        reason: "旧版 Agent 缺少远程升级能力，请先重新安装或单独升级",
      };
    return { eligible: true, reason: "在线 · 支持远程升级" };
  }
  if (!agent.runtime?.[engine]?.installed)
    return {
      eligible: false,
      reason: `未安装 ${engine || "所选内核"}，不能执行当前动作`,
    };
  return { eligible: true, reason: "在线 · 已安装所选内核" };
}

export function batchSelectAllState(inputs) {
  const eligible = [...inputs].filter((input) =>
    input.dataset?.batchEligible === undefined
      ? !input.disabled
      : input.dataset.batchEligible === "1",
  );
  const selected = eligible.filter((input) => input.checked);
  return {
    eligible: eligible.length,
    selected: selected.length,
    checked: eligible.length > 0 && selected.length === eligible.length,
    indeterminate: selected.length > 0 && selected.length < eligible.length,
  };
}

function mihomoDevelopmentSourceFieldset(canMirror) {
  return `<fieldset class="release-channel-fieldset development-source-field" data-development-source hidden><legend>开发版来源</legend><div class="release-channel-options"><label><input type="radio" name="core_source" value="official" checked><span>MetaCubeX 官方（默认，推荐）</span></label><label><input type="radio" name="core_source" value="mirror" ${canMirror ? "" : "disabled"}><span>vernesong/mihomo Alpha 镜像（第三方）${canMirror ? "" : "（需升级 Agent）"}</span></label></div>${canMirror ? "" : `<p class="source-upgrade-note">当前 Agent 尚未声明 mihomo-development-source-v1，镜像来源不可用；请先在面板升级 Agent。</p>`}</fieldset>`;
}

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
  if (!isGloballyRoutableNormalized(normalized)) return null;
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
      interfaceSource: interfacePublicAddress(metrics, true),
      fallback: observedIPv4,
    },
    {
      label: "IPv6",
      cls: "v6",
      manualSource: manualPublicAddress(labels, false),
      probed: metrics.public_ipv6,
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
    if (
      probed &&
      probedIsIPv4 === (family.label === "IPv4") &&
      isGloballyRoutableNormalized(probed) &&
      !isCloudflareRelayNormalized(probed)
    ) {
      return { ...family, value: probed.slice(5), source: "公网探测", ok: true };
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

export function nodeCardDropIndex(rects, pointer, grabOffset = { x: 0, y: 0 }) {
  if (!rects.length) return 0;
  const x = pointer.x - grabOffset.x;
  const y = pointer.y - grabOffset.y;
  const rows = [];
  rects
    .map((rect, index) => ({
      index,
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
      centerX: rect.left + (rect.right - rect.left) / 2,
    }))
    .sort((a, b) => a.top - b.top || a.left - b.left)
    .forEach((slot) => {
      const row = rows.find(
        (item) => slot.top < item.bottom && slot.bottom > item.top,
      );
      if (row) {
        row.top = Math.min(row.top, slot.top);
        row.bottom = Math.max(row.bottom, slot.bottom);
        row.slots.push(slot);
      } else {
        rows.push({ top: slot.top, bottom: slot.bottom, slots: [slot] });
      }
    });
  const row = rows.reduce((nearest, candidate) => {
    const distance =
      y < candidate.top
        ? candidate.top - y
        : y > candidate.bottom
          ? y - candidate.bottom
          : 0;
    return !nearest || distance < nearest.distance
      ? { row: candidate, distance }
      : nearest;
  }, null).row;
  return row.slots
    .sort((a, b) => a.centerX - b.centerX)
    .reduce((nearest, slot) => {
      const distance = Math.abs(x - slot.centerX);
      return !nearest || distance < nearest.distance
        ? { index: slot.index, distance }
        : nearest;
    }, null).index;
}

export function animateNodeCardDrop(
  items,
  oldRects,
  {
    requestFrame = (callback) => requestAnimationFrame(callback),
    cancelFrame = (frame) => cancelAnimationFrame(frame),
    setTimer = (callback, delay) => setTimeout(callback, delay),
    clearTimer = (timer) => clearTimeout(timer),
    fallbackDelay = 240,
    onSettled = () => {},
  } = {},
) {
  let active = true;
  let settled = false;
  let frame = null;
  let timer = null;
  const animated = new Set();
  const listeners = new Map();
  const clear = (snap = false) => {
    if (!active) return;
    active = false;
    if (frame != null) cancelFrame(frame);
    if (timer != null) clearTimer(timer);
    listeners.forEach((listener, item) =>
      item.removeEventListener("transitionend", listener),
    );
    items.forEach((item) => {
      if (snap) {
        item.style.transition = "none";
        item.style.transform = "";
        void item.offsetWidth;
      }
      item.style.transition = "";
      item.style.transform = "";
    });
    if (!settled) {
      settled = true;
      onSettled();
    }
  };
  items.forEach((item) => {
    const prev = oldRects.get(item);
    if (!prev) return;
    const rect = item.getBoundingClientRect();
    const dx = prev.left - rect.left;
    const dy = prev.top - rect.top;
    if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;
    item.style.transition = "none";
    item.style.transform = `translate(${dx}px, ${dy}px)`;
    void item.offsetWidth;
    animated.add(item);
  });
  if (!animated.size) {
    clear();
    return () => {};
  }
  animated.forEach((item) => {
    const listener = (event) => {
      if (event.target !== item || event.propertyName !== "transform") return;
      animated.delete(item);
      if (!animated.size) clear();
    };
    listeners.set(item, listener);
    item.addEventListener("transitionend", listener);
  });
  frame = requestFrame(() => {
    frame = null;
    if (!active) return;
    animated.forEach((item) => {
      item.style.transition = "";
      item.style.transform = "";
    });
    timer = setTimer(() => clear(), fallbackDelay);
  });
  return () => clear(true);
}

export function clearNodeCardDragState(
  grid,
  drag,
  { clearAnimationStyles = true, body = document.body } = {},
) {
  if (!drag) return;
  drag.card.classList.remove("dragging");
  body.classList.remove("node-card-dragging");
  grid.querySelectorAll(".node-card").forEach((card) => {
    card.classList.remove("drop-target");
    card.style.order = "";
    if (clearAnimationStyles) {
      card.style.transform = "";
      card.style.transition = "";
    }
  });
  drag.ghost?.remove();
}

export function installAgents(ctx) {
  const { api, optionalAPI, state, engines, can, esc, engineName, statusTone, serviceStatusName, short, date, ago, heartbeat, percent, bytes, conciseVersion, rate, actionName, serviceActionDisabled, trafficChart, renderConfigDiff, notify, confirmAction, shell } = ctx;
  const cardIPRow = (row) => {
    const value = row.value || "";
    const title = value ? `复制 ${row.label} 地址` : "暂无地址";
    const aria = `复制 ${row.label} 公网地址 ${value}`;
    return `<span class="card-ip-row ${value ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${value ? "" : "hidden"}><i class="ip-family ${row.cls}">${row.label}</i><code title="${esc(value)}">${esc(value || "未探测到")}</code><button type="button" class="card-ip-copy" data-copy-ip="${esc(value)}" aria-label="${esc(aria)}" title="${esc(title)}" ${value ? "" : "hidden"}><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0 2 2v8a2 2 0 0 0 2 2h2"/><path class="copy-check" d="m9.5 13.5 2 2 4-4.5"/></svg></button></span>`;
  };
  const metricsRefresh = createRefreshChannel({
    isCurrent: () => state.route === "node-settings",
    getScope: () => state.navigationEpoch,
  });
  const cardInteractions = createInteractionGate();
  let cancelCardDrag = () => {};
  let agentPageRequest = 0;
  let syncActiveBatchSnapshot = null;
  let structureRefreshQueued = false;
  let structureRefreshRunning = false;
  const requestAgentStructureRefresh = () => {
    structureRefreshQueued = true;
    cardInteractions.defer(() => void flushAgentStructureRefresh(), "structure");
  };
  async function flushAgentStructureRefresh() {
    if (
      structureRefreshRunning ||
      !structureRefreshQueued ||
      state.route !== "node-settings"
    )
      return;
    structureRefreshQueued = false;
    structureRefreshRunning = true;
    try {
      await renderAgentPage();
    } catch (error) {
      notify(error.message, "error");
    } finally {
      structureRefreshRunning = false;
      if (structureRefreshQueued)
        cardInteractions.defer(
          () => void flushAgentStructureRefresh(),
          "structure",
        );
    }
  }
async function agents(options = {}) {
  return nodeSettings(true, options);
}

async function nodeSettings(presetMode = false, { overview: preloadedOverview } = {}) {
  const request = ++agentPageRequest;
  const expectedRoute = presetMode ? "agents" : "node-settings";
  const [agents, deployments, accessEntries, tokens] =
    await Promise.all([
      api("/agents"),
      presetMode && can("deployments.read")
        ? api("/deployments")
        : Promise.resolve([]),
      presetMode && can("client-access.read")
        ? api("/client-access")
        : Promise.resolve([]),
      !presetMode && can("enrollment.manage")
        ? api("/enrollment-tokens")
        : Promise.resolve([]),
    ]);
  if (request !== agentPageRequest || state.route !== expectedRoute) return;
  state.data.agents = agents;
  if (!agents.some((agent) => agent.id === state.data.selectedAgent))
    state.data.selectedAgent = agents[0]?.id || "";
  const anchor = String(state.anchor || "");
  if (!presetMode) {
    if (
      anchor.startsWith("settings-node-") ||
      (anchor.startsWith("node-") && anchor !== "node-settings")
    )
      state.data.nodeView = "detail";
    else if (anchor === "node-settings" || anchor === "enrollment")
      state.data.nodeView = "overview";
  }
  const overview = can("overview.read")
    ? preloadedOverview || await api("/overview")
    : {};
  if (request !== agentPageRequest || state.route !== expectedRoute) return;
  state.data.overview = overview;

  const savedConfigs = presetMode && can("agent-config.read")
    ? (
        await Promise.all(
          agents.map((agent) =>
            api(`/agents/${encodeURIComponent(agent.id)}/configs`),
          ),
        )
      ).flat()
    : [];
  if (request !== agentPageRequest || state.route !== expectedRoute) return;
  const configByService = new Map(
    savedConfigs.map((config) => [
      `${config.agent_id}|${config.engine}`,
      config,
    ]),
  );
  const deploymentByService = new Map(
    deployments.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const accessByService = new Map(
    accessEntries.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const configDiffByService = new Map();
  await Promise.all(
    savedConfigs.map(async (saved) => {
      const key = `${saved.agent_id}|${saved.engine}`;
      const deployed = deploymentByService.get(key);
      if (
        !deployed?.config_id ||
        (deployed.config_id === saved.id &&
          deployed.config_version === saved.version)
      )
        return;
      const deployedConfig = await optionalAPI(
        `/configs/${encodeURIComponent(deployed.config_id)}/revisions/${deployed.config_version}`,
      );
      if (!deployedConfig) return;
      const diff = renderConfigDiff(saved.content, deployedConfig.content);
      if (diff) configDiffByService.set(key, diff);
    }),
  );
  if (request !== agentPageRequest || state.route !== expectedRoute) return;

  const tokenRows =
    tokens
      .map(
        (token) =>
          `<article><div><strong>${esc(token.name)}</strong><small>${token.reusable ? `可重复安装 · 删除前长期有效 · 已安装 ${token.used_count} 次` : `有效至 ${date(token.expires_at)} · 已使用 ${token.used_count}/${token.max_uses} 次`}</small></div><button class="access-history-delete" type="button" data-delete-enrollment="${esc(token.id)}" aria-label="删除添加命令 ${esc(token.name)}">删除</button></article>`,
      )
      .join("") || "";

  const selectedAgent = agents.find(
    (agent) => agent.id === state.data.selectedAgent,
  );
  const detailMode =
    !presetMode && state.data.nodeView === "detail" && selectedAgent;
  const visibleAgents = detailMode
    ? [selectedAgent]
    : presetMode
      ? selectedAgent
        ? [selectedAgent]
        : []
      : orderAgents(agents);
  const serviceActionIcons = {
    status:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12h3l2-5 4 10 2-5h5"/></svg>',
    start:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m8 5 11 7-11 7z"/></svg>',
    restart:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 7v5h-5"/><path d="M18.5 16a8 8 0 1 1 .8-7.2L20 12"/></svg>',
    stop:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="1"/></svg>',
  };
  const nodeCards = visibleAgents
    .map((agent) => {
      const metrics = can("metrics.read") ? agent.metrics || {} : {};
      const addressRows = publicAddressRows(
        metrics,
        agent.labels || {},
        agent.features || [],
      );
      const connectionAddressNote = manualConnectionAddressNote(agent.labels);
      const services = (agent.capabilities || [])
        .map((engine) => {
          const key = `${agent.id}|${engine}`;
          const runtime = agent.runtime?.[engine] || {};
          const deployed = deploymentByService.get(key);
          const saved = configByService.get(key);
          const access = accessByService.get(key);
          const configDiff = configDiffByService.get(key) || "";
          const drift =
            saved &&
            (!deployed ||
              deployed.config_id !== saved.id ||
              deployed.config_version < saved.version);
          const firstProfile = access?.profiles?.[0];
          const port = firstProfile?.profile?.fields?.find(
            (field) => field.label === "端口",
          )?.value;
          const endpoint = access ? formatHostPort(access.address, port) : "";
          const installed = Boolean(runtime.installed);
          const existingPending = Boolean(
            runtime.existing_config_available,
          );
          const existingUnsupportedReason = String(
            runtime.existing_config_unsupported_reason || "",
          );
          const existingBlocked = Boolean(existingUnsupportedReason);
          const canMirror = (agent.features || []).includes(
            "mihomo-development-source-v1",
          );
          const serviceState = existingPending
            ? "现有服务待迁移"
            : existingBlocked
            ? "检测到但不可迁移"
            : installed
            ? serviceStatusName(runtime.service_status)
            : "未安装";
          const serviceTone = existingPending
            ? "warn"
            : existingBlocked
            ? "warn"
            : installed
            ? statusTone(runtime.service_status)
            : "muted";
          let primaryActions = "";
          if (presetMode && can("agent-config.read")) {
            primaryActions = existingBlocked
              ? `<button class="button service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">查看配置</button><button class="button primary" type="button" data-manual-import data-manual-agent="${esc(agent.id)}" data-manual-engine="${esc(engine)}">查看不可迁移原因</button>`
              : existingPending
              ? `<button class="button primary" type="button" data-manual-import data-manual-agent="${esc(agent.id)}" data-manual-engine="${esc(engine)}">前往手动导入</button>`
              : drift
              ? `<button class="button service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">查看配置</button>${can("tasks.execute") ? `<button class="button primary" type="button" data-deploy="${esc(agent.id)}" data-engine="${esc(engine)}" data-config-id="${esc(saved.id)}">部署 v${saved.version}</button>` : ""}`
              : `<button class="button primary service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">配置 <span>→</span></button>`;
          }
          if (!presetMode) {
            const runtimeActions = installed
              ? ["status", "start", "restart", "stop"]
                  .map(
                    (action) =>
                      `<button class="core-action ${action === "stop" ? "danger" : ""}" type="button" data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" data-task-action="${action}" data-service-action="${action}" aria-label="${esc(`${actionName(action)} ${engineName(engine)}`)}" title="${esc(actionName(action))}" ${existingPending || existingBlocked || serviceActionDisabled(action, agent.status === "online", installed, runtime.service_status) ? "disabled" : ""}>${serviceActionIcons[action]}</button>`,
                  )
                  .join("")
              : "";
            return `<article class="service-card core-runtime-row service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installed ? 1 : 0}" data-existing-pending="${existingPending ? 1 : 0}" data-existing-unsupported="${esc(existingUnsupportedReason)}">
              <div class="core-runtime-summary">
                <div class="core-runtime-name"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${serviceTone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></div>
                <div class="core-runtime-version"><small>当前版本</small><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : "尚未安装")}">${esc(installed ? conciseVersion(engine, runtime.version) : "尚未安装")}</strong></div>
                <div class="core-runtime-actions">${runtimeActions ? `<div class="core-action-group" aria-label="${esc(engineName(engine))} 服务操作">${runtimeActions}</div>` : ""}<button class="button small ${installed ? "" : "primary"}" type="button" data-open-version-form ${existingPending || existingBlocked ? "disabled" : ""}>${existingBlocked ? "不可迁移" : existingPending ? "待迁移" : installed ? "版本" : "安装"}</button></div>
              </div>
              <details class="core-version-panel version-drawer"><summary><b>${installed ? "版本管理" : `安装 ${esc(engineName(engine))}`}</b><span>收起</span></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset>${mihomoDevelopmentSourceFieldset(canMirror)}<label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${existingPending || existingBlocked || agent.status !== "online" || !can("operator") ? "disabled" : ""}>${existingBlocked ? "不可自动迁移" : existingPending ? "请先手动导入" : installed ? "升级或切换版本" : "安装内核"}</button><small>${existingBlocked ? esc(existingUnsupportedReason) : existingPending ? "先在手动配置页确认配置并迁移服务" : installed ? "Release · SHA-256 校验" : "安装至 QAgent 专用目录，不影响系统已有内核 · Release · SHA-256 校验"}</small></form></div></details>
            </article>`;
          }
          return `<article class="service-card service-${esc(engine)}" data-refresh-key="service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installed ? 1 : 0}" data-existing-pending="${existingPending ? 1 : 0}" data-existing-unsupported="${esc(existingUnsupportedReason)}">
            <div class="service-card-main ${presetMode ? "" : "operations-only"}">
              <div class="service-overview"><header><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${serviceTone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></header><div class="service-version"><span class="service-version-label"><small>内核版本</small><button class="service-version-toggle" type="button" data-open-version-form aria-label="打开 ${esc(engineName(engine))} ${installed ? "版本切换" : "安装内核"}" ${existingPending || existingBlocked ? "disabled" : ""}>${existingBlocked ? "不可迁移" : existingPending ? "待迁移" : installed ? "切换版本" : "安装内核"}</button></span><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : "尚未安装")}">${esc(installed ? conciseVersion(engine, runtime.version) : "尚未安装")}</strong></div></div>
              ${presetMode ? `<div class="service-deployment"><dl class="service-facts"><div><dt>已部署配置</dt><dd>${deployed?.config_version ? `v${deployed.config_version}` : "—"}</dd></div><div><dt>已保存配置</dt><dd>${saved?.version ? `v${saved.version}` : "—"}</dd></div></dl>${drift ? `<div class="deployment-drift"><span>${deployed ? "已保存版本尚未部署" : "已保存配置尚未部署"}</span><b>待部署 v${saved.version}</b></div>` : ""}${configDiff ? `<details class="config-diff-drawer"><summary>查看配置差异 <i>＋</i></summary>${configDiff}</details>` : ""}<div class="service-endpoint ${endpoint ? "" : "empty"}">${endpoint ? `<span><b>${esc(firstProfile?.protocol || "客户端入站")}</b><small>${esc(firstProfile?.profile?.format || "已部署配置")}</small></span><code>${esc(endpoint)}</code>` : `<b>${deployed ? "自定义配置" : saved ? "尚未部署" : "尚未配置"}</b>`}</div></div>` : ""}
              ${primaryActions ? `<div class="service-primary-action">${primaryActions}</div>` : ""}
            </div>
            <details class="runtime-drawer version-drawer"><summary><span><b>${installed ? "版本切换" : "安装内核"}</b><small>${existingBlocked ? "检测到的现有服务未被接管" : installed ? "升级或切换内核版本" : "从 Release 安装"}</small></span><i>＋</i></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset>${mihomoDevelopmentSourceFieldset(canMirror)}<label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${existingBlocked || agent.status !== "online" || !can("operator") ? "disabled" : ""}>${existingBlocked ? "不可自动迁移" : installed ? "升级或切换版本" : "安装内核"}</button><small>${existingBlocked ? esc(existingUnsupportedReason) : installed ? "Release · SHA-256 校验" : "安装至 QAgent 专用目录，不影响系统已有内核 · Release · SHA-256 校验"}</small></form></div></details>
            ${presetMode && access?.profiles?.length ? `<a class="service-client-access" href="#client-access" data-client-agent="${esc(agent.id)}" data-client-engine="${esc(engine)}"><span><b>客户端配置</b><small>${esc(access.source)} · ${esc(access.address)}</small></span><strong>${access.profiles.length} 个入站 <i>→</i></strong></a>` : ""}
          </article>`;
        })
        .join("");
      const labels = Object.entries(agent.labels || {})
        .map(([key, value]) => `<span>${esc(key)}=${esc(value)}</span>`)
        .join("");
      if (detailMode) {
        const installedCount = (agent.capabilities || []).filter(
          (engine) => agent.runtime?.[engine]?.installed,
        ).length;
        const activeTab = ["cores", "metrics", "agent"].includes(
          state.data.nodeSettingsTab,
        )
          ? state.data.nodeSettingsTab
          : "cores";
        const tabID = (name) => `node-${agent.id}-${name}`;
        const tabButton = (name, label, count = "") =>
          `<button id="${esc(tabID(`${name}-tab`))}" type="button" role="tab" data-node-tab="${name}" aria-controls="${esc(tabID(`${name}-panel`))}" aria-selected="${activeTab === name}" tabindex="${activeTab === name ? 0 : -1}">${label}${count ? `<span>${count}</span>` : ""}</button>`;
        return `<section class="node-operations-workspace" id="settings-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-available="${metrics.collected_at ? 1 : 0}">
          <header class="node-operations-header"><div class="node-operations-title"><span class="machine-avatar">●</span><div><span class="node-live-state"><i class="status-dot ${statusTone(agent.status)}" data-agent-status-dot></i><b data-agent-status-label>${agent.status === "online" ? "在线" : "离线"}</b><small data-agent-heartbeat>${esc(heartbeat(agent.last_seen))}</small></span><h2>${esc(agent.name)}</h2><code>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(agent.id))}</code></div></div><div class="node-operations-actions">${can("metrics.read") ? `<button class="button small" type="button" data-agent-refresh title="刷新节点状态">刷新</button>` : ""}${can("operator") ? `<button type="button" class="button primary small" data-upgrade-agent="${esc(agent.id)}">升级 Agent</button>` : ""}</div></header>
          <section class="node-resource-strip" aria-label="节点资源"><div><span>CPU</span><strong data-metric-text="cpu">${metrics.cpu_available ? `${Number(metrics.cpu_percent).toFixed(1)}%` : "等待采集"}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" max="100" value="${metrics.cpu_available ? Number(metrics.cpu_percent) : 0}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${metrics.memory_available ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : "等待采集"}</strong><progress aria-label="内存使用率" data-metric-progress="memory" max="100" value="${percent(metrics.memory_used_bytes, metrics.memory_total_bytes)}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${metrics.disk_available ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : "等待采集"}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" max="100" value="${percent(metrics.disk_used_bytes, metrics.disk_total_bytes)}"></progress></div><div class="node-resource-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">${metrics.network_available ? rate(metrics.network_rx_bps) : "等待采集"}</i> · ↑ <i data-metric-text="upload-rate">${metrics.network_available ? rate(metrics.network_tx_bps) : "等待采集"}</i></strong><small>累计 ↓ <b data-metric-text="download-total">${metrics.network_available ? bytes(metrics.network_rx_bytes) : "—"}</b> · ↑ <b data-metric-text="upload-total">${metrics.network_available ? bytes(metrics.network_tx_bytes) : "—"}</b></small></div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section>
          <nav class="node-settings-tabs" role="tablist" aria-label="节点设置分区">${tabButton("cores", "内核", `${installedCount}/${(agent.capabilities || []).length}`)}${tabButton("metrics", "监控")}${tabButton("agent", "Agent")}</nav>
          <div class="node-settings-panels">
            <section id="${esc(tabID("cores-panel"))}" class="node-tab-panel node-cores-panel" data-node-panel="cores" role="tabpanel" aria-labelledby="${esc(tabID("cores-tab"))}" ${activeTab === "cores" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>内核管理</h3><small>服务状态与版本</small></div><span data-installed-summary>${installedCount ? `${installedCount} 个已安装` : "尚未安装内核"}</span></header><div class="core-runtime-list">${services}</div></section>
            <section id="${esc(tabID("metrics-panel"))}" class="node-tab-panel node-metrics-panel" data-node-panel="metrics" role="tabpanel" aria-labelledby="${esc(tabID("metrics-tab"))}" ${activeTab === "metrics" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>流量趋势</h3><small>最近 24 小时</small></div><span data-metric-text="stamp">${metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据"}</span></header><section class="metric-trend-empty" data-metric-history="${esc(agent.id)}" aria-label="暂无指标趋势"><span>⌁</span><b>正在载入指标趋势</b><small>节点上报指标后显示最近 24 小时的上下行速率。</small></section></section>
            <section id="${esc(tabID("agent-panel"))}" class="node-tab-panel node-agent-panel" data-node-panel="agent" role="tabpanel" aria-labelledby="${esc(tabID("agent-tab"))}" ${activeTab === "agent" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>Agent 与身份</h3><small>注册信息和安全通道</small></div><span data-agent-version>${esc(agent.version || "未知")}</span></header><dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>Agent 版本</dt><dd data-agent-version>${esc(agent.version || "未知")}</dd></div><div><dt>注册时间</dt><dd>${date(agent.enrolled_at)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl><section class="node-public-ips" aria-label="公网地址"><header><b>公网地址 · 双栈</b><small>手动设置优先 · 出口探测 · 默认路由接口 · 已验证连接来源</small><small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></header>${addressRows.map((row) => `<div class="public-ip-row ${row.ok ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${row.value ? "" : "hidden"}><span class="ip-family ${row.cls}">${row.label}</span><code>${esc(row.value || "未探测到")}</code><small>${esc(row.source)}</small></div>`).join("")}</section>${labels ? `<div class="labels">${labels}</div>` : ""}<footer class="node-identity-refresh"><span>节点身份已验证</span><div></div></footer>${can("agents.manage") ? `<section class="node-danger-zone"><span><b>删除节点</b><small>断开节点并清理关联配置；QAgent 不会被远程卸载。</small></span><button class="button small danger-button" type="button" data-delete="${esc(agent.id)}">删除节点</button></section>` : ""}</section>
          </div>
        </section>`;
      }
      if (!presetMode) {
        const installedCount = (agent.capabilities || []).filter(
          (engine) => agent.runtime?.[engine]?.installed,
        ).length;
        const coreChips = (agent.capabilities || [])
          .map((engine) => {
            const runtime = agent.runtime?.[engine] || {};
            const installed = Boolean(runtime.installed);
            const serviceState = installed
              ? serviceStatusName(runtime.service_status)
              : "未安装";
            const tone = installed
              ? statusTone(runtime.service_status)
              : "muted";
            return `<span class="core-chip service-${esc(engine)}" data-core-installed="${installed ? 1 : 0}"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${tone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></span>`;
          })
          .join("");
        return `<a class="node-card" href="#settings-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-state="${agent.status === "online" ? "online" : "offline"}" data-available="${metrics.collected_at ? 1 : 0}">
              <header class="node-card-head"><span class="machine-avatar" aria-hidden="true">●</span><div class="node-card-title"><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)} · ${installedCount ? `${installedCount}/${(agent.capabilities || []).length} 内核已安装` : "尚未安装内核"}</small></div><span class="node-card-state"><i class="status-dot ${statusTone(agent.status)}" data-agent-status-dot></i><b data-agent-status-label>${agent.status === "online" ? "在线" : "离线"}</b><small data-agent-heartbeat>${esc(heartbeat(agent.last_seen))}</small></span><span class="node-card-grip" title="拖动调整顺序" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg></span></header>
              <div class="node-card-ips" aria-label="公网地址">${addressRows.map(cardIPRow).join("")}<small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></div>
              <section class="node-card-resources" aria-label="节点资源"><div><span>CPU</span><strong data-metric-text="cpu">${metrics.cpu_available ? `${Number(metrics.cpu_percent).toFixed(1)}%` : "等待采集"}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" max="100" value="${metrics.cpu_available ? Number(metrics.cpu_percent) : 0}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${metrics.memory_available ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : "等待采集"}</strong><progress aria-label="内存使用率" data-metric-progress="memory" max="100" value="${percent(metrics.memory_used_bytes, metrics.memory_total_bytes)}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${metrics.disk_available ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : "等待采集"}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" max="100" value="${percent(metrics.disk_used_bytes, metrics.disk_total_bytes)}"></progress></div><div><span>网络</span><strong>↓ <i data-metric-text="download-rate">${metrics.network_available ? rate(metrics.network_rx_bps) : "等待采集"}</i> · ↑ <i data-metric-text="upload-rate">${metrics.network_available ? rate(metrics.network_tx_bps) : "等待采集"}</i></strong><small>累计 ↓ <b data-metric-text="download-total">${metrics.network_available ? bytes(metrics.network_rx_bytes) : "—"}</b> · ↑ <b data-metric-text="upload-total">${metrics.network_available ? bytes(metrics.network_tx_bytes) : "—"}</b></small></div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section>
              <section class="node-card-cores" aria-label="内核状态">${coreChips}</section>
              <footer class="node-card-foot"><small><i></i><span data-agent-version>${esc(agent.version || "未知")}</span></small><span class="node-card-stamp" data-metric-text="stamp">${metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据"}</span><span class="node-card-open">管理节点 <i aria-hidden="true">→</i></span></footer>
            </a>`;
      }
      return `<section class="preset-node-workspace workspace-panel machine-body" id="preset-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-available="${metrics.collected_at ? 1 : 0}" aria-label="选中节点的内核预设"><section class="service-canvas"><header class="service-canvas-head"><h2>节点内核</h2><span>${(agent.capabilities || []).length} 个内核</span></header><div class="service-grid">${services}</div></section></section>`;
    })
    .join("");

  const batch =
    !presetMode && !detailMode && agents.length > 1 && can("operator")
      ? `<details class="node-batch-panel"><summary><span><b>批量操作</b><small>全选仅包含当前列表中可执行所选动作的节点</small></span><i>＋</i></summary><form class="batch-toolbar" id="batch-form"><div class="batch-selection-head"><label class="batch-select-all"><input type="checkbox" data-batch-select-all aria-label="全选当前合格节点" aria-checked="false"><span data-batch-select-all-label>全选</span></label><strong data-batch-count aria-live="polite">已选择 0 个节点</strong></div><fieldset class="batch-node-options"><legend>当前节点范围</legend>${agents.map((agent) => `<label class="batch-select" title="选择此节点参与批量操作"><input type="checkbox" data-batch-checkbox value="${esc(agent.id)}" aria-label="选择 ${esc(agent.name)} 参与批量操作"><span><b>${esc(agent.name)}</b><small data-batch-eligibility>${agent.status === "online" ? "在线" : "离线"}</small></span></label>`).join("")}</fieldset><div class="batch-controls"><label>动作<select name="action"><option value="upgrade-agent">批量更新 Agent</option><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label><label data-batch-engine-wrap>内核<select name="engine">${engines.map((engine) => `<option value="${engine}">${esc(engineName(engine))}</option>`).join("")}</select></label><button class="button small" type="submit" disabled>执行</button><button class="button small" type="button" data-batch-clear disabled>清空选择</button></div><section class="batch-results" data-batch-results aria-live="polite" hidden></section></form></details>`
      : "";
  const onlineAgents = agents.filter(
    (agent) => agent.status === "online",
  ).length;
  const introTone = agents.length
    ? onlineAgents === agents.length
      ? "ok"
      : onlineAgents
        ? "warn"
        : "bad"
    : "";
  const pageIntro = presetMode
    ? ""
    : !detailMode && agents.length
      ? `<header class="node-page-intro"><div><p class="eyebrow">节点设置</p><h2>全部节点</h2><p>每个节点的资源占用与内核状态一屏总览；点击卡片进入管理台，拖动卡片左侧手柄调整顺序。</p></div><span class="node-intro-live"><i class="status-dot ${introTone}"></i>${agents.length} 个节点 · ${onlineAgents} 在线</span></header>`
      : !detailMode
        ? `<header class="node-page-intro"><div><p class="eyebrow">节点设置</p><h2>全部节点</h2><p>${can("enrollment.manage") ? "使用页面顶栏的“添加节点”生成部署命令；" : "当前账号没有添加节点权限；"}节点上线后即可在这里管理 Agent、内核与运行状态。</p></div></header>`
      : "";
  shell(
    `${pageIntro}${presetMode ? "" : `<div class="node-settings-page">${batch}${detailMode ? `<a class="node-back-link" href="#node-settings">← 全部节点</a>` : ""}`}${nodeCards ? `<section class="${presetMode ? "machine-stack" : detailMode ? "node-settings-stack" : "node-card-grid"}">${nodeCards}</section>` : '<div class="empty large"><strong>还没有节点</strong><p>点击上方“添加节点”生成部署命令。</p></div>'}${presetMode ? "" : "</div>"}`,
    presetMode ? "内核配置预设" : "节点设置",
    {
      viewKey: presetMode
        ? `preset-${state.data.selectedAgent || "empty"}`
        : detailMode
          ? `node-settings-${state.data.selectedAgent || "empty"}`
          : "node-settings-overview",
    },
  );
  document.querySelectorAll("[data-context-agent]").forEach((link) => {
    const prefix = presetMode ? "preset-node" : "settings-node";
    link.href = `#${prefix}-${link.dataset.contextAgent}`;
  });
  if (presetMode) compactPresetPage();
  bindAgentPage(agents, presetMode, { tokenRows, tokenCount: tokens.length });
}

function renderAgentPage() {
  return state.route === "agents" ? agents() : nodeSettings();
}

function refreshAgentPage() {
  if (
    state.route === "node-settings" &&
    cardInteractions.activeCount() > 0
  ) {
    requestAgentStructureRefresh();
    return Promise.resolve(false);
  }
  return renderAgentPage();
}

function cancelAgentInteractions() {
  cardInteractions.cancel();
  cancelCardDrag();
  cancelCardDrag = () => {};
}

const nodeCardOrderKey = "qcontrolhub:node-card-order";

function savedCardOrder() {
  try {
    const parsed = JSON.parse(localStorage.getItem(nodeCardOrderKey));
    return Array.isArray(parsed) ? parsed.filter((id) => typeof id === "string") : [];
  } catch {
    return [];
  }
}

function orderAgents(agents) {
  const saved = savedCardOrder();
  if (!saved.length) return agents;
  const position = new Map(saved.map((id, index) => [id, index]));
  return [...agents].sort(
    (a, b) =>
      (position.get(a.id) ?? saved.length) - (position.get(b.id) ?? saved.length),
  );
}

// Drag reordering of the overview cards. A cloned ghost follows the cursor and
// the target card is highlighted, while the grid itself does not reflow
// mid-drag; on release the cards FLIP-animate to their new layout and the
// order is committed to localStorage.
function enableCardDrag(grid) {
  let drag = null;
  let cancelLanding = null;
  const dropIndex = (pointerX, pointerY) => {
    const rects = [...grid.querySelectorAll(".node-card")].map((card) =>
      card.getBoundingClientRect(),
    );
    return nodeCardDropIndex(
      rects,
      { x: pointerX, y: pointerY },
      drag.grabOffset,
    );
  };
  const highlight = (index) => {
    grid
      .querySelectorAll(".node-card")
      .forEach((card) => card.classList.remove("drop-target"));
    if (index == null) return;
    const rest = [...grid.querySelectorAll(".node-card")].filter(
      (card) => card !== drag.card,
    );
    rest[index]?.classList.add("drop-target");
  };
  const clearDragState = (clearAnimationStyles) => {
    if (!drag) return;
    clearNodeCardDragState(grid, drag, {
      clearAnimationStyles,
    });
    drag = null;
  };
  const reset = () => {
    const landing = cancelLanding;
    cancelLanding = null;
    const releaseInteraction = drag?.releaseInteraction;
    landing?.();
    clearDragState(true);
    if (!landing) releaseInteraction?.();
  };
  cancelCardDrag = reset;
  const finish = (event) => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const { card, moved, drop, releaseInteraction } = drag;
    if (!moved || !grid.contains(card)) return reset();
    const rest = [...grid.querySelectorAll(".node-card")].filter(
      (item) => item !== card,
    );
    const ghostRect = drag.ghost
      ? drag.ghost.getBoundingClientRect()
      : card.getBoundingClientRect();
    const oldRects = new Map(
      [card, ...rest].map((item) => [
        item,
        item === card ? ghostRect : item.getBoundingClientRect(),
      ]),
    );
    const target = drop == null || drop >= rest.length ? null : rest[drop];
    if (target) target.before(card);
    else grid.append(card);
    const next = [...grid.querySelectorAll(".node-card")];
    const ids = next.map((item) => item.dataset.agentNode);
    clearDragState(false);
    let settled = false;
    const cancelAnimation = animateNodeCardDrop(next, oldRects, {
      onSettled: () => {
        settled = true;
        cancelLanding = null;
        releaseInteraction();
      },
    });
    if (!settled) cancelLanding = cancelAnimation;
    try {
      localStorage.setItem(nodeCardOrderKey, JSON.stringify(ids));
    } catch {}
  };
  const cancel = (event) => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    reset();
  };
  grid.querySelectorAll(".node-card-grip").forEach((grip) => {
    bindEvent(grip, "pointerdown", (event) => {
      if (event.button !== 0 || drag) return;
      const releaseInteraction = cardInteractions.begin();
      cancelLanding?.();
      cancelLanding = null;
      const card = grip.closest(".node-card");
      if (!card) {
        releaseInteraction();
        return;
      }
      event.preventDefault();
      const rect = card.getBoundingClientRect();
      drag = {
        card,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        grabOffset: {
          x: event.clientX - (rect.left + rect.width / 2),
          y: event.clientY - (rect.top + rect.height / 2),
        },
        rect,
        started: false,
        moved: false,
        drop: null,
        ghost: null,
        releaseInteraction,
      };
      grip.setPointerCapture(event.pointerId);
    });
    bindEvent(grip, "pointermove", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      if (!drag.card.isConnected) {
        reset();
        return;
      }
      if (!drag.started) {
        if (
          Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4
        )
          return;
        drag.started = true;
        drag.card.classList.add("dragging");
        document.body.classList.add("node-card-dragging");
        const ghost = drag.card.cloneNode(true);
        ghost.classList.remove("dragging");
        ghost.classList.add("node-card-ghost");
        ghost.removeAttribute("href");
        ghost.removeAttribute("data-agent-node");
        ghost.removeAttribute("data-agent-metrics");
        ghost.removeAttribute("data-metric-poll");
        ghost.style.position = "fixed";
        ghost.style.left = `${drag.rect.left}px`;
        ghost.style.top = `${drag.rect.top}px`;
        ghost.style.width = `${drag.rect.width}px`;
        drag.ghost = ghost;
        document.body.appendChild(ghost);
      }
      drag.moved = true;
      const dx = event.clientX - drag.startX;
      const dy = event.clientY - drag.startY;
      drag.ghost.style.transform = `translate(${dx}px, ${dy}px) scale(.99) rotate(.3deg)`;
      const index = dropIndex(event.clientX, event.clientY);
      drag.drop = index;
      highlight(index);
    });
    bindEvent(grip, "pointerup", finish);
    bindEvent(grip, "pointercancel", cancel);
    bindEvent(grip, "lostpointercapture", cancel);
    bindEvent(grip, "click", (event) => {
      event.preventDefault();
      event.stopPropagation();
    });
  });
}

function compactPresetPage() {
  document.querySelector("#enrollment")?.remove();
  document.querySelector("#batch-form")?.remove();
  document.querySelectorAll(".preset-node-workspace").forEach((item) => {
    item.querySelector(".machine-resource-summary")?.remove();
    item.querySelector(".machine-state")?.remove();
    item.querySelector(".node-inspector")?.remove();
    item.querySelector(".machine-footer")?.remove();
    item.querySelectorAll(".service-management-unavailable, [data-upgrade-agent]").forEach((element) => element.remove());
    item.querySelectorAll("[data-batch-checkbox]").forEach((element) => element.closest("label")?.remove());
  });
}

function bindAgentPage(agentItems, presetMode = false, enrollmentHistory = {}) {
  const agentsByID = new Map(agentItems.map((agent) => [agent.id, agent]));
  syncActiveBatchSnapshot = null;
  document
    .querySelectorAll(
      ".preset-node-workspace, .machine-workspace, .node-operations-workspace",
    )
    .forEach((item) => {
      const agent = agentsByID.get(item.dataset.agentNode);
      const installedCount = (agent?.capabilities || []).filter(
        (engine) => agent.runtime?.[engine]?.installed,
      ).length;
      const serviceCount = item.querySelector(
        ".service-canvas-head > span, [data-installed-summary]",
      );
      if (serviceCount) {
        const compact = serviceCount.hasAttribute("data-installed-summary");
        serviceCount.textContent = installedCount
          ? compact
            ? `${installedCount} 个已安装`
            : `${installedCount} 个已安装 · ${(agent?.capabilities || []).length} 个可用`
          : "尚未安装内核";
      }
      const machineFooter = item.querySelector(".machine-footer");
      item.querySelectorAll("[data-upgrade-agent]").forEach((button) => {
        const supported = (agent?.features || []).includes(
          "agent-self-upgrade-v1",
        );
        if (!supported) {
          button.disabled = true;
          button.title =
            "当前 Agent 不支持远程升级，请重新执行一次添加节点命令";
          button.textContent = "需重新安装 Agent";
        }
      });
      if (!presetMode && can("enrollment.manage")) {
        const actions = item.querySelector(".node-identity-refresh > div");
        if (actions && !actions.querySelector("[data-reinstall-agent]")) {
          const button = document.createElement("button");
          button.type = "button";
          button.className = "button small";
          button.dataset.reinstallAgent = agent.id;
          button.textContent = "生成新安装命令";
          actions.append(button);
        }
      }
      machineFooter?.remove();
      if (agent) updateAgentMetrics(agent);
      if (item instanceof HTMLDetailsElement) {
        bindEvent(item, "toggle", () => {
          if (item.open) {
            state.data.selectedAgent = item.dataset.agentNode;
            if (can("metrics.read")) loadMetricHistory(state.data.selectedAgent);
          }
        });
        if (item.open && can("metrics.read"))
          loadMetricHistory(item.dataset.agentNode);
      } else if (
        can("metrics.read") &&
        item.querySelector('[data-node-panel="metrics"]:not([hidden])')
      ) {
        loadMetricHistory(item.dataset.agentNode);
      }
    });
  document.querySelectorAll("[data-node-tab]").forEach((button) => {
    button.onclick = () => {
      const workspace = button.closest(".node-operations-workspace");
      if (!workspace) return;
      const tab = button.dataset.nodeTab;
      state.data.nodeSettingsTab = tab;
      workspace.querySelectorAll("[data-node-tab]").forEach((candidate) => {
        const selected = candidate === button;
        candidate.setAttribute("aria-selected", String(selected));
        candidate.tabIndex = selected ? 0 : -1;
      });
      workspace.querySelectorAll("[data-node-panel]").forEach((panel) => {
        panel.hidden = panel.dataset.nodePanel !== tab;
      });
      if (tab === "metrics" && can("metrics.read"))
        loadMetricHistory(workspace.dataset.agentNode);
    };
    button.onkeydown = (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
        return;
      event.preventDefault();
      const tabs = [...button.closest("[role=tablist]").querySelectorAll("[role=tab]")];
      const current = tabs.indexOf(button);
      const next =
        event.key === "Home"
          ? 0
          : event.key === "End"
            ? tabs.length - 1
            : (current + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
              tabs.length;
      tabs[next].focus();
      tabs[next].click();
    };
  });
  document.querySelectorAll("[data-config]").forEach((button) => {
    button.onclick = () => {
      state.data.agentId = button.dataset.config;
      state.data.engine = button.dataset.engine;
      location.hash = "#agent-config";
    };
  });
  document.querySelectorAll("[data-client-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.accessAgent = link.dataset.clientAgent;
      state.data.accessEngine = link.dataset.clientEngine;
    };
  });
  document.querySelectorAll("[data-task-action]").forEach((button) => {
    button.onclick = async () => {
      if (
        button.dataset.taskAction === "stop" &&
        !(await confirmAction(
          `确定停止 ${engineName(button.dataset.taskEngine)} 服务？现有连接会立即中断，需再次启动才能恢复。`,
          "停止服务",
        ))
      )
        return;
      await submitTask({
        agent_id: button.dataset.taskAgent,
        engine: button.dataset.taskEngine,
        action: button.dataset.taskAction,
      });
    };
  });
  document.querySelectorAll("[data-deploy]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          `确定将已保存配置部署到 ${engineName(button.dataset.engine)} 并重启服务？`,
          button.textContent.trim(),
        ))
      )
        return;
      await submitTask({
        agent_id: button.dataset.deploy,
        engine: button.dataset.engine,
        action: "deploy",
        config_id: button.dataset.configId,
      });
    };
  });
  document.querySelectorAll("[data-manual-import]").forEach((button) => {
    button.onclick = () => {
      state.data.liveAgent = button.dataset.manualAgent;
      state.data.liveEngine = button.dataset.manualEngine;
      location.hash = "#live-config";
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    form.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(form);
      const channel = values.get("release_channel");
      const engine = form.dataset.versionEngine;
      const version =
        channel === "custom" ? values.get("custom_version") : channel;
      const payload = {
        agent_id: form.dataset.versionAgent,
        engine,
        action: "install",
        core_version: version,
      };
      const source = coreSourceForInstall(engine, channel, values.get("core_source"));
      if (source !== undefined) payload.core_source = source;
      const sourceNote =
        payload.core_source === "mirror"
          ? "来源：vernesong/mihomo Alpha 镜像（第三方）。"
          : payload.core_source === "official"
            ? "来源：MetaCubeX/mihomo 官方（默认）。"
            : "";
      if (
        !(await confirmAction(
          `确定提交内核安装或版本切换任务？${sourceNote}下载和校验完成后，目标服务会重启。`,
          "提交任务",
        ))
      )
        return;
      await submitTask(payload);
    };
  });
  document.querySelectorAll("[data-open-version-form]").forEach((button) => {
    button.onclick = () => {
      const drawer = button
        .closest(".service-card")
        ?.querySelector(".version-drawer");
      if (drawer) drawer.open = true;
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    const custom = form.querySelector(".custom-version-field");
    const input = custom?.querySelector("input");
    const developmentSource = form.querySelector("[data-development-source]");
    const sync = () => {
      const checked = form.querySelector('input[name="release_channel"]:checked');
      const channel = checked?.value;
      const enabled = channel === "custom";
      custom?.classList.toggle("is-disabled", !enabled);
      if (input) {
        input.disabled = !enabled;
        input.required = enabled;
      }
      if (developmentSource) {
        developmentSource.hidden = !developmentSourceVisible(
          form.dataset.versionEngine,
          channel,
        );
      }
    };
    form
      .querySelectorAll('input[name="release_channel"]')
      .forEach((radio) => bindEvent(radio, "change", sync));
    sync();
  });
  document.querySelectorAll("[data-delete]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定删除此节点？控制面会断开连接并清理关联配置，节点上的 QAgent 不会被远程卸载；以后可通过新的添加节点命令重新安装。",
          "删除节点",
        ))
      )
        return;
      await api(`/agents/${encodeURIComponent(button.dataset.delete)}`, {
        method: "DELETE",
      });
      await refreshAgentPage();
    };
  });
  const batchForm = document.querySelector("#batch-form");
  const updateBatch = () => {
    if (!batchForm) return;
    const engine = batchForm.elements.engine.value;
    const action = batchForm.elements.action.value;
    const busy = batchForm.dataset.busy === "1";
    const inputs = [...batchForm.querySelectorAll("[data-batch-checkbox]")];
    inputs.forEach((input) => {
      const agent = agentsByID.get(input.value);
      const eligibility = batchAgentEligibility(agent, action, engine);
      input.dataset.batchEligible = eligibility.eligible ? "1" : "0";
      input.disabled = busy || !eligibility.eligible;
      input.closest(".batch-select").title = eligibility.reason;
      const reason = input.closest(".batch-select").querySelector(
        "[data-batch-eligibility]",
      );
      if (reason) reason.textContent = eligibility.reason;
      if (!eligibility.eligible) input.checked = false;
    });
    const selection = batchSelectAllState(inputs);
    const selectAll = batchForm.querySelector("[data-batch-select-all]");
    if (selectAll) {
      selectAll.disabled = busy || selection.eligible === 0;
      selectAll.checked = selection.checked;
      selectAll.indeterminate = selection.indeterminate;
      selectAll.setAttribute(
        "aria-checked",
        selection.indeterminate ? "mixed" : String(selection.checked),
      );
    }
    const selectAllLabel = batchForm.querySelector(
      "[data-batch-select-all-label]",
    );
    if (selectAllLabel)
      selectAllLabel.textContent = selection.checked ? "取消全选" : "全选";
    const button = batchForm?.querySelector("button[type=submit]");
    if (button)
      button.disabled = selection.selected === 0 || batchForm.dataset.busy === "1";
    const clear = batchForm.querySelector("[data-batch-clear]");
    if (clear) clear.disabled = busy || selection.selected === 0;
    const label = batchForm?.querySelector("[data-batch-count]");
    if (label)
      label.textContent = `已选择 ${selection.selected} 个节点 · 当前可选 ${selection.eligible} 个`;
    const engineWrap = batchForm.querySelector("[data-batch-engine-wrap]");
    if (engineWrap) engineWrap.hidden = action === "upgrade-agent";
    batchForm.elements.action.disabled = busy;
    batchForm.elements.engine.disabled = busy;
    batchForm
      .querySelectorAll("[data-batch-retry]")
      .forEach((retry) => {
        const eligibility = batchAgentEligibility(
          agentsByID.get(retry.dataset.batchRetry),
          retry.dataset.batchRetryAction,
          retry.dataset.batchRetryEngine,
        );
        retry.disabled = busy || !eligibility.eligible;
        retry.title = eligibility.eligible ? "重试失败任务" : eligibility.reason;
      });
  };
  const setBatchBusy = (busy) => {
    if (!batchForm) return;
    batchForm.dataset.busy = busy ? "1" : "";
    updateBatch();
  };
  syncActiveBatchSnapshot = (items) => {
    if (!batchForm?.isConnected) return;
    agentsByID.clear();
    items.forEach((agent) => agentsByID.set(agent.id, agent));
    updateBatch();
  };
  batchForm
    ?.querySelectorAll("[data-batch-checkbox]")
    .forEach((input) => (input.onchange = updateBatch));
  const selectAll = batchForm?.querySelector("[data-batch-select-all]");
  if (selectAll)
    selectAll.onchange = () => {
      const shouldSelect = selectAll.checked;
      [...batchForm.querySelectorAll("[data-batch-checkbox]")]
        .filter((input) => input.dataset.batchEligible === "1")
        .forEach((input) => (input.checked = shouldSelect));
      updateBatch();
    };
  const clearBatch = batchForm?.querySelector("[data-batch-clear]");
  if (clearBatch)
    clearBatch.onclick = () => {
      batchForm
        .querySelectorAll("[data-batch-checkbox]")
        .forEach((input) => (input.checked = false));
      updateBatch();
    };
  bindEvent(batchForm?.elements.engine, "change", updateBatch);
  bindEvent(batchForm?.elements.action, "change", updateBatch);
  updateBatch();
  if (batchForm)
    batchForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(batchForm);
      const action = String(values.get("action"));
      const engine = String(values.get("engine"));
      let selected = [
        ...batchForm.querySelectorAll("[data-batch-checkbox]:checked"),
      ].filter((input) =>
        batchAgentEligibility(agentsByID.get(input.value), action, engine)
          .eligible,
      );
      if (!selected.length || batchForm.dataset.busy === "1" || batchForm.dataset.confirming === "1")
        return;
      batchForm.dataset.confirming = "1";
      let confirmed = false;
      try {
        confirmed = await confirmAction(
          action === "upgrade-agent"
            ? `确定在 ${selected.length} 个在线节点上批量更新 Agent？升级期间节点会短暂离线。`
            : `确定在 ${selected.length} 个节点上执行 ${engineName(engine)} ${actionName(action)}？`,
          "提交批量任务",
        );
      } finally {
        batchForm.dataset.confirming = "";
      }
      if (!confirmed || batchForm.dataset.busy === "1")
        return;
      updateBatch();
      selected = [
        ...batchForm.querySelectorAll("[data-batch-checkbox]:checked"),
      ].filter((input) =>
        batchAgentEligibility(agentsByID.get(input.value), action, engine)
          .eligible,
      );
      if (!selected.length) return;
      setBatchBusy(true);
      const results = batchForm.querySelector("[data-batch-results]");
      const settled = [];
      for (const input of selected) {
        const agent = agentsByID.get(input.value);
        const eligibility = batchAgentEligibility(agent, action, engine);
        if (!eligibility.eligible) {
          settled.push({
            agent,
            error: new Error(`节点状态已变化：${eligibility.reason}`),
            ok: false,
          });
          continue;
        }
        try {
          const task = await api("/tasks", {
            method: "POST",
            body: JSON.stringify({
              agent_id: input.value,
              ...(action === "upgrade-agent" ? {} : { engine }),
              action,
            }),
          });
          settled.push({ agent, task, ok: true });
        } catch (error) {
          settled.push({ agent, error, ok: false });
        }
      }
      if (results) {
        results.hidden = false;
        results.innerHTML = `<header><b>批量结果</b><small>${settled.filter((item) => item.ok).length}/${settled.length} 成功</small></header>${settled.map((item) => `<div class="batch-result-row ${item.ok ? "ok" : "error"}"><span><b>${esc(item.agent?.name || item.agent?.id || "节点")}</b><small>${item.ok ? `任务 ${esc(item.task?.id || "已提交")}` : esc(item.error?.message || "提交失败")}</small></span>${item.ok ? "" : `<button type="button" class="button small" data-batch-retry="${esc(item.agent?.id || "")}" data-batch-retry-action="${esc(action)}" data-batch-retry-engine="${esc(engine)}">重试</button>`}</div>`).join("")}`;
      }
      setBatchBusy(false);
      const success = settled.filter((item) => item.ok).length;
      notify(success === settled.length ? `已提交 ${success} 个任务` : `已提交 ${success}/${settled.length} 个任务`, success === settled.length ? "success" : "error");
      bindBatchRetries(
        batchForm,
        action,
        engine,
        agentsByID,
        setBatchBusy,
      );
    };
  document.querySelectorAll("[data-open-enrollment]").forEach((button) => {
    button.onclick = () =>
      showEnrollmentDialog({
        tokenRows: enrollmentHistory.tokenRows || "",
        tokenCount: enrollmentHistory.tokenCount || 0,
        onDelete: async (id) => {
          await api(`/enrollment-tokens/${encodeURIComponent(id)}`, {
            method: "DELETE",
          });
          try {
            await refreshAgentPage();
          } catch (error) {
            notify(`添加记录刷新失败：${error.message}`, "error");
          }
        },
        onSubmit: async (name, close) => {
          const created = await api("/enrollment-tokens", {
            method: "POST",
            body: JSON.stringify({ name }),
          });
          const escapedToken = created.token.replaceAll("'", "'\\''");
          const escapedName = name.replaceAll("'", "'\\''");
          const command = `curl -fsSL -H 'X-QControlHub-Enrollment: ${escapedToken}' ${location.origin}/install-agent.sh | sudo sh -s -- ${location.origin} '${escapedToken}' '${escapedName}'`;
          close();
          showCommand(command, async () => {
            try {
              await refreshAgentPage();
            } catch (error) {
              notify(`添加记录刷新失败，部署命令未受影响：${error.message}`, "error");
            }
          });
        },
      });
  });
  document
    .querySelectorAll("[data-agent-refresh]")
    .forEach((button) => (button.onclick = () => pollAgentMetrics()));
  document.querySelectorAll("[data-upgrade-agent]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定升级这个节点的 QAgent？控制面会把当前版本的 Agent 二进制签名下发到节点，原子替换后自动重连；升级期间节点会短暂离线。",
          "升级 Agent",
        ))
      )
        return;
      const task = await submitTask({
        agent_id: button.dataset.upgradeAgent,
        action: "upgrade-agent",
      });
      if (task) location.hash = "#tasks";
    };
  });
  document.querySelectorAll("[data-reinstall-agent]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定为这个节点生成新的 Agent 部署命令？已有命令继续有效；本操作只生成可复制命令，不会自动执行。",
          "生成安装命令",
        ))
      )
        return;
      try {
        const created = await api(
          `/agents/${encodeURIComponent(button.dataset.reinstallAgent)}/enrollment-token`,
          { method: "POST" },
        );
        const escapedToken = created.token.replaceAll("'", "'\\''");
        const escapedName = created.name.replaceAll("'", "'\\''");
        const command = `curl -fsSL -H 'X-QControlHub-Enrollment: ${escapedToken}' ${location.origin}/install-agent.sh | sudo sh -s -- ${location.origin} '${escapedToken}' '${escapedName}'`;
        showCommand(command, async () => {
          try { await refreshAgentPage(); } catch (error) { notify(error.message, "error"); }
        }, "复制 Agent 安装命令");
      } catch (error) {
        notify(error.message, "error");
      }
    };
  });
  const cardGrid = document.querySelector(".node-card-grid");
  if (cardGrid) enableCardDrag(cardGrid);
  document.querySelectorAll("[data-copy-ip]").forEach((button) => {
    bindEvent(button, "click", async (event) => {
      // The card itself is a link to the node workspace; copying must not
      // navigate away from the overview.
      event.preventDefault();
      event.stopPropagation();
      const value = button.dataset.copyIp;
      if (!value) return;
      try {
        await navigator.clipboard.writeText(value);
      } catch {
        const fallback = document.createElement("textarea");
        fallback.value = value;
        fallback.style.position = "fixed";
        fallback.style.opacity = "0";
        document.body.append(fallback);
        fallback.select();
        document.execCommand("copy");
        fallback.remove();
      }
      const originalTitle = button.title;
      button.classList.add("copied");
      button.title = "已复制";
      window.setTimeout(() => {
        button.classList.remove("copied");
        if (button.isConnected) button.title = originalTitle;
      }, 1600);
    });
  });
  clearTimeout(state.agentPollTimer);
  if (!presetMode && can("metrics.read"))
    state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
}

function bindBatchRetries(form, action, engine, agentsByID, setBatchBusy) {
  form.querySelectorAll("[data-batch-retry]").forEach((button) => {
    button.onclick = async () => {
      if (form.dataset.busy === "1") return;
      const agentID = button.dataset.batchRetry;
      const agent = agentsByID.get(agentID);
      if (!agent) return;
      const eligibility = batchAgentEligibility(agent, action, engine);
      if (!eligibility.eligible) {
        notify(`无法重试：${eligibility.reason}`, "error");
        return;
      }
      setBatchBusy(true);
      try {
        const currentAgent = agentsByID.get(agentID);
        const currentEligibility = batchAgentEligibility(
          currentAgent,
          action,
          engine,
        );
        if (!currentEligibility.eligible) {
          notify(`无法重试：${currentEligibility.reason}`, "error");
          return;
        }
        const task = await api("/tasks", {
          method: "POST",
          body: JSON.stringify({
            agent_id: agentID,
            ...(action === "upgrade-agent" ? {} : { engine }),
            action,
          }),
        });
        button.closest(".batch-result-row").className = "batch-result-row ok";
        button.closest(".batch-result-row").querySelector("small").textContent = `任务 ${task?.id || "已提交"}`;
        button.remove();
        notify("重试任务已提交");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        setBatchBusy(false);
      }
    };
  });
}

async function submitTask(payload) {
  try {
    const task = await api("/tasks", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    notify("任务已提交");
    return task;
  } catch (error) {
    notify(error.message, "error");
    return null;
  }
}

async function loadMetricHistory(agentID) {
  const target = document.querySelector(
    `[data-metric-history="${CSS.escape(agentID)}"]`,
  );
  if (!target || target.dataset.loaded) return;
  target.dataset.loaded = "1";
  try {
    const samples = await api(`/metrics/${encodeURIComponent(agentID)}`);
    const chart = trafficChart(samples);
    if (!target.isConnected) return;
    if (chart) {
      const panel = document.createElement("section");
      panel.className = "metric-trend-panel";
      panel.setAttribute("aria-label", "最近 24 小时流量趋势");
      panel.innerHTML = `<header><b>流量趋势</b><small>最近 24 小时 · 每分钟采样</small></header>${chart}`;
      target.replaceWith(panel);
    } else {
      target.innerHTML =
        "<span>⌁</span><b>暂无指标趋势</b><small>节点上线并上报指标后，这里将显示最近 24 小时的上下行速率曲线。</small>";
    }
  } catch {
    if (target.isConnected) {
      target.dataset.loaded = "";
      target.innerHTML =
        "<span>⌁</span><b>指标趋势载入失败</b><small>点击节点资源刷新按钮后重试。</small>";
    }
  }
}

function updateAgentMetrics(item) {
  const root = document.querySelector(
    `[data-agent-metrics="${CSS.escape(item.id)}"]`,
  );
  if (!root) return;
  const metrics = item.metrics || {};
  const setText = (name, value) => {
    const element = root.querySelector(`[data-metric-text="${name}"]`);
    if (element) element.textContent = value;
  };
  const setProgress = (name, available, value) => {
    const element = root.querySelector(`[data-metric-progress="${name}"]`);
    if (!element) return;
    element.value = available ? value : 0;
    element.dataset.available = available ? "1" : "0";
  };
  const online = item.status === "online";
  const unavailable = metrics.collected_at ? "不可用" : "等待采集";
  root.dataset.available = metrics.collected_at ? "1" : "0";
  const dot = root.querySelector("[data-agent-status-dot]");
  if (dot) dot.className = `status-dot ${statusTone(item.status)}`;
  const status = root.querySelector("[data-agent-status-label]");
  if (status) status.textContent = online ? "在线" : "离线";
  const lastSeen = root.querySelector("[data-agent-heartbeat]");
  if (lastSeen) lastSeen.textContent = heartbeat(item.last_seen);
  root
    .querySelectorAll("[data-agent-version]")
    .forEach((element) => (element.textContent = item.version || "未知"));
  setText(
    "stamp",
    metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据",
  );
  setText(
    "cpu",
    metrics.cpu_available
      ? `${Number(metrics.cpu_percent).toFixed(1)}%`
      : unavailable,
  );
  setProgress("cpu", metrics.cpu_available, Number(metrics.cpu_percent || 0));
  setText(
    "memory",
    metrics.memory_available
      ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}`
      : unavailable,
  );
  setProgress(
    "memory",
    metrics.memory_available,
    percent(metrics.memory_used_bytes, metrics.memory_total_bytes),
  );
  setText(
    "disk",
    metrics.disk_available
      ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}`
      : unavailable,
  );
  setProgress(
    "disk",
    metrics.disk_available,
    percent(metrics.disk_used_bytes, metrics.disk_total_bytes),
  );
  setText(
    "download-rate",
    metrics.network_available ? rate(metrics.network_rx_bps) : unavailable,
  );
  setText(
    "upload-rate",
    metrics.network_available ? rate(metrics.network_tx_bps) : unavailable,
  );
  setText(
    "download-total",
    metrics.network_available ? bytes(metrics.network_rx_bytes) : "—",
  );
  setText(
    "upload-total",
    metrics.network_available ? bytes(metrics.network_tx_bytes) : "—",
  );
  Object.entries(item.runtime || {}).forEach(([engine, runtime]) => {
    const card = root.querySelector(`.service-${CSS.escape(engine)}`);
    const installed = Boolean(runtime.installed);
    const existingPending = Boolean(runtime.existing_config_available);
    const existingUnsupportedReason = String(
      runtime.existing_config_unsupported_reason || "",
    );
    // Structure transitions go through the interaction-aware, coalesced
    // refresh path and must not commit the comparison marker first: if the
    // render rejects, the next poll still sees the mismatch and retries
    // instead of leaving the DOM permanently stale.
    if (
      card?.dataset.runtimeStructure === "full" &&
      (card.dataset.coreInstalled !== (installed ? "1" : "0") ||
        card.dataset.existingPending !== (existingPending ? "1" : "0") ||
        card.dataset.existingUnsupported !== existingUnsupportedReason)
    ) {
      requestAgentStructureRefresh();
      return;
    }
    const version = root.querySelector(
      `[data-core-version="${CSS.escape(engine)}"]`,
    );
    if (version)
      version.textContent = runtime.installed
        ? conciseVersion(engine, runtime.version)
        : "尚未安装";
    const service = root.querySelector(
      `[data-core-service="${CSS.escape(engine)}"]`,
    );
    if (service) {
      service.textContent = existingPending
        ? "现有服务待迁移"
        : existingUnsupportedReason
        ? "检测到但不可迁移"
        : installed
        ? serviceStatusName(runtime.service_status)
        : "未安装";
      service.closest(".engine-state").className =
        `engine-state ${existingPending || existingUnsupportedReason ? "warn" : installed ? statusTone(runtime.service_status) : "muted"}`;
      service
        .closest(".service-card")
        ?.querySelectorAll("[data-service-action]")
        .forEach((button) => {
          button.disabled = serviceActionDisabled(
            button.dataset.serviceAction,
            online,
            installed,
            runtime.service_status,
          ) || existingPending || Boolean(existingUnsupportedReason);
        });
    }
  });
  root.querySelectorAll(".core-version-form button[type=submit]").forEach(
    (button) => {
      const card = button.closest(".service-card");
      button.disabled =
        !online ||
        !can("operator") ||
        card?.dataset.existingPending === "1" ||
        Boolean(card?.dataset.existingUnsupported);
    },
  );
  updatePublicIPDisplays(root, metrics, item.labels || {}, item.features || []);
}

async function pollAgentMetrics() {
  if (state.route !== "node-settings" || !can("metrics.read")) return;
  clearTimeout(state.agentPollTimer);
  state.agentPollTimer = null;
  if (document.hidden) {
    clearTimeout(state.agentPollTimer);
    state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
    return;
  }
  const indicators = document.querySelectorAll("[data-metric-poll]");
  cardInteractions.defer(
    () =>
      indicators.forEach((element) => (element.textContent = "正在刷新…")),
    "metrics",
  );
  try {
    await metricsRefresh.run(
      (signal) => api("/agents", { signal }),
      (items) => {
        // Keep the shared runtime snapshot current even when an active card
        // interaction defers DOM patches. Other routes (notably live-config)
        // must not inherit the stale state that preceded an Agent upgrade.
        state.data.agents = items;
        syncActiveBatchSnapshot?.(items);
        cardInteractions.defer(() => {
          items.forEach(updateAgentMetrics);
          const online = items.filter((item) => item.status === "online").length;
          const count = document.querySelector("[data-online-count]");
          if (count) {
            count.textContent = String(online);
            count.hidden = online === 0;
          }
          const sync = document.querySelector("[data-sync-state]");
          sync?.classList.toggle("inactive", online === 0);
          const syncLabel = document.querySelector("[data-sync-label]");
          if (syncLabel)
            syncLabel.textContent = online
              ? `${online} 个节点在线`
              : "等待节点连接";
          indicators.forEach((element) => (element.textContent = "刚刚更新"));
        }, "metrics");
      },
    );
  } catch {
    cardInteractions.defer(
      () =>
        indicators.forEach(
          (element) => (element.textContent = "刷新失败，保留上次数据"),
        ),
      "metrics",
    );
  } finally {
    clearTimeout(state.agentPollTimer);
    if (state.route === "node-settings")
      state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
  }
}

function bindCodeEditors() {
  const formatCodeBytes = (value) => {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / (1024 * 1024)).toFixed(2)} MiB`;
  };
  document.querySelectorAll("[data-code-editor]").forEach((editor) => {
    const input = editor.querySelector("[data-code-input]");
    const gutter = editor.querySelector("[data-line-numbers]");
    const byteLabel = editor.querySelector("[data-code-bytes]");
    const position = editor.querySelector("[data-code-position]");
    const status = editor.querySelector("[data-code-status]");
    const statusDot = editor.querySelector("[data-code-status-dot]");
    const validation = editor.querySelector("[data-code-validation]");
    const reset = editor.querySelector("[data-code-reset]");
    const format = editor.querySelector("[data-code-format]");
    if (!input || !gutter) return;
    const form = input.closest("form");
    const maxBytes = Number(editor.dataset.codeMaxBytes) || 2 * 1024 * 1024;
    const original = input.value;
    const baselineStatus = status?.textContent || "已保存";
    const baselineValidation = validation?.textContent || "";
    input.setAttribute("wrap", "off");
    const updatePosition = () => {
      if (!position) return;
      const before = input.value.slice(0, input.selectionStart);
      const lineStart = before.lastIndexOf("\n") + 1;
      position.textContent = `行 ${before.split("\n").length}，列 ${before.length - lineStart + 1}`;
    };
    const inspect = () => {
      const size = new Blob([input.value]).size;
      if (size > maxBytes)
        return {
          valid: false,
          status: "内容过大",
          message: "配置源码超过 2 MiB 上限，无法提交。",
          size,
        };
      if (!input.value.trim())
        return {
          valid: false,
          status: "内容为空",
          message: "配置源码不能为空。",
          size,
        };
      if ((editor.dataset.codeLanguage || "").toUpperCase() === "JSON") {
        try {
          JSON.parse(input.value);
          return { valid: true, json: true, size };
        } catch {
          return {
            valid: false,
            status: "语法错误",
            message: "JSON 语法错误，请检查括号、逗号和引号。",
            size,
          };
        }
      }
      return { valid: true, size };
    };
    const blockSubmit = (blocked) => {
      form
        ?.querySelectorAll('button[type="submit"], input[type="submit"]')
        .forEach((control) => {
          if (blocked && !control.disabled) {
            control.disabled = true;
            control.dataset.codeBlocked = "1";
          } else if (!blocked && control.dataset.codeBlocked === "1") {
            control.disabled = false;
            delete control.dataset.codeBlocked;
          }
        });
    };
    const update = () => {
      const result = inspect();
      const dirty = input.value !== original;
      gutter.textContent = Array.from(
        { length: Math.max(1, input.value.split("\n").length) },
        (_, index) => String(index + 1),
      ).join("\n");
      if (byteLabel)
        byteLabel.textContent =
          `${formatCodeBytes(result.size)}${result.size > maxBytes ? " / 2 MiB" : ""}`;
      editor.dataset.dirty = dirty ? "1" : "0";
      editor.dataset.codeValid = result.valid ? "1" : "0";
      input.classList.toggle("is-invalid", !result.valid);
      if (reset) reset.disabled = !dirty;
      if (!result.valid) {
        if (status) status.textContent = result.status;
        if (validation) validation.textContent = result.message;
        if (statusDot) statusDot.style.background = "var(--red)";
      } else if (dirty) {
        if (status) status.textContent = "未保存";
        if (validation)
          validation.textContent = result.json
            ? "JSON 语法有效；提交后仍会由节点内核校验。"
            : baselineValidation;
        if (statusDot) statusDot.style.background = "var(--amber)";
      } else {
        if (status) status.textContent = baselineStatus;
        if (validation) validation.textContent = baselineValidation;
        if (statusDot) statusDot.style.background = "var(--green)";
      }
      blockSubmit(!result.valid);
      updatePosition();
    };
    bindEvent(input, "input", update);
    bindEvent(input, "scroll", () => {
      gutter.scrollTop = input.scrollTop;
    });
    ["click", "keyup", "select"].forEach((name) =>
      bindEvent(input, name, updatePosition),
    );
    bindEvent(input, "keydown", (event) => {
      if (
        event.key !== "Tab" ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey
      )
        return;
      event.preventDefault();
      const start = input.selectionStart;
      const end = input.selectionEnd;
      if (!event.shiftKey && start === end) {
        input.setRangeText("  ", start, end, "end");
      } else {
        const value = input.value;
        const lineStart = value.lastIndexOf("\n", Math.max(0, start - 1)) + 1;
        const nextBreak = value.indexOf("\n", end);
        const lineEnd = nextBreak === -1 ? value.length : nextBreak;
        const replacement = value
          .slice(lineStart, lineEnd)
          .split("\n")
          .map((line) =>
            event.shiftKey ? line.replace(/^(?: {1,2}|\t)/, "") : `  ${line}`,
          )
          .join("\n");
        input.setRangeText(replacement, lineStart, lineEnd, "select");
      }
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    bindEvent(reset, "click", () => {
      input.value = original;
      input.setSelectionRange(0, 0);
      update();
      input.focus();
    });
    const runFormat = () => {
      if (!format || input.readOnly || input.disabled) return;
      const selectionStart = input.selectionStart;
      const selectionEnd = input.selectionEnd;
      const scrollTop = input.scrollTop;
      const scrollLeft = input.scrollLeft;
      if (new Blob([input.value]).size > maxBytes) {
        if (validation) validation.textContent = "配置源码超过 2 MiB 上限，无法格式化。";
        if (status) status.textContent = "内容过大";
        if (statusDot) statusDot.style.background = "var(--red)";
        return;
      }
      try {
        const formatted = formatConfigContent(
          input.value,
          editor.dataset.codeLanguage || "",
        );
        if (formatted === input.value) {
          if (validation)
            validation.textContent = "内容已符合排版格式。";
          return;
        }
        if (new Blob([formatted]).size > maxBytes) {
          if (validation)
            validation.textContent = "格式化后超过 2 MiB 上限，已保留原文。";
          if (status) status.textContent = "内容过大";
          if (statusDot) statusDot.style.background = "var(--red)";
          return;
        }
        input.value = formatted;
        const nextLength = input.value.length;
        input.setSelectionRange(
          Math.min(selectionStart, nextLength),
          Math.min(selectionEnd, nextLength),
        );
        input.scrollTop = scrollTop;
        input.scrollLeft = scrollLeft;
        update();
        if (validation)
          validation.textContent = "已格式化；内容未保存，需提交校验。";
        input.focus();
      } catch (error) {
        input.setSelectionRange(
          Math.min(selectionStart, input.value.length),
          Math.min(selectionEnd, input.value.length),
        );
        input.scrollTop = scrollTop;
        input.scrollLeft = scrollLeft;
        if (validation)
          validation.textContent =
            error instanceof ConfigFormatError
              ? error.message
              : "当前内容无法安全格式化。";
        if (status) status.textContent = "无法格式化";
        if (statusDot) statusDot.style.background = "var(--red)";
      }
    };
    bindEvent(format, "click", runFormat);
    bindEvent(
      form,
      "submit",
      (event) => {
        if (inspect().valid) return;
        event.preventDefault();
        update();
        input.focus();
      },
      { capture: true },
    );
    update();
  });
}

function bindModalLifecycle(wrap, onClose) {
  const previousFocus = document.activeElement;
  const restoreEnrollmentEntry = previousFocus?.matches?.(
    "[data-open-enrollment]",
  );
  const inertRoots = new Map();
  const lockBackground = () => {
    document.querySelectorAll(".desktop-app").forEach((root) => {
      if (!inertRoots.has(root)) inertRoots.set(root, root.inert || false);
      root.inert = true;
    });
  };
  const observer = new MutationObserver(lockBackground);
  const previousOverflow = document.body.style.overflow;
  let closed = false;
  lockBackground();
  observer.observe(document.body, { childList: true, subtree: true });
  document.body.style.overflow = "hidden";
  const focusable = () => [
    ...wrap.querySelectorAll(
      'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [href], [tabindex]:not([tabindex="-1"])',
    ),
  ].filter((element) => !element.hidden);
  const close = () => {
    if (closed) return;
    closed = true;
    observer.disconnect();
    document.removeEventListener("keydown", onKeydown);
    inertRoots.forEach((inert, root) => {
      if (root.isConnected) root.inert = inert;
    });
    document.body.style.overflow = previousOverflow;
    wrap.remove();
    const restoreTarget = previousFocus instanceof HTMLElement && previousFocus.isConnected
      ? previousFocus
      : restoreEnrollmentEntry
        ? document.querySelector("[data-open-enrollment]")
        : null;
    restoreTarget?.focus();
    onClose?.();
  };
  const onKeydown = (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab") return;
    const items = focusable();
    if (!items.length) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  document.addEventListener("keydown", onKeydown);
  wrap.querySelectorAll("[data-close]").forEach((button) => {
    button.onclick = close;
  });
  wrap.onclick = (event) => {
    if (event.target === wrap) close();
  };
  return close;
}

function showEnrollmentDialog({ tokenRows, tokenCount, onDelete, onSubmit }) {
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop";
  wrap.innerHTML = `<section class="deploy-command-modal enrollment-dialog" role="dialog" aria-modal="true" aria-labelledby="enrollment-dialog-title" aria-describedby="enrollment-dialog-description"><header class="deploy-command-head"><span class="deploy-command-icon" aria-hidden="true">＋</span><div><p class="eyebrow">添加节点</p><h2 id="enrollment-dialog-title">生成 Agent 部署命令</h2><p id="enrollment-dialog-description">为一台新节点生成长期有效的 enrollment 凭据；命令只会显示供复制，浏览器绝不会执行。</p></div><button class="deploy-command-close" type="button" data-close aria-label="关闭添加节点弹窗">×</button></header><div class="deploy-command-body enrollment-dialog-body"><form class="enrollment-dialog-form"><label>节点名称<input name="name" maxlength="100" required autocomplete="off" placeholder="例如 shanghai-edge-01"></label><p class="enrollment-security-note"><b>凭据仅在生成后显示一次</b><span>新命令在删除前长期有效；生成新命令后，已有安装命令会继续有效，已有命令继续有效。</span></p><footer class="enrollment-form-actions"><button class="button" type="button" data-close>取消</button><button class="button primary" type="submit">生成部署命令</button></footer></form><section class="enrollment-history" aria-labelledby="enrollment-history-title"><header><div><b id="enrollment-history-title">添加记录</b><small>删除记录只会立即撤销对应凭据，不会删除已注册节点或卸载 Agent。</small></div><span data-enrollment-history-count>${tokenCount || 0}</span></header><div data-enrollment-history-list>${tokenRows || '<p class="enrollment-history-empty">暂无添加记录</p>'}</div></section></div></section>`;
  document.body.append(wrap);
  const close = bindModalLifecycle(wrap);
  wrap.querySelectorAll("[data-delete-enrollment]").forEach((button) => {
    button.onclick = async () => {
      if (button.dataset.confirmDelete !== "1") {
        button.dataset.confirmDelete = "1";
        button.dataset.defaultAriaLabel = button.getAttribute("aria-label") || "";
        button.textContent = "再次点击确认删除";
        button.setAttribute(
          "aria-label",
          "再次点击确认删除；凭据会失效，已注册节点和 Agent 不受影响",
        );
        window.setTimeout(() => {
          if (!button.isConnected || button.disabled) return;
          button.dataset.confirmDelete = "";
          button.textContent = "删除";
          if (button.dataset.defaultAriaLabel)
            button.setAttribute("aria-label", button.dataset.defaultAriaLabel);
          else button.removeAttribute("aria-label");
        }, 5000);
        return;
      }
      button.disabled = true;
      try {
        await onDelete(button.dataset.deleteEnrollment);
        button.closest("article")?.remove();
        const list = wrap.querySelector("[data-enrollment-history-list]");
        const count = list?.querySelectorAll("article").length || 0;
        const countLabel = wrap.querySelector("[data-enrollment-history-count]");
        if (countLabel) countLabel.textContent = String(count);
        if (list && count === 0)
          list.innerHTML = '<p class="enrollment-history-empty">暂无添加记录</p>';
        notify("添加节点凭据已删除");
      } catch (error) {
        button.disabled = false;
        notify(error.message, "error");
      }
    };
  });
  wrap.querySelector("form").onsubmit = async (event) => {
    event.preventDefault();
    const submit = event.currentTarget.querySelector("button[type=submit]");
    submit.disabled = true;
    try { await onSubmit(String(new FormData(event.currentTarget).get("name") || "").trim(), close); }
    catch (error) { submit.disabled = false; notify(error.message, "error"); }
  };
  wrap.querySelector("input").focus();
}

function showCommand(command, onClose, heading = "一键添加 QAgent 节点") {
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop";
  wrap.innerHTML = `<section class="deploy-command-modal" role="dialog" aria-modal="true" aria-labelledby="deploy-command-title" aria-describedby="deploy-command-description"><header class="deploy-command-head"><span class="deploy-command-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m7 8 4 4-4 4M13 16h4"/></svg></span><div><p class="eyebrow">Agent 部署命令</p><h2 id="deploy-command-title">${esc(heading)}</h2><p id="deploy-command-description">命令仅供复制；关闭页面不会连接、安装或重启任何节点。</p></div><button class="deploy-command-close" type="button" data-close aria-label="关闭部署命令弹窗">×</button></header><div class="deploy-command-body"><div class="deploy-command-notice"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 5 6v5c0 4.6 2.8 8.1 7 10 4.2-1.9 7-5.4 7-10V6l-7-3Z"/><path d="m9.5 12 1.7 1.7 3.5-3.7"/></svg><span><b>凭据仅在此处显示一次</b><small>请安全复制并妥善保存；命令在对应添加记录被删除前长期有效，控制台不会自动执行或保存命令正文。</small></span></div><section class="deploy-command-shell" aria-label="Agent 安装命令"><header><span><i></i>Terminal</span><small>只读复制模式</small></header><div><span class="deploy-command-prompt" aria-hidden="true">$</span><textarea class="deploy-command-input" rows="5" readonly spellcheck="false" aria-label="Agent 安装命令" data-command>${esc(command)}</textarea></div></section></div><footer class="deploy-command-actions"><span>复制后请在目标 Linux 节点自行执行</span><div><button class="button" type="button" data-close>关闭</button><button class="button primary deploy-command-copy" type="button" data-copy-command><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2v8a2 2 0 0 0 2 2h2"/></svg><span data-copy-label>复制部署命令</span></button></div></footer></section>`;
  document.body.append(wrap);
  const copyButton = wrap.querySelector("[data-copy-command]");
  const commandInput = wrap.querySelector("[data-command]");
  let resetCopyLabel;
  bindModalLifecycle(wrap, () => {
    window.clearTimeout(resetCopyLabel);
    onClose?.();
  });
  copyButton.onclick = async () => {
    try {
      await navigator.clipboard.writeText(command);
    } catch {
      commandInput.select();
      document.execCommand("copy");
      commandInput.setSelectionRange(0, 0);
    }
    const copyLabel = copyButton.querySelector("[data-copy-label]");
    copyButton.classList.add("copied");
    copyLabel.textContent = "已复制";
    window.clearTimeout(resetCopyLabel);
    resetCopyLabel = window.setTimeout(() => {
      copyButton.classList.remove("copied");
      copyLabel.textContent = "复制部署命令";
    }, 1800);
  };
  copyButton.focus();
}
  return {
    agents,
    nodeSettings,
    submitTask,
    bindCodeEditors,
    showCommand,
    pollAgentMetrics,
    updateAgentMetrics,
    cancelAgentInteractions,
    compactPresetPage,
  };
}
