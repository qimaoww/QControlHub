import { orderNodesBySavedOrder } from "./node-order.js";
import { accountStorage } from "./account-storage.js";
const displayFormatKey = "qcontrolhub:client-display-format";
export function loadClientDisplayFormat() {
  try { return accountStorage.getItem(displayFormatKey) === "mihomo" ? "mihomo" : "url"; }
  catch { return "url"; }
}
export function saveClientDisplayFormat(format) {
  try { accountStorage.setItem(displayFormatKey, format === "mihomo" ? "mihomo" : "url"); }
  catch { /* The current view still works when browser storage is unavailable. */ }
}
export function clientAccessExport(item, format) {
  // A group profile repeats its first variant at the top level. Export each
  // real scheme once, choosing a format independently for every variant.
  const variants = item.profile?.variants;
  const schemes = variants?.length ? variants : [{ profile: item.profile, mihomo_yaml: item.mihomo_yaml, mihomo_error: item.mihomo_error }];
  const outputs = [], errors = [];
  for (const scheme of schemes) {
    const profile = scheme.profile || {}, uri = String(profile.uri || "");
    const isURL = /^(?:ss|vless|vmess|trojan|hysteria2|tuic|socks5|anytls):\/\/[^\s]+$/i.test(uri);
    const yaml = scheme.mihomo_error || profile.mihomo_error ? "" : String(scheme.mihomo_yaml || "");
    let output;
    const native = () => {
      if (uri && !isURL) {
        // The builders deliberately put native content in `uri`. The label
        // alone is never proof of a URL (or of valid JSON).
        if (/YAML/i.test(profile.format || "")) return { value: uri, format: profile.format, extension: "yaml", mime: "application/yaml" };
        try {
          const parsed = JSON.parse(uri);
          if (parsed && typeof parsed === "object" && !Array.isArray(parsed) && Array.isArray(parsed.outbounds) && parsed.outbounds.length)
            return { value: uri, format: /sing-box/i.test(profile.format || "") ? "sing-box JSON" : /Xray/i.test(profile.format || "") ? "Xray JSON" : "客户端 JSON", extension: "json", mime: "application/json" };
        } catch { /* Other native formats are emitted by dedicated builders. */ }
        if (/^Surge Snell/.test(profile.format || "") || profile.format === "WireGuard native config")
          return { value: uri, format: profile.format === "WireGuard native config" ? "WireGuard 配置" : profile.format, extension: "conf", mime: "text/plain" };
      }
      if (profile.xray_json) return { value: profile.xray_json, format: "Xray JSON", extension: "json", mime: "application/json" };
      if (yaml) return { value: yaml, format: "Mihomo YAML", extension: "yaml", mime: "application/yaml" };
      return null;
    };
    if (!item.error) {
      if (format === "mihomo" && yaml) output = { value: yaml, format: "Mihomo YAML", extension: "yaml", mime: "application/yaml" };
      else if (isURL && !profile.uri_error) output = { value: uri, format: "URL", extension: "txt", mime: "text/plain", isURL: true };
      else output = native();
    }
    if (output) outputs.push({ ...output, name: scheme.name || "" });
    else errors.push([scheme.name, item.error || profile.uri_error || scheme.mihomo_error || item.mihomo_error || "暂无可用接入配置"].filter(Boolean).join(" · "));
  }
  return {
    outputs, errors, value: clientAccessExportText(outputs),
    format: [...new Set(outputs.map(output => output.format))].join(" / "), error: errors.join("；"),
  };
}

export function clientAccessExportText(outputs) {
  if (outputs.every(output => output.isURL)) return outputs.map(output => output.value).join("\n");
  if (outputs.length === 1) return outputs[0].value;
  return outputs.map((output, index) => `===== ${output.label || output.name || `方案 ${index + 1}`} · ${output.format} =====\n${output.value}`).join("\n\n");
}

export function clientAccessRecordKey(entry, item) {
  return JSON.stringify([entry.agent_id, entry.engine, entry.config_id || "", entry.instance_id || "", item?.tag || "", item?.port || 0]);
}

export function clientAccessBatchExport(groups, format) {
  const outputs = [], errors = [], seen = new Set();
  for (const group of groups) for (const entry of group.entries) {
    const nodeName = entry.agent_name || entry.agent_id;
    if (!entry.profiles?.length) errors.push(`${nodeName} · ${entry.engine}：${entry.error || (entry.address_required ? "暂无接入地址" : "暂无可用接入配置")}`);
    for (const item of entry.profiles || []) {
      const key = clientAccessRecordKey(entry, item);
      if (seen.has(key)) continue;
      seen.add(key);
      const label = `${nodeName} [${entry.agent_id}] · ${item.protocol || entry.engine} · ${item.tag} · ${item.port}`;
      const exported = clientAccessExport(item, format);
      outputs.push(...exported.outputs.map(output => ({ ...output, label: [label, output.name].filter(Boolean).join(" · ") })));
      errors.push(...exported.errors.map(error => `${label}：${error}`));
    }
  }
  // Even a single native result in a batch needs its node and scheme boundary.
  const value = outputs.every(output => output.isURL) ? clientAccessExportText(outputs)
    : outputs.map(output => `===== ${output.label} · ${output.format} =====\n${output.value}`).join("\n\n");
  return { value, outputs, errors };
}
export function normalizeClientAccessFilters(entries, agents, filters = {}) {
  const agentIDs = new Set((agents || []).map((agent) => agent.id));
  let agent = String(filters.agent || "");
  if (agent && !agentIDs.has(agent)) agent = "";

  const scopedEntries = agent
    ? (entries || []).filter((entry) => entry.agent_id === agent)
    : entries || [];
  const engineIDs = new Set(scopedEntries.map((entry) => entry.engine));
  let engine = String(filters.engine || "");
  if (engine && !engineIDs.has(engine)) engine = "";

  return {
    agent,
    engine,
    query: String(filters.query || "").trim(),
  };
}

export function filterClientAccessEntries(entries, filters = {}) {
  const query = String(filters.query || "").trim().toLowerCase();
  return (entries || []).flatMap((entry) => {
    if (filters.agent && entry.agent_id !== filters.agent) return [];
    if (filters.engine && entry.engine !== filters.engine) return [];
    if (!query) return [entry];
    const entryMatches = [
      entry.agent_name,
      entry.client_name,
      entry.agent_id,
      entry.engine,
      entry.address,
      entry.source,
      ...(entry.address_options || []).flatMap((option) => [option.address, option.source]),
    ]
      .join(" ")
      .toLowerCase()
      .includes(query);
    if (entryMatches) return [entry];
    const profiles = (entry.profiles || []).filter((profile) =>
      [
        profile.tag,
        profile.client_name,
        profile.protocol,
        profile.profile?.format,
      ]
        .join(" ")
        .toLowerCase()
        .includes(query),
    );
    return profiles.length ? [{ ...entry, profiles }] : [];
  });
}

export function groupClientAccessEntries(entries, agents = []) {
  const groups = [];
  const byAgent = new Map();
  for (const entry of entries || []) {
    let group = byAgent.get(entry.agent_id);
    if (!group) {
      group = { agent_id: entry.agent_id, entries: [] };
      byAgent.set(entry.agent_id, group);
      groups.push(group);
    }
    group.entries.push(entry);
  }
  // Match the node cards and sidebar, even when exports arrive in another order.
  const nodes = agents.length ? agents : groups.map((group) => ({ id: group.agent_id }));
  const position = new Map(
    orderNodesBySavedOrder(nodes).map((node, index) => [node.id, index]),
  );
  return groups.sort(
    (left, right) =>
      (position.get(left.agent_id) ?? nodes.length) -
      (position.get(right.agent_id) ?? nodes.length),
  );
}

export function clientAccessAddressChoices(entry) {
  const options = entry?.address_options || [];
  const byFamily = new Map();
  for (const option of options) {
    if ((option.family === "ipv4" || option.family === "ipv6") && !byFamily.has(option.family)) {
      byFamily.set(option.family, option);
    }
  }
  if (!byFamily.has("ipv4") || !byFamily.has("ipv6")) return [];
  return [
    { value: "auto", label: "自动选择" },
    { value: "ipv4", label: `IPv4 · ${byFamily.get("ipv4").address}` },
    { value: "ipv6", label: `IPv6 · ${byFamily.get("ipv6").address}` },
  ];
}
