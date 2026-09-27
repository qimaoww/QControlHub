export const utcMonth = (value = new Date()) => {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return date.toISOString().slice(0, 7);
};

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
