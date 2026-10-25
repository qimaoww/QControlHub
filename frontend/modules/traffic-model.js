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
  const confirmed = policies.filter(policy => policy.deployment_confirmed === true && endpoints.some(endpoint =>
    endpoint.agent_id === policy.agent_id && endpoint.engine === policy.engine && endpoint.port === policy.port &&
    (!policy.config_id || policy.config_id === endpoint.config_id && policy.config_version === endpoint.config_version) &&
    policy.owner_id === endpoint.owner_id));
  const monitored = new Set(confirmed.map(trafficPortIdentity));
  const visiblePolicies = confirmed.filter((policy) => policy.monitoring_enabled !== false);
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

// The traffic workspace serves scope-only navigations from the last complete
// read of the current account. The window stays below the 45s rate staleness
// in trafficRateForDisplay, so reused rates are still truthful.
export const trafficSnapshotTtl = 30_000;

// A snapshot belongs to exactly one account scope. Identity decides, not
// content: logout, an account switch and an authorization change replace
// state.data or state.session, and this refuses the old snapshot outright.
export function reusableTrafficSnapshot(
  snapshot,
  { data, session, canRead, epoch, now = Date.now() },
) {
  if (!snapshot) return null;
  if (snapshot.data !== data || snapshot.session !== session) return null;
  if (snapshot.canRead !== canRead) return null;
  // A call inside one navigation is a live refresh (poll, save, batch, sync)
  // and must read the current values instead of replaying the snapshot.
  if (epoch <= snapshot.epoch) return null;
  if (!(now - snapshot.at < trafficSnapshotTtl)) return null;
  return snapshot;
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
    if (agent?.status === "offline") return "offline";
    if (policy.service_status === "inactive") return "stopped";
    if (policy.service_status === "unknown") return "waiting";
    if (!(agent?.features || []).includes("port-traffic-v1")) return "waiting";
    if (policy.blocked) return "blocked";
    if (!policy.last_reported_at) return "waiting";
    if (!policy.enforcement_available) return "error";
    if (policy.quota_enabled === false) return "metering";
    return "active";
  };
export const policyStatus = (policy, agent) => {
    if (agent?.status === "offline") return ["节点离线", "warn"];
    if (policy.service_status === "inactive") return ["服务已停止", "neutral"];
    if (policy.service_status === "unknown") return ["部署待确认", "warn"];
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

// Ownership is computed by the server from the authenticated principal. Node
// ownership must never decide which user's port enters this scope.
export const trafficInUserScope = (value, filters = {}) => filters.user_scope === "all" || value.is_current_user === true;
export const trafficPolicyEditable = (policy, agent, can) => can("traffic.manage", agent) && agent?.can_manage !== false && !policy.shared_quota;
