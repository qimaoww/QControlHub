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
	agents := frontendSources(t, "analytics/nodes.js", "analytics/node-batch.js")
	styles := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{
		`let view = preference.view === 'list' ? 'list' : 'cards', selecting = false;`,
		`const batchToggle = ctx.can('tasks.execute')`, `batchBar.hidden = !selecting`,
		`batchToggle.hidden = selecting`, `all.querySelector('input').indeterminate`,
		`selected.clear(); draw()`, `className:'a-probe-choice'`, `selecting?['选择']:[]`,
		`disabled: !selectable(agent)`, `agent.can_manage !== false || ctx.can('instance.manage', agent)`,
		`openedScopes.get(agent.id)?.get(options.engine)`, `if (!ctx.can('tasks.execute'))`,
		`if (scope(agent, options.engine) !==`, `row.retry.disabled = busy`,
	} {
		if !strings.Contains(agents, required) {
			t.Errorf("native node selection missing %q", required)
		}
	}
	for _, required := range []string{".a-probe-grid { display: grid;", ".a-probe-batch[hidden] { display: none; }", ".a-probe-choice", ".a-probe-list"} {
		if !strings.Contains(styles, required) {
			t.Errorf("native node selection styles missing %q", required)
		}
	}
	for _, forbidden := range []string{"node-page-intro", "node-batch-panel"} {
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
	content := string(mustReadFrontendFile(t, "modules/agent-enrollment.js"))
	for file, markers := range map[string][]string{
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
