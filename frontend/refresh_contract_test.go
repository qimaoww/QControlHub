package frontend

import (
	"os"
	"strings"
	"testing"
)

func TestRefreshPathsUseStableViewsAndScopedCoordinators(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	app := frontendSources(t, "app.js", "modules/session-api.js", "modules/shell-view.js")
	refresh := read("modules/refresh.js")
	for _, required := range []string{
		"createSessionAPI({ state, renderLogin })",
		"const shell = createShellView({",
		"combineAbortSignals(options.signal, routeSignal)",
		"[\"GET\", \"HEAD\", \"OPTIONS\"].includes(method)",
		"reconcileView(currentView, template.content.firstElementChild",
		"const routeChanged = !previousMain || previousRoute !== state.route",
		"previousMain.dataset.refreshKey !== workspaceKey",
		"routeChanged || viewChanged ? ` page-enter${firstScreenClass}`",
		"workspace-main${motionClass}",
		"const hasSharedData =",
		"const sharedDataPromise = Promise.all([",
		"const panelReads = createPanelReads(",
		`readPanelData("overview", { live: state.route === "dashboard" })`,
		`readPanelData("settings", { live: state.route === "settings" })`,
		"function primeRouteTransition()",
		"main.inert = true",
		"main.classList.add(\"is-route-pending\")",
		"const firstScreenClass = !previousMain ? \" first-screen\" : \"\"",
		"const contextChanged = routeChanged || state.data.contextMotionKey !== contextKey",
		"const contextMotionClass = contextChanged ? \" context-enter\" : \"\"",
		"data-refresh-key=\"context-${esc(contextKey)}\"",
		"state.navigationEpoch += 1",
		"cancelActive: () => routeController?.abort()",
		`routeModules.peek("agents")?.cancelAgentInteractions()`,
		"confirmResolver?.(false)",
		"state.route = \"login\"",
		"if (!state.session || state.route === \"login\") return",
		"if (renderedRoute === state.route) notify(error.message",
	} {
		if !strings.Contains(app, required) {
			t.Errorf("app refresh coordination is missing %q", required)
		}
	}
	for _, required := range []string{
		"export function reconcileView",
		"export function createRefreshChannel",
		"export function createPoller",
		"export function createInteractionGate",
		"const preserveInert = current.classList?.contains(\"desktop-app\") && current.inert",
		"scope !== getScope()",
		"state.active.focus({ preventScroll: true })",
		`typeof current.isEqualNode === "function"`,
	} {
		if !strings.Contains(refresh, required) {
			t.Errorf("shared refresh runtime is missing %q", required)
		}
	}
	contracts := map[string][]string{
		"modules/agents.js": {
			"createInteractionGate()",
			"requestAgentStructureRefresh()",
			"cardInteractions.activeCount() > 0",
			"cardInteractions.cancel()",
			"createAgentRefresh(ctx,",
			"refresh.markRendered(",
		},
		"modules/agent-view.js": {
			"data-refresh-key=\"agent-${esc(agent.id)}\"",
		},
		"modules/agent-refresh.js": {
			"createRefreshChannel({",
			"getScope: () => state.navigationEpoch",
			"syncBatchSnapshot(items)",
		},
		"modules/client-access.js": {
			"createRefreshChannel({",
			"getScope: () => state.navigationEpoch",
			"input.defaultValue = address",
			`button.form?.elements.namedItem("address")`,
		},
		"modules/core-logs.js": {
			"createPoller({",
			"filterCoreLogEntries(sourceEntries, filters)",
			"data-core-log-engine",
			"data-core-log-level",
			"data-refresh-key=\"core-log-${esc(entry.id)}\"",
			"data-refresh-scroll",
		},
		"modules/tasks.js": {
			"return reconcileView(existingCard, freshCard)",
			"api(`/tasks?${query}`, { signal })",
		},
		"modules/dashboard.js": {
			"api(`/traffic-usage?month=${encodeURIComponent(trafficMonth)}`",
			"data-dashboard-traffic-month",
			"data-dashboard-traffic-dialog",
			"trafficDetailsDialog.showModal()",
			`can("panel-metrics.read")`,
			"panelMetrics.mount()",
			"orderNodesBySavedOrder(agents)",
		},
		"modules/panel-metrics.js": {
			"createPoller({",
			"createRefreshChannel({",
			`api("/panel-metrics", { signal })`,
			"!document.hidden",
			`scope?.addEventListener("abort", stop`,
			"state.session === session",
			"reconcileView(root, template.content.firstElementChild)",
			"刷新失败 · 保留上次数据",
		},
		"modules/traffic.js": {
			"createPoller({",
			"data-refresh-key=\"traffic-policy-${esc(policy.id)}\"",
			"data-traffic-card-key=\"${esc(trafficCardIdentity(item))}\"",
			"qcontrolhub:traffic-card-order",
			"enableTrafficCardDrag(cardGrid",
			"trafficRateForDisplay(policy.receive_bps",
			"data-traffic-filter=\"engine\"",
			"data-traffic-edit-dialog",
			"class=\"traffic-edit-dialog traffic-create-dialog\"",
			"dialog?.showModal()",
			"name=\"auto_block\"",
		},
	}
	for path, markers := range contracts {
		content := frontendFeatureSources(t, path)
		for _, marker := range markers {
			if !strings.Contains(content, marker) {
				t.Errorf("%s refresh contract is missing %q", path, marker)
			}
		}
	}
	if !strings.Contains(read("module_smoke.mjs"), `import "./refresh_smoke.mjs"`) {
		t.Error("frontend smoke must execute the refresh interaction runtime")
	}
}

// Node grids and the application shell render on every navigation. These
// contracts keep the two shapes that made a page switch cost a request per
// card: per-node region lookups and a per-route shell refetch.
func TestPanelReadsAndNodeRegionsAvoidPerRenderRequests(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	for _, required := range []string{
		"export function createPanelReads(",
		"overview: 30_000",
		"settings: 300_000",
	} {
		if !strings.Contains(read("modules/panel-reads.js"), required) {
			t.Errorf("panel read cache is missing %q", required)
		}
	}
	regions := read("modules/regions.js")
	for _, required := range []string{
		"geoRegionDetails(agent.region_code)",
		"state.data.agentRegions",
	} {
		if !strings.Contains(regions, required) {
			t.Errorf("node list region rendering is missing %q", required)
		}
	}
	if !strings.Contains(read("modules/agent-komari.js"), "state.data.agentKomari") ||
		!strings.Contains(read("modules/agents.js"), "createAgentKomariDisplay({") {
		t.Error("node cards must cache the Komari read per node")
	}
	if !strings.Contains(read("module_smoke.mjs"), `import "./panel_reads_smoke.mjs"`) {
		t.Error("frontend smoke must execute the panel read cache runtime")
	}
	if !strings.Contains(regions, "api(`/agents/${encodeURIComponent(agent.id)}/region`)") {
		t.Error("nodes without a resolved region still need the per-node fallback")
	}
}
