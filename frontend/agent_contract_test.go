package frontend

import (
	"os"
	"strings"
	"testing"
)

func TestNodeSidebarsUseDraggedNodeSettingsOrder(t *testing.T) {
	order := frontendSources(t, "modules/node-order.js")
	for _, marker := range []string{`export const nodeCardOrderKey = "qcontrolhub:node-card-order"`, `export function createNodeOrderSync`, `endpoint = "/preferences/node-order"`, `api(endpoint`, `revision: order.revision`, `error.status !== 409`, `orderUnconfirmed`, `onStorageAccountChange`} {
		if !strings.Contains(order, marker) {
			t.Errorf("account-scoped shared node ordering missing %q", marker)
		}
	}
	// Native pages own selectors and cards instead of duplicate shell sidebars.
	for path, markers := range map[string][]string{
		"analytics/live-config.js":   {`await readOrderedNodes(ctx.api)`},
		"analytics/configs.js":       {`readOrderedNodes(ctx.api)`},
		"analytics/nodes.js":         {`orderNodesBySavedOrder(agents, ordering.ids())`, `bindNodeOrdering`},
		"analytics/observability.js": {`orderNodesBySavedOrder(coreLogSourceAgents(sources), nodeOrder)`},
		"analytics/traffic.js":       {`orderNodesBySavedOrder(agents, ordering.ids())`},
		"analytics/client-access.js": {`orderNodesBySavedOrder(list(agents), ordering.ids())`},
		"analytics/connections.js":   {`orderNodesBySavedOrder(sources.map`, `connectionQuery(filters, target, nodeOrder)`},
		"analytics/substore.js":      {`savedNodeOrder()`, `groupSubStoreProfiles`},
		"analytics/tcp.js":           {`orderNodesBySavedOrder(list(agents), order?.ids)`},
		"analytics/ip-quality.js":    {`orderNodesBySavedOrder(list(snapshot.agents), nodeOrder)`},
		"analytics/operations.js":    {`orderNodesBySavedOrder(list(readings.get("agents")), readings.get("order")?.ids)`},
	} {
		content := frontendSources(t, path)
		for _, marker := range markers {
			if !strings.Contains(content, marker) {
				t.Errorf("%s shared ordering missing %q", path, marker)
			}
		}
	}
}

func TestNodeSettingsStartsWithOperationsAndCards(t *testing.T) {
	app := string(mustReadFrontendFile(t, "modules/shell-view.js"))
	agents := frontendSources(t, "modules/agent-view.js", "modules/agent-batch-view.js", "modules/agent-batch-controller.js")
	styles := string(mustReadFrontendFile(t, "app.css"))
	if strings.Contains(agents, `class="node-page-intro"`) || strings.Contains(styles, `.node-page-intro`) {
		t.Fatal("node settings must not repeat its page title in a separate introduction panel")
	}
	if strings.Contains(agents, `class="node-batch-panel"`) {
		t.Fatal("node settings must not reserve an inline row for batch operations")
	}
	for _, required := range []string{
		`data-node-batch-toggle`,
		`data-node-batch-card`,
		`data-batch-checkbox`,
		`class="node-batch-bar"`,
		`data-close-node-batch`,
		`: "node-card-grid"`,
	} {
		if !strings.Contains(app+agents, required) {
			t.Errorf("node batch selection is missing %q", required)
		}
	}
	if !strings.Contains(styles, `.node-batch-bar{position:fixed`) ||
		!strings.Contains(styles, `.node-card.batch-selecting.selected`) {
		t.Fatal("node batch mode must use a floating action bar and visible card selection")
	}
}

func TestSystemBBRCardsUseDraggedNodeSettingsOrder(t *testing.T) {
	module := frontendSources(t, "analytics/tcp.js")
	for _, required := range []string{
		`import { orderNodesBySavedOrder } from '../modules/node-order.js';`,
		`orderNodesBySavedOrder(list(agents), order?.ids)`,
		`agent.can_manage !== false`,
	} {
		if !strings.Contains(module, required) {
			t.Errorf("system BBR cards are not using shared node ordering: missing %q", required)
		}
	}
}

func TestAgentBatchAndEnrollmentSafetyContracts(t *testing.T) {
	content := string(mustReadFrontendFile(t, "modules/agent-enrollment.js"))
	for file, markers := range map[string][]string{
		"modules/agents.js": {
			`createAgentEnrollment({`,
			`enrollment.bindEnrollmentPage(enrollmentHistory)`,
			`batch.bind(agentsByID)`,
		},
		"modules/agent-batch-controller.js": {
			`batchForm.dataset.busy === "1"`,
			`batchForm.dataset.confirming === "1"`,
			`for (const input of selected)`,
			`data-batch-retry`,
			`data-batch-select-all`,
			`selectAll.indeterminate = selection.indeterminate`,
			`selection.indeterminate ? "mixed"`,
		},
		"modules/agent-batch.js": {
			`agent-self-upgrade-v1`,
			`旧版 Agent 缺少远程升级能力`,
		},
		"modules/agent-enrollment.js": {
			`showCommand(command, async () =>`,
			`id="deploy-command-description">命令仅供复制，不会自动执行。`,
			`命令仅供复制，不会自动执行。`,
			`document.body.style.overflow = "hidden"`,
			`root.inert = true`,
			`new MutationObserver(`,
			`!wrap.isConnected`,
			`else lockBackground()`,
			`window.removeEventListener("hashchange", navigate)`,
			`timers.forEach(timer => window.clearTimeout(timer))`,
			`event.key !== "Tab"`,
			`button.dataset.confirmDelete !== "1"`,
		},
	} {
		source := string(mustReadFrontendFile(t, file))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing batch/enrollment safety contract %q", file, marker)
			}
		}
	}
	created := strings.Index(content, `const created = await api("/enrollment-tokens"`)
	if created < 0 {
		t.Fatal("enrollment flow must create an enrollment token")
	}
	shown := strings.Index(content[created:], `showCommand(command`)
	if shown < 0 {
		t.Fatal("enrollment flow must show the generated command")
	}
	shown += created
	refreshed := strings.Index(content[created:], `await refreshAgentPage()`)
	if refreshed >= 0 && created+refreshed < shown {
		t.Error("enrollment flow must display the command before refresh can lose it")
	}
}

func TestEnrollmentUsesARealDialogWithoutPersistentPanel(t *testing.T) {
	module := string(mustReadFrontendFile(t, "modules/agents.js")) + "\n" +
		string(mustReadFrontendFile(t, "modules/agent-enrollment.js"))
	css := string(mustReadFrontendFile(t, "app.css"))
	app := string(mustReadFrontendFile(t, "modules/shell-view.js"))
	if strings.Contains(app, `href="#enrollment"`) || !strings.Contains(app, `type="button" data-open-enrollment`) {
		t.Fatal("populated and empty node settings must expose a top dialog button without changing routes")
	}
	if strings.Contains(module, `class="enrollment-sheet"`) {
		t.Fatal("node settings must not retain the persistent enrollment sheet")
	}
	if strings.Contains(css, `.node-settings-page>.enrollment-sheet{display:block}`) || strings.Contains(css, `.node-settings-page>.enrollment-sheet:not([open]){display:block}`) {
		t.Fatal("effective CSS must not force a persistent enrollment sheet visible")
	}
	for _, marker := range []string{
		`role="dialog" aria-modal="true"`,
		`aria-labelledby="enrollment-dialog-title"`,
		`aria-describedby="enrollment-dialog-description"`,
		`data-enrollment-history-list`,
		`删除仅撤销凭据，不影响已注册节点。`,
		`添加记录刷新失败，部署命令未受影响`,
	} {
		if !strings.Contains(module, marker) {
			t.Errorf("enrollment dialog contract is missing %q", marker)
		}
	}
	if !strings.Contains(css, `.modal-backdrop{position:fixed`) || !strings.Contains(css, `.enrollment-history`) {
		t.Fatal("effective CSS must render the dialog backdrop and enrollment history")
	}
}

func TestAgentStructureRefreshDoesNotPrecommitComparisonMarkers(t *testing.T) {
	agents, err := os.ReadFile("modules/agent-refresh.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(agents)
	start := strings.Index(content, "function updateAgentMetrics")
	end := strings.Index(content, "async function pollAgentMetrics")
	if start < 0 || end <= start {
		t.Fatal("agent-refresh.js is missing the updateAgentMetrics body boundary")
	}
	body := content[start:end]
	for _, required := range []string{
		`requestAgentStructureRefresh();`,
		`card?.dataset.runtimeStructure === "full"`,
		`const installedKnown = typeof runtime.installed === "boolean";`,
		`const installedMarker = installedKnown ? installed ? "1" : "0" : "unknown";`,
		`card.dataset.coreInstalled !== installedMarker`,
		`card.dataset.existingPending !== (existingPending ? "1" : "0")`,
		`card.dataset.existingUnsupported !== existingUnsupportedReason`,
		`card.dataset.runtimeStructure !== "full"`,
		`card.dataset.coreInstalled = installedMarker`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("updateAgentMetrics structural comparison is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`card.dataset.existingPending =`,
		`card.dataset.existingUnsupported =`,
		`refreshAgentPage();`,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("updateAgentMetrics must not precommit structure markers or bypass the coalesced refresh path (%q)", forbidden)
		}
	}
}

func TestAgentPollingRefreshesNewlyEnrolledNodeStructure(t *testing.T) {
	agents := frontendSources(t, "modules/agents.js", "modules/agent-refresh.js")
	for _, required := range []string{
		`export function agentStructureSignature(agents = [])`,
		`renderedAgentStructure = agentStructureSignature(visibleAgents)`,
		`visibleAgentStructure(items) !== renderedAgentStructure`,
		`if (structureChanged) {`,
		`requestAgentStructureRefresh();`,
		`if (!presetMode)`,
		`state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);`,
	} {
		if !strings.Contains(agents, required) {
			t.Errorf("newly enrolled Agent polling contract is missing %q", required)
		}
	}
	pollStart := strings.Index(agents, "async function pollAgentMetrics()")
	if pollStart < 0 {
		t.Fatal("Agent roster polling function boundary is missing")
	}
	pollBody := agents[pollStart:]
	pollEnd := strings.Index(pollBody, "\n  return {\n    pollAgentMetrics,")
	if pollEnd < 0 {
		t.Fatal("Agent roster polling function end is missing")
	}
	pollBody = pollBody[:pollEnd]
	if strings.Contains(pollBody, `!can("metrics.read")`) {
		t.Error("new Agent discovery must not require metrics.read")
	}
}
