import { installAgentFixture } from "./browser/agent-fixture.mjs";
import { testCapabilitySettingsRuntime } from "./browser/capability-settings.mjs";
import { testAgentBatchLayout } from "./browser/agent-batch-layout.mjs";
import { testAdminRuntime } from "./browser/agent-actions.mjs";
import { testWriteOnlyCoreUninstall, testNativeNodeTaskLifecycle } from "./browser/agent-detail-actions.mjs";
import { testEmptyRuntime, testSharedNodeRuntime, testReadonlyRuntime } from "./browser/agent-access.mjs";
import { testEnrollmentLayoutRuntime } from "./browser/enrollment.mjs";
import { testUsersLayoutRuntime } from "./browser/users-layout.mjs";
import { testRegionRuntime } from "./browser/regions.mjs";
import { testPortNamesAndRuntimeRefresh, testClientNodeOrderRuntime } from "./browser/node-runtime.mjs";
import { testClientLayoutRuntime } from "./browser/client-layout.mjs";
import { testSystemTCPRuntime } from "./browser/system-tcp.mjs";
import { testLargeLogRuntime, testLogPreferenceRestoreRuntime } from "./browser/core-logs.mjs";
import { testTrafficLayoutRuntime, testDenseTrafficLayoutRuntime } from "./browser/traffic-layout.mjs";
import { testConfigLayoutRuntime } from "./browser/config-layout.mjs";
import { testShellLayoutRuntime } from "./browser/shell-layout.mjs";
import { testSelectionRuntime } from "./browser/selections.mjs";
import { testCardCoordinateRuntime } from "./browser/card-coordinates.mjs";
import { testCardMoveRuntime } from "./browser/card-movement.mjs";
import { testMotionRuntime } from "./browser/motion.mjs";
import { testPopupRuntime } from "./browser/popups.mjs";
import { testAgentTimeZoneLayout } from "./browser/agent-time-zone-layout.mjs";

const mode = new URLSearchParams(location.search).get("mode") || "admin";
const scenario = installAgentFixture(mode);

try {
  if (mode.startsWith("task-retry")) {
    if (new URLSearchParams(location.search).get('preview') === 'tasks-live') {
      const { testLiveTaskRuntime } = await import('./browser/task-live.mjs');
      await testLiveTaskRuntime(true);
    }
    const { testEditedRetryDialog } = await import("./browser/task-retry.mjs");
    await testEditedRetryDialog(new URLSearchParams(location.search).has("preview"));
    if (!new URLSearchParams(location.search).has('preview')) {
      const { testLiveTaskRuntime } = await import('./browser/task-live.mjs');
      await testLiveTaskRuntime();
    }
  } else if (mode.startsWith("select-menus")) {
    const {testSelectMenusRuntime} = await import("./browser/select-menus.mjs");
    await testSelectMenusRuntime();
  } else if (mode === "archive-engines") {
    const { testArchiveEnginesRuntime } = await import("./browser/archive-engines.mjs");
    await testArchiveEnginesRuntime(new URLSearchParams(location.search).has("preview"));
  } else if (mode.startsWith("agent-time-zone-layout")) {
    await testAgentTimeZoneLayout(scenario);
  } else if (mode.startsWith("capabilities-settings")) {
    await testCapabilitySettingsRuntime(scenario);
  } else if (mode.startsWith("config-layout")) {
    await testConfigLayoutRuntime(scenario);
  } else if (mode.startsWith("bbr")) {
    await testSystemTCPRuntime(scenario, new URLSearchParams(location.search).has("preview"));
  } else if (mode.startsWith("connections")) {
    const { testClientConnectionsRuntime } = await import("./browser/client-connections.mjs");
    await testClientConnectionsRuntime(new URLSearchParams(location.search).has("preview"));
  } else if (mode.startsWith("ip-quality")) {
    const { testIPQualityRuntime } = await import("./browser/ip-quality.mjs");
    await testIPQualityRuntime(mode, new URLSearchParams(location.search).has("preview"));
  } else if (mode.startsWith("dashboard")) {
    const { testDashboardRuntime } = await import("./dashboard_browser_runtime.mjs");
    await testDashboardRuntime(mode, new URLSearchParams(location.search).has("preview"));
  } else if (mode === "sharing" || mode === "sharing-mobile") {
    const { testAgentSharingRuntime } = await import("./sharing_browser_runtime.mjs");
    await testAgentSharingRuntime();
  } else if (mode === "users" || mode === "users-mobile") {
    const { testUsersRuntime } = await import("./users_browser_runtime.mjs");
    await testUsersRuntime(new URLSearchParams(location.search).has("preview"));
  } else if (mode === "config-scope" || mode === "substore-scope" || mode === "substore-layout") {
    const { testConfigScopeRuntime, testSubStoreScopeRuntime } = await import("./config_scope_browser_runtime.mjs");
    const test = mode === "config-scope" ? testConfigScopeRuntime : testSubStoreScopeRuntime;
    await test(new URLSearchParams(location.search).has("preview"));
  } else if (mode === "presets" || mode === "presets-mobile") {
    const { testPresetsRuntime } = await import("./presets_browser_runtime.mjs");
    await testPresetsRuntime(new URLSearchParams(location.search).has("preview"));
  } else if (mode === "config-inbounds" || mode === "config-inbounds-mobile") {
    const { testConfigInboundsRuntime } = await import("./config_inbounds_browser_runtime.mjs");
    await testConfigInboundsRuntime(new URLSearchParams(location.search).has("preview"));
  } else if (mode === "config-restrictions") {
    const { testConfigRestrictionsRuntime } = await import("./config_restrictions_browser_runtime.mjs");
    await testConfigRestrictionsRuntime();
  } else if (mode === "config-migration") {
    const {testConfigMigrationRuntime} = await import("./config_migration_browser_runtime.mjs");
    await testConfigMigrationRuntime(new URLSearchParams(location.search).has("preview"));
  } else if (mode.startsWith("node-task-lifecycle")) {
    await testNativeNodeTaskLifecycle(scenario);
  } else if (mode === 'admin') {
    await testAdminRuntime(scenario);
  } else if (mode.startsWith("batch-layout")) {
    const { testAgentBatchActions } = await import('./browser/agent-batch-actions.mjs');
    await testAgentBatchActions(scenario);
    await testAgentBatchLayout(scenario);
  } else if (mode === "empty") {
    await testEmptyRuntime(scenario);
  } else if (mode.startsWith("shared-node")) {
    await testSharedNodeRuntime(scenario);
  } else if (mode === "readonly") {
    await testReadonlyRuntime(scenario);
  } else if (mode.startsWith("regions")) {
    await testRegionRuntime(scenario);
  } else if (mode.startsWith("enrollment")) {
    await testEnrollmentLayoutRuntime(scenario);
  } else if (mode.startsWith("shell-layout")) {
    await testShellLayoutRuntime(scenario);
  } else if (mode === "uninstall-writeonly") {
    await testWriteOnlyCoreUninstall(scenario);
  } else if (mode === "ports" || mode === "ports-mobile") {
    await testPortNamesAndRuntimeRefresh(scenario);
  } else if (mode.startsWith("client-layout")) {
    await testClientLayoutRuntime(scenario);
  } else if (mode.startsWith("client-order")) {
    await testClientNodeOrderRuntime(scenario);
  } else if (mode.startsWith("traffic-layout")) {
    await (mode.includes("dense") ? testDenseTrafficLayoutRuntime : testTrafficLayoutRuntime)(scenario);
  } else if (mode.startsWith("users-layout")) {
    await testUsersLayoutRuntime(scenario);
  } else if (mode === "logs" || mode === "logs-mobile") {
    await testLargeLogRuntime(scenario);
  } else if (mode === "logs-restore") {
    await testLogPreferenceRestoreRuntime(scenario);
  } else if (mode.startsWith('motion-cards-coordinates')) {
    await testCardCoordinateRuntime(scenario, new URLSearchParams(location.search).has('preview'));
  } else if (mode.startsWith('motion-cards')) {
    await testCardMoveRuntime(scenario, new URLSearchParams(location.search).has('preview'));
  } else if (mode.startsWith('motion-selection')) {
    await testSelectionRuntime(scenario, new URLSearchParams(location.search).has('preview'));
  } else if (mode.startsWith('motion-popup')) {
    await testPopupRuntime(scenario, new URLSearchParams(location.search).has('preview'));
  } else if (mode.startsWith('motion')) {
    await testMotionRuntime(scenario, new URLSearchParams(location.search).has('preview'));
  } else if (mode === 'navigation-preview') {
    const session = await (await fetch('/api/v1/auth/session')).json();
    const { startAnalytics } = await import('./analytics/app.js');
    startAnalytics({ app: document.querySelector('#app'), session });
    await new Promise(resolve => requestAnimationFrame(resolve));
  } else if (mode === 'tasks-preview') {
    const { testEditedRetryDialog } = await import('./browser/task-retry.mjs');
    await testEditedRetryDialog(true);
  } else {
    throw new Error(`Unknown browser smoke mode: ${mode}`);
  }
  document.documentElement.dataset.browserSmoke = "passed";
  if (!new URLSearchParams(location.search).has("preview"))
    document.body.innerHTML = `<pre id="browser-smoke-result">PASS ${mode}${window.cardMovementResult ? " " + window.cardMovementResult : window.logPressureResult ? " " + JSON.stringify(window.logPressureResult) : ""}</pre>`;
} catch (error) {
  document.documentElement.dataset.browserSmoke = "failed";
  document.body.innerHTML = `<pre id="browser-smoke-result"></pre>`;
  document.querySelector("#browser-smoke-result").textContent = String(error?.stack || error);
  console.error(error);
}
