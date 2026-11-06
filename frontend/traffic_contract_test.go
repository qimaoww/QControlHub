package frontend

import (
	"strings"
	"testing"
)

func TestTrafficUsesOneNodeFilterSurface(t *testing.T) {
	traffic := frontendSources(t, "analytics/traffic.js")
	for _, required := range []string{
		`route.get('agent')`, `const agentFilter = field('节点', 'agent'`,
		`['', '全部节点']`, `filterbar(node('div'`, `search, agentFilter`,
		`ctx.state.data.analyticsTrafficFilters = currentFilters()`,
		`const defaults = { search: '', scope: 'current', agent: '', engine: '', status: '' }`,
		`ctx.api('/traffic-endpoints'`, `mergeTrafficPorts(policies, endpoints)`,
		`!item.policy`, `button('设置配额', () => edit(null, value))`,
		`value.is_current_user === true`, `value.owner_name || '其他用户'`, `muted('共享实例')`,
		`trafficPolicyEditable(value, agent, ctx.can)`,
		`String(value.port)`, "`${value.port}`",
	} {
		if !strings.Contains(traffic, required) {
			t.Errorf("native traffic scope/ownership/configured port flow missing %q", required)
		}
	}
	if strings.Count(traffic, `const agentFilter = field(`) != 1 {
		t.Error("traffic must have one page-level node filter")
	}
	if strings.Contains(traffic, "`:${value.port}`") {
		t.Error("standalone ports must not have a leading colon")
	}
	table := frontendSources(t, "analytics/traffic-table.js")
	for _, required := range []string{`a-probe-grid`, `a-traffic-list`, `button('卡片'`, `button('列表'`, `page * 12`, `patch(old, next)`, `patch(card, fresh)`} {
		if !strings.Contains(table, required) {
			t.Errorf("traffic views must retain pagination and stable updates: missing %q", required)
		}
	}
	styles := frontendSources(t, "analytics/theme.css")
	for _, required := range []string{`.a-probe-grid`, `.a-traffic-list`, `@media`} {
		if !strings.Contains(styles, required) {
			t.Errorf("native traffic responsive styles missing %q", required)
		}
	}
}

// A complete read is reused only on a later navigation within the same account
// and authorization scope. Polling and writes read fresh data. Quota drafts
// suspend polling; live details and sync dialogs retain their own state.
func TestTrafficServesScopeChangesFromTheAccountSnapshot(t *testing.T) {
	traffic := frontendSources(t, "analytics/traffic.js")
	model := frontendSources(t, "modules/traffic-model.js")
	app := frontendSources(t, "analytics/app.js")
	for _, required := range []string{
		`const data = ctx.state.data, session = ctx.session`,
		`initial && reusableTrafficSnapshot(data.analyticsTrafficSnapshot, { data, session, canRead: canReadNodes, epoch })`,
		`cached ? Promise.resolve([cached.policies, cached.endpoints, cached.agents])`,
		`if (!cached) data.analyticsTrafficSnapshot =`, `at: Date.now()`,
		`agents: structuredClone(agents), policies: structuredClone(policies), endpoints: structuredClone(endpoints)`,
		`if (!ctx.current() || expected !== readSequence) return false`,
		`await reload({ initial: true })`, `ctx.onRefresh(() => reload({ quiet: true }))`,
		`dialog.a-dialog[open]:not([data-traffic-details]):not([data-traffic-sync])`,
		`if ([401, 403, 404].includes(error.status))`, `agents.length = policies.length = endpoints.length = 0`,
		`刷新失败，保留上次数据`,
	} {
		if !strings.Contains(traffic, required) {
			t.Errorf("native traffic snapshot lifecycle missing %q", required)
		}
	}
	for _, required := range []string{
		`export const trafficSnapshotTtl = 30_000;`, `export function reusableTrafficSnapshot(`,
		`if (snapshot.data !== data || snapshot.session !== session) return null;`,
		`if (snapshot.canRead !== canRead) return null;`, `if (epoch <= snapshot.epoch) return null;`,
		`if (!(now - snapshot.at < trafficSnapshotTtl)) return null;`,
	} {
		if !strings.Contains(model, required) {
			t.Errorf("traffic snapshot model missing %q", required)
		}
	}
	for _, required := range []string{`if (writing) delete data.analyticsTrafficSnapshot`, `.finally(() => { if (writing) delete data.analyticsTrafficSnapshot; })`} {
		if !strings.Contains(app, required) {
			t.Errorf("writes must invalidate before and after completion, including uncertain failures: missing %q", required)
		}
	}
	browser := frontendSources(t, "browser/traffic-layout.mjs")
	for _, required := range []string{`returning within the account snapshot window repeated traffic reads`, `expired traffic snapshot did not reload policies and configured ports`, `polling reset selection`, `late mutation`} {
		if !strings.Contains(browser, required) {
			t.Errorf("native traffic browser coverage missing %q", required)
		}
	}
}
