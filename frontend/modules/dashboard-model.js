export const utcMonth = (value = new Date()) => {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return date.toISOString().slice(0, 7);
};

export function panelMetricUsage(metrics, kind) {
  const finite = value => typeof value === 'number' && Number.isFinite(value) && value >= 0;
  if (!metrics?.[`${kind}_available`]) return null;
  if (kind === 'cpu') return finite(metrics.cpu_percent) ? Math.min(100, metrics.cpu_percent) : null;
  const used = metrics[`${kind}_used_bytes`], total = metrics[`${kind}_total_bytes`];
  return finite(used) && finite(total) && total > 0 ? Math.min(100, used / total * 100) : null;
}

// Offline and stale samples do not contribute to a live fleet rate. Shared
// instances use the already scoped Agent runtime returned for this account.
export function summarizeDashboardNodes(agents, now = Date.now()) {
  const result = { installed: 0, running: 0, stopped: 0, offline: 0, receiveBPS: 0, sendBPS: 0, sampled: 0, engines: [] };
  const engines = new Map();
  for (const agent of agents) {
    if (agent.status !== 'online') result.offline++;
    for (const [engine, runtime] of Object.entries(agent.runtime || {})) {
      if (!runtime.installed) continue;
      const count = engines.get(engine) || { engine, installed: 0, running: 0 };
      count.installed++; result.installed++;
      if (agent.status === 'online' && runtime.service_status === 'active') { count.running++; result.running++; }
      else if (agent.status === 'online' && runtime.service_status === 'inactive') result.stopped++;
      engines.set(engine, count);
    }
    const metrics = agent.metrics || {}, sampledAt = Date.parse(metrics.collected_at);
    if (agent.status !== 'online' || !metrics.network_available || !Number.isFinite(sampledAt) || now - sampledAt > 15000 || sampledAt - now > 15000) continue;
    if (![metrics.network_rx_bps, metrics.network_tx_bps].every(value => typeof value === 'number' && Number.isFinite(value) && value >= 0)) continue;
    result.sampled++; result.receiveBPS += metrics.network_rx_bps; result.sendBPS += metrics.network_tx_bps;
  }
  result.engines = [...engines.values()].sort((a, b) => a.engine.localeCompare(b.engine));
  return result;
}

export function rankDashboardTraffic(rows, month, today = new Date().toISOString().slice(0, 10), agentIDs = []) {
  const totals = new Map(agentIDs.filter(Boolean).map(id => [id, 0]));
  for (const row of rows || []) {
    if (!row.agent_id || !row.day?.startsWith(`${month}-`) || row.day > today) continue;
    const amount = Number(row.received_bytes || 0) + Number(row.sent_bytes || 0);
    if (!Number.isFinite(amount) || amount < 0) continue;
    totals.set(row.agent_id, (totals.get(row.agent_id) || 0) + amount);
  }
  return [...totals].map(([agentID, used]) => ({ agentID, used })).sort((a, b) => b.used - a.used || a.agentID.localeCompare(b.agentID));
}

export function dashboardTrafficMonthDays(month) {
  if (!/^\d{4}-\d{2}$/.test(String(month || ""))) return [];
  const [year, monthNumber] = month.split("-").map(Number);
  const count = new Date(Date.UTC(year, monthNumber, 0)).getUTCDate();
  return Array.from({ length: count }, (_, index) =>
    `${month}-${String(index + 1).padStart(2, "0")}`,
  );
}

export function aggregateDashboardTrafficDays(rows, month) {
  const totals = new Map(dashboardTrafficMonthDays(month).map((day) => [day, {
    day,
    received_bytes: 0,
    sent_bytes: 0,
    used_bytes: 0,
    peak_receive_bps: 0,
    peak_send_bps: 0,
  }]));
  (rows || []).forEach((row) => {
    const current = totals.get(row.day);
    if (!current) return;
    current.received_bytes += Number(row.received_bytes || 0);
    current.sent_bytes += Number(row.sent_bytes || 0);
    current.used_bytes += Number(row.used_bytes || 0);
    current.peak_receive_bps = Math.max(current.peak_receive_bps, Number(row.peak_receive_bps || 0));
    current.peak_send_bps = Math.max(current.peak_send_bps, Number(row.peak_send_bps || 0));
  });
  return [...totals.values()];
}

// Days come from the month aggregation above, including zero-usage dates.
// The current UTC day participates in the average; future days never do.
export function summarizeDashboardTraffic(days, month, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const currentMonth = today.slice(0, 7);
  const elapsedDays = days.filter((day) => day.day <= today);
  const usage = (day) => day.received_bytes + day.sent_bytes;
  const total = elapsedDays.reduce((sum, day) => sum + usage(day), 0);
  const peak = elapsedDays.reduce((best, day) =>
    usage(day) > (best ? usage(best) : 0) ? day : best, null);
  const todayUsage = days.find((day) => day.day === today);
  return {
    isCurrentMonth: month === currentMonth,
    todayBytes: month === currentMonth ? (todayUsage ? usage(todayUsage) : 0) : null,
    averageBytes: elapsedDays.length ? total / elapsedDays.length : null,
    elapsedDays: elapsedDays.length,
    peakDay: peak?.day || null,
    peakBytes: peak ? usage(peak) : 0,
  };
}

export function summarizeDashboardTrafficPeaks(days, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const elapsedDays = (Array.isArray(days) ? days : []).filter((day) => day.day <= today);
  return {
    peakReceiveBPS: elapsedDays.reduce((best, day) => Math.max(best, Number(day.peak_receive_bps) || 0), 0),
    peakSendBPS: elapsedDays.reduce((best, day) => Math.max(best, Number(day.peak_send_bps) || 0), 0),
  };
}

export const taskActivity = (items, limit = 7) => {
    const groups = [];
    for (const task of items) {
      const previous = groups.at(-1);
      if (previous && previous.task.action === task.action && previous.task.agent_id === task.agent_id && previous.task.engine === task.engine && previous.task.status === task.status) previous.count += 1;
      else groups.push({ task, count: 1 });
      if (groups.length >= limit) break;
    }
    return groups;
  };

const finiteNonNegative = (value) => typeof value === "number" && Number.isFinite(value) && value >= 0;

export function panelNetworkRatesValid(metrics) {
  return Boolean(
    metrics?.network_available &&
    metrics?.network_rate_available !== false &&
    finiteNonNegative(metrics.network_rx_bps) &&
    finiteNonNegative(metrics.network_tx_bps),
  );
}

export function sanitizePanelSample(sample) {
  const collectedMs = Date.parse(sample?.collected_at);
  if (!Number.isFinite(collectedMs) || collectedMs <= 0) return null;
  return {
    collected_at: sample.collected_at,
    cpu_available: Boolean(sample.cpu_available),
    cpu_percent: panelMetricUsage(sample, "cpu"),
    memory_available: Boolean(sample.memory_available),
    memory_percent: panelMetricUsage(sample, "memory"),
    disk_available: Boolean(sample.disk_available),
    disk_percent: panelMetricUsage(sample, "disk"),
    network_rate_valid: panelNetworkRatesValid(sample),
    network_rx_bps: finiteNonNegative(sample.network_rx_bps) ? sample.network_rx_bps : null,
    network_tx_bps: finiteNonNegative(sample.network_tx_bps) ? sample.network_tx_bps : null,
  };
}

export function mergePanelMetricsHistory(snapshot, previousHistory = [], capacity = 60) {
  const incoming = Array.isArray(snapshot?.history) ? snapshot.history.map(sanitizePanelSample).filter(Boolean) : [];
  if (incoming.length) {
    return incoming.slice(-capacity);
  }
  const current = sanitizePanelSample(snapshot);
  if (!current) return previousHistory.slice(-capacity);
  const next = previousHistory.filter((item) => item && item.collected_at !== current.collected_at);
  next.push(current);
  next.sort((a, b) => Date.parse(a.collected_at) - Date.parse(b.collected_at));
  return next.slice(-capacity);
}

function normalizeQuotaBlocked(policy, agent) {
  if (!policy) return false;
  if (policy.blocked === true || policy.auto_blocked === true || policy.status === "blocked") return true;
  if (policy.enforcement_available === false || agent?.status !== "online") return false;
  const limit = Number(policy.limit_bytes);
  const used = Number(policy.used_bytes);
  return policy.quota_enabled !== false && Number.isFinite(limit) && limit > 0 && policy.auto_block !== false && Number.isFinite(used) && used >= limit;
}

function buildAgentMetaMap(agents = [], pageAgents = []) {
  const map = new Map();
  for (const agent of [...pageAgents, ...agents]) {
    if (!agent?.id) continue;
    const prev = map.get(agent.id) || {};
    map.set(agent.id, { ...prev, ...agent });
  }
  return map;
}

function buildPolicyAgentRollup(policies = [], agentMeta = new Map(), scope = "all") {
  const byAgent = new Map();
  let hasCrossOwner = false;
  for (const policy of policies || []) {
    if (!policy?.agent_id) continue;
    if (policy.is_current_user === false) hasCrossOwner = true;
    if (scope === "current" && policy.is_current_user !== true) continue;
    const entry = byAgent.get(policy.agent_id) || {
      agentID: policy.agent_id,
      received: 0,
      sent: 0,
      used: 0,
      receiveBPS: 0,
      sendBPS: 0,
      limitBytes: 0,
      limitedUsedBytes: 0,
      portCount: 0,
      blocked: false,
      hasCurrentUser: false,
      hasOtherUser: false,
      owners: new Set(),
    };
    entry.portCount += 1;
    if (policy.is_current_user === true) entry.hasCurrentUser = true;
    else {
      entry.hasOtherUser = true;
      if (policy.owner_name) entry.owners.add(policy.owner_name);
    }
    const rx = finiteNonNegative(Number(policy.received_bytes)) ? Number(policy.received_bytes) : 0;
    const tx = finiteNonNegative(Number(policy.sent_bytes)) ? Number(policy.sent_bytes) : 0;
    const used = finiteNonNegative(Number(policy.used_bytes)) ? Number(policy.used_bytes) : rx + tx;
    entry.received += rx;
    entry.sent += tx;
    entry.used += used;
    const agent = agentMeta.get(policy.agent_id);
    const sampledAt = Date.parse(policy.last_collected_at || policy.last_reported_at);
    const onlineAndFresh = (!agent?.status || agent.status === "online") && (!Number.isFinite(sampledAt) || Date.now() - sampledAt <= 15000);
    if (policy.enforcement_available !== false && onlineAndFresh) {
      if (finiteNonNegative(Number(policy.receive_bps))) entry.receiveBPS += Number(policy.receive_bps);
      if (finiteNonNegative(Number(policy.send_bps))) entry.sendBPS += Number(policy.send_bps);
    }
    const shared = policy.shared_quota;
    const source = shared || policy;
    const limit = Number(source?.limit_bytes);
    if ((shared || policy.quota_enabled !== false) && Number.isFinite(limit) && limit > 0) {
      entry.limitBytes += Number(policy.limit_bytes) > 0 ? Number(policy.limit_bytes) : limit;
      entry.limitedUsedBytes += finiteNonNegative(Number(source.used_bytes)) ? Number(source.used_bytes) : used;
    }
    if (normalizeQuotaBlocked(policy, agent)) entry.blocked = true;
    byAgent.set(policy.agent_id, entry);
  }
  return { byAgent, hasCrossOwner };
}

export function rankDashboardTrafficView({
  period = "month",
  rows = [],
  month = utcMonth(),
  today = new Date().toISOString().slice(0, 10),
  agents = [],
  policies = [],
  pageAgents = [],
  scope = "all",
  limit = "5",
  orderIDs = [],
} = {}) {
  const agentMeta = buildAgentMetaMap(agents, pageAgents);
  const { byAgent: policyMap, hasCrossOwner: policyCrossOwner } = buildPolicyAgentRollup(policies, agentMeta, scope);
  const hasCrossOwner = policyCrossOwner || [...agentMeta.values()].some((a) => a.is_current_user === false || a.shared === true);
  const orderIndex = new Map(orderIDs.map((id, idx) => [id, idx]));
  let entries = [];

  if (period === "cycle") {
    entries = [...policyMap.values()].map((item) => ({
      ...item,
      owners: [...item.owners],
      agent: agentMeta.get(item.agentID) || null,
      quotaPercent: item.limitBytes > 0 ? Math.min(1000, (item.limitedUsedBytes / item.limitBytes) * 100) : null,
    }));
  } else {
    const allowedAgents = new Set(
      scope === "current"
        ? (policies.length
            ? [...policyMap.keys()]
            : [...agentMeta.values()].filter((a) => a.is_current_user !== false && !a.shared).map((a) => a.id))
        : [...agentMeta.keys()],
    );
    const totals = new Map();
    if (scope === "all" || allowedAgents.size) {
      for (const id of allowedAgents) {
        totals.set(id, { received: 0, sent: 0, used: 0 });
      }
    }
    for (const row of rows || []) {
      if (!row?.agent_id || !row.day?.startsWith(`${month}-`) || row.day > today) continue;
      if (scope === "current" && allowedAgents.size && !allowedAgents.has(row.agent_id)) continue;
      const rx = finiteNonNegative(Number(row.received_bytes)) ? Number(row.received_bytes) : 0;
      const tx = finiteNonNegative(Number(row.sent_bytes)) ? Number(row.sent_bytes) : 0;
      const prev = totals.get(row.agent_id) || { received: 0, sent: 0, used: 0 };
      prev.received += rx;
      prev.sent += tx;
      prev.used += rx + tx;
      totals.set(row.agent_id, prev);
    }
    entries = [...totals.entries()].map(([agentID, item]) => {
      const p = policyMap.get(agentID);
      return {
        agentID,
        received: item.received,
        sent: item.sent,
        used: item.used,
        receiveBPS: p?.receiveBPS || 0,
        sendBPS: p?.sendBPS || 0,
        limitBytes: p?.limitBytes || 0,
        limitedUsedBytes: p?.limitedUsedBytes || 0,
        quotaPercent: p && p.limitBytes > 0 ? Math.min(1000, (p.limitedUsedBytes / p.limitBytes) * 100) : null,
        blocked: Boolean(p?.blocked),
        portCount: p?.portCount || 0,
        hasCurrentUser: p ? p.hasCurrentUser : true,
        hasOtherUser: Boolean(p?.hasOtherUser),
        owners: p ? [...p.owners] : [],
        agent: agentMeta.get(agentID) || null,
      };
    });
  }

  entries.sort((a, b) => {
    if (b.used !== a.used) return b.used - a.used;
    const oa = orderIndex.has(a.agentID) ? orderIndex.get(a.agentID) : Number.MAX_SAFE_INTEGER;
    const ob = orderIndex.has(b.agentID) ? orderIndex.get(b.agentID) : Number.MAX_SAFE_INTEGER;
    if (oa !== ob) return oa - ob;
    return a.agentID.localeCompare(b.agentID);
  });

  const totalUsed = entries.reduce((sum, item) => sum + item.used, 0);
  const maxUsed = entries.length ? entries[0].used : 0;
  const maxCount = limit === "all" ? entries.length : Math.max(1, Number(limit) || 5);
  return {
    items: entries.slice(0, maxCount),
    totalCount: entries.length,
    totalUsed,
    maxUsed,
    hasCrossOwner,
  };
}

export function probeDirectionStatus(dir, availability, activeState = "") {
  if (availability && availability.ready === false) {
    return { label: availability.reason || "暂不可用", tone: "warn", available: false };
  }
  if (availability?.running || ["preparing", "running", "stopping"].includes(activeState)) {
    if (!dir) return { label: "探测中", tone: "warn", available: true };
  }
  if (!dir) {
    return { label: "待采样", tone: "", available: true };
  }
  if (dir.state === "running" || dir.state === "preparing") return { label: "探测中", tone: "warn", available: true };
  if (dir.state === "failed" || dir.error) return { label: dir.error || "探测失败", tone: "bad", available: true };
  if (!dir.reached || dir.loss_percent >= 100) return { label: "不可达", tone: "bad", available: true };
  if (dir.loss_percent >= 20) return { label: `高丢包 ${Number(dir.loss_percent).toFixed(0)}%`, tone: "bad", available: true };
  if (dir.loss_percent > 0) return { label: `丢包 ${Number(dir.loss_percent).toFixed(1)}%`, tone: "warn", available: true };
  return { label: "正常", tone: "ok", available: true };
}

export function summarizeDashboardProbes(pairs = []) {
  const result = { total: pairs.length, enabled: 0, running: 0, healthy: 0, warn: 0, bad: 0 };
  for (const pair of pairs) {
    if (pair.auto_enabled) result.enabled++;
    if (pair.availability?.running || ["preparing", "running", "stopping"].includes(pair.active_state)) result.running++;
    const fwd = pair.latest?.forward;
    const rev = pair.bidirectional ? pair.latest?.reverse : null;
    const statuses = [
      probeDirectionStatus(fwd, pair.availability, pair.active_state),
      ...(pair.bidirectional ? [probeDirectionStatus(rev, pair.availability, pair.active_state)] : []),
    ];
    if (statuses.some((s) => s.tone === "bad")) result.bad++;
    else if (statuses.some((s) => s.tone === "warn")) result.warn++;
    else if (statuses.some((s) => s.tone === "ok")) result.healthy++;
  }
  return result;
}

export function probeHistorySeries(history = [], direction = "forward") {
  if (!Array.isArray(history) || !history.length) return [];
  const items = [...history].sort((a, b) => Date.parse(a.sampled_at) - Date.parse(b.sampled_at));
  return items
    .map((sample) => {
      const dir = sample?.[direction];
      if (!dir) return null;
      return {
        sampledAt: sample.sampled_at,
        avgMS: typeof dir.avg_ms === "number" && Number.isFinite(dir.avg_ms) ? dir.avg_ms : null,
        lossPercent: typeof dir.loss_percent === "number" && Number.isFinite(dir.loss_percent) ? dir.loss_percent : 100,
        reached: Boolean(dir.reached),
      };
    })
    .filter(Boolean);
}

export function isValidProbeIPAddress(value) {
  const trimmed = String(value || "").trim();
  if (!trimmed) return false;
  if (/^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(trimmed)) return true;
  if (!trimmed.includes(":") || /[^0-9a-fA-F:]/.test(trimmed)) return false;
  const parts = trimmed.split("::");
  if (parts.length > 2) return false;
  const segments = trimmed.split(":").filter((s) => s.length > 0);
  if (parts.length === 1 && segments.length !== 8) return false;
  if (parts.length === 2 && segments.length > 7) return false;
  return segments.every((seg) => /^[0-9a-fA-F]{1,4}$/.test(seg));
}

export function isClockSyncAbnormal(clockSync) {
  if (!clockSync || !clockSync.status) return false;
  const status = String(clockSync.status).toLowerCase();
  if (["synchronized", "unavailable", "ok", "synced", "healthy", "normal"].includes(status)) {
    const offset = Number(clockSync.offset_milliseconds);
    return status === "synchronized" && Number.isFinite(offset) && Math.abs(offset) > 2000;
  }
  return true;
}

export function summarizeClientConnectionsPulse(payload) {
  const timeline = Array.isArray(payload?.timeline) ? payload.timeline : [];
  const sources = Array.isArray(payload?.sources) ? payload.sources : [];
  const buckets = timeline.map((item) => ({
    time: item.time,
    flows: finiteNonNegative(Number(item.flows)) ? Number(item.flows) : 0,
    ips: finiteNonNegative(Number(item.ips)) ? Number(item.ips) : 0,
  }));
  return {
    ips: finiteNonNegative(Number(payload?.ips)) ? Number(payload.ips) : 0,
    flows: finiteNonNegative(Number(payload?.flows)) ? Number(payload.flows) : 0,
    activeSources: sources.filter((s) => s.status === "ok").length,
    totalSources: sources.length,
    buckets,
  };
}

export function summarizeExpiringCertificates(response, now = Date.now(), days = 14) {
  const items = Array.isArray(response?.items) ? response.items : [];
  const threshold = now + days * 86400 * 1000;
  let totalOwned = 0;
  let expiringSoon = 0;
  let abnormal = 0;
  const urgent = [];
  for (const cert of items) {
    if (!cert?.owned) continue;
    const status = String(cert.status || "").toLowerCase();
    const notAfterMs = Date.parse(cert.not_after);
    const alreadyExpired = status === "expired" || status === "revoked" || (Number.isFinite(notAfterMs) && notAfterMs <= now);
    if (alreadyExpired) continue;
    totalOwned++;
    const hasError = status === "failed" || String(cert.sync_status || "").toLowerCase() === "failed" || Boolean(cert.error || cert.sync_error);
    const soon = Number.isFinite(notAfterMs) && notAfterMs <= threshold;
    if (hasError) abnormal++;
    if (soon) expiringSoon++;
    if (hasError || soon) {
      urgent.push({
        id: cert.id,
        name: cert.name || cert.domains?.[0] || "未命名证书",
        daysLeft: Number.isFinite(notAfterMs) ? Math.max(0, Math.ceil((notAfterMs - now) / 86400000)) : null,
        abnormal: hasError,
        status: cert.status,
      });
    }
  }
  urgent.sort((a, b) => Number(b.abnormal) - Number(a.abnormal) || (a.daysLeft ?? 999) - (b.daysLeft ?? 999));
  return { totalOwned, expiringSoon, abnormal, urgent: urgent.slice(0, 3) };
}

export function selectMostActiveNodes(agents = [], { limit = 5 } = {}) {
  const list = Array.isArray(agents) ? agents.filter(Boolean) : [];
  if (!list.length) return [];
  const cap = limit === "all" ? list.length : Math.max(1, Number(limit) || 5);
  return list
    .map((agent, index) => {
      const online = agent.status === "online" ? 1 : 0;
      const throughput = online && panelNetworkRatesValid(agent.metrics)
        ? agent.metrics.network_rx_bps + agent.metrics.network_tx_bps
        : 0;
      const activeCores = online
        ? Object.values(agent.runtime || {}).filter((item) => item?.installed && item?.service_status === "active").length
        : 0;
      const cpu = online ? (panelMetricUsage(agent.metrics, "cpu") ?? 0) : 0;
      return { agent, online, throughput, activeCores, cpu, index };
    })
    .sort((a, b) => (
      b.online - a.online ||
      b.throughput - a.throughput ||
      b.activeCores - a.activeCores ||
      b.cpu - a.cpu ||
      a.index - b.index
    ))
    .slice(0, cap)
    .map((item) => item.agent);
}

export function summarizeIPQualityFleet(history) {
  const records = Array.isArray(history?.records) ? history.records : [];
  let succeeded = 0, running = 0, failed = 0;
  for (const r of records) {
    if (r.status === "succeeded") succeeded++;
    else if (r.status === "pending" || r.status === "running") running++;
    else if (r.status === "failed" || r.status === "canceled") failed++;
  }
  return { total: records.length, succeeded, running, failed };
}
