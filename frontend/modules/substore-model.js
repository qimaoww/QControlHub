import { orderNodesBySavedOrder } from "./node-order.js";
export function filterSubStoreProfiles(profiles, agentID = "", search = "") {
  const normalizedQuery = String(search || "").trim().toLowerCase();
  return (profiles || []).filter((profile) => {
    if (agentID && profile.agent_id !== agentID) return false;
    if (!normalizedQuery) return true;
    return [
      profile.agent_name,
      profile.default_name,
      profile.custom_name,
      profile.profile_tag,
      profile.protocol,
      profile.port,
      profile.engine,
      ...(profile.addresses || []).flatMap((address) => [address.address, address.source, address.family]),
    ]
      .join(" ")
      .toLowerCase()
      .includes(normalizedQuery);
  });
}

export function groupSubStoreProfiles(profiles, agents = [], savedOrder) {
  const groups = [];
  const byAgent = new Map();
  for (const profile of profiles || []) {
    const agentID = profile.agent_id || "missing";
    let group = byAgent.get(agentID);
    if (!group) {
      group = { agent_id: agentID, profiles: [] };
      byAgent.set(agentID, group);
      groups.push(group);
    }
    group.profiles.push(profile);
  }

  const nodes = agents.length
    ? agents
    : groups.map((group) => ({ id: group.agent_id }));
  const position = new Map(
    orderNodesBySavedOrder(nodes, savedOrder).map((node, index) => [node.id, index]),
  );
  return groups.sort(
    (left, right) =>
      (position.get(left.agent_id) ?? nodes.length) -
      (position.get(right.agent_id) ?? nodes.length),
  );
}

export function subStoreSelectionPayload(profiles) {
  return (profiles || [])
    .filter((profile) => profile.selected)
    .map((profile) => ({
      agent_id: profile.agent_id,
      ...(profile.config_id ? { config_id: profile.config_id } : {}),
      engine: profile.engine,
      profile_tag: profile.profile_tag,
      custom_name: String(profile.custom_name || profile.default_name || "").trim(),
      address_mode: String(profile.address_mode || "auto"),
    }));
}

export function subStoreAddressChoices(profile) {
  const byFamily = new Map();
  for (const option of profile?.addresses || []) {
    if ((option.family === "ipv4" || option.family === "ipv6") && !byFamily.has(option.family)) {
      byFamily.set(option.family, option);
    }
  }
  const choices = [{ value: "auto", label: "自动选择" }];
  if (byFamily.has("ipv4")) {
    choices.push({ value: "ipv4", label: `IPv4 · ${byFamily.get("ipv4").address}` });
  }
  if (byFamily.has("ipv6")) {
    choices.push({ value: "ipv6", label: `IPv6 · ${byFamily.get("ipv6").address}` });
  }
  if (byFamily.has("ipv4") && byFamily.has("ipv6")) {
    choices.push({ value: "both", label: "IPv4 + IPv6（同步两条）" });
  }
  return choices;
}

export function subStoreProfileNodeCount(profile) {
  return profile?.selected && profile?.available && profile.address_mode === "both" ? 2 : 1;
}

export function subStoreAddressModeLabel(mode) {
  return {
    auto: "自动选择",
    ipv4: "仅 IPv4",
    ipv6: "仅 IPv6",
    both: "IPv4 + IPv6",
  }[mode] || "自动选择";
}
