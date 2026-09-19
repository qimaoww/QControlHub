import { installAgentFixture } from "./browser/agent-fixture.mjs";
import { testCapabilitySettingsRuntime } from "./browser/capability-settings.mjs";
import { testAgentBatchLayout } from "./browser/agent-batch-layout.mjs";
import { testAdminRuntime } from "./browser/agent-actions.mjs";
import { testEmptyRuntime, testSharedNodeRuntime, testReadonlyRuntime } from "./browser/agent-access.mjs";
import { testEnrollmentLayoutRuntime } from "./browser/enrollment.mjs";
import { testUsersLayoutRuntime } from "./browser/users-layout.mjs";
import { testRegionRuntime } from "./browser/regions.mjs";
import { testPortNamesAndRuntimeRefresh, testClientNodeOrderRuntime } from "./browser/node-runtime.mjs";
import { testSystemTCPRuntime } from "./browser/system-tcp.mjs";
import { testLargeLogRuntime, testLogPreferenceRestoreRuntime } from "./browser/core-logs.mjs";
import { testTrafficLayoutRuntime } from "./browser/traffic-layout.mjs";
import { testConfigLayoutRuntime } from "./browser/config-layout.mjs";
import { testShellLayoutRuntime } from "./browser/shell-layout.mjs";

const mode = new URLSearchParams(location.search).get("mode") || "admin";
const scenario = installAgentFixture(mode);

try {
  if (mode.startsWith("dashboard")) {
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
  } else if (mode === "presets") {
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
  } else {
    await import("./app.js");
    if (mode.startsWith("traffic-layout")) await testTrafficLayoutRuntime(scenario);
    else if (mode === "config-layout") await testConfigLayoutRuntime(scenario);
    else if (mode.startsWith("shell-layout")) await testShellLayoutRuntime(scenario);
    else if (mode.startsWith("capabilities-settings")) await testCapabilitySettingsRuntime(scenario);
    else if (mode === "bbr-preview" || mode === "regions-preview") await new Promise(() => {});
    else if (mode.startsWith("bbr")) await testSystemTCPRuntime(scenario);
    else if (mode.startsWith("batch-layout")) await testAgentBatchLayout(scenario);
    else if (mode === "admin") await testAdminRuntime(scenario);
    else if (mode.startsWith("client-order")) await testClientNodeOrderRuntime(scenario);
    else if (mode === "ports" || mode === "ports-mobile") await testPortNamesAndRuntimeRefresh(scenario);
    else if (mode === "regions") await testRegionRuntime(scenario);
    else if (mode === "empty") await testEmptyRuntime(scenario);
    else if (mode.startsWith("enrollment")) await testEnrollmentLayoutRuntime(scenario);
    else if (mode.startsWith("users-layout")) await testUsersLayoutRuntime(scenario);
    else if (mode.startsWith("shared-node")) await testSharedNodeRuntime(scenario);
    else if (mode === "logs") await testLargeLogRuntime(scenario);
    else if (mode === "logs-restore") await testLogPreferenceRestoreRuntime(scenario);
    else await testReadonlyRuntime(scenario);
  }
  document.documentElement.dataset.browserSmoke = "passed";
  if (!new URLSearchParams(location.search).has("preview"))
    document.body.innerHTML = `<pre id="browser-smoke-result">PASS ${mode}${window.logPressureResult ? " " + JSON.stringify(window.logPressureResult) : ""}</pre>`;
} catch (error) {
  document.documentElement.dataset.browserSmoke = "failed";
  document.body.innerHTML = `<pre id="browser-smoke-result"></pre>`;
  document.querySelector("#browser-smoke-result").textContent = String(error?.stack || error);
  console.error(error);
}
