export const trafficPortIdentity = (item) => `${item.agent_id}:${item.port}`;
export const trafficEndpointKey = (item) => `endpoint:${item.agent_id}:${item.engine}:${item.port}:${item.protocol}`;
export const trafficCardOrderKey = "qcontrolhub:traffic-card-order";

export const trafficCardIdentity = (item) =>
  trafficPortIdentity(item.policy || item.endpoint || item);

export function orderTrafficItems(items = [], savedOrder = [], nodeOrder = []) {
  if (!savedOrder.length && !nodeOrder.length) return items;
  const position = new Map(savedOrder.map((key, index) => [key, index]));
  const nodePosition = new Map(nodeOrder.map((id, index) => [id, index]));
  const nodeRank = (item) =>
    nodePosition.get((item.policy || item.endpoint || item).agent_id) ?? nodeOrder.length;
  // Explicit card positions win; the remaining cards follow the node list.
  return [...items].sort(
    (left, right) =>
      (position.get(trafficCardIdentity(left)) ?? savedOrder.length) -
      (position.get(trafficCardIdentity(right)) ?? savedOrder.length) ||
      nodeRank(left) - nodeRank(right),
  );
}

// Reordering a filtered view changes only the visible slots in the complete
// card order. Hidden cards keep their relative positions and are not lost
// when the persisted order is updated.
export function mergeVisibleTrafficCardOrder(allKeys = [], visibleKeys = []) {
  const visible = new Set(visibleKeys);
  let nextVisible = 0;
  const merged = allKeys.map((key) =>
    visible.has(key) ? visibleKeys[nextVisible++] : key,
  );
  for (const key of visibleKeys) {
    if (!merged.includes(key)) merged.push(key);
  }
  return merged;
}

// The stored rate describes the last complete Agent report interval. It is no
// longer live once that Agent is offline or the next report is overdue.
export function trafficRateForDisplay(
  value,
  lastReportedAt,
  agentStatus,
  now = Date.now(),
) {
  const reportedAt = Date.parse(lastReportedAt || "");
  const age = now - reportedAt;
  if (
    agentStatus !== "online" ||
    !Number.isFinite(reportedAt) ||
    age < -5000 ||
    age > 45_000
  )
    return 0;
  const numeric = Number(value || 0);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : 0;
}

export function mergeTrafficPorts(policies = [], endpoints = []) {
  const monitored = new Set(policies.map(trafficPortIdentity));
  const visiblePolicies = policies.filter((policy) => policy.monitoring_enabled !== false);
  const discovered = new Map();
  for (const endpoint of endpoints) {
    const identity = trafficPortIdentity(endpoint);
    if (!monitored.has(identity) && !discovered.has(identity)) discovered.set(identity, endpoint);
  }
  return [
    ...visiblePolicies.map((policy) => ({ kind: "policy", key: `policy:${policy.id}`, policy })),
    ...[...discovered.values()]
      .map((endpoint) => ({ kind: "endpoint", key: trafficEndpointKey(endpoint), endpoint })),
  ];
}

export const gibibyte = 1024 ** 3;
export const dateInputValue = (value = new Date()) => {
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return "";
    return date.toISOString().slice(0, 10);
  };
export const cycleName = (value) => (value === "yearly" ? "每年" : "每月");
export const protocolName = (value) => ({ tcp: "TCP", udp: "UDP", both: "TCP + UDP" })[value] || value;
export const quotaInputValue = (value) => {
    const gib = Number(value || 0) / gibibyte;
    if (!gib) return "";
    return (gib >= 0.01 ? gib.toFixed(2) : gib.toFixed(6)).replace(/\.?0+$/, "");
  };

export const policyStatusKey = (policy, agent) => {
    if (!(agent?.features || []).includes("port-traffic-v1")) return "waiting";
    if (policy.blocked) return "blocked";
    if (!policy.last_reported_at) return "waiting";
    if (!policy.enforcement_available) return "error";
    if (policy.quota_enabled === false) return "metering";
    return "active";
  };
export const policyStatus = (policy, agent) => {
    if (!(agent?.features || []).includes("port-traffic-v1")) return ["需升级 Agent", "warn"];
    if (policy.shared_quota?.revoked) return ["已撤销授权", "bad"];
    if (policy.blocked) return ["已超额封禁", "bad"];
    if (!policy.last_reported_at) return ["等待 Agent 同步", "warn"];
    if (!policy.enforcement_available) return ["监控不可用", "bad"];
    if (policy.quota_enabled === false) return ["持续统计", "ok"];
    if (policy.auto_block === false) return ["配额统计", "neutral"];
    return ["监控中", "ok"];
  };
export const eligibleAgents = (agents) => agents.filter((agent) =>
    agent.can_manage !== false && (agent.features || []).includes("port-traffic-v1") && (agent.capabilities || []).length,
  );
