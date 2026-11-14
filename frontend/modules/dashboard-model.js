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
    if (dir && (dir.state === "failed" || dir.error || !dir.reached || Number(dir.loss_percent) >= 100)) {
      return { label: availability.reason || "不可达", tone: "bad", available: false };
    }
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

const NICE_BYTE_STEPS_BY3 = [
  1.5, 3, 4.5, 6, 7.5, 9, 12, 15, 18, 24, 30, 36, 45, 60, 75, 90,
  105, 120, 150, 180, 240, 300, 360, 450, 600, 750, 900, 1050,
];

export function niceTrafficScaleMax(maxBytes) {
  const raw = Number(maxBytes);
  if (!Number.isFinite(raw) || raw <= 0) return 3;
  const target = raw * 1.06;
  if (target <= 3) return 3;
  const exp = Math.min(4, Math.max(0, Math.floor(Math.log(target) / Math.log(1024))));
  const unit = 1024 ** exp;
  const scaled = target / unit;
  for (const step of NICE_BYTE_STEPS_BY3) {
    if (step >= scaled) return Math.round(step * unit);
  }
  return Math.ceil(scaled / 300) * 300 * unit;
}

export function summarizeDashboardTrafficBreakdown(days, month, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const currentMonth = today.slice(0, 7);
  const elapsed = (Array.isArray(days) ? days : []).filter((d) => d.day <= today);
  let rxTotal = 0;
  let txTotal = 0;
  let activeDays = 0;
  let peakRow = null;
  let peakTotal = 0;
  for (const day of elapsed) {
    const rx = Number(day.received_bytes || 0);
    const tx = Number(day.sent_bytes || 0);
    const total = rx + tx;
    rxTotal += rx;
    txTotal += tx;
    if (total > 0) activeDays += 1;
    if (total > peakTotal) {
      peakTotal = total;
      peakRow = day;
    }
  }
  const todayRow = month === currentMonth ? elapsed.find((d) => d.day === today) : null;
  return {
    isCurrentMonth: month === currentMonth,
    todayReceiveBytes: month === currentMonth ? Number(todayRow?.received_bytes || 0) : null,
    todaySentBytes: month === currentMonth ? Number(todayRow?.sent_bytes || 0) : null,
    averageReceiveBytes: elapsed.length ? rxTotal / elapsed.length : null,
    averageSentBytes: elapsed.length ? txTotal / elapsed.length : null,
    peakDay: peakRow?.day || null,
    peakReceiveBytes: peakRow ? Number(peakRow.received_bytes || 0) : 0,
    peakSentBytes: peakRow ? Number(peakRow.sent_bytes || 0) : 0,
    activeDays,
    elapsedDays: elapsed.length,
  };
}

const MACRO_REGION_CODES = {
  APAC: new Set(["HK", "JP", "SG", "TW", "KR", "CN", "MY", "TH", "VN", "ID", "PH", "IN", "AU", "NZ", "MO", "APAC", "AP"]),
  EMEA: new Set(["DE", "NL", "UK", "GB", "FR", "IT", "ES", "CH", "AT", "BE", "PL", "SE", "NO", "FI", "DK", "IE", "PT", "RU", "AE", "SA", "QA", "IL", "TR", "ZA", "EG", "NG", "KE", "EU", "EMEA", "ME", "AF"]),
  AMER: new Set(["US", "CA", "BR", "MX", "AR", "CL", "CO", "PE", "NA", "LATAM", "AMER"]),
};

const MACRO_REGION_PHRASES = {
  APAC: ["香港", "hong kong", "hongkong", "东京", "大阪", "日本", "tokyo", "osaka", "japan", "新加坡", "狮城", "singapore", "台北", "台湾", "高雄", "taipei", "taiwan", "首尔", "韩国", "seoul", "korea", "吉隆坡", "马来", "kuala lumpur", "malaysia", "悉尼", "墨尔本", "澳洲", "澳大利亚", "sydney", "melbourne", "australia", "孟买", "印度", "mumbai", "india", "曼谷", "bangkok", "雅加达", "jakarta", "马尼拉", "manila", "胡志明", "河内", "hanoi", "奥克兰", "auckland", "北京", "上海", "深圳", "广州", "杭州", "成都"],
  EMEA: ["法兰克福", "德国", "frankfurt", "germany", "阿姆斯特丹", "荷兰", "amsterdam", "netherlands", "伦敦", "英国", "london", "paris", "巴黎", "法国", "france", "苏黎世", "zurich", "维也纳", "vienna", "华沙", "warsaw", "斯德哥尔摩", "stockholm", "都柏林", "dublin", "马德里", "madrid", "米兰", "milan", "罗马", "rome", "莫斯科", "moscow", "迪拜", "阿联酋", "dubai", "uae", "利雅得", "riyadh", "约翰内斯堡", "南非", "johannesburg", "开罗", "cairo", "伊斯坦布尔", "istanbul"],
  AMER: ["洛杉矶", "圣何塞", "硅谷", "纽约", "西雅图", "芝加哥", "达拉斯", "迈阿密", "华盛顿", "亚特兰大", "凤凰城", "丹佛", "美国", "los angeles", "san jose", "new york", "seattle", "chicago", "dallas", "miami", "ashburn", "atlanta", "phoenix", "denver", "多伦多", "温哥华", "蒙特利尔", "加拿大", "toronto", "vancouver", "montreal", "canada", "圣保罗", "巴西", "sao paulo", "são paulo", "brazil", "墨西哥城", "mexico", "布宜诺斯艾利斯", "buenos aires", "圣地亚哥", "santiago", "波哥大", "bogota"],
};

const MACRO_CODE_RE = {
  APAC: /(?:^|[^a-z0-9])(?:hk|jp|sg|tw|kr|cn|my|th|vn|id|ph|in|au|nz|mo)(?:[-_ ]?\d+)(?:$|[^a-z0-9])/i,
  EMEA: /(?:^|[^a-z0-9])(?:de|nl|uk|gb|fr|it|es|ch|at|be|pl|se|no|fi|dk|ie|pt|ru|ae|sa|qa|il|tr|za|eg|ng|ke)(?:[-_ ]?\d+)(?:$|[^a-z0-9])/i,
  AMER: /(?:^|[^a-z0-9])(?:us|ca|br|mx|ar|cl|co|pe)(?:[-_ ]?\d+)(?:$|[^a-z0-9])/i,
};

/**
 * Classifies a topology node or endpoint into a macro geographic region
 * (`APAC`, `EMEA`, `AMER`, or `""`) using region codes with city/code fallback.
 */
export function macroRegionForNode(ep) {
  if (!ep || ep.kind === "ext") return "";
  const code = String(ep.regionCode || ep.region_code || ep.region || "").trim().toUpperCase();
  if (code) {
    for (const [macro, set] of Object.entries(MACRO_REGION_CODES)) {
      if (set.has(code)) return macro;
    }
  }
  const text = [ep.name, ep.city, ep.id, ep.regionCode, ep.region_code].filter(Boolean).join(" ").toLowerCase();
  if (!text) return "";
  for (const macro of ["APAC", "EMEA", "AMER"]) {
    if (MACRO_CODE_RE[macro].test(text)) return macro;
    if (MACRO_REGION_PHRASES[macro].some((kw) => text.includes(kw))) return macro;
  }
  return "";
}

/**
 * Projects existing dashboard datasets into a connected delay-monitoring topology.
 */
export function buildDashboardTopology({
  agents = [],
  pageAgents = [],
  orderIDs = [],
  usageRows = [],
  policies = [],
  probes = [],
  metrics = null,
  connections = null,
  month = utcMonth(),
  today = new Date().toISOString().slice(0, 10),
  now = Date.now(),
  maxNodes = 6,
  onlyProbedNodes = false,
} = {}) {
  const probeEndpointSet = new Set();
  for (const pair of probes || []) {
    if (pair?.node1?.agent_id) probeEndpointSet.add(pair.node1.agent_id);
    if (pair?.target_mode !== "ip" && pair?.node2?.agent_id) probeEndpointSet.add(pair.node2.agent_id);
    if (Array.isArray(pair?.nodes)) {
      for (let i = 0; i < pair.nodes.length; i += 1) {
        if (pair?.target_mode === "ip" && i === pair.nodes.length - 1) continue;
        if (pair.nodes[i]?.agent_id) probeEndpointSet.add(pair.nodes[i].agent_id);
      }
    }
  }

  const agentMeta = buildAgentMetaMap(agents, pageAgents);
  const { byAgent: policyMap } = buildPolicyAgentRollup(policies, agentMeta, "all");
  const monthlyByAgent = new Map();
  let monthlyRxTotal = 0;
  let monthlyTxTotal = 0;

  for (const row of usageRows || []) {
    if (!row?.agent_id || !row.day?.startsWith(`${month}-`) || row.day > today) continue;
    if (onlyProbedNodes && !probeEndpointSet.has(row.agent_id)) continue;
    const rx = finiteNonNegative(Number(row.received_bytes)) ? Number(row.received_bytes) : 0;
    const tx = finiteNonNegative(Number(row.sent_bytes)) ? Number(row.sent_bytes) : 0;
    monthlyRxTotal += rx;
    monthlyTxTotal += tx;
    const prev = monthlyByAgent.get(row.agent_id) || { rx: 0, tx: 0, used: 0 };
    prev.rx += rx;
    prev.tx += tx;
    prev.used += rx + tx;
    monthlyByAgent.set(row.agent_id, prev);
  }

  const allIDs = new Set([...agentMeta.keys(), ...policyMap.keys(), ...monthlyByAgent.keys()]);
  const connSources = new Set(
    (connections?.sources || [])
      .filter((src) => src?.agent_id && src.status === "ok")
      .map((src) => src.agent_id),
  );

  const orderIndex = new Map((orderIDs || []).map((id, idx) => [id, idx]));
  const allNodes = [];
  let fleetRxBPS = 0;
  let fleetTxBPS = 0;
  let onlineCount = 0;
  let runningEngines = 0;

  for (const id of allIDs) {
    if (onlyProbedNodes && !probeEndpointSet.has(id)) continue;
    const agent = agentMeta.get(id) || { id, name: id, status: "unknown" };
    const policy = policyMap.get(id) || null;
    const usage = monthlyByAgent.get(id) || { rx: 0, tx: 0, used: 0 };
    const status = agent.status || "unknown";
    const isOnline = status === "online";
    if (isOnline) onlineCount += 1;

    const runtimeEntries = Object.entries(agent.runtime || {});
    const installedEngines = runtimeEntries.filter(([, rt]) => rt?.installed).map(([name]) => name);
    const activeEngines = runtimeEntries
      .filter(([, rt]) => rt?.installed && isOnline && rt?.service_status === "active")
      .map(([name]) => name);
    runningEngines += activeEngines.length;

    const sampledAt = Date.parse(agent.metrics?.collected_at);
    const metricRateValid = Boolean(
      isOnline &&
      agent.metrics?.network_available &&
      Number.isFinite(sampledAt) &&
      Math.abs(now - sampledAt) <= 15000 &&
      finiteNonNegative(Number(agent.metrics.network_rx_bps)) &&
      finiteNonNegative(Number(agent.metrics.network_tx_bps)),
    );
    const rxBPS = metricRateValid
      ? Number(agent.metrics.network_rx_bps)
      : (isOnline && policy ? policy.receiveBPS : 0);
    const txBPS = metricRateValid
      ? Number(agent.metrics.network_tx_bps)
      : (isOnline && policy ? policy.sendBPS : 0);

    fleetRxBPS += rxBPS;
    fleetTxBPS += txBPS;

    allNodes.push({
      id,
      name: agent.name || id,
      regionCode: agent.region_code || "",
      status,
      isOnline,
      ip: agent.public_ipv4 || agent.metrics?.public_ipv4 || agent.public_ipv6 || agent.metrics?.public_ipv6 || "",
      installedEngines,
      activeEngines,
      rxBPS,
      txBPS,
      monthlyRxBytes: usage.rx,
      monthlyTxBytes: usage.tx,
      monthlyUsedBytes: usage.used,
      portCount: policy?.portCount || 0,
      blocked: Boolean(policy?.blocked),
      reportingConnections: connSources.has(id),
      inProbe: probeEndpointSet.has(id),
    });
  }

  allNodes.sort((a, b) => {
    const aOnline = a.isOnline ? 0 : 1;
    const bOnline = b.isOnline ? 0 : 1;
    if (aOnline !== bOnline) return aOnline - bOnline;

    const aProbe = a.inProbe ? 0 : 1;
    const bProbe = b.inProbe ? 0 : 1;
    if (aProbe !== bProbe) return aProbe - bProbe;

    const aActive = (a.rxBPS + a.txBPS > 0 || a.activeEngines.length > 0 || a.reportingConnections) ? 0 : 1;
    const bActive = (b.rxBPS + b.txBPS > 0 || b.activeEngines.length > 0 || b.reportingConnections) ? 0 : 1;
    if (aActive !== bActive) return aActive - bActive;

    const oa = orderIndex.has(a.id) ? orderIndex.get(a.id) : Number.MAX_SAFE_INTEGER;
    const ob = orderIndex.has(b.id) ? orderIndex.get(b.id) : Number.MAX_SAFE_INTEGER;
    if (oa !== ob) return oa - ob;

    const aVol = a.rxBPS + a.txBPS + a.monthlyUsedBytes;
    const bVol = b.rxBPS + b.txBPS + b.monthlyUsedBytes;
    if (bVol !== aVol) return bVol - aVol;
    return a.id.localeCompare(b.id);
  });

  const hasCap = maxNodes !== null && maxNodes !== undefined && Number.isFinite(Number(maxNodes)) && Number(maxNodes) > 0;
  const visibleNodes = hasCap ? allNodes.slice(0, Number(maxNodes)) : allNodes;
  const visibleSet = new Set(visibleNodes.map((n) => n.id));
  const hiddenNodes = hasCap ? allNodes.slice(Number(maxNodes)) : [];

  const maxRxSignal = Math.max(
    0,
    ...visibleNodes.map((n) => (n.rxBPS > 0 ? n.rxBPS : n.monthlyRxBytes)),
  );
  const maxTxSignal = Math.max(
    0,
    ...visibleNodes.map((n) => (n.txBPS > 0 ? n.txBPS : n.monthlyTxBytes)),
  );

  const weightFor = (live, monthly, maxSignal) => {
    const sig = live > 0 ? live : monthly;
    if (!maxSignal || sig <= 0) return 0.25;
    return Math.max(0.28, Math.min(1, 0.28 + 0.72 * (sig / maxSignal)));
  };

  const ingressLinks = visibleNodes.map((node) => ({
    id: `in:${node.id}`,
    from: "ingress",
    to: node.id,
    bytesPerSec: node.rxBPS,
    monthlyBytes: node.monthlyRxBytes,
    active: node.isOnline && (node.rxBPS > 0 || node.activeEngines.length > 0 || node.reportingConnections),
    reportingConnections: node.reportingConnections,
    weight: weightFor(node.rxBPS, node.monthlyRxBytes, maxRxSignal),
  }));

  const egressLinks = visibleNodes.map((node) => ({
    id: `out:${node.id}`,
    from: node.id,
    to: "egress",
    bytesPerSec: node.txBPS,
    monthlyBytes: node.monthlyTxBytes,
    active: node.isOnline && (node.txBPS > 0 || node.activeEngines.length > 0),
    blocked: node.blocked,
    weight: weightFor(node.txBPS, node.monthlyTxBytes, maxTxSignal),
  }));

  const visibleNodeByID = new Map(visibleNodes.map((n) => [n.id, n]));
  const externalTargetMap = new Map();
  const probeLinks = [];
  const probeChains = [];
  const monitoredNodeIDs = new Set();
  let omittedProbeCount = 0;
  let abnormalLinks = 0;

  const summarizeDir = (dir, pair) => {
    const status = probeDirectionStatus(dir, pair?.availability, pair?.active_state);
    const latencyMS = dir?.reached && finiteNonNegative(Number(dir.avg_ms)) ? Number(dir.avg_ms) : null;
    const minMS = dir?.reached && finiteNonNegative(Number(dir.min_ms)) ? Number(dir.min_ms) : null;
    const maxMS = dir?.reached && finiteNonNegative(Number(dir.max_ms)) ? Number(dir.max_ms) : null;
    const stdevMS = dir?.reached && finiteNonNegative(Number(dir.stdev_ms)) ? Number(dir.stdev_ms) : null;
    const lossVal = dir && finiteNonNegative(Number(dir.loss_percent)) ? Number(dir.loss_percent) : null;
    return { status, latencyMS, minMS, maxMS, stdevMS, lossPercent: lossVal, reached: Boolean(dir?.reached) };
  };

  const formatSegmentMetric = (fwd, rev, pair, bidirectional) => {
    const running = Boolean(pair?.availability?.running || ["preparing", "running", "stopping"].includes(pair?.active_state));
    if (running || fwd.status.label === "探测中" || rev?.status?.label === "探测中") return "探测中";
    if (fwd.status.tone === "bad" && !fwd.reached && (!rev || !rev.reached)) {
      const lossMax = Math.max(fwd.lossPercent ?? 0, rev?.lossPercent ?? 0);
      return lossMax >= 100 ? "不可达" : fwd.status.label;
    }
    if (pair?.availability?.ready === false && fwd.latencyMS == null && rev?.latencyMS == null) return "暂不可用";
    const maxLoss = [fwd.lossPercent, rev?.lossPercent].filter((v) => v != null).reduce((m, v) => Math.max(m, v), -1);
    if (fwd.latencyMS != null) {
      const rtt = `${fwd.latencyMS.toFixed(fwd.latencyMS >= 100 ? 0 : 1)}ms`;
      return maxLoss > 0 ? `${rtt} · 丢包${Math.round(maxLoss)}%` : rtt;
    }
    if (bidirectional && rev?.latencyMS != null) {
      const rtt = `回${rev.latencyMS.toFixed(rev.latencyMS >= 100 ? 0 : 1)}ms`;
      return maxLoss > 0 ? `${rtt} · 丢包${Math.round(maxLoss)}%` : rtt;
    }
    return fwd.status.label;
  };

  const pickTone = (fwdTone, revTone, hopTones = []) => {
    const tones = [fwdTone, revTone, ...hopTones].filter(Boolean);
    if (tones.includes("bad")) return "bad";
    if (tones.includes("warn")) return "warn";
    if (tones.includes("ok")) return "ok";
    return "";
  };

  for (const pair of probes || []) {
    const isExternal = pair?.target_mode === "ip";
    const rawNodes = Array.isArray(pair?.nodes) && pair.nodes.length >= 2
      ? pair.nodes
      : [pair?.node1, pair?.node2].filter(Boolean);
    const chainMeta = Array.isArray(pair?.chain) ? pair.chain : [];
    const endpoints = rawNodes.map((ep, idx) => {
      const ext = isExternal && idx === rawNodes.length - 1;
      const meta = chainMeta[idx] || null;
      const addr = String(ep?.address || meta?.address || "").trim();
      const id = ext ? `ext:${addr}` : String(ep?.agent_id || "").trim();
      const visNode = ext ? null : (visibleNodeByID.get(id) || null);
      const agent = ext ? null : (agentMeta.get(id) || null);
      return {
        kind: ext ? "ext" : "node",
        id,
        agentID: ext ? "" : id,
        name: ext
          ? (ep?.name && ep.name !== addr ? ep.name : addr)
          : (meta?.name || ep?.name || visNode?.name || agent?.name || id),
        regionCode: ext ? "" : (meta?.region || visNode?.regionCode || agent?.region_code || ""),
        address: addr,
        online: ext ? true : (meta ? Boolean(meta.online) : Boolean(visNode?.isOnline)),
      };
    });

    const fwdDir = pair?.latest?.forward || null;
    const revDir = pair?.bidirectional ? (pair?.latest?.reverse || null) : null;
    const fwdSum = summarizeDir(fwdDir, pair);
    const revSum = pair?.bidirectional ? summarizeDir(revDir, pair) : null;
    const overallPreTone = pickTone(fwdSum.status.tone, revSum?.status?.tone);

    const allEndpointsVisible = endpoints.length >= 2 && endpoints.every((ep) => (
      ep.kind === "ext" ? Boolean(ep.address) : (Boolean(ep.id) && visibleSet.has(ep.id))
    ));
    if (!allEndpointsVisible) {
      if (overallPreTone === "bad" || overallPreTone === "warn") abnormalLinks += 1;
      omittedProbeCount += 1;
      continue;
    }

    const isMultiHop = endpoints.length >= 3;
    const groupName = String(pair?.group_name || "").trim();
    const running = Boolean(pair?.availability?.running || ["preparing", "running", "stopping"].includes(pair?.active_state));
    const rawHops = Array.isArray(pair?.latest?.hops) && pair.latest.hops.length === endpoints.length - 1
      ? pair.latest.hops
      : (endpoints.length === 2 ? [{ hop_index: 0, forward: fwdDir, reverse: revDir }] : []);

    const builtHops = [];
    const chainNodeIDs = endpoints.filter((ep) => ep.kind === "node").map((ep) => ep.id);
    for (const nid of chainNodeIDs) monitoredNodeIDs.add(nid);

    for (let i = 0; i < endpoints.length - 1; i += 1) {
      const fromEp = endpoints[i];
      const toEp = endpoints[i + 1];
      const hopIsExt = toEp.kind === "ext";
      const hopBidir = Boolean(pair?.bidirectional) && !hopIsExt;
      const hopSample = rawHops[i] || null;
      const hFwd = summarizeDir(hopSample?.forward || (!isMultiHop ? fwdDir : null), pair);
      const hRev = hopBidir ? summarizeDir(hopSample?.reverse || (!isMultiHop ? revDir : null), pair) : null;
      const hTone = pickTone(hFwd.status.tone, hRev?.status?.tone);
      const hLossValues = [hFwd.lossPercent, hRev?.lossPercent].filter((v) => v != null);
      const hMaxLoss = hLossValues.length ? Math.max(...hLossValues) : null;
      const hMetric = formatSegmentMetric(hFwd, hRev, pair, hopBidir);
      const hStatusLabel = hTone === "bad" ? (hFwd.status.tone === "bad" ? hFwd.status.label : hRev?.status?.label || hFwd.status.label) : hFwd.status.label;
      const segID = isMultiHop ? `${pair.id}::hop:${i}` : pair.id;

      if (hopIsExt) {
        if (!externalTargetMap.has(toEp.id)) {
          externalTargetMap.set(toEp.id, {
            id: toEp.id,
            name: toEp.name,
            address: toEp.address,
            linkCount: 0,
          });
        }
        externalTargetMap.get(toEp.id).linkCount += 1;
      }

      const segObj = {
        id: segID,
        probeID: pair.id,
        name: isMultiHop ? `${fromEp.name} ${hopBidir ? "↔" : "→"} ${toEp.name} (${pair.name || "全程链路"})` : (pair.name || `${fromEp.name} → ${toEp.name}`),
        groupName,
        isMultiHop,
        hopIndex: i,
        hopCount: endpoints.length - 1,
        chainEndpoints: chainNodeIDs,
        from: fromEp.id,
        to: toEp.id,
        fromName: fromEp.name,
        toName: toEp.name,
        isExternal: hopIsExt,
        bidirectional: hopBidir,
        running,
        tone: hTone,
        statusLabel: hStatusLabel,
        metricLabel: hMetric,
        latencyMS: hFwd.latencyMS,
        revLatencyMS: hRev ? hRev.latencyMS : null,
        fwdLossPercent: hFwd.lossPercent,
        revLossPercent: hRev ? hRev.lossPercent : null,
        lossPercent: hMaxLoss,
        forward: hFwd,
        reverse: hRev,
        sourceAddr: fromEp.address,
        targetAddr: toEp.address,
        sampledAt: pair?.latest?.sampled_at || "",
      };
      builtHops.push(segObj);
      probeLinks.push(segObj);
    }

    const chainTone = pickTone(fwdSum.status.tone, revSum?.status?.tone, builtHops.map((h) => h.tone));
    if (chainTone === "bad" || chainTone === "warn") abnormalLinks += 1;
    const lossValues = [fwdSum.lossPercent, revSum?.lossPercent].filter((v) => v != null);
    const maxLoss = lossValues.length ? Math.max(...lossValues) : null;
    const chainArrow = pair?.bidirectional ? "↔" : "→";
    const defaultChainTitle = endpoints.map((e) => e.name).join(` ${chainArrow} `);

    probeChains.push({
      id: pair.id,
      name: pair.name || defaultChainTitle,
      groupName,
      endpoints,
      hops: builtHops,
      isMultiHop,
      isExternal,
      bidirectional: Boolean(pair?.bidirectional),
      running,
      tone: chainTone,
      statusLabel: chainTone === "bad" ? (fwdSum.status.tone === "bad" ? fwdSum.status.label : revSum?.status?.label || "异常") : fwdSum.status.label,
      metricLabel: formatSegmentMetric(fwdSum, revSum, pair, Boolean(pair?.bidirectional)),
      latencyMS: fwdSum.latencyMS,
      revLatencyMS: revSum ? revSum.latencyMS : null,
      fwdLossPercent: fwdSum.lossPercent,
      revLossPercent: revSum ? revSum.lossPercent : null,
      lossPercent: maxLoss,
      forward: fwdSum,
      reverse: revSum,
      sampledAt: pair?.latest?.sampled_at || "",
      availabilityReady: pair?.availability?.ready !== false,
      reason: pair?.availability?.reason || "",
      autoEnabled: Boolean(pair?.auto_enabled),
      autoIntervalSeconds: Number(pair?.auto_interval_seconds || 60),
      revision: Number(pair?.revision || 0),
      raw: pair,
    });
  }

  // Group topology into coherent monitoring groups for grouped display.
  const namedGroupMap = new Map();
  const multiHopChains = [];
  const regionalBuckets = {
    APAC: [],
    EMEA: [],
    AMER: [],
    CROSS: [],
  };
  const externalChains = [];

  for (const ch of probeChains) {
    if (ch.groupName) {
      if (!namedGroupMap.has(ch.groupName)) namedGroupMap.set(ch.groupName, []);
      namedGroupMap.get(ch.groupName).push(ch);
    } else if (ch.isMultiHop) {
      multiHopChains.push(ch);
    } else if (ch.isExternal) {
      externalChains.push(ch);
    } else {
      const nodeEps = (ch.endpoints || []).filter((ep) => ep.kind !== "ext");
      const regions = nodeEps.map((ep) => macroRegionForNode(ep));
      if (nodeEps.length >= 2 && regions[0] && regions.every((r) => r === regions[0])) {
        regionalBuckets[regions[0]].push(ch);
      } else {
        regionalBuckets.CROSS.push(ch);
      }
    }
  }

  const summarizeGroupStats = (chains, nodes = [], extTargets = [], links = []) => {
    const okCount = chains.filter((c) => c.tone === "ok").length;
    const badCount = chains.filter((c) => c.tone === "bad" || c.tone === "warn").length;
    const runningCount = chains.filter((c) => c.running).length;
    const hopOrChain = links.length ? links : chains;
    const fwdVals = hopOrChain.map((c) => c.latencyMS).filter((v) => v != null);
    const revVals = hopOrChain.filter((c) => c.bidirectional).map((c) => c.revLatencyMS).filter((v) => v != null);
    return {
      total: chains.length,
      hopCount: links.length,
      nodeCount: nodes.length,
      extCount: extTargets.length,
      okCount,
      badCount,
      runningCount,
      avgFwdMS: fwdVals.length ? Math.round((fwdVals.reduce((s, v) => s + v, 0) / fwdVals.length) * 10) / 10 : null,
      avgRevMS: revVals.length ? Math.round((revVals.reduce((s, v) => s + v, 0) / revVals.length) * 10) / 10 : null,
    };
  };

  const visibleOrder = new Map(visibleNodes.map((n, idx) => [n.id, idx]));
  const finalizeGroup = ({ id, title, kind, region = "", chains = [], explicitNodes = [] }) => {
    const nodeMap = new Map();
    const extMap = new Map();
    const links = [];
    for (const node of explicitNodes) {
      if (node?.id && !nodeMap.has(node.id)) nodeMap.set(node.id, node);
    }
    for (const chain of chains) {
      for (const hop of chain.hops || []) links.push(hop);
      for (const ep of chain.endpoints || []) {
        if (ep.kind === "ext") {
          const prev = extMap.get(ep.id);
          if (prev) {
            prev.linkCount += 1;
          } else {
            const shared = externalTargetMap.get(ep.id);
            extMap.set(ep.id, {
              id: ep.id,
              name: ep.name || shared?.name || ep.address,
              address: ep.address || shared?.address || "",
              linkCount: 1,
            });
          }
        } else if (ep.id && !nodeMap.has(ep.id)) {
          const vis = visibleNodeByID.get(ep.id);
          nodeMap.set(ep.id, vis || {
            id: ep.id,
            name: ep.name || ep.id,
            regionCode: ep.regionCode || "",
            status: ep.online ? "online" : "offline",
            isOnline: Boolean(ep.online),
            ip: ep.address || "",
          });
        }
      }
    }
    const nodes = [...nodeMap.values()].sort((a, b) => {
      const ia = visibleOrder.has(a.id) ? visibleOrder.get(a.id) : Number.MAX_SAFE_INTEGER;
      const ib = visibleOrder.has(b.id) ? visibleOrder.get(b.id) : Number.MAX_SAFE_INTEGER;
      if (ia !== ib) return ia - ib;
      return String(a.name || a.id).localeCompare(String(b.name || b.id));
    });
    const externalTargets = [...extMap.values()];
    return {
      id,
      title,
      kind,
      region,
      chains,
      links,
      nodes,
      externalTargets,
      stats: summarizeGroupStats(chains, nodes, externalTargets, links),
    };
  };

  const probeGroups = [];
  for (const [gTitle, chains] of namedGroupMap.entries()) {
    probeGroups.push(finalizeGroup({
      id: `named:${gTitle}`,
      title: gTitle,
      kind: "named",
      chains,
    }));
  }
  if (multiHopChains.length) {
    probeGroups.push(finalizeGroup({
      id: "chains",
      title: "全程多跳专线",
      kind: "chain",
      chains: multiHopChains,
    }));
  }
  const regionDefs = [
    { key: "APAC", id: "region:apac", title: "亚太节点分组", kind: "region" },
    { key: "EMEA", id: "region:emea", title: "欧非中东分组", kind: "region" },
    { key: "AMER", id: "region:amer", title: "美洲节点分组", kind: "region" },
    { key: "CROSS", id: "cross-region", title: "跨区骨干分组", kind: "mesh" },
  ];
  for (const def of regionDefs) {
    const bucket = regionalBuckets[def.key];
    if (bucket.length) {
      probeGroups.push(finalizeGroup({
        id: def.id,
        title: def.title,
        kind: def.kind,
        region: def.key,
        chains: bucket,
      }));
    }
  }
  if (externalChains.length) {
    probeGroups.push(finalizeGroup({
      id: "external",
      title: "公网出口延迟监测",
      kind: "external",
      chains: externalChains,
    }));
  }
  const unmonitoredNodes = visibleNodes.filter((n) => !monitoredNodeIDs.has(n.id));
  if (unmonitoredNodes.length) {
    probeGroups.push(finalizeGroup({
      id: "unmonitored",
      title: "待纳入监测节点",
      kind: "nodes",
      chains: [],
      explicitNodes: unmonitoredNodes,
    }));
  }

  return {
    nodes: visibleNodes,
    hiddenNodeCount: hiddenNodes.length,
    hiddenOnlineCount: hiddenNodes.filter((n) => n.isOnline).length,
    totalNodes: allNodes.length,
    onlineNodes: onlineCount,
    runningEngines,
    externalTargets: [...externalTargetMap.values()],
    ingressLinks,
    egressLinks,
    probeLinks,
    probeChains,
    probeGroups,
    omittedProbeCount,
    abnormalLinks,
    totalProbeLinks: (probes || []).length,
    ingress: {
      rateBPS: fleetRxBPS,
      monthlyBytes: monthlyRxTotal,
      clientIPs: connections ? Number(connections.ips || 0) : null,
      clientFlows: connections ? Number(connections.flows || 0) : null,
      activeSources: connections ? connSources.size : null,
    },
    egress: {
      rateBPS: fleetTxBPS,
      monthlyBytes: monthlyTxTotal,
      panelRxBPS: panelNetworkRatesValid(metrics) ? Number(metrics.network_rx_bps) : null,
      panelTxBPS: panelNetworkRatesValid(metrics) ? Number(metrics.network_tx_bps) : null,
    },
  };
}
