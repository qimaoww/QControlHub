package frontend

import (
	"os"
	"strings"
	"testing"
)

func TestServerPlanRegenerationStaysLocalAndUsesCurrentFormState(t *testing.T) {
	form := string(mustReadFrontendFile(t, "modules/server-plan-form.js"))
	content := form + "\n" + frontendSources(t,
		"modules/preset-editor.js", "modules/preset-view.js", "modules/preset-bindings.js")
	start := strings.Index(form, "export function bindServerPlanRegeneration")
	if start < 0 {
		t.Fatal("server-plan-form.js is missing the isolated server-plan regeneration handler")
	}
	handler := form[start:]
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
		"const esc = (value) =>",
		`["port", "port", "生成监听端口"]`,
		`["credential", "credential", "生成凭据"]`,
		`["secondary_credential", "secondary_credential", "生成次凭据"]`,
		`"reality_private_key,reality_public_key"`,
		`["reality_short_id", "reality_short_id", "生成 Short ID"]`,
		`"vless_decryption,vless_encryption"`,
		`protocol?.uses_vless_encryption`,
		`name="vless_decryption"`,
		`name="vless_encryption"`,
		`const portForward = Boolean(protocol?.port_forward);`,
		`name="target_address"`,
		`name="target_port"`,
		`name="network"`,
		`<strong>转发目标</strong>`,
		`snellProtocolOptions(plan, false)`,
		`snellProtocolOptions(plan, true)`,
		`sudokuProtocolOptions(plan)`,
		`name="snell_obfs_mode" value="shadow-tls"`,
		`optionSecret("snell_shadow_tls_password"`,
		`optionSecret("sudoku_client_key"`,
		`name="sudoku_httpmask_mode"`,
		`name="sudoku_multiplex"`,
		`bindProtocolOptionVisibility(serverPlanForm)`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("server-plan field generation is missing %q", required)
		}
	}
	if strings.Contains(content, "skip-cert-verify") {
		t.Error("safe Snell presets must not expose a certificate verification bypass")
	}
	for _, forbidden := range []string{`name="sudoku_key_mode"`, `name="sudoku_smux_enabled"`, `name="sudoku_allow_insecure_aead"`} {
		if strings.Contains(content, forbidden) {
			t.Errorf("safe Sudoku preset must not expose %q", forbidden)
		}
	}
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(styles), ".protocol-catalog-wide.protocol-two-rows .protocol-browser>nav{display:grid") ||
		!strings.Contains(content, `protocolCatalogNav.scrollWidth > protocolCatalogNav.clientWidth + 1`) ||
		!strings.Contains(content, `String(Math.ceil(workspace.protocols.length / 2))`) ||
		!strings.Contains(content, `"vless-enc-xhttp-reality-vision": "VLESS-ENC-XHTTP-Vision-uTLS-REALITY"`) {
		t.Error("large protocol preset catalogs must render as exactly two rows")
	}
}

func TestLegacyPresetLinksAndRendererKeepScopedNodeSelection(t *testing.T) {
	app := frontendSources(t, "app.js", "modules/routes.js", "modules/shell-context.js")
	agents := frontendSources(t, "modules/agents.js", "modules/agent-view.js")
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
		`if (hash.startsWith("preset-node-")) return "live-config";`,
		`state.data.liveAgent = hash.slice(12);`,
		`let hash = presetSelection ? "live-config"`,
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
		if !strings.Contains(agents, required) {
			t.Errorf("focused preset workspace is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`<details class="machine-workspace"`,
		`<summary class="machine-header"`,
		`<header class="node-page-intro"><div><p class="eyebrow">节点配置</p>`,
		`agent.id === state.data.selectedAgent ? "open" : ""`,
	} {
		if strings.Contains(agents, forbidden) {
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

func TestManualConfigRequiresExplicitImportOfNodeSnapshot(t *testing.T) {
	content := frontendSources(t, "modules/live-config-page.js", "modules/live-config-state.js",
		"modules/live-config-view.js", "modules/live-config-submit.js", "modules/live-config-reader.js")
	for _, required := range []string{
		`data-live-intent="import">手动导入并迁移`,
		`迁移任一步失败都会自动恢复原服务`,
		`action: "import-existing"`,
		`existing_config_unsupported_reason`,
		`检测到现有服务，但不可自动迁移`,
		`系统服务只读快照`,
		`data-live-source="managed"`,
		`data-live-source="import"`,
		`QAgent 托管配置`,
		`系统服务 · 只读快照`,
		`read-managed-config`,
		`prefer_cached: true`,
		`liveConfig({ preferCachedRead: false })`,
		`手动刷新及部署前核验会跳过缓存`,
		`最近 600 秒内已校验的节点快照`,
		`liveConfigSnapshotReusable`,
		`assertAgentConfigBaseline`,
		`正在核验 Agent 当前配置`,
		`beforeDeploy`,
		`submitLiveConfigChange`,
		`!unsupportedReason`,
		`esc(unsupportedReason)`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("manual configuration flow is missing %q", required)
		}
	}
	agents, err := os.ReadFile("modules/agent-view.js")
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
	app, err := os.ReadFile("modules/shell-context.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`(item) => privateAccount || liveConfigEngineEligible(agent.runtime?.[item], can("agent-config.write"))`,
		`class="live-engine-bar" aria-label="选择内核"`,
		`data-live-engine="${esc(item)}" aria-pressed="${active}"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("manual configuration engine top bar is missing %q", required)
		}
	}
	sidebar := strings.Split(strings.Split(string(app), `if (state.route === "live-config") {`)[1], `if (state.route === "archive-config")`)[0]
	if strings.Contains(sidebar, "data-live-engine") || !strings.Contains(sidebar, "data-live-agent") {
		t.Fatal("manual configuration sidebar must select nodes only")
	}
}
