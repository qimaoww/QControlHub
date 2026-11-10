package frontend

import (
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
		"analytics/live-config.js":   {`orderNodesBySavedOrder(list(await ctx.api('/agents')), savedNodeOrder())`},
		"analytics/configs.js":       {`readOrderedNodes(ctx.api)`},
		"analytics/nodes.js":         {`orderNodesBySavedOrder(agents, ordering.ids())`, `bindNodeOrdering`},
		"analytics/observability.js": {`orderNodesBySavedOrder(coreLogSourceAgents(sources), nodeOrder)`},
		"analytics/traffic.js":       {`orderNodesBySavedOrder(agents, ordering.ids())`},
		"analytics/client-access.js": {`orderNodesBySavedOrder(agents, ordering.ids())`},
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
	agents := frontendSources(t, "analytics/nodes.js", "analytics/node-batch.js")
	styles := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{
		`view = value.view === 'list' ? 'list' : 'cards';`,
		`const batchToggle = ctx.can('tasks.execute')`, `batchBox?.isConnected`,
		`openNodeBatch(ctx, (value = agents) => orderNodesBySavedOrder(value, ordering.ids()))`,
		`openedScopes.get(agent.id)?.get(options.engine)`, `if (!ctx.can('tasks.execute'))`,
		`if (scope(agent, options.engine) !==`, `row.selected = !selected`,
		`已选 ${selected} / 可选 ${eligible} 个节点`, `expected_instance_id: original[2]`,
	} {
		if !strings.Contains(agents, required) {
			t.Errorf("native node selection missing %q", required)
		}
	}
	for _, required := range []string{".a-probe-grid { display: grid;", ".a-probe-list"} {
		if !strings.Contains(styles, required) {
			t.Errorf("native node selection styles missing %q", required)
		}
	}
	for _, forbidden := range []string{"node-page-intro", "node-batch-panel", "selecting", "selected.has(", "drawBatch"} {
		if strings.Contains(agents, forbidden) {
			t.Errorf("native node settings retains a redundant panel: %q", forbidden)
		}
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
	content := string(mustReadFrontendFile(t, "analytics/nodes.js"))
	for file, markers := range map[string][]string{
		"analytics/node-batch.js": {
			`const options = readOptions(values)`,
			`if (!await ctx.confirm(`,
			`const fresh = list(await ctx.api('/agents'))`,
			`for (const id of targets)`,
			`const reason = eligibility(agents.find(agent => agent.id === id), options)`,
			`disabled: busy || Boolean(result && eligibility`,
			`if (busy || !row.result?.error) return`,
			`已提交 ${submitted}，提交失败 ${failed}`,
		},
		"analytics/nodes.js": {
			`if (!ctx.current() || !box.isConnected) return`,
			`if (!ctx.current() || !box.open) return`,
			`删除此添加记录？已接入的节点不会被卸载。`,
			`box.close(); command(created)`,
		},
		"analytics/app.js": {
			`if (pending || submit.disabled) return`,
			`controls.disabled = true`,
			`if (pending) { announce(`,
			`document.querySelectorAll("dialog.a-dialog").forEach(box => box.close())`,
			`routeAbort?.abort()`,
			`active?.isConnected`,
		},
		"modules/agent-batch.js": {
			`agent-self-upgrade-v1`,
			`旧版 Agent 缺少远程升级能力`,
		},
		"analytics/ui.js": {
			`box.showModal()`,
			`event.key !== 'Tab'`,
		},
	} {
		source := string(mustReadFrontendFile(t, file))
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing batch/enrollment safety contract %q", file, marker)
			}
		}
	}
	created := strings.Index(content, `const created = await ctx.api('/enrollment-tokens',`)
	if created < 0 {
		t.Fatal("enrollment flow must create an enrollment token")
	}
	shown := strings.Index(content[created:], `box.close(); command(created)`)
	if shown < 0 {
		t.Fatal("enrollment flow must show the generated command")
	}
	shown += created
	refreshed := strings.Index(content[created:], `await load()`)
	if refreshed >= 0 && created+refreshed < shown {
		t.Error("enrollment flow must display the command before refresh can lose it")
	}
}

func TestEnrollmentUsesARealDialogWithoutPersistentPanel(t *testing.T) {
	nodes := string(mustReadFrontendFile(t, "analytics/nodes.js"))
	dialogs := frontendSources(t, "analytics/app.js", "analytics/ui.js")
	css := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{
		`const box = ctx.dialog('添加节点', stack(form, history))`, `ctx.dialog('QAgent 安装命令'`,
		`enrollmentInstallCommand(created)`, `field('节点名称', 'name', ''`,
		`field('对管理员隐藏此节点', 'admin_hidden', false`, `添加记录 · ${tokens.length}`,
		`ctx.api('/enrollment-tokens')`, `/enrollment-tokens/${pathID(token.id)}/command`,
		`token.command_available`, `token.revoked_at`, `if (!ctx.current() || !box.isConnected) return`,
		`删除此添加记录？已接入的节点不会被卸载。`, `box.onRequestClose = async () =>`,
		`放弃未保存的节点添加信息？`,
	} {
		if !strings.Contains(nodes, required) {
			t.Errorf("native enrollment dialog missing %q", required)
		}
	}
	for _, required := range []string{`const box = node("dialog"`, `"aria-labelledby": heading.id`, `box.showModal()`, `event.key !== 'Tab'`, `active?.focus`, `box.addEventListener("cancel"`} {
		if !strings.Contains(dialogs, required) {
			t.Errorf("native modal accessibility missing %q", required)
		}
	}
	for _, required := range []string{".a-dialog::backdrop { position: fixed;", ".a-dialog-body", ".a-disclosure-body"} {
		if !strings.Contains(css, required) {
			t.Errorf("native enrollment styles missing %q", required)
		}
	}
	if strings.Contains(nodes, "enrollment-sheet") {
		t.Fatal("enrollment must not retain a persistent panel")
	}
}
