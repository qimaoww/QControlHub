package frontend

import (
	"strings"
	"testing"
)

func TestServerPlanRegenerationStaysLocalAndUsesCurrentFormState(t *testing.T) {
	form := string(mustReadFrontendFile(t, "analytics/config-studio.js"))
	content := form + "\n" + frontendSources(t,
		"modules/inbound-options-model.js", "analytics/inbound-fields.js", "analytics/inbound-options.js")
	start := strings.Index(form, "for (const [name, fields, label] of generatedFieldActions)")
	if start < 0 {
		t.Fatal("native editor is missing isolated field generation")
	}
	end := start + strings.Index(form[start:], "bindAnalyticsCertificateSelector(ctx")
	if end <= start {
		t.Fatal("native editor is missing isolated field generation")
	}
	handler := form[start:end]
	for _, required := range []string{
		`form.elements.namedItem(name)`, `ctx.mutate(event.currentTarget`,
		`body: { protocol: selected.key, input: read() }`,
		`!ctx.current() || !box.isConnected || selected !== protocol`,
		`requestedRevision !== revision || draft !== JSON.stringify(read())`,
		`for (const key of requested)`, `Object.hasOwn(generated, key)`,
		`参数已修改，未应用过期生成结果`, `ctx.announce(`,
	} {
		if !strings.Contains(handler, required) {
			t.Errorf("native field generation is missing %q", required)
		}
	}
	for _, forbidden := range []string{"agentConfig(", "location.", "shell(", "replaceChildren(", "render(generated)", "await committed("} {
		if strings.Contains(handler, forbidden) {
			t.Errorf("field generation must stay local and must not use %q", forbidden)
		}
	}
	for _, required := range []string{
		`readServerPlanInput(form, protocol, values)`, `Object.entries(baseline || {})`,
		`["port", "port", "生成监听端口"]`,
		`["credential", "credential", "生成凭据"]`,
		`["secondary_credential", "secondary_credential", "生成次凭据"]`,
		`"reality_private_key,reality_public_key"`,
		`["reality_short_id", "reality_short_id", "生成 Short ID"]`,
		`"vless_decryption,vless_encryption"`, `protocol.uses_vless_encryption`,
		`control('vless_decryption')`, `control('vless_encryption')`,
		`protocol.port_forward`, `'target_address','target_port','network'`,
		`snell_shadow_tls_password`, `sudoku_client_key`, `sudoku_httpmask_mode`, `sudoku_multiplex`,
		`'snell_'`, `'sudoku_'`, `protocolOptions(form, protocol, plan)`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native server-plan field generation is missing %q", required)
		}
	}
	for _, forbidden := range []string{"skip-cert-verify", "sudoku_key_mode", "sudoku_smux_enabled", "sudoku_allow_insecure_aead"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("safe native protocol fields must not expose %q", forbidden)
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
	content := frontendSources(t, "analytics/live-config.js", "modules/live-config-state.js", "modules/config-snapshot.js")
	for _, required := range []string{
		`importSource ? "导入并迁移" : "保存并部署"`,
		`action: "import-existing"`, `existing_config_unsupported_reason`,
		`["managed", "QAgent 托管配置"]`, `["import", "可导入的系统服务配置"]`,
		`readonly: importSource || !editable`, `系统配置快照`,
		`read-managed-config`, `prefer_cached: true`, `snapshot = async (preferCached = false)`,
		`const result = await snapshot()`, `requestConfigSnapshot`, `liveConfigSnapshotReusable`,
		`beforeDeploy`, `submitLiveConfigChange`, `if (!source?.content)`,
		`source, existingAvailable: importSource`, `beforeDeploy,`,
		`const errorText = runtime.existing_config_unsupported_reason`, `errorText && notice(errorText)`,
		`canDeploy = !configDeployBlockReason(agent, engine, ctx.can)`,
		`field("内核", "engine", engine, { options: engines })`,
		`const engines = target.capabilities`, `if (values.agent !== agent.id)`,
		`delete values.engine; delete values.source`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native manual configuration flow is missing %q", required)
		}
	}
	// External source is explicitly read-only and requires a migration task;
	// normal save/deploy must never silently adopt a system service.
	for _, required := range []string{
		`if (values.intent === "import")`, `请先读取可导入的系统服务配置`,
		`导入此配置并迁移现有系统服务`, `canReadHost`, `ctx.can("host.manage", agent)`,
		`runtime.existing_config_available && canReadHost`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native import permission/confirmation missing %q", required)
		}
	}
	nodes := string(mustReadFrontendFile(t, "analytics/nodes.js"))
	for _, required := range []string{
		`runtime.existing_config_unsupported_reason && notice(runtime.existing_config_unsupported_reason)`,
		`runtime.existing_config_available && ctx.can('host.manage', agent)`,
		`link('导入系统配置',`, `&source=import`,
	} {
		if !strings.Contains(nodes, required) {
			t.Errorf("native node service controls omit migration state %q", required)
		}
	}
}
