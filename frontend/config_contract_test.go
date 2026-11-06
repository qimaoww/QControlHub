package frontend

import (
	"os"
	"strings"
	"testing"
)

func TestServerPlanRegenerationStaysLocalAndUsesCurrentFormState(t *testing.T) {
	form := string(mustReadFrontendFile(t, "modules/server-plan-form.js"))
	content := form + "\n" + frontendSources(t,
		"modules/inbound-options-model.js", "modules/preset-editor.js", "modules/preset-view.js", "modules/preset-bindings.js")
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
	styles := string(mustReadFrontendFile(t, "analytics/theme.css"))
	native := frontendSources(t, "analytics/config-studio.js", "analytics/ui.js")
	// The native select replaces the retired two-row protocol catalogue. Every
	// eligible protocol must remain reachable without depending on old CSS.
	for _, marker := range []string{
		`field('协议','protocol'`, `options:protocols.map(protocol=>[protocol.key,protocol.name])`,
		`input = node("select"`, `options.map(item =>`,
	} {
		if !strings.Contains(native, marker) {
			t.Errorf("native protocol catalogue missing %q", marker)
		}
	}
	if !strings.Contains(styles, ".a-field select") || !strings.Contains(styles, ".a-form-grid") {
		t.Error("native protocol fields must use the responsive form controls")
	}
}

func TestLegacyPresetLinksAndRendererKeepScopedNodeSelection(t *testing.T) {
	app := frontendSources(t, "analytics/app.js", "analytics/live-config.js", "modules/routes.js", "modules/preset-route.js")
	for _, marker := range []string{
		`if (hash.startsWith("preset-node-")) return "live-config";`,
		`const selection = readPresetRoute(location.hash) || {};`,
		`location.hash.match(/^#preset-node-(.+)$/)?.[1]`,
		`'live-config': ['configs', 'liveConfigPage']`,
		`selection.agentId`, `const selectedEngine = selection.engine || "";`,
		`if (requested && !available.some(agent => agent.id === requested))`,
		`此节点不可用，或当前账号无权访问。`, `available.find(agent => agent.id === requested)`,
		`engines.includes(selectedEngine) ? selectedEngine`,
		`/agents/${pathID(target.id)}/configs/${pathID(engine)}`,
		`const targetField = field("节点", "agent", target.id`,
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("native scoped preset routing missing %q", marker)
		}
	}
	styles := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, marker := range []string{`.a-stack { display: grid; grid-template-columns: minmax(0, 1fr)`, `.a-form-grid`, `.a-field select`} {
		if !strings.Contains(styles, marker) {
			t.Errorf("native focused configuration layout missing %q", marker)
		}
	}
}

func TestManualConfigRequiresExplicitImportOfNodeSnapshot(t *testing.T) {
	content := frontendSources(t, "modules/live-config-page.js", "modules/live-config-state.js",
		"modules/live-config-view.js", "modules/live-config-submit.js", "modules/live-config-reader.js", "modules/config-snapshot.js")
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
		`手动刷新会重新读取`,
		`最近 600 秒内已校验的节点快照`,
		`liveConfigSnapshotReusable`,
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
