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
  if (format === "mihomo") {
    const value = String(item.mihomo_yaml || "");
    const error = item.mihomo_error || (value ? "" : "此配置暂不支持 Mihomo YAML");
    return { value: error ? "" : value, format: "Mihomo YAML", error };
  }
  const profile = item.profile || {};
  const value = String(profile.uri || "");
  const native = value && !/^[a-z][a-z0-9+.-]*:\/\//i.test(value) && !/URI|URL/i.test(profile.format || "");
  return {
    value: native ? "" : value,
    format: profile.format || "URL",
    error: native ? "此协议不支持 URL，请使用其他显示格式或查看参数详情。" : value ? "" : "暂无可用 URL",
  };
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

export function clientAccessEntryForAddress(entry, mode = "auto") {
  const options = entry?.address_options || [];
  const selected = mode === "auto"
    ? options[0]
    : options.find((option) => option.family === mode) || options[0];
  return selected
    ? { ...entry, address: selected.address, source: selected.source, profiles: selected.profiles }
    : entry;
}
