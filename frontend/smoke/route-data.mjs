import assert from "node:assert/strict";

import { aggregateDashboardTrafficDays, dashboardTrafficMonthDays } from "../modules/dashboard-model.js";

import { summarizeDashboardTraffic, summarizeDashboardNodes, rankDashboardTraffic } from "../modules/dashboard-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
  // Native dashboard API selection, monthly history, details, day tooltips,
  // permission boundaries and CSP are exercised by dashboard browser modes.
  // Keep all shared numerical/date/ordering edge cases here.
  assert.equal(dashboardTrafficMonthDays("2024-02").length, 29, "dashboard traffic month helper observes leap years");
  assert.deepEqual(
    aggregateDashboardTrafficDays([
      { day: "2026-08-01", received_bytes: 4, sent_bytes: 3, used_bytes: 7, peak_receive_bps: 2 },
      { day: "2026-08-01", received_bytes: 5, sent_bytes: 2, used_bytes: 7, peak_receive_bps: 8 },
    ], "2026-08")[0],
    { day: "2026-08-01", received_bytes: 9, sent_bytes: 5, used_bytes: 14, peak_receive_bps: 8, peak_send_bps: 0 },
    "same-day policy rows are aggregated for the dashboard chart",
  );
  const trendDays = aggregateDashboardTrafficDays([
    { day: "2024-02-01", received_bytes: 30, sent_bytes: 10, used_bytes: 40 },
    { day: "2024-02-02", received_bytes: 10, sent_bytes: 20, used_bytes: 30 },
    { day: "2024-02-02", received_bytes: 5, sent_bytes: 5, used_bytes: 10 },
  ], "2024-02");
  assert.deepEqual(summarizeDashboardTraffic(trendDays, "2024-02", "2024-02-02T23:00:00Z"), {
    isCurrentMonth: true, todayBytes: 40, averageBytes: 40, elapsedDays: 2,
    peakDay: "2024-02-01", peakBytes: 40,
  }, "current averages exclude future dates, aggregate policies and choose the earliest tied peak");
  const missingToday = summarizeDashboardTraffic(trendDays, "2024-02", "2024-02-04T01:00:00+08:00");
  assert.equal(missingToday.todayBytes, 0, "a UTC day without usage displays zero");
  assert.equal(missingToday.elapsedDays, 3, "trend date boundaries use UTC rather than local time");
  assert.equal(missingToday.averageBytes, 80 / 3, "zero-usage days participate in the daily average");
  const historical = summarizeDashboardTraffic(trendDays, "2024-02", "2024-03-01T00:00:00Z");
  assert.equal(historical.todayBytes, null, "historical months cannot claim to show today's traffic");
  assert.equal(historical.averageBytes, 80 / 29, "historical leap-year averages include all calendar days");
  const future = summarizeDashboardTraffic(trendDays, "2024-02", "2024-01-31T23:59:59Z");
  assert.equal(future.averageBytes, null, "future months have no elapsed-day average");
  assert.equal(future.peakDay, null, "future dates cannot become a peak");
  const emptyTrend = summarizeDashboardTraffic(aggregateDashboardTrafficDays([], "2024-02"), "2024-02", "2024-02-01T00:00:00Z");
  assert.equal(emptyTrend.averageBytes, 0, "empty current-month traffic has a zero average");
  assert.equal(emptyTrend.peakDay, null, "empty traffic must not invent a peak date");
  const now = Date.parse('2026-11-06T10:00:00Z');
  const network = { network_available: true, network_rx_bps: 12, network_tx_bps: 8, collected_at: new Date(now).toISOString() };
  const fleet = summarizeDashboardNodes([
    { id: 'one', name: 'same', status: 'online', runtime: { xray: { installed: true, service_status: 'active' } }, metrics: network },
    { id: 'two', name: 'same', status: 'offline', runtime: { xray: { installed: true, service_status: 'active' } }, metrics: network },
    { id: 'three', status: 'online', runtime: { mihomo: { installed: true, service_status: 'inactive' } }, metrics: { ...network, collected_at: new Date(now - 16000).toISOString() } },
  ], now);
  assert.equal(fleet.running, 1, 'offline cached service state must not count as running');
  assert.equal(fleet.installed, 3, 'same display names must not merge different instances');
  assert.equal(fleet.sampled, 1, 'offline and stale rates must not enter the live total');
  assert.equal(fleet.receiveBPS + fleet.sendBPS, 20);
  assert.deepEqual(rankDashboardTraffic([
    { agent_id: 'one', day: '2026-11-01', received_bytes: 10, sent_bytes: 5 },
    { agent_id: 'one', day: '2026-11-02', received_bytes: 2, sent_bytes: 3 },
    { agent_id: 'two', day: '2026-11-01', received_bytes: 3, sent_bytes: 4 },
    { agent_id: 'two', day: '2026-12-01', received_bytes: 9000 },
    { agent_id: 'two', day: '2026-11-07', received_bytes: 9000 },
  ], '2026-11', '2026-11-06'), [{ agentID: 'one', used: 20 }, { agentID: 'two', used: 7 }], 'ranking sums all authorized port rows in the selected month and excludes future days');
  assert.deepEqual(rankDashboardTraffic([{agent_id:'one',day:'2026-11-01',received_bytes:10}], '2026-11', '2026-11-06', ['one','two','three']), [{agentID:'one',used:10},{agentID:'three',used:0},{agentID:'two',used:0}], 'authorized zero-usage nodes remain available without fabricating traffic');

  // Task cache, poll timer and directory assertions now exercise the native
  // application in browser/task-live.mjs on desktop and mobile.
}
