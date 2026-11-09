import assert from "node:assert/strict";

import { aggregateDashboardTrafficDays, dashboardTrafficMonthDays } from "../modules/dashboard-model.js";

import { summarizeDashboardTraffic, summarizeDashboardNodes, rankDashboardTraffic, summarizeDashboardTrend, dashboardPeriodStart, dashboardPeriodCurrent, dashboardPeriodState } from "../modules/dashboard-model.js";

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
    { day: "2026-08-01", received_bytes: 9, sent_bytes: 5, used_bytes: 14, peak_receive_bps: 8, peak_send_bps: 0, record_count: 2, sample_count: 0, recorded: true },
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
  const network = { network_available: true, network_rate_available: true, network_rx_bps: 12, network_tx_bps: 8, collected_at: new Date(now).toISOString(), network_sampled_at: new Date(now).toISOString() };
  const fleet = summarizeDashboardNodes([
    { id: 'one', name: 'same', status: 'online', runtime: { xray: { installed: true, service_status: 'active' } }, metrics: network },
    { id: 'two', name: 'same', status: 'offline', runtime: { xray: { installed: true, service_status: 'active' } }, metrics: network },
    { id: 'three', status: 'online', runtime: { mihomo: { installed: true, service_status: 'inactive' } }, metrics: { ...network, collected_at: new Date(now - 16000).toISOString(), network_sampled_at: new Date(now - 16000).toISOString() } },
  ], now);
  assert.equal(fleet.running, 1, 'offline cached service state must not count as running');
  assert.equal(fleet.installed, 3, 'same display names must not merge different instances');
  assert.equal(fleet.sampled, 1, 'offline and stale rates must not enter the live total');
  assert.equal(fleet.receiveBPS + fleet.sendBPS, 20);
  const deduplicated = summarizeDashboardNodes([{ id: 'one', status: 'online', metrics: network }, { id: 'one', status: 'online', metrics: network }], now);
  assert.equal(deduplicated.sampled, 1, 'a repeated shared-agent row cannot double count live rates');
  assert.equal(deduplicated.receiveBPS + deduplicated.sendBPS, 20);
  assert.equal(summarizeDashboardNodes([{ id: 'invalid-rate', status: 'online', metrics: { ...network, network_rate_available: false } }], now).sampled, 0, 'an unavailable rate cannot become a real zero');
  assert.equal(summarizeDashboardNodes([{ id: 'missing-rate-state', status: 'online', metrics: { ...network, network_rate_available: undefined } }], now).sampled, 0, 'a missing rate availability flag is not a valid sample');
  const noRecords = aggregateDashboardTrafficDays([{ day: '2024-02-02', record_count: 0, sample_count: 0, received_bytes: 0, sent_bytes: 0, total_bytes: 0 }], '2024-02');
  assert.equal(noRecords[1].recorded, false, 'server-generated empty calendar days are not real samples');
  assert.equal(noRecords[1].record_count, 0);
  for (const [period, instant, start] of [
    ['today', '2027-01-01T00:00:00Z', '2027-01-01'],
    ['week', '2027-01-01T00:00:00Z', '2026-12-28'],
    ['month', '2027-01-01T00:00:00Z', '2027-01-01'],
    ['week', '2024-03-01T12:00:00Z', '2024-02-26'],
    ['week', '2026-11-09T00:00:00Z', '2026-11-09'],
    ['week', '2026-11-15T23:59:59Z', '2026-11-09'],
  ]) assert.equal(dashboardPeriodStart(period, instant), start, `${period} start at ${instant}`);
  assert.equal(dashboardPeriodCurrent('week', { start: '2026-12-28' }, '2027-01-01T00:00:00Z'), true);
  assert.equal(dashboardPeriodCurrent('today', { start: '2026-12-31' }, '2027-01-01T00:00:00Z'), false, 'a pre-midnight response cannot become today');
  assert.equal(dashboardPeriodState({ record_count: 0, sample_count: 0 }), 'empty');
  assert.equal(dashboardPeriodState({ record_count: 1, sample_count: 2, received_bytes: 0, sent_bytes: 0, total_bytes: 0 }), 'ready', 'a sampled zero is real zero traffic');
  assert.equal(dashboardPeriodState({ record_count: 1, sample_count: 0, received_bytes: 0, sent_bytes: 0, total_bytes: 0 }), 'incomplete');
  assert.equal(dashboardPeriodState({ record_count: null }), 'unavailable');
  const coverage = summarizeDashboardTrend(aggregateDashboardTrafficDays([{ day: '2024-02-01', received_bytes: 0, sent_bytes: 0, sample_count: 2 }], '2024-02'), '2024-02', '2024-03-01T00:00:00Z');
  assert.equal(coverage.recordedDays, 1, 'only dates with records count as covered');
  assert.equal(coverage.elapsedDays, 29, 'historical leap month keeps its full coverage denominator');
  assert.equal(coverage.receivedBytes + coverage.sentBytes, 0, 'recorded zero usage remains zero rather than becoming a failure');
  const partial = aggregateDashboardTrafficDays([
    { day: '2024-02-01', received_bytes: 4, sent_bytes: 2, sample_count: 1 },
    { day: '2024-02-02', received_bytes: 900, sent_bytes: 900, sample_count: 0 },
  ], '2024-02');
  const partialSummary = summarizeDashboardTrend(partial, '2024-02', '2024-02-02T12:00:00Z');
  assert.equal(partialSummary.receivedBytes + partialSummary.sentBytes, 6, 'incomplete samples cannot inflate the valid subtotal');
  assert.equal(partialSummary.sampledDays, 1);
  assert.equal(partialSummary.incompleteDays, 1);
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
