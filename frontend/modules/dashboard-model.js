export const utcMonth = (value = new Date()) => {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return date.toISOString().slice(0, 7);
};

export function dashboardPeriodStart(period, now = new Date()) {
  const date = new Date(now);
  if (!Number.isFinite(date.getTime())) return "";
  const today = date.toISOString().slice(0, 10);
  if (period === "today") return today;
  if (period === "month") return today.slice(0, 8) + "01";
  if (period === "week") {
    const start = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()));
    start.setUTCDate(start.getUTCDate() - (start.getUTCDay() + 6) % 7);
    return start.toISOString().slice(0, 10);
  }
  return "";
}

// A response from before a UTC boundary must not be relabelled as the new
// current period while its replacement is still loading.
export function dashboardPeriodCurrent(period, value, now = new Date()) {
  const start = dashboardPeriodStart(period, now);
  return Boolean(start && value?.start === start);
}

export function dashboardPeriodState(value) {
  if (!value || typeof value.record_count !== "number" || !Number.isFinite(value.record_count) || value.record_count < 0) return "unavailable";
  if (value.record_count === 0) return "empty";
  if (typeof value.sample_count !== "number" || !Number.isFinite(value.sample_count) || value.sample_count <= 0) return "incomplete";
  const counters = ["received_bytes", "sent_bytes", "total_bytes"].map(key => value[key]);
  return counters.every(number => typeof number === "number" && Number.isFinite(number) && number >= 0) ? "ready" : "incomplete";
}

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
  const engines = new Map(), seen = new Set();
  for (const agent of agents) {
    // The API already returns one scoped runtime per Agent. Keep the aggregate
    // idempotent if a transport fixture or future response repeats that row.
    if (agent.id && seen.has(agent.id)) continue;
    if (agent.id) seen.add(agent.id);
    if (agent.status !== 'online') result.offline++;
    for (const [engine, runtime] of Object.entries(agent.runtime || {})) {
      if (!runtime.installed) continue;
      const count = engines.get(engine) || { engine, installed: 0, running: 0 };
      count.installed++; result.installed++;
      if (agent.status === 'online' && runtime.service_status === 'active') { count.running++; result.running++; }
      else if (agent.status === 'online' && runtime.service_status === 'inactive') result.stopped++;
      engines.set(engine, count);
    }
    const metrics = agent.metrics || {}, sampledAt = Date.parse(metrics.network_sampled_at);
    if (agent.status !== 'online' || !metrics.network_available || metrics.network_rate_available !== true || !Number.isFinite(sampledAt) || now - sampledAt > 15000 || sampledAt - now > 15000) continue;
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
    record_count: 0,
    sample_count: 0,
    recorded: false,
  }]));
  (rows || []).forEach((row) => {
    const current = totals.get(row.day);
    if (!current) return;
    const recorded = row.record_count == null || Number(row.record_count) > 0;
    if (!recorded) return;
    current.received_bytes += Number(row.received_bytes || 0);
    current.sent_bytes += Number(row.sent_bytes || 0);
    current.used_bytes += Number(row.total_bytes ?? row.used_bytes ?? 0);
    current.peak_receive_bps = Math.max(current.peak_receive_bps, Number(row.peak_receive_bps || 0));
    current.peak_send_bps = Math.max(current.peak_send_bps, Number(row.peak_send_bps || 0));
    current.record_count += Math.max(1, Number(row.record_count) || 0);
    current.sample_count += Number(row.sample_count) || 0;
    current.recorded = true;
  });
  return [...totals.values()];
}

export function summarizeDashboardTrend(days, month, now = new Date()) {
  const today = new Date(now).toISOString().slice(0, 10);
  const elapsed = days.filter(day => day.day <= today);
  const recorded = elapsed.filter(day => day.recorded !== false);
  const sampled = recorded.filter(day => day.sample_count > 0);
  const usage = day => day.received_bytes + day.sent_bytes;
  const peak = sampled.reduce((best, day) => usage(day) > (best ? usage(best) : 0) ? day : best, null);
  return {
    receivedBytes: sampled.reduce((sum, day) => sum + day.received_bytes, 0),
    sentBytes: sampled.reduce((sum, day) => sum + day.sent_bytes, 0),
    recordedDays: recorded.length,
    sampledDays: sampled.length,
    incompleteDays: recorded.length - sampled.length,
    elapsedDays: month === today.slice(0, 7) ? elapsed.length : days.length,
    peakDay: peak?.day || null,
    peakBytes: peak ? usage(peak) : 0,
  };
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
