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

export function dashboardMonthBytes(row, month, direction) {
  if (!row.day?.startsWith(`${month}-`)) return 0;
  const dated = Number(row[`${direction}_bytes`] || 0);
  return dated + Number(row[`month_undated_${direction}_bytes`] || 0);
}

export function rankDashboardTraffic(rows, month, today = new Date().toISOString().slice(0, 10), agentIDs = []) {
  const totals = new Map(agentIDs.filter(Boolean).map(id => [id, 0]));
  for (const row of rows || []) {
    if (!row.agent_id || !row.day?.startsWith(`${month}-`) || row.day > today) continue;
    const amount = dashboardMonthBytes(row, month, 'received') + dashboardMonthBytes(row, month, 'sent');
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
    rows: 0,
  }]));
  (rows || []).forEach((row) => {
    const current = totals.get(row.day);
    if (!current) return;
    current.rows++;
    current.received_bytes += Number(row.received_bytes || 0);
    current.sent_bytes += Number(row.sent_bytes || 0);
    current.used_bytes += Number(row.used_bytes || 0);
    if (row.peak_sampled) current.peak_sampled = true;
    if (row.undated_since) {
      if (!current.undated_since || row.undated_since < current.undated_since) current.undated_since = row.undated_since;
      current.undated_received_bytes = (current.undated_received_bytes || 0) + (row.undated_received_bytes || 0);
      current.undated_sent_bytes = (current.undated_sent_bytes || 0) + (row.undated_sent_bytes || 0);
    }
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

const UTC_DAY_MS = 86400000;
const utcDayShift = (day, offset) => new Date(Date.parse(`${day}T00:00:00Z`) + offset * UTC_DAY_MS).toISOString().slice(0, 10);
const nonNegativeAmount = (value) => {
  const amount = Number(value || 0);
  return Number.isFinite(amount) && amount >= 0 ? amount : null;
};

// Monday 00:00 UTC of the natural week that contains now.
export function dashboardTrafficWeekStart(now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const weekday = new Date(`${today}T00:00:00Z`).getUTCDay();
  return utcDayShift(today, -((weekday + 6) % 7));
}

// Months whose persisted daily rows are needed for the current natural
// periods: the current UTC month, preceded by the previous one only while
// the natural week started in it.
export function dashboardTrafficPeriodMonths(now = new Date()) {
  const month = new Date(now).toISOString().slice(0, 7);
  const weekMonth = dashboardTrafficWeekStart(now).slice(0, 7);
  return weekMonth === month ? [month] : [weekMonth, month];
}

// Today, natural week and natural month totals in UTC, summed from persisted
// per-port daily rows. rows counts the persisted rows inside a period: zero
// rows means nothing was collected, which is not the same as a measured zero.
// Rows repeated for the same port and day are counted once.
export function summarizeDashboardTrafficPeriods(rows, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const month = today.slice(0, 7);
  const weekStart = dashboardTrafficWeekStart(now);
  const daily = new Map();
  const seen = new Set();
  for (const row of rows || []) {
    const day = String(row?.day || "");
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day) || day > today) continue;
    const received = nonNegativeAmount(row.received_bytes), sent = nonNegativeAmount(row.sent_bytes);
    if (received == null || sent == null) continue;
    const identity = row.policy_id ? `${row.policy_id}|${day}` : "";
    if (identity && seen.has(identity)) continue;
    if (identity) seen.add(identity);
    const entry = daily.get(day) || { day, received: 0, sent: 0, total: 0, rows: 0 };
    entry.received += received; entry.sent += sent; entry.total += received + sent; entry.rows++;
    daily.set(day, entry);
  }
  const period = (start, days) => {
    const result = { start, end: today, received: 0, sent: 0, total: 0, rows: 0, series: [] };
    for (const day of days) {
      const entry = daily.get(day), future = day > today;
      result.series.push({ day, total: entry?.total || 0, rows: entry?.rows || 0, future });
      if (future || !entry) continue;
      result.received += entry.received; result.sent += entry.sent; result.total += entry.total; result.rows += entry.rows;
    }
    return result;
  };
  return {
    today: period(today, [today]),
    week: period(weekStart, Array.from({ length: 7 }, (_, index) => utcDayShift(weekStart, index))),
    month: period(`${month}-01`, dashboardTrafficMonthDays(month)),
    months: dashboardTrafficPeriodMonths(now),
  };
}

// Day axis of the traffic stage. The current UTC month is preceded by the
// days of the natural week that fall in the previous month, so today, this
// week and this month sit on one axis. A past month shows its own days only.
export function dashboardTrafficAxis(month, now = new Date()) {
  const days = dashboardTrafficMonthDays(month);
  const today = new Date(now).toISOString().slice(0, 10);
  if (!days.length || month !== today.slice(0, 7)) return days;
  const lead = [];
  for (let day = dashboardTrafficWeekStart(now); day < days[0]; day = utcDayShift(day, 1)) lead.push(day);
  return [...lead, ...days];
}

// Inclusive UTC day range of one stage period. A past month is always read as
// a whole month, whatever natural period is selected.
export function dashboardTrafficRange(period, month, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const days = dashboardTrafficMonthDays(month);
  if (month !== today.slice(0, 7)) return { start: days[0] || "", end: days.at(-1) || "" };
  if (period === "today") return { start: today, end: today };
  if (period === "week") return { start: dashboardTrafficWeekStart(now), end: today };
  return { start: `${month}-01`, end: today };
}

// Per-day totals of the persisted rows on an axis. Rows repeated for the
// same port and day are counted once. A day without a row stays empty,
// which is not the same as a measured zero.
export function buildDashboardTrafficGrid(rows, axis, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const index = new Map(axis.map((day, position) => [day, position]));
  const days = axis.map(day => ({ day, received: 0, sent: 0, total: 0, rows: 0, peakReceiveBPS: 0, peakSendBPS: 0, future: day > today }));
  const seen = new Set();
  for (const row of rows || []) {
    const position = index.get(row?.day);
    if (position == null || days[position].future) continue;
    const received = nonNegativeAmount(row.received_bytes), sent = nonNegativeAmount(row.sent_bytes);
    if (received == null || sent == null) continue;
    const identity = row.policy_id ? `${row.policy_id}|${row.day}` : "";
    if (identity && seen.has(identity)) continue;
    if (identity) seen.add(identity);
    const entry = days[position];
    entry.received += received; entry.sent += sent; entry.total += received + sent; entry.rows++;
    entry.peakReceiveBPS = Math.max(entry.peakReceiveBPS, Number(row.peak_receive_bps) || 0);
    entry.peakSendBPS = Math.max(entry.peakSendBPS, Number(row.peak_send_bps) || 0);
  }
  return { days, dayPeak: Math.max(0, ...days.map(day => day.total)) };
}

// Figures of one inclusive day range of a grid: totals and the busiest
// day. Days without a row are left out.
export function summarizeDashboardTrafficRange(days, range) {
  const result = { received: 0, sent: 0, total: 0, rows: 0, elapsed: 0, collected: 0, peakDay: "", peakBytes: 0 };
  for (const day of days || []) {
    if (day.future || day.day < range.start || day.day > range.end) continue;
    result.elapsed++;
    if (!day.rows) continue;
    result.collected++;
    result.rows += day.rows; result.received += day.received; result.sent += day.sent; result.total += day.total;
    if (!result.peakDay || day.total > result.peakBytes) { result.peakDay = day.day; result.peakBytes = day.total; }
  }
  return result;
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

// A ranking normally covers the elapsed days of one UTC month. An explicit
// inclusive day range replaces that, so a natural week that spans two months
// is ranked as one period.
const inRankingRange = (day, range, month, today) => range
  ? Boolean(day) && day >= range.start && day <= range.end
  : Boolean(day?.startsWith(`${month}-`)) && day <= today;

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
  range = null,
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
      if (!row?.agent_id || !inRankingRange(row.day, range, month, today)) continue;
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

export function isHighJitter(dir) {
  if (!dir?.reached) return false;
  if (dir.stdev_ms == null || dir.stdev_ms === "") return false;
  const stdev = Number(dir.stdev_ms);
  if (!Number.isFinite(stdev)) return false;
  const avg = dir.avg_ms != null && dir.avg_ms !== "" ? Number(dir.avg_ms) : NaN;
  return (Number.isFinite(avg) && avg > 0 && stdev / avg >= 0.35 && stdev >= 15) || stdev >= 50;
}

export function isHighLatency(dir) {
  if (!dir?.reached) return false;
  if (dir.avg_ms == null || dir.avg_ms === "") return false;
  const avg = Number(dir.avg_ms);
  return Number.isFinite(avg) && avg >= 600;
}

export function probeDirectionStatus(dir, availability, activeState = "") {
  if (availability && availability.ready === false) {
    if (dir && (dir.state === "failed" || dir.error || !dir.reached || Number(dir.loss_percent) >= 100)) {
      return { label: availability.reason || "不可达", tone: "bad", available: false };
    }
    return { label: availability.reason || "暂不可用", tone: "warn", available: false };
  }
  if (availability?.running || ["preparing", "running", "stopping", "cleanup_pending"].includes(activeState)) {
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
  if (isHighJitter(dir)) return { label: "高抖动", tone: "warn", available: true };
  if (isHighLatency(dir)) return { label: "高延迟", tone: "warn", available: true };
  return { label: "正常", tone: "ok", available: true };
}

export function summarizeDashboardProbes(pairs = []) {
  const result = { total: pairs.length, enabled: 0, running: 0, healthy: 0, warn: 0, bad: 0 };
  for (const pair of pairs) {
    if (pair.auto_enabled) result.enabled++;
    if (pair.availability?.running || ["preparing", "running", "stopping", "cleanup_pending"].includes(pair.active_state)) result.running++;
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

const ACTIVE_PROBE_STATES = new Set(["preparing", "running", "stopping", "cleanup_pending"]);

export function isProbeRunning(pair) {
  return Boolean(pair?.availability?.running || ACTIVE_PROBE_STATES.has(pair?.active_state));
}

function normalizeDirectionMetrics(dir, availability, activeState = "") {
  const status = probeDirectionStatus(dir, availability, activeState);
  const hasDir = Boolean(dir);
  const avgMS = dir?.reached && dir.avg_ms != null && dir.avg_ms !== "" && finiteNonNegative(Number(dir.avg_ms)) ? Number(dir.avg_ms) : null;
  const minMS = dir?.reached && dir.min_ms != null && dir.min_ms !== "" && finiteNonNegative(Number(dir.min_ms)) ? Number(dir.min_ms) : null;
  const maxMS = dir?.reached && dir.max_ms != null && dir.max_ms !== "" && finiteNonNegative(Number(dir.max_ms)) ? Number(dir.max_ms) : null;
  const stdevMS = dir?.reached && dir.stdev_ms != null && dir.stdev_ms !== "" && finiteNonNegative(Number(dir.stdev_ms)) ? Number(dir.stdev_ms) : null;
  const lossPercent = hasDir && dir.loss_percent != null && dir.loss_percent !== "" && finiteNonNegative(Number(dir.loss_percent)) ? Number(dir.loss_percent) : null;
  const sent = hasDir && dir.sent != null && dir.sent !== "" && finiteNonNegative(Number(dir.sent)) ? Number(dir.sent) : null;
  const received = hasDir && dir.received != null && dir.received !== "" && finiteNonNegative(Number(dir.received)) ? Number(dir.received) : null;
  const jitterHigh = Boolean(isHighJitter(dir));
  const latencyHigh = Boolean(isHighLatency(dir));
  const jitterRatio = (avgMS > 0 && stdevMS != null) ? stdevMS / avgMS : null;
  return {
    available: hasDir,
    state: dir?.state || "",
    reached: Boolean(dir?.reached),
    avgMS,
    minMS,
    maxMS,
    stdevMS,
    lossPercent,
    sent,
    received,
    error: String(dir?.error || ""),
    jitterHigh,
    latencyHigh,
    jitterRatio,
    status,
  };
}

function pickSeverityTone(tones = []) {
  const list = tones.filter(Boolean);
  if (list.includes("bad")) return "bad";
  if (list.includes("warn")) return "warn";
  if (list.includes("ok")) return "ok";
  return "";
}

function nodeRoleLabel(index, total, isExternal) {
  if (isExternal && index === total - 1) return "公网目标";
  if (index === 0) return "起点";
  if (index === total - 1) return "终点";
  return `中继 ${index}`;
}

function nodeIssueLabel(meta, ep, isExternal) {
  if (isExternal) return "";
  if (!meta) return "";
  if (!meta.name && !meta.online && !meta.granted && !meta.ping) return "节点不可访问或已删除";
  if (meta.granted === false) return ep?.instance_id && ep.instance_id !== "main" ? "共享未授权" : "无探测权限";
  if (meta.online === false) return "节点离线";
  if (meta.ping === false) return "缺少 ping";
  return "";
}

function buildGroupStatusBadge({ running, availability, forward, reverse, hops, bidirectional }) {
  if (running) return { label: "探测中", tone: "warn" };
  if (availability && availability.ready === false) {
    if (forward.status.tone === "bad" || (bidirectional && reverse?.status.tone === "bad")) {
      return { label: "不可达", tone: "bad" };
    }
    return { label: "节点不可用", tone: "warn" };
  }
  const dirTones = [forward.status.tone, ...(bidirectional && reverse ? [reverse.status.tone] : [])];
  const hopTones = hops.map((h) => h.tone);
  const worstTone = pickSeverityTone([...dirTones, ...hopTones]);
  if (worstTone === "bad") {
    const isUnreach = (!forward.available || !forward.reached || (forward.lossPercent ?? 0) >= 100)
      || (bidirectional && reverse && (!reverse.reached || (reverse.lossPercent ?? 0) >= 100));
    if (isUnreach) return { label: "不可达", tone: "bad" };
    return { label: "链路异常", tone: "bad" };
  }
  if (worstTone === "warn") {
    const hasLoss = (forward.lossPercent ?? 0) > 0
      || (bidirectional && (reverse?.lossPercent ?? 0) > 0)
      || hops.some((h) => (h.worstLossPercent ?? 0) > 0);
    if (hasLoss) return { label: "丢包告警", tone: "warn" };
    const hasJitter = Boolean(forward.jitterHigh)
      || (bidirectional && Boolean(reverse?.jitterHigh))
      || hops.some((h) => Boolean(h.forward?.jitterHigh) || (h.bidirectional && Boolean(h.reverse?.jitterHigh)));
    if (hasJitter) return { label: "高抖动", tone: "warn" };
    const hasLatency = Boolean(forward.latencyHigh)
      || (bidirectional && Boolean(reverse?.latencyHigh))
      || hops.some((h) => Boolean(h.forward?.latencyHigh) || (h.bidirectional && Boolean(h.reverse?.latencyHigh)));
    if (hasLatency) return { label: "高延迟", tone: "warn" };
    return { label: "丢包告警", tone: "warn" };
  }
  if (worstTone === "ok") return { label: "正常", tone: "ok" };
  return { label: "待采样", tone: "" };
}

/**
 * Builds normalized One-Group-One-Link monitoring models from saved probe pairs
 * and live agent metadata. Each group strictly corresponds to one link (2-node,
 * custom IP, or 2-10 hop chain).
 */
export function buildProbeGroupModels({ pairs = [], agents = [], orderIDs = [] } = {}) {
  const agentByID = new Map();
  for (const agent of agents || []) {
    if (agent?.id) agentByID.set(agent.id, agent);
  }
  const orderIndex = new Map((orderIDs || []).map((id, idx) => [id, idx]));

  return (pairs || []).map((pair, pairIdx) => {
    const isExternal = pair?.target_mode === "ip";
    const bidirectional = Boolean(pair?.bidirectional) && !isExternal;
    const running = isProbeRunning(pair);
    const availability = {
      ready: pair?.availability?.ready !== false,
      reason: String(pair?.availability?.reason || ""),
      running,
    };
    const rawNodes = Array.isArray(pair?.nodes) && pair.nodes.length >= 2
      ? pair.nodes
      : [pair?.node1, pair?.node2].filter(Boolean);
    const chainMeta = Array.isArray(pair?.chain) ? pair.chain : [];

    const nodes = rawNodes.map((ep, idx) => {
      const ext = isExternal && idx === rawNodes.length - 1;
      const meta = chainMeta[idx] || null;
      const agentID = ext ? "" : String(ep?.agent_id || meta?.agent_id || "").trim();
      const instanceID = ext ? "" : String(ep?.instance_id || meta?.instance_id || "").trim();
      const liveAgent = agentID ? agentByID.get(agentID) || null : null;
      const address = String(ep?.address || meta?.address || "").trim();
      const name = ext
        ? (address || ep?.name || "公网目标")
        : String(meta?.name || ep?.name || liveAgent?.name || agentID || address || `节点 ${idx + 1}`);
      const region = ext ? "" : String(meta?.region || liveAgent?.region_code || liveAgent?.labels?.region_code || "").trim().toUpperCase();
      const online = ext ? true : (meta ? Boolean(meta.online) : liveAgent ? liveAgent.status === "online" : false);
      const granted = ext ? true : (meta ? Boolean(meta.granted) : true);
      const ping = ext ? true : (meta ? Boolean(meta.ping) : true);
      const issue = nodeIssueLabel(meta || (liveAgent ? { name: liveAgent.name, online: liveAgent.status === "online", granted: true, ping: true } : null), ep, ext);
      return {
        index: idx,
        role: nodeRoleLabel(idx, rawNodes.length, ext),
        kind: ext ? "ext" : "node",
        agentID,
        instanceID,
        shared: Boolean(!ext && instanceID && instanceID !== "main"),
        publicIP: ext ? address : String(liveAgent?.public_ipv4 || liveAgent?.metrics?.public_ipv4 || liveAgent?.public_ipv6 || liveAgent?.metrics?.public_ipv6 || address || "").trim(),
        name,
        region,
        address,
        online,
        granted,
        ping,
        issue,
      };
    });

    const latest = pair?.latest || null;
    const fwdSummary = normalizeDirectionMetrics(latest?.forward || null, availability, pair?.active_state);
    const revSummary = bidirectional ? normalizeDirectionMetrics(latest?.reverse || null, availability, pair?.active_state) : null;

    const rawHops = Array.isArray(latest?.hops) && latest.hops.length
      ? latest.hops
      : (nodes.length === 2 && latest ? [{ hop_index: 0, forward: latest.forward, reverse: latest.reverse }] : []);

    const hops = [];
    for (let i = 0; i < Math.max(0, nodes.length - 1); i += 1) {
      const from = nodes[i];
      const to = nodes[i + 1];
      const hopBidir = bidirectional && to.kind !== "ext";
      const hopSample = rawHops.find((h) => h?.hop_index === i) || rawHops[i] || null;
      const hopAvail = {
        ready: availability.ready && (from.kind === "ext" || (from.online && from.granted && from.ping)) && (to.kind === "ext" || (to.online && to.granted && to.ping)),
        reason: from.issue || to.issue || availability.reason,
        running,
      };
      const fwd = normalizeDirectionMetrics(hopSample?.forward || null, hopAvail, pair?.active_state);
      const rev = hopBidir ? normalizeDirectionMetrics(hopSample?.reverse || null, hopAvail, pair?.active_state) : null;
      const tone = pickSeverityTone([fwd.status.tone, rev?.status?.tone]);
      const lossList = [fwd.lossPercent, rev?.lossPercent].filter((v) => v != null);
      const worstLoss = lossList.length ? Math.max(...lossList) : null;
      const primaryTone = tone || (!hopAvail.ready ? "warn" : "");
      hops.push({
        id: `${pair.id}::hop:${i}`,
        hopIndex: i,
        from,
        to,
        bidirectional: hopBidir,
        forward: fwd,
        reverse: rev,
        tone: primaryTone,
        statusLabel: primaryTone === "bad"
          ? (fwd.status.tone === "bad" ? fwd.status.label : (rev?.status?.label || "异常"))
          : (fwd.status.tone === "warn" ? fwd.status.label : (rev?.status?.tone === "warn" ? rev.status.label : fwd.status.label)),
        worstLossPercent: worstLoss,
        ready: hopAvail.ready,
        unavailableReason: hopAvail.ready ? "" : hopAvail.reason,
      });
    }

    const lossCandidates = [
      fwdSummary.lossPercent,
      revSummary?.lossPercent,
      ...hops.map((h) => h.worstLossPercent),
    ].filter((v) => v != null);
    const worstLossPercent = lossCandidates.length ? Math.max(...lossCandidates) : null;
    const overallTone = pickSeverityTone([
      fwdSummary.status.tone,
      revSummary?.status?.tone,
      ...hops.map((h) => h.tone),
      availability.ready ? "" : "warn",
    ]);
    const statusBadge = buildGroupStatusBadge({
      running,
      availability,
      forward: fwdSummary,
      reverse: revSummary,
      hops,
      bidirectional,
    });
    const arrow = bidirectional ? " ⇄ " : " → ";
    const routeText = nodes.map((n) => n.name).join(arrow);
    const groupName = String(pair?.group_name || "").trim() || String(pair?.name || "").trim() || pair.id;
    const remark = String(pair?.name || "").trim();
    const firstOrder = nodes[0]?.agentID && orderIndex.has(nodes[0].agentID)
      ? orderIndex.get(nodes[0].agentID)
      : Number.MAX_SAFE_INTEGER;

    return {
      id: pair.id,
      groupName,
      remark: remark && remark !== groupName ? remark : "",
      routeText,
      targetMode: isExternal ? "ip" : "",
      isExternal,
      isMultiHop: nodes.length > 2,
      bidirectional,
      autoEnabled: Boolean(pair?.auto_enabled),
      autoIntervalSeconds: Number(pair?.auto_interval_seconds || 60),
      parameters: pair?.parameters || {},
      revision: Number(pair?.revision || 0),
      running,
      activeState: String(pair?.active_state || ""),
      availability,
      nodes,
      hopCount: hops.length,
      hops,
      overall: {
        forward: fwdSummary,
        reverse: revSummary,
        tone: overallTone,
        statusBadge,
        worstLossPercent,
        sampledAt: latest?.sampled_at || "",
        error: String(latest?.error || ""),
      },
      history: Array.isArray(pair?.history) ? pair.history : [],
      orderHint: firstOrder,
      sourceIndex: pairIdx,
      raw: pair,
    };
  });
}

export function summarizeProbeGroups(groups = []) {
  const stats = {
    total: groups.length,
    healthy: 0,
    warn: 0,
    bad: 0,
    idle: 0,
    running: 0,
    autoEnabled: 0,
    multiHop: 0,
    unavailable: 0,
  };
  for (const g of groups) {
    if (g.running) stats.running += 1;
    if (g.autoEnabled) stats.autoEnabled += 1;
    if (g.isMultiHop) stats.multiHop += 1;
    if (!g.availability?.ready) stats.unavailable += 1;
    const tone = g.overall?.statusBadge?.tone || g.overall?.tone || "";
    if (tone === "bad") stats.bad += 1;
    else if (tone === "warn") stats.warn += 1;
    else if (tone === "ok") stats.healthy += 1;
    else stats.idle += 1;
  }
  return stats;
}

export function groupMetricForDirection(group, metricDir = "both") {
  const fwd = group?.overall?.forward || null;
  const rev = group?.bidirectional ? (group?.overall?.reverse || null) : null;
  if (metricDir === "forward") {
    return {
      latencyMS: fwd?.avgMS ?? null,
      lossPercent: fwd?.lossPercent ?? null,
      dir: fwd,
      unavailableReverse: false,
    };
  }
  if (metricDir === "reverse") {
    return {
      latencyMS: rev?.avgMS ?? null,
      lossPercent: rev?.lossPercent ?? null,
      dir: rev,
      unavailableReverse: !group?.bidirectional,
    };
  }
  const lats = [fwd?.avgMS, rev?.avgMS].filter((v) => typeof v === "number" && Number.isFinite(v));
  const avgBoth = lats.length ? Math.max(...lats) : null;
  return {
    latencyMS: avgBoth,
    lossPercent: group?.overall?.worstLossPercent ?? null,
    dir: fwd || rev,
    unavailableReverse: false,
  };
}

export function filterAndSortProbeGroups(
  groups = [],
  { statusFilter = "all", sortBy = "default", metricDir = "both" } = {},
) {
  const filtered = groups.filter((g) => {
    if (statusFilter === "abnormal") {
      return !g.availability?.ready || g.overall?.tone === "bad" || g.overall?.tone === "warn";
    }
    if (statusFilter === "running") return Boolean(g.running);
    if (statusFilter === "multihop") return Boolean(g.isMultiHop);
    if (statusFilter === "paused") return !g.autoEnabled;
    return true;
  });

  const toneRank = { bad: 0, warn: 1, "": 2, ok: 3 };
  return [...filtered].sort((a, b) => {
    if (sortBy === "name_asc") {
      return a.groupName.localeCompare(b.groupName, "zh-CN") || a.id.localeCompare(b.id);
    }
    if (sortBy === "latency_desc") {
      const la = groupMetricForDirection(a, metricDir).latencyMS;
      const lb = groupMetricForDirection(b, metricDir).latencyMS;
      if (la == null && lb == null) return a.sourceIndex - b.sourceIndex;
      if (la == null) return 1;
      if (lb == null) return -1;
      if (lb !== la) return lb - la;
      return a.groupName.localeCompare(b.groupName, "zh-CN");
    }
    if (sortBy === "loss_desc") {
      const la = groupMetricForDirection(a, metricDir).lossPercent ?? -1;
      const lb = groupMetricForDirection(b, metricDir).lossPercent ?? -1;
      if (lb !== la) return lb - la;
      const ma = groupMetricForDirection(a, metricDir).latencyMS ?? -1;
      const mb = groupMetricForDirection(b, metricDir).latencyMS ?? -1;
      if (mb !== ma) return mb - ma;
      return a.groupName.localeCompare(b.groupName, "zh-CN");
    }
    if (sortBy === "abnormal") {
      const ra = toneRank[a.overall?.tone || ""] ?? 2;
      const rb = toneRank[b.overall?.tone || ""] ?? 2;
      if (ra !== rb) return ra - rb;
      const la = groupMetricForDirection(a, metricDir).lossPercent ?? -1;
      const lb = groupMetricForDirection(b, metricDir).lossPercent ?? -1;
      if (lb !== la) return lb - la;
      const ma = groupMetricForDirection(a, metricDir).latencyMS ?? -1;
      const mb = groupMetricForDirection(b, metricDir).latencyMS ?? -1;
      if (mb !== ma) return mb - ma;
      return a.groupName.localeCompare(b.groupName, "zh-CN");
    }
    return a.sourceIndex - b.sourceIndex || a.id.localeCompare(b.id);
  });
}

export function resolveSelectedProbeGroup(groups = [], savedGroupName = "") {
  const target = String(savedGroupName || "").trim();
  if (!groups.length) return { group: null, fellBack: Boolean(target) };
  if (target) {
    const exact = groups.find((g) => g.groupName === target);
    if (exact) return { group: exact, fellBack: false };
  }
  return { group: groups[0], fellBack: Boolean(target) };
}

const MACRO_REGION_CODES = {
  APAC: new Set(["CN", "HK", "MO", "TW", "JP", "KR", "SG", "MY", "TH", "VN", "ID", "PH", "IN", "AU", "NZ", "PK", "BD", "LK", "APAC"]),
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

const DOMAIN_GROUP_META = Object.freeze({
  "domain:multihop": { id: "domain:multihop", title: "多跳全程专线", kind: "multihop", region: "", order: 0 },
  "domain:apac": { id: "domain:apac", title: "亚太互联拓扑", kind: "region", region: "APAC", order: 1 },
  "domain:emea": { id: "domain:emea", title: "欧非中东拓扑", kind: "region", region: "EMEA", order: 2 },
  "domain:amer": { id: "domain:amer", title: "美洲互联拓扑", kind: "region", region: "AMER", order: 3 },
  "domain:cross": { id: "domain:cross", title: "跨区骨干拓扑", kind: "cross", region: "CROSS", order: 4 },
  "domain:external": { id: "domain:external", title: "公网出口探测", kind: "external", region: "", order: 5 },
});

export function domainGroupForProbeGroup(group) {
  if (group?.isExternal) return DOMAIN_GROUP_META["domain:external"];
  if (group?.isMultiHop) return DOMAIN_GROUP_META["domain:multihop"];
  const nodeEps = (group?.nodes || []).filter((ep) => ep.kind !== "ext");
  const regions = nodeEps.map((ep) => macroRegionForNode(ep));
  if (nodeEps.length >= 2 && regions[0] && regions.every((r) => r === regions[0])) {
    const key = `domain:${regions[0].toLowerCase()}`;
    if (DOMAIN_GROUP_META[key]) return DOMAIN_GROUP_META[key];
  }
  return DOMAIN_GROUP_META["domain:cross"];
}

function formatTopologyLinkMetric({ running, ready, reason, dirMode, bidirectional, fwd, rev }) {
  if (running) return "探测中";
  if (dirMode === "reverse" && !bidirectional) return "单向无回程";
  const pickDir = dirMode === "reverse" ? rev : fwd;
  const activeStatus = pickDir?.status;
  if (!ready && !pickDir?.available) return reason || "暂不可用";
  if (!pickDir?.available) return "待采样";
  if (activeStatus?.tone === "bad" && (!pickDir.reached || (pickDir.lossPercent ?? 0) >= 100)) {
    return pickDir.error || "不可达";
  }
  const fmtRtt = (ms) => (typeof ms === "number" && Number.isFinite(ms) ? `${ms.toFixed(ms >= 100 ? 0 : 1)}ms` : "—");
  if (dirMode === "both" && bidirectional) {
    const fStr = fwd?.reached && fwd.avgMS != null ? fmtRtt(fwd.avgMS) : (fwd?.available ? "×" : "—");
    const rStr = rev?.reached && rev.avgMS != null ? fmtRtt(rev.avgMS) : (rev?.available ? "×" : "—");
    const lossList = [fwd?.lossPercent, rev?.lossPercent].filter((v) => v != null);
    const worstLoss = lossList.length ? Math.max(...lossList) : null;
    return `${fStr}/${rStr}${worstLoss > 0 ? ` · ${worstLoss.toFixed(worstLoss % 1 ? 1 : 0)}%` : ""}`;
  }
  const rtt = pickDir.avgMS != null ? fmtRtt(pickDir.avgMS) : "—";
  const loss = pickDir.lossPercent != null && pickDir.lossPercent > 0
    ? ` · 丢包${pickDir.lossPercent.toFixed(pickDir.lossPercent % 1 ? 1 : 0)}%`
    : "";
  return `${rtt}${loss}`;
}

function directionalSlice(group, hopOrOverall, metricDir) {
  const dirModeActive = metricDir === "reverse" ? (hopOrOverall.reverse?.state || "") : (hopOrOverall.forward?.state || "");
  const hopRunning = ACTIVE_PROBE_STATES.has(hopOrOverall.forward?.state)
    || (Boolean(hopOrOverall.bidirectional ?? group.bidirectional) && ACTIVE_PROBE_STATES.has(hopOrOverall.reverse?.state))
    || ACTIVE_PROBE_STATES.has(dirModeActive);
  const running = Boolean(group.running || hopRunning);
  const bidirectional = Boolean(hopOrOverall.bidirectional ?? group.bidirectional);
  const fwd = hopOrOverall.forward || null;
  const rev = bidirectional ? (hopOrOverall.reverse || null) : null;
  const ready = hopOrOverall.ready !== undefined ? Boolean(hopOrOverall.ready) : Boolean(group.availability?.ready);
  const reason = hopOrOverall.unavailableReason || group.availability?.reason || "";

  const fwdTone = fwd?.status?.tone || (running ? "warn" : (!ready ? "warn" : ""));
  const revTone = bidirectional ? (rev?.status?.tone || (running ? "warn" : (!ready ? "warn" : ""))) : "";
  const fwdJitterHigh = Boolean(fwd?.jitterHigh);
  const revJitterHigh = Boolean(rev?.jitterHigh);
  const jitterHigh = metricDir === "reverse"
    ? Boolean(rev?.jitterHigh)
    : metricDir === "forward" || !bidirectional
      ? Boolean(fwd?.jitterHigh)
      : Boolean(fwd?.jitterHigh || rev?.jitterHigh);
  const latencyHigh = metricDir === "reverse"
    ? Boolean(rev?.latencyHigh)
    : metricDir === "forward" || !bidirectional
      ? Boolean(fwd?.latencyHigh)
      : Boolean(fwd?.latencyHigh || rev?.latencyHigh);
  const autoEnabled = Boolean(group.autoEnabled);
  const autoIntervalSeconds = Number(group.autoIntervalSeconds || 60);

  if (metricDir === "reverse") {
    if (!bidirectional) {
      return {
        tone: "",
        running,
        fwdTone,
        revTone,
        statusLabel: "单向",
        metricLabel: "单向无回程",
        latencyMS: null,
        revLatencyMS: null,
        fwdLossPercent: null,
        revLossPercent: null,
        lossPercent: null,
        fwdJitterHigh,
        revJitterHigh,
        jitterHigh,
        latencyHigh,
        autoEnabled,
        autoIntervalSeconds,
        unavailableReverse: true,
      };
    }
    return {
      tone: running ? "warn" : (rev?.status?.tone || (!ready ? "warn" : "")),
      running,
      fwdTone,
      revTone,
      statusLabel: running ? "探测中" : (rev?.status?.label || (!ready ? (reason || "暂不可用") : "待采样")),
      metricLabel: formatTopologyLinkMetric({ running, ready, reason, dirMode: "reverse", bidirectional, fwd, rev }),
      latencyMS: rev?.avgMS ?? null,
      revLatencyMS: null,
      fwdLossPercent: null,
      revLossPercent: rev?.lossPercent ?? null,
      lossPercent: rev?.lossPercent ?? null,
      fwdJitterHigh,
      revJitterHigh,
      jitterHigh,
      latencyHigh,
      autoEnabled,
      autoIntervalSeconds,
      unavailableReverse: false,
    };
  }

  if (metricDir === "forward" || !bidirectional) {
    return {
      tone: running ? "warn" : (fwd?.status?.tone || (!ready ? "warn" : "")),
      running,
      fwdTone,
      revTone,
      statusLabel: running ? "探测中" : (fwd?.status?.label || (!ready ? (reason || "暂不可用") : "待采样")),
      metricLabel: formatTopologyLinkMetric({ running, ready, reason, dirMode: "forward", bidirectional, fwd, rev }),
      latencyMS: fwd?.avgMS ?? null,
      revLatencyMS: null,
      fwdLossPercent: fwd?.lossPercent ?? null,
      revLossPercent: null,
      lossPercent: fwd?.lossPercent ?? null,
      fwdJitterHigh,
      revJitterHigh,
      jitterHigh,
      latencyHigh,
      autoEnabled,
      autoIntervalSeconds,
      unavailableReverse: false,
    };
  }

  const lossList = [fwd?.lossPercent, rev?.lossPercent].filter((v) => v != null);
  const worstLoss = lossList.length ? Math.max(...lossList) : null;
  const tone = running
    ? "warn"
    : pickSeverityTone([fwd?.status?.tone, rev?.status?.tone, ready ? "" : "warn"]);
  return {
    tone,
    running,
    fwdTone,
    revTone,
    statusLabel: running
      ? "探测中"
      : tone === "bad"
        ? (fwd?.status?.tone === "bad" ? fwd.status.label : (rev?.status?.label || "链路异常"))
        : tone === "warn"
          ? (fwd?.status?.tone === "warn" ? fwd.status.label : (rev?.status?.label || "告警"))
          : (fwd?.status?.label || "正常"),
    metricLabel: formatTopologyLinkMetric({ running, ready, reason, dirMode: "both", bidirectional, fwd, rev }),
    latencyMS: fwd?.avgMS ?? null,
    revLatencyMS: rev?.avgMS ?? null,
    fwdLossPercent: fwd?.lossPercent ?? null,
    revLossPercent: rev?.lossPercent ?? null,
    lossPercent: worstLoss,
    fwdJitterHigh,
    revJitterHigh,
    jitterHigh,
    latencyHigh,
    autoEnabled,
    autoIntervalSeconds,
    unavailableReverse: false,
  };
}

/**
 * Projects normalized One-Group-One-Link probe groups into a pure network-monitoring
 * graph and Pacific map model (excluding unrelated traffic/bandwidth abstractions).
 */
export function buildProbeTopologyModel({ groups = [], metricDir = "both", domainFilter = "" } = {}) {
  const dirMode = metricDir === "forward" || metricDir === "reverse" ? metricDir : "both";
  const domainBuckets = new Map();
  const normalizedChains = [];

  for (const group of groups || []) {
    if (!group) continue;
    const domain = domainGroupForProbeGroup(group);
    const endpoints = (group.nodes || []).map((ep) => ({
      kind: ep.kind === "ext" ? "ext" : "node",
      id: ep.kind === "ext" ? `ext:${ep.address}` : ep.agentID,
      agentID: ep.kind === "ext" ? "" : ep.agentID,
      name: ep.name,
      regionCode: ep.region || "",
      address: ep.address || "",
      publicIP: ep.publicIP || ep.address || "",
      online: Boolean(ep.online),
      granted: Boolean(ep.granted),
      ping: Boolean(ep.ping),
      shared: Boolean(ep.shared),
      issue: ep.issue || "",
      role: ep.role || "",
    }));

    const chainSlice = directionalSlice(group, {
      bidirectional: group.bidirectional,
      forward: group.overall?.forward,
      reverse: group.overall?.reverse,
      ready: group.availability?.ready,
      unavailableReason: group.availability?.reason,
    }, dirMode);

    const hops = (group.hops || []).map((hop) => {
      const hopSlice = directionalSlice(group, hop, dirMode);
      const fromID = hop.from?.kind === "ext" ? `ext:${hop.from.address}` : hop.from?.agentID;
      const toID = hop.to?.kind === "ext" ? `ext:${hop.to.address}` : hop.to?.agentID;
      return {
        id: hop.id,
        probeID: group.id,
        groupName: group.groupName,
        name: group.isMultiHop
          ? `${hop.from.name} ${hop.bidirectional ? "⇄" : "→"} ${hop.to.name} (${group.groupName})`
          : group.groupName,
        isMultiHop: group.isMultiHop,
        hopIndex: hop.hopIndex,
        hopCount: group.hopCount,
        chainEndpoints: endpoints.filter((ep) => ep.kind === "node").map((ep) => ep.id),
        from: fromID,
        to: toID,
        fromName: hop.from.name,
        toName: hop.to.name,
        isExternal: hop.to?.kind === "ext",
        bidirectional: hop.bidirectional,
        running: Boolean(group.running || hopSlice.running),
        ready: hop.ready,
        unavailableReason: hop.unavailableReason || "",
        metricDir: dirMode,
        tone: hopSlice.tone,
        fwdTone: hopSlice.fwdTone,
        revTone: hopSlice.revTone,
        statusLabel: hopSlice.statusLabel,
        metricLabel: hopSlice.metricLabel,
        latencyMS: hopSlice.latencyMS,
        revLatencyMS: hopSlice.revLatencyMS,
        fwdLossPercent: hopSlice.fwdLossPercent,
        revLossPercent: hopSlice.revLossPercent,
        lossPercent: hopSlice.lossPercent,
        fwdJitterHigh: hopSlice.fwdJitterHigh,
        revJitterHigh: hopSlice.revJitterHigh,
        jitterHigh: hopSlice.jitterHigh,
        latencyHigh: hopSlice.latencyHigh,
        autoEnabled: hopSlice.autoEnabled,
        autoIntervalSeconds: hopSlice.autoIntervalSeconds,
        unavailableReverse: hopSlice.unavailableReverse,
        forward: hop.forward,
        reverse: hop.reverse,
        sourceAddr: hop.from.address || "",
        targetAddr: hop.to.address || "",
        sampledAt: group.overall?.sampledAt || "",
      };
    });

    const chainObj = {
      id: group.id,
      name: group.groupName,
      groupName: group.groupName,
      remark: group.remark || "",
      domainId: domain.id,
      domainTitle: domain.title,
      endpoints,
      hops,
      isMultiHop: group.isMultiHop,
      isExternal: group.isExternal,
      bidirectional: group.bidirectional,
      running: group.running,
      tone: chainSlice.tone,
      fwdTone: chainSlice.fwdTone,
      revTone: chainSlice.revTone,
      statusLabel: group.overall?.statusBadge?.label || chainSlice.statusLabel,
      metricLabel: chainSlice.metricLabel,
      latencyMS: chainSlice.latencyMS,
      revLatencyMS: chainSlice.revLatencyMS,
      fwdLossPercent: chainSlice.fwdLossPercent,
      revLossPercent: chainSlice.revLossPercent,
      lossPercent: chainSlice.lossPercent,
      fwdJitterHigh: chainSlice.fwdJitterHigh,
      revJitterHigh: chainSlice.revJitterHigh,
      jitterHigh: chainSlice.jitterHigh,
      latencyHigh: chainSlice.latencyHigh,
      forward: group.overall?.forward,
      reverse: group.overall?.reverse,
      sampledAt: group.overall?.sampledAt || "",
      availabilityReady: group.availability?.ready !== false,
      reason: group.availability?.reason || "",
      autoEnabled: chainSlice.autoEnabled,
      autoIntervalSeconds: chainSlice.autoIntervalSeconds,
      revision: group.revision,
      group,
    };
    normalizedChains.push(chainObj);
    if (!domainBuckets.has(domain.id)) {
      domainBuckets.set(domain.id, { meta: domain, chains: [] });
    }
    domainBuckets.get(domain.id).chains.push(chainObj);
  }

  const materializeSlice = (chains) => {
    const nodeMap = new Map();
    const extMap = new Map();
    const links = [];

    for (const ch of chains) {
      for (const h of ch.hops) links.push(h);
      for (const ep of ch.endpoints) {
        if (ep.kind === "ext") {
          if (!ep.id) continue;
          const existing = extMap.get(ep.id);
          if (existing) {
            existing.linkCount += 1;
            if (!existing.groupNames.includes(ch.groupName)) existing.groupNames.push(ch.groupName);
          } else {
            extMap.set(ep.id, {
              id: ep.id,
              name: ep.name || ep.address,
              address: ep.address,
              linkCount: 1,
              groupNames: [ch.groupName],
            });
          }
        } else if (ep.id) {
          const existing = nodeMap.get(ep.id);
          if (existing) {
            if (!existing.groupNames.includes(ch.groupName)) existing.groupNames.push(ch.groupName);
            if (ep.issue && !existing.issue) existing.issue = ep.issue;
            if (!existing.ip && ep.address) existing.ip = ep.address;
          } else {
            nodeMap.set(ep.id, {
              id: ep.id,
              name: ep.name || ep.id,
              regionCode: ep.regionCode || "",
              status: ep.online ? "online" : "offline",
              isOnline: Boolean(ep.online),
              ip: ep.address || ep.publicIP || "",
              shared: Boolean(ep.shared),
              ping: Boolean(ep.ping),
              granted: Boolean(ep.granted),
              issue: ep.issue || "",
              role: ep.role || "",
              groupNames: [ch.groupName],
            });
          }
        }
      }
    }

    const nodes = [...nodeMap.values()];
    const externalTargets = [...extMap.values()];
    const okCount = chains.filter((c) => c.tone === "ok").length;
    const warnCount = chains.filter((c) => c.tone === "warn").length;
    const badCount = chains.filter((c) => c.tone === "bad").length;
    const runningCount = chains.filter((c) => c.running).length;
    const fwdVals = links.map((l) => l.latencyMS).filter((v) => typeof v === "number" && Number.isFinite(v));
    const revVals = links.filter((l) => l.bidirectional).map((l) => l.revLatencyMS).filter((v) => typeof v === "number" && Number.isFinite(v));

    return {
      nodes,
      externalTargets,
      probeLinks: links,
      probeChains: chains,
      onlineNodes: nodes.filter((n) => n.isOnline).length,
      totalNodes: nodes.length,
      abnormalLinks: badCount + warnCount,
      stats: {
        total: chains.length,
        hopCount: links.length,
        nodeCount: nodes.length,
        extCount: externalTargets.length,
        okCount,
        warnCount,
        badCount,
        alertCount: badCount + warnCount,
        runningCount,
        avgFwdMS: fwdVals.length ? Math.round((fwdVals.reduce((s, v) => s + v, 0) / fwdVals.length) * 10) / 10 : null,
        avgRevMS: revVals.length ? Math.round((revVals.reduce((s, v) => s + v, 0) / revVals.length) * 10) / 10 : null,
      },
    };
  };

  const domainGroups = [...domainBuckets.values()]
    .sort((a, b) => a.meta.order - b.meta.order)
    .map(({ meta, chains }) => ({
      ...meta,
      chains,
      ...materializeSlice(chains),
    }));

  const activeDomain = domainFilter && domainFilter !== "all"
    ? domainGroups.find((d) => d.id === domainFilter) || null
    : null;
  const activeGroups = activeDomain ? [activeDomain] : domainGroups;
  const activeChains = activeGroups.flatMap((d) => d.chains);
  const activeSlice = materializeSlice(activeChains);

  return {
    ...activeSlice,
    metricDir: dirMode,
    activeDomainId: activeDomain ? activeDomain.id : "all",
    domainGroups,
    probeGroups: activeGroups,
    totalGroupCount: normalizedChains.length,
  };
}

export function buildSingleGroupTopologyModel(group, { metricDir = "both" } = {}) {
  if (!group) {
    return {
      nodes: [],
      externalTargets: [],
      probeLinks: [],
      probeChains: [],
      onlineNodes: 0,
      totalNodes: 0,
      abnormalLinks: 0,
      metricDir,
      activeDomainId: "all",
      domainGroups: [],
      probeGroups: [],
      totalGroupCount: 0,
      singleGroup: null,
    };
  }
  return {
    ...buildProbeTopologyModel({ groups: [group], metricDir }),
    singleGroup: group,
  };
}
