package frontend

import (
	"strings"
	"testing"
)

func TestClientAccessUsesOneScopedNodeFilter(t *testing.T) {
	content := frontendSources(t, "analytics/client-access.js", "modules/client-access-model.js")
	for _, required := range []string{
		`const agentFilter = field('节点', 'agent', ''`, `const engineFilter = field('内核', 'engine', ''`,
		`const search = field('搜索接入配置'`, `normalizeClientAccessFilters`, `groupClientAccessEntries(entries, agents)`,
		`orderNodesBySavedOrder(list(nodes), ordering.ids())`, `filterClientAccessEntries`,
		`agentFilter.querySelector('select').value`, `engineFilter.querySelector('select').value`,
		`loadClientDisplayFormat()`, `saveClientDisplayFormat`,
		`button('参数详情', () => details(entry, item))`, `part.secret ? secret(part.label, part.value)`,
		`type: 'password', readonly: true, autocomplete: 'off'`, `if (multiline) input.value = '••••••••'`,
		`disabled: Boolean(exported.error || !exported.value)`, `secret('完整 Xray JSON', value.xray_json, true)`,
		`button('修改显示参数', () => display(entry, item))`, `ctx.dialog('修改显示参数'`,
		`客户端地址协议栈`, `disabled: Boolean(item.address_overridden)`, `恢复自动识别`,
		`const selector = { engine: entry.engine, tag: item.tag, port: Number(item.port) }`,
		`/agents/${pathID(entry.agent_id)}/client-address`, `address: '', profile: selector`,
		`JSON.stringify([entry.agent_id, entry.engine, entry.instance_id || ''])`,
		`if (!ctx.current() || sequence !== readSequence) return`, `patch(result, node('div', { className: 'a-stack' }, content))`,
		`agent && ctx.can('agents.manage', agent)`, `ctx.signal.addEventListener('abort'`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native client access contract missing %q", required)
		}
	}
	navigation := string(mustReadFrontendFile(t, "analytics/app.js"))
	if !strings.Contains(navigation, `["substore-sync", "订阅同步"`) {
		t.Error("client users must retain the subscription sync navigation entry")
	}
	if strings.Count(content, `field('节点', 'agent'`) != 1 {
		t.Error("client access needs one node filter")
	}
	css := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{`.a-stack { display: grid; grid-template-columns: minmax(0, 1fr)`, `.a-dialog.wide`, `.a-table-scroll`, `overflow-wrap: anywhere`, `.a-field`, `min-height: 44px`} {
		if !strings.Contains(css, required) {
			t.Errorf("native client access layout missing %q", required)
		}
	}
}

func TestSubStoreSyncUsesCompactPanelPatterns(t *testing.T) {
	content := frontendSources(t, "analytics/app.js", "analytics/substore.js", "modules/substore-model.js")
	for _, required := range []string{
		`'substore-sync': ['substore-sync', 'substorePage']`, `groupSubStoreProfiles`, `savedNodeOrder()`,
		`const writable = ctx.can("settings.manage")`, `const target = list(resource.targets).find`,
		`const request = ++serial`, `if (!ctx.current() || serial !== request) return`,
		`if (dirty)`, `selectionFields.disabled = true`, `selectionFields.disabled = false`,
		`subStoreSelectionPayload(profiles)`, `body: { target_id: target.id, selections }`,
		`ctx.api("/substore-sync/selections"`, `ctx.api("/substore-sync/run"`,
		`body: { target_id: target.id }`, `ctx.dialog("Sub-Store 连接设置"`, `ctx.api("/substore-sync/settings"`,
		`ctx.api("/substore-sync/test"`, `ctx.api("/substore-sync/remote-targets")`,
		`/substore-sync/targets/${pathID(target.id)}/remote`, `"/substore-sync/targets/import"`,
		`field("同步模式", "sync_mode"`, `"incremental"`, `"managed"`, `增量更新`, `完全托管`,
		`field("同步组名称", "display_name"`, `field("同步格式", "sync_format"`, `rename_remote`,
		`field("搜索节点", "search"`, `field("节点", "agent"`, `subStoreAddressChoices(profile)`,
		`writable && target && !blocked && profile.selected`, `filterSubStoreProfiles`,
		`移除面板中的同步关系？Sub-Store 远端组会保留。`, `label: "同步节点"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native Sub-Store contract missing %q", required)
		}
	}
	css := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{`.a-card-body`, `.a-table-scroll`, `.a-dialog`, `.a-field`, `.a-probe-list`, `min-width: 0`, `min-height: 44px`} {
		if !strings.Contains(css, required) {
			t.Errorf("native Sub-Store styles missing %q", required)
		}
	}
	for _, forbidden := range []string{"substore-hero", "Sub-Store 使用说明", "同步工作台"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("redundant Sub-Store presentation: %q", forbidden)
		}
	}
}
