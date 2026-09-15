package frontend

import (
	"os"
	"strings"
	"testing"
)

func TestClientAccessUsesContextSidebarAsOnlyNodeFilter(t *testing.T) {
	app, err := os.ReadFile("modules/shell-context.js")
	if err != nil {
		t.Fatal(err)
	}
	clientAccess := []byte(frontendFeatureSources(t, "modules/client-access.js"))
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
		`href="#substore-sync"`,
		`client-access-node-card`,
		`data-client-display-open`,
		`class="traffic-edit-dialog client-display-dialog"`,
		`修改显示参数`,
		`客户端地址协议栈`,
		`当前使用手动连接地址`,
		`button small client-display-settings-open`,
		`button small client-parameter-open`,
		`data-client-parameter-open`,
		`class="traffic-edit-dialog client-parameter-dialog"`,
		`dialog?.showModal();`,
		`new ResizeObserver(layout)`,
		`card.style.gridRowEnd = `,
		`groupClientAccessEntries(filtered, agents)`,
		`import { orderNodesBySavedOrder } from "./node-order.js";`,
		`normalizeClientAccessFilters(entries, agents`,
		`renderClientAccess();`,
	} {
		if !strings.Contains(string(clientAccess), required) {
			t.Errorf("client access filtering is missing %q", required)
		}
	}
	for _, forbidden := range []string{`data-filter-agent`, `aria-label="按节点筛选"`, `filterAgentIDs`, `client-access-hero`, `client-access-summary`, `client-access-filter-panel`, `client-access-results-head`, `client-parameter-menu`, `client-parameter-grid`, `含凭据`, `client-address-editor`, `data-client-address-mode`, `修改地址`} {
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

func TestSubStoreSyncUsesCompactPanelPatterns(t *testing.T) {
	app := frontendSources(t, "app.js", "modules/shell-context.js")
	module := frontendFeatureSources(t, "modules/substore-sync.js")
	styles := string(mustReadFrontendFile(t, "app.css"))
	for _, required := range []string{
		`installSubStoreSync`, `async "substore-sync"()`, `href="#client-access"`,
		`import { orderNodesBySavedOrder } from "./node-order.js";`,
		`groupSubStoreProfiles(filtered, agents)`,
		`data-substore-settings-dialog`, `dialog?.showModal()`, `data-substore-run`,
		`data-substore-select`, `data-substore-parameters-form`, `data-substore-remove`,
		`substore-node-settings-row`, `name="sync_mode" value="incremental"`,
		`name="sync_mode" value="managed"`, `增量模式`, `完全托管模式`,
		`api("/substore-sync/selections"`, `api("/substore-sync/run"`,
		`data-substore-remote-import-button`, `/substore-sync/targets/${encodeURIComponent(targetID)}/remote`,
		`data-substore-remote-select`, `data-substore-remote-help`, `display_name: displayName`,
	} {
		if !strings.Contains(app+module, required) {
			t.Errorf("Sub-Store sync page is missing %q", required)
		}
	}
	for _, required := range []string{
		`.substore-status-bar`, `.substore-filter-bar`, `.substore-agent-grid`,
		`.substore-agent-card`, `.substore-settings-dialog`, `var(--radius-card)`,
		`.substore-node-settings-row`, `.substore-mode-options`,
		`.app-body.page-substore-sync .desktop-app{min-width:0}`,
		`minmax(min(100%,460px),1fr)`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Sub-Store sync page is missing theme style %q", required)
		}
	}
	for _, forbidden := range []string{`substore-hero`, `Sub-Store 使用说明`, `同步工作台`} {
		if strings.Contains(module, forbidden) {
			t.Errorf("Sub-Store sync page contains redundant presentation %q", forbidden)
		}
	}
}
