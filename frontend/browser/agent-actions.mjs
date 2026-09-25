import { testAgentOverview } from "./agent-overview.mjs";
import { testAgentEnrollmentActions } from "./agent-enrollment-actions.mjs";
import { testAgentBatchActions } from "./agent-batch-actions.mjs";
import { testAgentDetailActions } from "./agent-detail-actions.mjs";
import { testAgentRenameActions } from "./agent-rename-actions.mjs";
import { testAgentDeleteActions } from "./agent-delete-actions.mjs";

// Each phase receives the same page-local fixture and runs in the original
// order; importing a phase never installs a fixture or changes the DOM.
export async function testAdminRuntime(fixture) {
  await testAgentOverview(fixture);
  await testAgentEnrollmentActions(fixture);
  await testAgentBatchActions(fixture);
  await testAgentDetailActions(fixture);
  await testAgentRenameActions(fixture);
  await testAgentDeleteActions(fixture);
}
