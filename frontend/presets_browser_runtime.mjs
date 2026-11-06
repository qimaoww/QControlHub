import { testPresetCatalogRuntime } from "./browser/preset-catalog.mjs";
import { testManagedTLSRuntime } from "./browser/preset-tls.mjs";
import { testMieruRoundTripRuntime } from "./browser/preset-mieru.mjs";
import { testEditedRetryDialog } from "./browser/task-retry.mjs";
import { testPresetRecoveryRuntime } from "./browser/preset-recovery.mjs";
import { assert, waitFor, createConfigFixture } from "./browser/config-fixture.mjs";

export async function testPresetsRuntime(preview = false) {
  const query = new URLSearchParams(location.search);
  if (preview && query.has("retry")) return testEditedRetryDialog(true);
  const response = await fetch("/assets/preset-plans.json");
  assert(response.ok, "real preset test catalog failed to load");
  const catalog = await response.json();
  if (preview) {
    const engine = query.get("engine") || "xray", protocol = query.get("protocol");
    const actual = catalog.find(entry=>entry.engine===engine&&entry.protocol.key===protocol);
    const entries = actual?.profile ? [{agent_id:"node", agent_name:"XHTTP 测试节点", address:"203.0.113.10", engine,
      profiles:[{tag:actual.plan.tag,port:actual.plan.port,protocol:actual.protocol.name,profile:actual.profile}]}] : [];
    const test = await createConfigFixture(engine, {protocol,shared:query.has("shared"),clientEntries:entries});
    window.presetFixture = test;
    if (query.has("exports")) {
      location.hash = "#client-access";
      await waitFor(()=>[...document.querySelectorAll('.a-main button')].some(control=>control.textContent==='参数详情'),"native client export preview missing");
      test.clickButton("参数详情");
    } else test.click("modify");
    return;
  }
  const errors = [], onError = event=>errors.push(String(event.reason || event.message));
  window.addEventListener("unhandledrejection", onError);
  window.addEventListener("error", onError);
  try {
    // Native inbound/common suites retain strict deletion, CAS, installation,
    // history, auxiliary reads and read-only checks formerly on the classic page.
    await testPresetRecoveryRuntime();
    await testManagedTLSRuntime();
    await testPresetCatalogRuntime(catalog);
    await testMieruRoundTripRuntime();
    await testEditedRetryDialog();
    assert(errors.length===0, `uncaught browser errors: ${errors.join("; ")}`);
  } finally {
    window.removeEventListener("unhandledrejection", onError);
    window.removeEventListener("error", onError);
  }
}
