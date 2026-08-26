package frontend

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentBrowserRuntimeSmoke(t *testing.T) {
	command := exec.Command("node", "agents_browser_smoke.mjs")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("agent browser runtime smoke failed: %v\n%s", err, output)
	}
}

func TestSPAConsoleSurfaceMatchesInitialRelease(t *testing.T) {
	var scripts strings.Builder
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".js" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scripts.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	content := scripts.String()
	for _, required := range []string{
		`"client-access"`, `"live-config"`, `"archive-config"`,
		`machine-workspace`, `server-plan-form`, `field-form`,
		`revision-timeline`, `task-timeline`, `settings-section`,
		`node-settings`, `内核配置预设`, `node-settings-tabs`, `查看安装部署命令`, `复制 Agent 安装命令`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial console surface %q", required)
		}
	}
	for _, required := range []string{
		`data-theme-toggle`, `qcontrolhub-color-theme`, `login-theme-toggle`,
		`app.style.display = "contents"`, `X-QControlHub-Enrollment`,
		`/install-agent.sh`, `执行记录`, `手动配置`, `系统设置`,
		`data-delete-enrollment`, `可重复安装`, `删除添加命令`,
		`enrollment-token`, `/enrollment-command`,
		`heartbeat, percent`, `serviceActionDisabled, trafficChart, renderConfigDiff`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial visual/installation contract %q", required)
		}
	}
	if strings.Contains(content, "/ui/") {
		t.Error("SPA must use the JSON API instead of legacy HTML form routes")
	}
	for _, forbidden := range []string{"注册码", "入网码", "命令有效期"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SPA still exposes deprecated add-node wording %q", forbidden)
		}
	}
	if strings.Contains(content, "旧安装命令会立即失效") || strings.Contains(content, "重新生成后旧命令立即失效") {
		t.Error("SPA must keep existing Agent install commands valid when another command is generated")
	}
	for _, forbidden := range []string{"生成新安装命令", "data-reinstall-agent"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SPA must not expose single-node credential generation control %q", forbidden)
		}
	}
	if !strings.Contains(content, "命令可重复查看") || !strings.Contains(content, "命令生成后可重复查看") {
		t.Error("SPA does not explain that existing Agent install commands remain readable")
	}
}

func TestServerPlanRegenerationStaysLocalAndUsesCurrentFormState(t *testing.T) {
	configs, err := os.ReadFile("modules/configs.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(configs)
	start := strings.Index(content, "export function bindServerPlanRegeneration")
	end := strings.Index(content, "export function installConfigPages")
	if start < 0 || end <= start {
		t.Fatal("configs.js is missing the isolated server-plan regeneration handler")
	}
	handler := content[start:end]
	for _, required := range []string{
		"readServerPlanInput(form, protocol)",
		"JSON.stringify({ protocol: protocol.key, input })",
		"form.elements.namedItem(name)",
		"request !== latestRequest",
		"requestedRevision !== formRevision",
		`buttons.forEach((item)`,
		`item.disabled = true`,
		`button.setAttribute("aria-busy", "true")`,
		"生成参数失败",
	} {
		if !strings.Contains(handler, required) {
			t.Errorf("server-plan regeneration handler is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"agentConfig(", "location.", "state.data.inboundTag", "shell(", "notify(",
	} {
		if strings.Contains(handler, forbidden) {
			t.Errorf("server-plan regeneration handler must not use %q", forbidden)
		}
	}
	if strings.Contains(content, "delete state.data.serverPlans[") {
		t.Error("server-plan regeneration must not discard the current local plan before the request succeeds")
	}
	if !strings.Contains(content, "data-regenerate-status") {
		t.Error("server-plan regeneration needs a local status region that does not scroll the page")
	}
	for _, required := range []string{
		`["port", "port", "生成监听端口"]`,
		`["credential", "credential", "生成凭据"]`,
		`["secondary_credential", "secondary_credential", "生成次凭据"]`,
		`"reality_private_key,reality_public_key"`,
		`["reality_short_id", "reality_short_id", "生成 Short ID"]`,
		`const portForward = Boolean(protocol?.port_forward);`,
		`name="target_address"`,
		`name="target_port"`,
		`name="network"`,
		`<strong>转发目标</strong>`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("server-plan field generation is missing %q", required)
		}
	}
}

func TestRefreshPathsUseStableViewsAndScopedCoordinators(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	app := read("app.js")
	refresh := read("modules/refresh.js")
	for _, required := range []string{
		"combineAbortSignals(options.signal, routeSignal)",
		"[\"GET\", \"HEAD\", \"OPTIONS\"].includes(method)",
		"reconcileView(currentView, template.content.firstElementChild",
		"state.navigationEpoch += 1",
		"cancelActive: () => routeController?.abort()",
		"agentModule.cancelAgentInteractions()",
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
		"scope !== getScope()",
		"state.active.focus({ preventScroll: true })",
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
			"data-refresh-key=\"agent-${esc(agent.id)}\"",
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
		},
		"modules/traffic.js": {
			"createPoller({",
			"data-refresh-key=\"traffic-policy-${esc(policy.id)}\"",
			"data-traffic-filter=\"engine\"",
			"data-traffic-edit-dialog",
			"class=\"traffic-edit-dialog traffic-create-dialog\"",
			"dialog?.showModal()",
			"name=\"auto_block\"",
		},
	}
	for path, markers := range contracts {
		content := read(path)
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

func TestSidebarNavigationUsesWorkflowOrderAndResponsiveGrouping(t *testing.T) {
	app, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(app)
	start := strings.Index(content, "  const links = [")
	if start < 0 {
		t.Fatal("app.js is missing the sidebar navigation list")
	}
	end := strings.Index(content[start:], "  ];\n  const linkPermissions")
	if end < 0 {
		t.Fatal("app.js sidebar navigation list has no closing boundary")
	}
	navigation := content[start : start+end]
	previous := -1
	for _, route := range []string{
		"dashboard", "node-settings", "agents", "live-config",
		"client-access", "traffic", "core-logs", "tasks",
	} {
		position := strings.Index(navigation, `"`+route+`"`)
		if position < 0 {
			t.Errorf("sidebar navigation is missing route %q", route)
			continue
		}
		if position <= previous {
			t.Errorf("sidebar route %q is outside the expected workflow order", route)
		}
		previous = position
	}
	if strings.Contains(navigation, `"settings"`) {
		t.Error("settings must remain separated from the primary desktop navigation")
	}
	for _, required := range []string{
		`const dockIcons = Object.freeze({`,
		`["node-settings", "节点设置", dockIcons.server]`,
		`["agents", "内核预设", dockIcons.layers]`,
		`["live-config", "配置", dockIcons.fileCode]`,
		`["client-access", "客户端", dockIcons.monitorSmartphone]`,
		`["traffic", "流量", dockIcons.chart, true]`,
		`["core-logs", "日志", dockIcons.logs, true]`,
		`["tasks", "任务", dockIcons.listChecks]`,
		`class="dock-settings`,
		`mobileMoreRoutes.some(([id]) => activeDockRoute(id))`,
		`summary class="${mobileMoreActive ? "active" : ""}"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("sidebar navigation is missing responsive icon contract %q", required)
		}
	}
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`.dock-nav .dock-mobile-secondary{display:none}`,
		`.mobile-account-menu>summary.active`,
		`.mobile-account-menu a,.mobile-account-menu button`,
	} {
		if !strings.Contains(string(styles), required) {
			t.Errorf("sidebar styles are missing responsive grouping contract %q", required)
		}
	}
}

func TestTrafficUsesOneNodeFilterSurface(t *testing.T) {
	app, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(app)
	for _, required := range []string{
		"端口流量节点",
		`href="#traffic-all" data-context-traffic-agent="">全部节点`,
		`"traffic-all": "traffic"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("traffic route must keep all node selection in the context sidebar: missing %q", required)
		}
	}
	traffic, err := os.ReadFile("modules/traffic.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traffic), `data-traffic-filter="agent_id"`) {
		t.Error("traffic workspace must not duplicate the context sidebar node selector")
	}
	if !strings.Contains(string(traffic), `state.anchor === "traffic-all"`) ||
		!strings.Contains(string(traffic), `currentFilters.agent_id = ""`) {
		t.Error("traffic all-node sidebar action must clear the selected node scope")
	}
	styles := string(mustReadFrontendFile(t, "app.css"))
	if !strings.Contains(styles, `.traffic-workspace>.traffic-policy-grid{grid-template-columns:repeat(auto-fill,minmax(360px,1fr))`) {
		t.Error("traffic cards must use the same responsive column sizing as node settings cards")
	}
	if strings.Contains(styles, `.traffic-workspace{width:100%;max-width:1240px`) {
		t.Error("traffic cards must not use a narrower workspace than node settings cards")
	}
}

func TestPresetSidebarShowsOnlySelectedNodeContent(t *testing.T) {
	app, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := os.ReadFile("modules/agents.js")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}

	const sidebarPresetLink = `href="#node-${esc(agent.id)}" data-context-agent="${esc(agent.id)}"`
	if !strings.Contains(string(app), sidebarPresetLink) {
		t.Error("preset sidebar nodes must start from the per-node preset anchor")
	}
	if !strings.Contains(string(app), `state.data.selectedAgent === agent.id ? "active" : ""`) {
		t.Error("preset sidebar must retain the selected node active state")
	}
	const settingsDetailLink = `href="#settings-node-${esc(agent.id)}" data-context-agent="${esc(agent.id)}"`
	if strings.Contains(string(app), settingsDetailLink) {
		t.Error("preset sidebar nodes must not enter the node settings workflow")
	}
	for _, required := range []string{
		"hash.startsWith(\"preset-node-\")\n        ? \"agents\"",
		`if (hash.startsWith("preset-node-")) state.data.selectedAgent = hash.slice(12);`,
	} {
		if !strings.Contains(string(app), required) {
			t.Errorf("preset sidebar routing is missing %q", required)
		}
	}
	for _, required := range []string{
		`? selectedAgent`,
		`? [selectedAgent]`,
		`class="preset-node-workspace workspace-panel machine-body"`,
		`id="preset-node-${esc(agent.id)}"`,
		`<h2>节点内核</h2>`,
		`const prefix = presetMode ? "preset-node" : "settings-node";`,
		"link.href = `#${prefix}-${link.dataset.contextAgent}`;",
	} {
		if !strings.Contains(string(agents), required) {
			t.Errorf("focused preset workspace is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`<details class="machine-workspace"`,
		`<summary class="machine-header"`,
		`<header class="node-page-intro"><div><p class="eyebrow">节点配置</p>`,
		`agent.id === state.data.selectedAgent ? "open" : ""`,
	} {
		if strings.Contains(string(agents), forbidden) {
			t.Errorf("focused preset workspace still renders node accordion contract %q", forbidden)
		}
	}

	const presetSingleColumnRule = `.preset-node-workspace>.service-canvas>.service-grid{grid-template-columns:minmax(0,1fr)}`
	if strings.Count(string(styles), presetSingleColumnRule) != 1 {
		t.Error("selected preset workspace must define one scoped, shrinkable service-grid column")
	}
	const globalSingleColumnRule = `.service-grid{grid-template-columns:minmax(0,1fr)}`
	for _, line := range strings.Split(string(styles), "\n") {
		if strings.TrimSpace(line) == globalSingleColumnRule {
			t.Error("preset layout must not force every service grid into one column")
		}
	}
}

func TestNodeSettingsStartsWithOperationsAndCards(t *testing.T) {
	app := string(mustReadFrontendFile(t, "app.js"))
	agents := string(mustReadFrontendFile(t, "modules/agents.js"))
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

func TestClientAccessUsesContextSidebarAsOnlyNodeFilter(t *testing.T) {
	app, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	clientAccess, err := os.ReadFile("modules/client-access.js")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}

	for _, required := range []string{
		`data-access-agent=""`,
		`data-access-agent="${esc(agent.id)}"`,
		`state.data.accessAgent === agent.id ? "active" : ""`,
	} {
		if !strings.Contains(string(app), required) {
			t.Errorf("client access context sidebar is missing %q", required)
		}
	}
	for _, required := range []string{
		`if (filters.agent && entry.agent_id !== filters.agent) return [];`,
		`data-filter-engine=""`,
		`aria-label="按内核筛选"`,
		`.querySelectorAll("[data-access-agent]")`,
		`client-access-toolbar`,
		`client-access-node-card`,
		`data-client-parameter-open`,
		`class="traffic-edit-dialog client-parameter-dialog"`,
		`dialog?.showModal();`,
		`new ResizeObserver(layout)`,
		`card.style.gridRowEnd = `,
		`groupClientAccessEntries(filtered)`,
		`normalizeClientAccessFilters(entries, agents`,
		`renderClientAccess();`,
	} {
		if !strings.Contains(string(clientAccess), required) {
			t.Errorf("client access filtering is missing %q", required)
		}
	}
	for _, forbidden := range []string{`data-filter-agent`, `aria-label="按节点筛选"`, `filterAgentIDs`, `client-access-hero`, `client-access-summary`, `client-access-filter-panel`, `client-access-results-head`, `client-parameter-menu`, `client-parameter-grid`, `含凭据`} {
		if strings.Contains(string(clientAccess), forbidden) {
			t.Errorf("client access main workspace still contains superseded visual structure %q", forbidden)
		}
	}
	for _, required := range []string{
		`.client-access-toolbar`,
		`.client-profile-row`,
		`.client-access-node-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,390px),1fr))`,
		`grid-auto-rows:1px`,
		`.client-access-node-card{width:100%;min-width:0`,
		`.client-parameter-dialog{width:min(560px`,
		`.client-parameter-list`,
	} {
		if !strings.Contains(string(styles), required) {
			t.Errorf("client access compact layout is missing %q", required)
		}
	}
	const narrowSidebarRule = `@media(max-width:820px) and (pointer:coarse){.page-client-access .context-sidebar{display:flex}}`
	if !strings.Contains(string(styles), narrowSidebarRule) {
		t.Error("client access context sidebar must remain available on narrow screens")
	}
	if !strings.Contains(string(styles), `.context-menu,.context-list{display:flex;overflow:auto;`) {
		t.Error("narrow context navigation must keep overflow inside its own scroll container")
	}
}

func TestAgentWebSocketProxyForwardsSourceChain(t *testing.T) {
	nginx, err := os.ReadFile("nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	const agentProxy = `location /agent/ { proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade"; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for; proxy_pass $control_plane; }`
	if !strings.Contains(string(nginx), agentProxy) {
		t.Error("Agent WebSocket proxy must forward the existing trusted source chain")
	}
}

func TestOfficialDeploymentsTrustTheExactTwoHopProxyChain(t *testing.T) {
	compose, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	quickStart, err := os.ReadFile("../deploy/quick-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	production, err := os.ReadFile("../docs/production.md")
	if err != nil {
		t.Fatal(err)
	}

	for _, source := range []struct {
		name    string
		content string
	}{
		{name: "bundled compose", content: string(compose)},
		{name: "external quick-start compose", content: string(quickStart)},
	} {
		for _, required := range []string{
			`QCH_TRUSTED_PROXY_CIDRS: ${QCH_TRUSTED_PROXY_CIDRS:-172.30.254.2/32,172.30.254.1/32}`,
			`QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS: ${QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS:-}`,
			`ipv4_address: ${QCH_WEB_PROXY_ADDRESS:-172.30.254.2}`,
			`ipv4_address: ${QCH_CONTROL_PLANE_PROXY_ADDRESS:-172.30.254.3}`,
			`subnet: ${QCH_CONTROL_PROXY_SUBNET:-172.30.254.0/24}`,
			`gateway: ${QCH_CONTROL_PROXY_GATEWAY:-172.30.254.1}`,
		} {
			if !strings.Contains(source.content, required) {
				t.Errorf("%s does not close the exact proxy chain with %q", source.name, required)
			}
		}
	}
	for _, required := range []string{
		`QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS`,
		`previous_config_keys`,
		`prepend_unique_csv "$previous_config_keys" "$config_key"`,
	} {
		if !strings.Contains(string(quickStart), required) {
			t.Errorf("quick-start key rotation contract is missing %q", required)
		}
	}

	qcontrolWeb := strings.SplitN(string(compose), "\n  qcontrol-web:", 2)
	if len(qcontrolWeb) != 2 {
		t.Fatal("bundled compose is missing qcontrol-web")
	}
	qcontrolWebBlock := strings.SplitN(qcontrolWeb[1], "\nvolumes:", 2)[0]
	if strings.Contains(qcontrolWebBlock, "\n      - backend") || strings.Contains(qcontrolWebBlock, "\n      backend:") {
		t.Error("qcontrol-web must reach control-plane only through its fixed proxy-chain address")
	}

	for _, required := range []string{
		`'QCH_WEB_PROXY_ADDRESS=172.30.254.2'`,
		`'QCH_CONTROL_PLANE_PROXY_ADDRESS=172.30.254.3'`,
		`'QCH_TRUSTED_PROXY_CIDRS=172.30.254.2/32,172.30.254.1/32'`,
	} {
		if !strings.Contains(string(makefile), required) {
			t.Errorf("make init-env is missing %q", required)
		}
	}
	for _, required := range []string{
		`trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$web_proxy_address/32")"`,
		`trusted_proxy_cidrs="$(append_trusted_proxy "$trusted_proxy_cidrs" "$proxy_gateway/32")"`,
		`"QCH_TRUSTED_PROXY_CIDRS=$trusted_proxy_cidrs"`,
	} {
		if strings.Count(string(quickStart), required) != 2 {
			t.Errorf("bundled and external env preparation must both preserve %q", required)
		}
	}
	for _, required := range []string{"宿主 Nginx 与 `qcontrol-web` 两跳代理", "两个精确 `/32` 端点", "禁止改成整个私网"} {
		if !strings.Contains(string(production), required) {
			t.Errorf("production proxy documentation is missing %q", required)
		}
	}
}

func TestInitialConsoleStylesRemainExactOutsideApprovedExtensions(t *testing.T) {
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	const extensionMarker = "/* Deployment command dialog v48: a scoped extension to the initial console surface. */"
	parts := strings.SplitN(string(styles), extensionMarker, 2)
	if len(parts) != 2 {
		t.Fatalf("app.css is missing approved deployment dialog extension marker")
	}
	// Base hash covers the initial release plus the approved v56 revision that
	// reserved the compact media queries for coarse-pointer devices so desktop
	// zoom keeps one fixed layout.
	const expected = "b967be66daf4078b69fdc204cf88105800f424dae4aed74ec3e5807b47a3ff4c"
	if actual := fmt.Sprintf("%x", sha256.Sum256([]byte(parts[0]))); actual != expected {
		t.Fatalf("base app.css hash = %s, want initial release hash %s", actual, expected)
	}
	for _, required := range []string{".deploy-command-modal", ".deploy-command-input", ".deploy-command-copy"} {
		if !strings.Contains(parts[1], required) {
			t.Errorf("deployment dialog extension is missing %q", required)
		}
	}
}

func TestStaticAssetsUseBuildGeneratedCacheKeys(t *testing.T) {
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(index)
	for _, required := range []string{
		`/assets/app.css?v=__QCH_CSS_VERSION__`,
		`/assets/app.js?v=__QCH_JS_VERSION__`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("index.html is missing cache key placeholder %q", required)
		}
	}
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"__QCH_CSS_VERSION__", "__QCH_JS_VERSION__"} {
		if !strings.Contains(string(dockerfile), placeholder) {
			t.Errorf("Dockerfile does not replace %s", placeholder)
		}
	}
	if !strings.Contains(string(dockerfile), `modules/[^\"]+\\.js`) || !strings.Contains(string(dockerfile), `?v=${js_version}`) {
		t.Error("Dockerfile does not add the aggregate JavaScript cache key to module imports")
	}
	if !strings.Contains(string(dockerfile), `js_content_version`) || !strings.Contains(string(dockerfile), `${VERSION}`) {
		t.Error("Dockerfile JavaScript cache key must include both content and release version")
	}
}

func TestSPAModulesArePublished(t *testing.T) {
	for _, name := range []string{
		"dashboard.js",
		"agents.js",
		"client-access.js",
		"configs.js",
		"tasks.js",
		"traffic.js",
		"settings.js",
		"../module_smoke.mjs",
	} {
		if _, err := os.Stat(filepath.Join("modules", name)); err != nil {
			t.Errorf("missing SPA module %s: %v", name, err)
		}
	}
}

func TestAgentBatchAndEnrollmentSafetyContracts(t *testing.T) {
	content := string(mustReadFrontendFile(t, "modules/agents.js"))
	for _, marker := range []string{
		`agent-self-upgrade-v1`,
		`旧版 Agent 缺少远程升级能力`,
		`batchForm.dataset.busy === "1"`,
		`batchForm.dataset.confirming === "1"`,
		`for (const input of selected)`,
		`data-batch-retry`,
		`data-batch-select-all`,
		`selectAll.indeterminate = selection.indeterminate`,
		`selection.indeterminate ? "mixed"`,
		`命令仅供复制；关闭页面不会连接、安装或重启任何节点。`,
		`showCommand(command, async () =>`,
		`浏览器绝不会执行`,
		`document.body.style.overflow = "hidden"`,
		`root.inert = true`,
		`new MutationObserver(lockBackground)`,
		`event.key !== "Tab"`,
		`button.dataset.confirmDelete !== "1"`,
	} {
		if !strings.Contains(content, marker) {
			t.Errorf("agent batch/enrollment safety contract is missing %q", marker)
		}
	}
	created := strings.Index(content, `const created = await api("/enrollment-tokens"`)
	shown := strings.Index(content[created:], `showCommand(command`)
	if created < 0 || shown < 0 {
		t.Fatal("enrollment flow must show the generated command")
	}
	shown += created
	refreshed := strings.Index(content[created:], `await refreshAgentPage()`)
	if refreshed >= 0 && created+refreshed < shown {
		t.Error("enrollment flow must display the command before refresh can lose it")
	}
}

func TestEnrollmentUsesARealDialogWithoutPersistentPanel(t *testing.T) {
	module := string(mustReadFrontendFile(t, "modules/agents.js"))
	css := string(mustReadFrontendFile(t, "app.css"))
	app := string(mustReadFrontendFile(t, "app.js"))
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
		`删除记录只会立即撤销对应凭据`,
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

func mustReadFrontendFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestManualConfigRequiresExplicitImportOfNodeSnapshot(t *testing.T) {
	configs, err := os.ReadFile("modules/configs.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(configs)
	for _, required := range []string{
		`data-live-intent="import">手动导入并迁移`,
		`迁移任一步失败都会自动恢复原服务`,
		`action: "import-existing"`,
		`existing_config_unsupported_reason`,
		`检测到现有服务，但不可自动迁移`,
		`只读导入快照`,
		`data-live-source="managed"`,
		`data-live-source="import"`,
		`QAgent 现有配置`,
		`系统服务配置（只读）`,
		`read-managed-config`,
		`submitLiveConfigChange`,
		`!unsupportedReason`,
		`esc(unsupportedReason)`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("manual configuration flow is missing %q", required)
		}
	}
	agents, err := os.ReadFile("modules/agents.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`data-existing-pending`, `data-existing-unsupported`, `data-manual-import`,
		`导入现有服务`, `检测到但不可迁移`, `查看现有服务不可导入原因`,
		`existing_config_unsupported_reason`, `esc(existingUnsupportedReason)`,
	} {
		if !strings.Contains(string(agents), required) {
			t.Errorf("node service controls do not represent pending migration state %q", required)
		}
	}
	app, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`liveConfigEngineEligible,`,
		`(engine) => liveConfigEngineEligible(selected.runtime?.[engine])`,
		`class="${engine === state.data.liveEngine ? "active" : ""}"`,
	} {
		if !strings.Contains(string(app), required) {
			t.Errorf("manual configuration context sidebar is missing %q", required)
		}
	}
}

func TestCoreLogsLabelFollowsAdvertisedFeature(t *testing.T) {
	app, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(app)
	for _, required := range []string{
		`(agent.features || []).includes("core-logs-v1")`,
		`"集中日志已启用"`,
		`"需升级 Agent"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-logs sidebar is missing %q", required)
		}
	}
	if strings.Contains(content, `agent.features.includes("core-logs-v1") ? "需升级 Agent"`) {
		t.Error("core-logs sidebar must enable streaming only when core-logs-v1 is advertised")
	}
}

func TestCoreLogsUseImmediateFiltersAndSidebarNodeScope(t *testing.T) {
	module, err := os.ReadFile("modules/core-logs.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(module)
	for _, required := range []string{
		`data-core-log-engine`,
		`data-core-log-level`,
		`renderLocalFilters({ q: event.currentTarget.value })`,
		`data-toggle-core-log-refresh`,
		`core-log-columns`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-log immediate filtering is missing %q", required)
		}
	}
	for _, duplicatedControl := range []string{
		`name="agent_id"`,
		`type="submit"`,
	} {
		if strings.Contains(content, duplicatedControl) {
			t.Errorf("core-log workspace still contains duplicated or deferred control %q", duplicatedControl)
		}
	}
}

func TestCoreLogsUseReadableAlignedDesktopGrid(t *testing.T) {
	stylesheet, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	content := string(stylesheet)
	for _, required := range []string{
		`--core-log-grid:168px 130px 72px 150px minmax(360px,1fr)`,
		`grid-template-columns:var(--core-log-grid);align-items:center`,
		`.core-log-row time,.core-log-row span{min-width:0;overflow:hidden;font-size:11px`,
		`.core-log-row pre{min-width:0;margin:0;color:var(--ink-2);font:11px/1.6`,
		`.core-log-filter-group,.core-log-filters label{display:grid;gap:7px;min-width:0;color:var(--muted);font-size:11px`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-log readable alignment contract is missing %q", required)
		}
	}
	for _, obsolete := range []string{
		`.core-log-row time{padding-top:3px`,
		`.core-log-level{padding-top:3px`,
		`.core-log-agent{padding-top:3px`,
	} {
		if strings.Contains(content, obsolete) {
			t.Errorf("core-log columns still use manual baseline offset %q", obsolete)
		}
	}
}

func TestAgentStructureRefreshDoesNotPrecommitComparisonMarkers(t *testing.T) {
	agents, err := os.ReadFile("modules/agents.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(agents)
	start := strings.Index(content, "function updateAgentMetrics")
	end := strings.Index(content, "async function pollAgentMetrics")
	if start < 0 || end <= start {
		t.Fatal("agents.js is missing the updateAgentMetrics body boundary")
	}
	body := content[start:end]
	for _, required := range []string{
		`requestAgentStructureRefresh();`,
		`card?.dataset.runtimeStructure === "full"`,
		`card.dataset.coreInstalled !== (installed ? "1" : "0")`,
		`card.dataset.existingPending !== (existingPending ? "1" : "0")`,
		`card.dataset.existingUnsupported !== existingUnsupportedReason`,
		`card.dataset.runtimeStructure !== "full"`,
		`card.dataset.coreInstalled = installed ? "1" : "0"`,
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
	agents := string(mustReadFrontendFile(t, "modules/agents.js"))
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
	pollEnd := strings.Index(pollBody, "function bindCodeEditors()")
	if pollEnd < 0 {
		t.Fatal("Agent roster polling function end is missing")
	}
	pollBody = pollBody[:pollEnd]
	if strings.Contains(pollBody, `!can("metrics.read")`) {
		t.Error("new Agent discovery must not require metrics.read")
	}
}

func TestTaskPollingKeepsTheScrollContainerStable(t *testing.T) {
	tasks, err := os.ReadFile("modules/tasks.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(tasks)
	for _, required := range []string{
		`tasks({ background: true })`,
		`reconcileTaskTimeline`,
		`captureTaskAnchor`,
		`restoreTaskAnchor`,
		`taskRenderSignature`,
		`syncTaskAgentFilter`,
		`data-task-age`,
		`data-task-timing`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("task polling is missing in-place refresh contract %q", required)
		}
	}
	if strings.Contains(content, `setTimeout(() => tasks(),`) {
		t.Error("task polling must not rebuild the complete application shell")
	}
}
