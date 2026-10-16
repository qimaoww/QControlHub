import assert from "node:assert/strict";
import { engineCapabilityToggles, selectedDefaultEngines, sharedEngineRuntimeBadges } from "./modules/engine-capabilities.js";
import {
  STATUS_MODEL_CATALOG,
  describeConnectionState,
  describeCoreRuntime,
  describeEngineCapability,
  describeNodeMetrics,
  describeServiceAction,
  describeShareEngineRuntime,
  describeSharedCoreInstances,
  resolveRetainedNodeMetrics,
} from "./modules/status-controls.js";

const defaults = engineCapabilityToggles([]);
assert.equal((defaults.match(/type="checkbox"/g) || []).length, 4);
assert.equal(defaults.includes("checked"), false);
assert.doesNotMatch(defaults, /<input[^>]*\sdisabled(?:\s|>)/, "globally disabled engines remain selectable");
assert.equal((defaults.match(/role="switch"/g) || []).length, 4);
assert.doesNotMatch(defaults, /settings-toggle/, "capabilities use compact switch rows instead of checkbox cards");
const node = engineCapabilityToggles(["mihomo"], { supported: ["mihomo", "xray"], node: true });
assert.match(node, /data-engine-capability="xray"[^>]*>/);
assert.doesNotMatch(node, /data-engine-capability="xray"[^>]*disabled/);
assert.match(node, /data-engine-capability="sing-box"[^>]*disabled/);
assert.match(node, /data-disabled-reason="当前 Agent 未声明支持 sing-box，请调整安装配置后重新注册"/);
assert.equal((engineCapabilityToggles([], { writable: false }).match(/<input[^>]*\sdisabled(?:\s|>)/g) || []).length, 4);
assert.match(engineCapabilityToggles([], { writable: false }), /data-disabled-reason="当前账号无权修改内核开关"/);
const form = new FormData();
assert.deepEqual(selectedDefaultEngines(form), []);
form.append("default_agent_engines", "xray");
form.append("default_agent_engines", "ss-rust");
assert.deepEqual(selectedDefaultEngines(form), ["xray", "ss-rust"]);

const stopping = engineCapabilityToggles(["mihomo"], { node: true, transitions: { mihomo: { status: "pending", enabled: false } } });
assert.match(stopping, /停止成功后关闭/);
assert.match(stopping, /离线节点上线后执行/);
assert.match(stopping, /data-engine-capability="mihomo" checked disabled/);
assert.match(stopping, /data-capability-state="stopping"/);
const starting = engineCapabilityToggles([], { node: true, transitions: { mihomo: { status: "running", enabled: true } } });
assert.match(starting, /启动成功后开启/);
assert.match(starting, /data-engine-capability="mihomo"\s+disabled/);
assert.match(starting, /data-capability-state="starting"/);
const failed = engineCapabilityToggles([], { node: true, transitions: { mihomo: { status: "failed", enabled: true } } });
assert.match(failed, /启停失败或取消/);
assert.doesNotMatch(failed, /data-engine-capability="mihomo"[^>]*disabled/);
assert.match(failed, /data-capability-state="failed"/);

// Verify unified status model catalog completeness.
assert.ok(STATUS_MODEL_CATALOG.connection.online);
assert.ok(STATUS_MODEL_CATALOG.connection.offline);
assert.ok(STATUS_MODEL_CATALOG.connection.unknown);
assert.ok(STATUS_MODEL_CATALOG.capability.enabled);
assert.ok(STATUS_MODEL_CATALOG.capability.disabled);
assert.ok(STATUS_MODEL_CATALOG.capability.unsupported);
assert.ok(STATUS_MODEL_CATALOG.kernel.existing_pending);
assert.ok(STATUS_MODEL_CATALOG.kernel.existing_blocked);
assert.ok(STATUS_MODEL_CATALOG.task.awaiting_confirmation);
assert.ok(STATUS_MODEL_CATALOG.freshness.cached);
assert.ok(STATUS_MODEL_CATALOG.freshness.stale);
assert.ok(STATUS_MODEL_CATALOG.feature.system_bbr);

// Verify connection status descriptors.
assert.equal(describeConnectionState("online", "2026-10-13T10:00:00Z").state, "online");
assert.equal(describeConnectionState("offline", "").label, "离线");
assert.equal(describeConnectionState("", "").state, "unknown");

// Verify capability & core runtime descriptors.
assert.equal(describeEngineCapability("sing-box", { selected: [], supported: ["mihomo"] }).state, "unsupported");
assert.equal(
  describeCoreRuntime("mihomo", { installed: false, existing_config_available: true }).existingState,
  "pending",
);
assert.equal(
  describeCoreRuntime("mihomo", { installed: false, existing_config_unsupported_reason: "非 systemd 托管" }).existingState,
  "blocked",
);
assert.equal(
  describeCoreRuntime("mihomo", { installed: true, service_status: "unknown" }).runtimeState,
  "unknown",
);

// Verify multi-user kernel status split for node owners and shared recipients.
const ownerMultiUser = describeCoreRuntime("mihomo", {
  installed: true,
  version: "1.19.0",
  service_status: "active",
  shared_instances: [
    { share_id: "shr_0000000000000001", username: "alice", display_name: "Alice", service_status: "active", deployed: true, config_version: 2, running: true },
    { share_id: "shr_0000000000000002", username: "bob", service_status: "inactive", deployed: true, config_version: 1, running: false },
    { share_id: "shr_0000000000000003", username: "carol", service_status: "inactive", deployed: false, config_version: 0, running: false },
  ],
});
assert.equal(ownerMultiUser.shared.hasShared, true);
assert.equal(ownerMultiUser.shared.totalCount, 3);
assert.equal(ownerMultiUser.shared.runningCount, 1);
assert.equal(ownerMultiUser.shared.stoppedCount, 1);
assert.equal(ownerMultiUser.shared.undeployedCount, 1);
assert.equal(ownerMultiUser.shared.summaryLabel, "多用户 1/3 运行");
assert.equal(ownerMultiUser.shared.chipLabel, "共享 1/3");
assert.equal(ownerMultiUser.shared.items[0].state, "active");
assert.equal(ownerMultiUser.shared.items[1].state, "inactive");
assert.equal(ownerMultiUser.shared.items[2].state, "undeployed");

const recipientUndeployed = describeCoreRuntime("mihomo", {
  installed: true,
  version: "1.19.0",
  service_status: "inactive",
  deployed: false,
  config_version: 0,
}, { isShared: true });
assert.equal(recipientUndeployed.hostInstallLabel, "宿主已安装");
assert.equal(recipientUndeployed.userInstanceLabel, "未部署");
assert.equal(recipientUndeployed.userInstanceTone, "muted");

const recipientRunning = describeCoreRuntime("mihomo", {
  installed: true,
  version: "1.19.0",
  service_status: "active",
  deployed: true,
  config_version: 3,
}, { isShared: true });
assert.equal(recipientRunning.userInstanceLabel, "运行中");
assert.equal(recipientRunning.userInstanceTone, "ok");
assert.equal(recipientRunning.userConfigVersion, 3);

const shareRuntime = describeShareEngineRuntime("xray", {
  installed: true,
  service_status: "active",
  deployed: true,
  config_version: 4,
  running: true,
});
assert.equal(shareRuntime.state, "active");
assert.equal(shareRuntime.statusLabel, "运行中");
assert.equal(shareRuntime.configVersion, 4);

const badgesMarkup = sharedEngineRuntimeBadges({
  engines: ["mihomo", "xray"],
  engine_runtime: {
    mihomo: { installed: true, service_status: "active", deployed: true, config_version: 2, running: true },
    xray: { installed: true, service_status: "inactive", deployed: false, config_version: 0, running: false },
  },
});
assert.match(badgesMarkup, /data-share-engine="mihomo"[^>]*data-share-engine-state="active"/);
assert.match(badgesMarkup, /data-share-engine="xray"[^>]*data-share-engine-state="undeployed"/);

const directInstances = describeSharedCoreInstances("mihomo", { installed: true, shared_instances: [] });
assert.equal(directInstances.hasShared, false);

// Verify service action eligibility and disabled reason explanations.
const offlineAction = describeServiceAction("restart", {
  online: false,
  installed: true,
  capabilityEnabled: true,
  serviceStatus: "active",
  can: () => true,
  engine: "mihomo",
});
assert.equal(offlineAction.disabled, true);
assert.match(offlineAction.reason, /节点当前离线/);

const stoppedStopAction = describeServiceAction("stop", {
  online: true,
  installed: true,
  capabilityEnabled: true,
  serviceStatus: "inactive",
  can: () => true,
  engine: "mihomo",
});
assert.equal(stoppedStopAction.disabled, true);
assert.match(stoppedStopAction.reason, /服务已停止/);

// Verify retained metrics when node goes offline and real-zero vs missing vs uncollected semantics.
const lastKnownMap = {};
const onlineAgent = {
  id: "node-1",
  status: "online",
  last_seen: "2026-10-13T10:00:00Z",
  metrics: {
    collected_at: "2026-10-13T10:00:00Z",
    cpu_available: true,
    cpu_percent: 0,
    memory_available: true,
    memory_used_bytes: 0,
    memory_total_bytes: 1073741824,
    disk_available: true,
    disk_used_bytes: 536870912,
    disk_total_bytes: 1073741824,
    network_available: true,
    network_rx_bps: 0,
    network_tx_bps: 0,
    network_rx_bytes: 0,
    network_tx_bytes: 0,
  },
};
const resolvedOnline = resolveRetainedNodeMetrics(onlineAgent, lastKnownMap);
assert.equal(resolvedOnline.retained, false);
const realZeroMetrics = describeNodeMetrics(onlineAgent, resolvedOnline.metrics);
assert.equal(realZeroMetrics.freshnessState, "fresh");
assert.equal(realZeroMetrics.cpu.text, "0.0%");
assert.equal(realZeroMetrics.network.downloadTotalText, "0 B");
assert.equal(realZeroMetrics.network.uploadTotalText, "0 B");

const offlineSameNode = {
  id: "node-1",
  status: "offline",
  last_seen: "2026-10-13T10:00:00Z",
  metrics: {},
};
const resolvedOffline = resolveRetainedNodeMetrics(offlineSameNode, lastKnownMap);
assert.equal(resolvedOffline.retained, true);
const retainedDesc = describeNodeMetrics(offlineSameNode, resolvedOffline.metrics, { retained: resolvedOffline.retained });
assert.equal(retainedDesc.freshnessState, "retained");
assert.equal(retainedDesc.cpu.text, "0.0%");
assert.match(retainedDesc.stampTitle, /保留最后已知有效采样/);

const uncollectedOnline = describeNodeMetrics({ id: "node-2", status: "online" }, {});
assert.equal(uncollectedOnline.freshnessState, "waiting");
assert.equal(uncollectedOnline.fallbackLabel, "等待采集");
assert.equal(uncollectedOnline.network.downloadTotalText, "—");
