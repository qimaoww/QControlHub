import assert from "node:assert/strict";
import {
  aggregateDashboardTrafficDays,
  buildProbeGroupModels,
  buildProbeTopologyModel,
  filterAndSortProbeGroups,
  groupMetricForDirection,
  isClockSyncAbnormal,
  isProbeRunning,
  isValidProbeIPAddress,
  mergePanelMetricsHistory,
  niceTrafficScaleMax,
  panelNetworkRatesValid,
  probeDirectionStatus,
  probeHistorySeries,
  rankDashboardTrafficView,
  resolveSelectedProbeGroup,
  sanitizePanelSample,
  selectMostActiveNodes,
  summarizeClientConnectionsPulse,
  summarizeDashboardProbes,
  summarizeDashboardTrafficBreakdown,
  summarizeDashboardTrafficPeaks,
  summarizeExpiringCertificates,
  summarizeIPQualityFleet,
  summarizeProbeGroups,
} from "../modules/dashboard-model.js";
import { buildProbePillLabel } from "../analytics/dashboard-topo-shared.js";

export async function run() {
  const days = aggregateDashboardTrafficDays([
    { day: "2026-08-01", received_bytes: 10, sent_bytes: 5, peak_receive_bps: 1200, peak_send_bps: 600 },
    { day: "2026-08-02", received_bytes: 20, sent_bytes: 10, peak_receive_bps: 900, peak_send_bps: 2400 },
    { day: "2026-08-09", received_bytes: 999, sent_bytes: 999, peak_receive_bps: 99999, peak_send_bps: 99999 },
  ], "2026-08");
  assert.deepEqual(
    summarizeDashboardTrafficPeaks(days, "2026-08-02T12:00:00Z"),
    { peakReceiveBPS: 1200, peakSendBPS: 2400 },
    "traffic peak speeds exclude future days and take max across elapsed days",
  );

  assert.equal(panelNetworkRatesValid({ network_available: true, network_rx_bps: 0, network_tx_bps: 10 }), true);
  assert.equal(panelNetworkRatesValid({ network_available: true, network_rate_available: false, network_rx_bps: 10, network_tx_bps: 10 }), false);
  assert.equal(panelNetworkRatesValid({ network_available: true, network_rx_bps: Number.NaN, network_tx_bps: 10 }), false);
  assert.equal(sanitizePanelSample({ collected_at: "invalid", cpu_available: true, cpu_percent: 12 }), null);

  const histFromSnapshot = mergePanelMetricsHistory({
    history: [
      { collected_at: "2026-11-01T00:00:02Z", cpu_available: true, cpu_percent: 10 },
      { collected_at: "bad" },
      { collected_at: "2026-11-01T00:00:04Z", cpu_available: true, cpu_percent: 25 },
    ],
  }, [], 60);
  assert.equal(histFromSnapshot.length, 2);
  const appended = mergePanelMetricsHistory({
    collected_at: "2026-11-01T00:00:03Z",
    cpu_available: true,
    cpu_percent: 15,
    network_available: true,
    network_rx_bps: 500,
    network_tx_bps: 250,
  }, histFromSnapshot, 2);
  assert.deepEqual(appended.map((s) => s.collected_at), [
    "2026-11-01T00:00:03Z",
    "2026-11-01T00:00:04Z",
  ], "rolling panel history sorts chronologically and respects window capacity");

  const nowISO = new Date().toISOString();
  const staleISO = new Date(Date.now() - 30_000).toISOString();
  const agents = [
    { id: "agt-b", name: "Beta", status: "online" },
    { id: "agt-a", name: "Alpha", status: "online" },
    { id: "agt-off", name: "Offline", status: "offline" },
  ];
  const policies = [
    {
      agent_id: "agt-a",
      received_bytes: 100,
      sent_bytes: 50,
      used_bytes: 150,
      receive_bps: 300,
      send_bps: 200,
      limit_bytes: 200,
      quota_enabled: true,
      auto_block: true,
      is_current_user: true,
      last_collected_at: nowISO,
    },
    {
      agent_id: "agt-b",
      received_bytes: 800,
      sent_bytes: 400,
      used_bytes: 1200,
      receive_bps: 900,
      send_bps: 100,
      limit_bytes: 1000,
      quota_enabled: true,
      auto_block: true,
      is_current_user: false,
      owner_name: "bob",
      last_collected_at: staleISO,
    },
    {
      agent_id: "agt-off",
      received_bytes: 50,
      sent_bytes: 10,
      used_bytes: 60,
      receive_bps: 777,
      send_bps: 888,
      limit_bytes: 60,
      quota_enabled: true,
      auto_block: true,
      is_current_user: true,
      last_collected_at: nowISO,
    },
  ];
  const cycleAll = rankDashboardTrafficView({
    period: "cycle",
    agents,
    policies,
    scope: "all",
    limit: "2",
    orderIDs: ["agt-a", "agt-b", "agt-off"],
  });
  assert.equal(cycleAll.hasCrossOwner, true);
  assert.equal(cycleAll.totalCount, 3);
  assert.equal(cycleAll.items.length, 2);
  assert.equal(cycleAll.items[0].agentID, "agt-b");
  assert.equal(cycleAll.items[0].blocked, true, "over-quota online agent reports blocked");
  assert.equal(cycleAll.items[0].receiveBPS, 0, "stale policy sample zeroes live bps");
  assert.equal(cycleAll.items[1].agentID, "agt-a");
  assert.equal(cycleAll.items[1].receiveBPS, 300);

  const cycleCurrent = rankDashboardTrafficView({
    period: "cycle",
    agents,
    policies,
    scope: "current",
    limit: "all",
  });
  assert.deepEqual(cycleCurrent.items.map((x) => x.agentID), ["agt-a", "agt-off"]);
  assert.equal(cycleCurrent.items[1].blocked, false, "offline node does not synthesize auto-block without explicit flag");

  const monthNoAgentsPerm = rankDashboardTrafficView({
    period: "month",
    month: "2026-11",
    today: "2026-11-10",
    rows: [
      { agent_id: "agt-b", day: "2026-11-01", received_bytes: 40, sent_bytes: 10 },
      { agent_id: "agt-a", day: "2026-11-01", received_bytes: 70, sent_bytes: 30 },
    ],
    pageAgents: [{ id: "agt-a", name: "Shared A", is_current_user: true }, { id: "agt-b", name: "Shared B", is_current_user: false }],
    policies: [{ agent_id: "agt-a", is_current_user: true, received_bytes: 1, sent_bytes: 1, used_bytes: 2 }],
    scope: "all",
    limit: "10",
  });
  assert.equal(monthNoAgentsPerm.hasCrossOwner, true);
  assert.deepEqual(monthNoAgentsPerm.items.map((x) => x.agentID), ["agt-a", "agt-b"]);
  assert.equal(monthNoAgentsPerm.items[1].agent.name, "Shared B");

  assert.equal(probeDirectionStatus(null, { ready: false, reason: "缺少共享诊断授权" }, "").tone, "warn");
  assert.equal(probeDirectionStatus(null, { ready: true, running: true }, "running").label, "探测中");
  assert.equal(probeDirectionStatus({ reached: false, loss_percent: 100 }, { ready: true }, "").label, "不可达");
  assert.equal(probeDirectionStatus({ reached: true, loss_percent: 25 }, { ready: true }, "").tone, "bad");
  assert.equal(probeDirectionStatus({ reached: true, loss_percent: 4.5 }, { ready: true }, "").tone, "warn");
  assert.equal(probeDirectionStatus({ reached: true, loss_percent: 0 }, { ready: true }, "").tone, "ok");

  assert.deepEqual(summarizeDashboardProbes([
    { auto_enabled: true, bidirectional: true, availability: { ready: true }, latest: { forward: { reached: true, loss_percent: 0 }, reverse: { reached: true, loss_percent: 5 } } },
    { auto_enabled: false, bidirectional: false, availability: { ready: true, running: true }, active_state: "running", latest: { forward: { reached: false, loss_percent: 100 } } },
    { auto_enabled: true, bidirectional: false, availability: { ready: true }, latest: { forward: { reached: true, loss_percent: 0 } } },
  ]), { total: 3, enabled: 2, running: 1, healthy: 1, warn: 1, bad: 1 });

  const series = probeHistorySeries([
    { sampled_at: "2026-11-01T00:05:00Z", forward: { avg_ms: 14.2, loss_percent: 0, reached: true } },
    { sampled_at: "2026-11-01T00:00:00Z", forward: { avg_ms: 11.4, loss_percent: 10, reached: true } },
  ], "forward");
  assert.deepEqual(series.map((s) => s.avgMS), [11.4, 14.2]);

  assert.equal(isValidProbeIPAddress("1.1.1.1"), true);
  assert.equal(isValidProbeIPAddress("2606:4700:4700::1111"), true);
  assert.equal(isValidProbeIPAddress("999.1.1.1"), false);
  assert.equal(isValidProbeIPAddress("1::2::3"), false);
  assert.equal(isValidProbeIPAddress(""), false);

  assert.equal(isClockSyncAbnormal({ status: "ok" }), false);
  assert.equal(isClockSyncAbnormal({ status: "synced" }), false);
  assert.equal(isClockSyncAbnormal({ status: "synchronized" }), false);
  assert.equal(isClockSyncAbnormal({ status: "synchronized", offset_milliseconds: 12 }), false);
  assert.equal(isClockSyncAbnormal({ status: "synchronized", offset_milliseconds: 2500 }), true);
  assert.equal(isClockSyncAbnormal({ status: "unsynchronized", offset_milliseconds: 1840 }), true);
  assert.equal(isClockSyncAbnormal({ status: "unavailable" }), false);
  assert.equal(isClockSyncAbnormal({ status: "skewed" }), true);
  assert.equal(isClockSyncAbnormal(null), false);

  const pulse = summarizeClientConnectionsPulse({
    ips: 14,
    flows: 88,
    sources: [{ status: "ok" }, { status: "error" }, { status: "ok" }],
    timeline: [{ time: "2026-11-01T00:00:00Z", flows: 5, ips: 2 }, { time: "2026-11-01T01:00:00Z", flows: "bad", ips: 0 }],
  });
  assert.deepEqual(pulse, {
    ips: 14,
    flows: 88,
    activeSources: 2,
    totalSources: 3,
    buckets: [{ time: "2026-11-01T00:00:00Z", flows: 5, ips: 2 }, { time: "2026-11-01T01:00:00Z", flows: 0, ips: 0 }],
  });

  const baseNow = Date.parse("2026-11-10T00:00:00Z");
  const certSummary = summarizeExpiringCertificates({
    items: [
      { id: "c0", owned: false, status: "active", not_after: "2026-11-12T00:00:00Z" },
      { id: "c1", owned: true, status: "expired", not_after: "2026-11-01T00:00:00Z" },
      { id: "c2", owned: true, name: "soon.example", status: "active", not_after: "2026-11-15T00:00:00Z" },
      { id: "c3", owned: true, name: "broken.example", status: "active", sync_status: "failed", not_after: "2027-01-01T00:00:00Z" },
      { id: "c4", owned: true, name: "healthy.example", status: "active", not_after: "2027-02-01T00:00:00Z" },
    ],
  }, baseNow, 14);
  assert.equal(certSummary.totalOwned, 3);
  assert.equal(certSummary.expiringSoon, 1);
  assert.equal(certSummary.abnormal, 1);
  assert.deepEqual(certSummary.urgent.map((x) => x.id), ["c3", "c2"]);

  assert.deepEqual(summarizeIPQualityFleet({
    records: [
      { status: "succeeded" },
      { status: "running" },
      { status: "failed" },
      { status: "canceled" },
    ],
  }), { total: 4, succeeded: 1, running: 1, failed: 2 });

  const fleet = [
    { id: "off", status: "offline", metrics: { network_available: true, network_rx_bps: 999999, network_tx_bps: 999999, cpu_available: true, cpu_percent: 99 }, runtime: { xray: { installed: true, service_status: "active" } } },
    { id: "idle-low", status: "online", metrics: { network_available: true, network_rx_bps: 0, network_tx_bps: 0, cpu_available: true, cpu_percent: 5 }, runtime: {} },
    { id: "idle-core-a", status: "online", metrics: { network_available: true, network_rx_bps: 0, network_tx_bps: 0, cpu_available: true, cpu_percent: 10 }, runtime: { mihomo: { installed: true, service_status: "active" } } },
    { id: "busy-mid", status: "online", metrics: { network_available: true, network_rx_bps: 4000, network_tx_bps: 1000, cpu_available: true, cpu_percent: 12 }, runtime: { xray: { installed: true, service_status: "active" } } },
    { id: "busy-top", status: "online", metrics: { network_available: true, network_rx_bps: 8000, network_tx_bps: 4000, cpu_available: true, cpu_percent: 20 }, runtime: { mihomo: { installed: true, service_status: "active" } } },
    { id: "idle-core-b", status: "online", metrics: { network_available: true, network_rx_bps: 0, network_tx_bps: 0, cpu_available: true, cpu_percent: 10 }, runtime: { singbox: { installed: true, service_status: "active" } } },
    { id: "idle-cpu", status: "online", metrics: { network_available: true, network_rx_bps: 0, network_tx_bps: 0, cpu_available: true, cpu_percent: 35 }, runtime: {} },
  ];
  assert.deepEqual(
    selectMostActiveNodes(fleet, { limit: 5 }).map((a) => a.id),
    ["busy-top", "busy-mid", "idle-core-a", "idle-core-b", "idle-cpu"],
    "selectMostActiveNodes ranks online throughput, active cores, cpu, and preserves tie order within top 5",
  );

  assert.equal(niceTrafficScaleMax(0), 3);
  assert.equal(niceTrafficScaleMax(5.5 * 1024 ** 3), 6 * 1024 ** 3, "5.5 GB scales cleanly to 6 GB (divisible by 3)");
  assert.equal(niceTrafficScaleMax(94.9 * 1024 ** 3), 105 * 1024 ** 3, "94.9 GB scales cleanly to 105 GB (divisible by 3)");
  const bd = summarizeDashboardTrafficBreakdown([
    { day: "2026-08-01", received_bytes: 300, sent_bytes: 100 },
    { day: "2026-08-02", received_bytes: 0, sent_bytes: 0 },
    { day: "2026-08-03", received_bytes: 60, sent_bytes: 40 },
    { day: "2026-08-09", received_bytes: 9999, sent_bytes: 9999 },
  ], "2026-08", "2026-08-03T12:00:00Z");
  assert.deepEqual(bd, {
    isCurrentMonth: true,
    todayReceiveBytes: 60,
    todaySentBytes: 40,
    averageReceiveBytes: 120,
    averageSentBytes: 140 / 3,
    peakDay: "2026-08-01",
    peakReceiveBytes: 300,
    peakSentBytes: 100,
    activeDays: 2,
    elapsedDays: 3,
  });

  // One-Group-One-Link model & multi-hop/status/filter/sort/fallback assertions
  assert.equal(isProbeRunning({ active_state: "cleanup_pending" }), true);
  assert.equal(isProbeRunning({ availability: { running: true } }), true);
  assert.equal(isProbeRunning({ active_state: "succeeded", availability: { running: false } }), false);

  const tenNodes = Array.from({ length: 10 }, (_, idx) => ({
    agent_id: `hop-${idx + 1}`,
    instance_id: "main",
    name: `节点 ${idx + 1}`,
    address: `10.10.0.${idx + 1}`,
  }));
  const tenChainMeta = tenNodes.map((ep, idx) => ({
    agent_id: ep.agent_id,
    instance_id: ep.instance_id,
    name: ep.name,
    region: idx % 2 === 0 ? "JP" : "HK",
    address: ep.address,
    online: true,
    granted: true,
    ping: true,
  }));
  const nineHops = Array.from({ length: 9 }, (_, idx) => ({
    hop_index: idx,
    from: tenNodes[idx].agent_id,
    to: tenNodes[idx + 1].agent_id,
    forward: {
      state: "succeeded",
      avg_ms: idx === 4 ? 190 : 20 + idx * 3,
      loss_percent: idx === 4 ? 25 : 0,
      reached: true,
    },
    reverse: {
      state: "succeeded",
      avg_ms: 22 + idx * 3,
      loss_percent: 0,
      reached: true,
    },
  }));

  const groupModels = buildProbeGroupModels({
    agents: [
      { id: "tokyo", name: "东京 · A", region_code: "JP", status: "online" },
      { id: "hk", name: "香港 · B", region_code: "HK", status: "online" },
      { id: "sg", name: "新加坡 · C", region_code: "SG", status: "offline" },
    ],
    orderIDs: ["tokyo", "hk", "sg"],
    pairs: [
      {
        id: "g_pair",
        group_name: "核心双节点",
        name: "稳定专线",
        target_mode: "",
        node1: { agent_id: "tokyo", instance_id: "main", name: "东京 · A", address: "10.0.0.1" },
        node2: { agent_id: "hk", instance_id: "shared-1", name: "香港 · B", address: "10.0.0.2" },
        bidirectional: true,
        auto_enabled: true,
        auto_interval_seconds: 60,
        availability: { ready: true, running: false },
        latest: {
          sampled_at: "2026-08-03T12:00:00Z",
          forward: { state: "succeeded", avg_ms: 31.2, loss_percent: 0, reached: true },
          reverse: { state: "succeeded", avg_ms: 32.8, loss_percent: 0, reached: true },
        },
      },
      {
        id: "g_ten",
        group_name: "跨洲十跳链",
        name: "Partial Hop Failure",
        target_mode: "",
        nodes: tenNodes,
        chain: tenChainMeta,
        bidirectional: true,
        auto_enabled: true,
        auto_interval_seconds: 60,
        availability: { ready: true, running: false },
        latest: {
          sampled_at: "2026-08-03T12:01:00Z",
          forward: { state: "succeeded", avg_ms: 360, loss_percent: 25, reached: true },
          reverse: { state: "succeeded", avg_ms: 310, loss_percent: 0, reached: true },
          hops: nineHops,
        },
      },
      {
        id: "g_ip_unreach",
        group_name: "外部黑洞检测",
        name: "",
        target_mode: "ip",
        node1: { agent_id: "tokyo", instance_id: "main", name: "东京 · A", address: "10.0.0.1" },
        node2: { name: "203.0.113.99", address: "203.0.113.99" },
        bidirectional: false,
        auto_enabled: false,
        auto_interval_seconds: 120,
        availability: { ready: true, running: false },
        latest: {
          sampled_at: "2026-08-03T11:58:00Z",
          forward: { state: "succeeded", loss_percent: 100, reached: false },
        },
      },
      {
        id: "g_unavail",
        group_name: "离线节点组",
        name: "",
        target_mode: "",
        node1: { agent_id: "sg", instance_id: "main", name: "新加坡 · C", address: "10.0.0.3" },
        node2: { agent_id: "hk", instance_id: "shared-1", name: "香港 · B", address: "10.0.0.2" },
        chain: [
          { agent_id: "sg", instance_id: "main", name: "新加坡 · C", region: "SG", address: "10.0.0.3", online: false, granted: true, ping: true },
          { agent_id: "hk", instance_id: "shared-1", name: "香港 · B", region: "HK", address: "10.0.0.2", online: true, granted: false, ping: true },
        ],
        bidirectional: true,
        auto_enabled: true,
        active_state: "running",
        availability: { ready: false, reason: "新加坡 · C 离线", running: true },
      },
    ],
  });

  assert.equal(groupModels.length, 4);
  assert.equal(groupModels[0].nodes[1].shared, true, "non-main instance must be tagged shared");
  assert.equal(groupModels[0].overall.statusBadge.tone, "ok");
  assert.equal(groupModels[1].nodes.length, 10, "10-hop chain preserves all 10 nodes");
  assert.equal(groupModels[1].hops.length, 9, "10-node chain has 9 hops");
  assert.equal(groupModels[1].isMultiHop, true);
  assert.equal(groupModels[1].hops[4].tone, "bad", "partial hop 25% loss marked bad");
  assert.equal(groupModels[1].overall.statusBadge.tone, "bad", "partial hop failure surfaces on overall group status");
  assert.equal(groupModels[2].isExternal, true);
  assert.equal(groupModels[2].overall.statusBadge.label, "不可达");
  assert.equal(groupModels[3].running, true);
  assert.equal(groupModels[3].nodes[0].issue, "节点离线");
  assert.equal(groupModels[3].nodes[1].issue, "共享未授权");

  assert.deepEqual(summarizeProbeGroups(groupModels), {
    total: 4,
    healthy: 1,
    warn: 1,
    bad: 2,
    idle: 0,
    running: 1,
    autoEnabled: 3,
    multiHop: 1,
    unavailable: 1,
  });

  assert.equal(groupMetricForDirection(groupModels[2], "reverse").unavailableReverse, true);
  assert.equal(groupMetricForDirection(groupModels[1], "forward").latencyMS, 360);
  assert.equal(groupMetricForDirection(groupModels[1], "both").lossPercent, 25);

  assert.deepEqual(
    filterAndSortProbeGroups(groupModels, { statusFilter: "multihop" }).map((g) => g.groupName),
    ["跨洲十跳链"],
  );
  assert.deepEqual(
    filterAndSortProbeGroups(groupModels, { statusFilter: "paused" }).map((g) => g.groupName),
    ["外部黑洞检测"],
  );
  assert.deepEqual(
    filterAndSortProbeGroups(groupModels, { statusFilter: "all", sortBy: "latency_desc", metricDir: "forward" }).map((g) => g.groupName),
    ["跨洲十跳链", "核心双节点", "外部黑洞检测", "离线节点组"],
  );
  assert.deepEqual(
    filterAndSortProbeGroups(groupModels, { statusFilter: "all", sortBy: "loss_desc", metricDir: "both" }).map((g) => g.groupName),
    ["外部黑洞检测", "跨洲十跳链", "核心双节点", "离线节点组"],
  );

  assert.deepEqual(
    resolveSelectedProbeGroup(groupModels, "跨洲十跳链").group.groupName,
    "跨洲十跳链",
  );
  const fallback = resolveSelectedProbeGroup(groupModels, "已被删除的组");
  assert.equal(fallback.group.groupName, "核心双节点");
  assert.equal(fallback.fellBack, true);
  assert.deepEqual(resolveSelectedProbeGroup([], "不存在"), { group: null, fellBack: true });

  const topoBoth = buildProbeTopologyModel({ groups: groupModels, metricDir: "both" });
  const topoFwd = buildProbeTopologyModel({ groups: groupModels, metricDir: "forward" });
  const topoRev = buildProbeTopologyModel({ groups: groupModels, metricDir: "reverse" });
  assert.ok(topoBoth.domainGroups.length >= 3, "expected multiple domain groups for mixed probe fleet");
  const pairLinkBoth = topoBoth.probeLinks.find((l) => l.probeID === "g_pair");
  const pairLinkFwd = topoFwd.probeLinks.find((l) => l.probeID === "g_pair");
  const pairLinkRev = topoRev.probeLinks.find((l) => l.probeID === "g_pair");
  assert.equal(buildProbePillLabel(pairLinkBoth), "31.2ms / 32.8ms · 0%");
  assert.equal(buildProbePillLabel(pairLinkFwd), "去 31.2ms · 0%");
  assert.equal(buildProbePillLabel(pairLinkRev), "回 32.8ms · 0%");
  const extLinkRev = topoRev.probeLinks.find((l) => l.probeID === "g_ip_unreach");
  assert.equal(buildProbePillLabel(extLinkRev), "单向");

  console.log("Dashboard telemetry model smoke passed");
}
