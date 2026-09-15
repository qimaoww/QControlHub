import assert from "node:assert/strict";
import { createDeploymentFixture } from "./deployment-fixture.mjs";
import { testFieldScopes } from "./deployment-field-scopes.mjs";
import { testAbortedMonitorRecovery, testFailedDeployPreservesCache, testValidationPreservesCache, testIndependentServiceCaches } from "./deployment-cache.mjs";
import { testDeploymentABA } from "./deployment-races.mjs";
import { testLiveEditorConverges, testSourceDeployConverges } from "./deployment-live.mjs";
import { testSamePageRecovery, testRapidNavigation } from "./preset-recovery.mjs";

export async function run() {
  const fixture = createDeploymentFixture();
  const { replacedRoutes } = fixture;
  try {
    await testFieldScopes(fixture);
    await testAbortedMonitorRecovery(fixture);
    await testFailedDeployPreservesCache(fixture);
    await testValidationPreservesCache(fixture);
    await testIndependentServiceCaches(fixture);
    await testDeploymentABA(fixture);
    await testLiveEditorConverges(fixture);
    await testSourceDeployConverges(fixture);
    await testSamePageRecovery(fixture);
    await testRapidNavigation(fixture);
    assert.ok(replacedRoutes.some(path=>path.includes("agent=test-agent") && path.includes("engine=xray")),
      "preset must bind normally when the real History API is present");
  } finally {
    fixture.restore();
  }
}
