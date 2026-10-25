package frontend

import (
	"strings"
	"testing"
)

func TestTrafficUsesOneNodeFilterSurface(t *testing.T) {
	content := frontendSources(t, "modules/shell-context.js", "modules/routes.js")
	for _, required := range []string{
		"端口流量节点",
		`href="#traffic-all" data-context-traffic-agent="">全部节点`,
		`"traffic-all": "traffic"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("traffic route must keep all node selection in the context sidebar: missing %q", required)
		}
	}
	traffic := []byte(frontendFeatureSources(t, "modules/traffic.js"))
	if strings.Contains(string(traffic), `data-traffic-filter="agent_id"`) {
		t.Error("traffic workspace must not duplicate the context sidebar node selector")
	}
	if !strings.Contains(string(traffic), `state.anchor === "traffic-all"`) ||
		!strings.Contains(string(traffic), `currentFilters.agent_id = ""`) {
		t.Error("traffic all-node sidebar action must clear the selected node scope")
	}
	for _, required := range []string{`api("/traffic-endpoints"`, `mergeTrafficPorts(policies, endpoints)`, `未设置配额`, `data-traffic-configure`} {
		if !strings.Contains(string(traffic), required) {
			t.Errorf("traffic workspace must expose configured ports before a quota exists: missing %q", required)
		}
	}
	styles := string(mustReadFrontendFile(t, "app.css"))
	if !strings.Contains(styles, `.traffic-workspace>.traffic-policy-grid{grid-template-columns:repeat(auto-fill,minmax(360px,1fr))`) {
		t.Error("traffic cards must use the same responsive column sizing as node settings cards")
	}
	if strings.Contains(styles, `.traffic-workspace{width:100%;max-width:1240px`) {
		t.Error("traffic cards must not use a narrower workspace than node settings cards")
	}
}

// The traffic workspace keeps one complete read per account scope. Sidebar node
// scope changes, the toolbar filters and returning to the page render that
// snapshot instead of re-reading agents, policies and configured ports; only a
// live refresh, an expired window, a quota write or an account change reads the
// server again. Draft dialogs survive background polling because a background
// render waits for the last unsaved quota dialog to close.
func TestTrafficServesScopeChangesFromTheAccountSnapshot(t *testing.T) {
	installer := frontendSources(t, "modules/traffic.js")
	model := frontendSources(t, "modules/traffic-model.js")
	forms := frontendSources(t, "modules/traffic-forms.js")
	for _, required := range []string{
		"reusableTrafficSnapshot(snapshot, { data, session, canRead, epoch })",
		"const cached = background ? null : reusableSnapshot(epoch, data, session, canRead);",
		"renderTraffic(cached.agents, cached.policies, cached.endpoints, resetCreate);",
		"snapshot.data !== state.data || snapshot.session !== state.session",
		`isCurrent: () => state.route === "traffic" &&`,
		"traffic.invalidate = () => { snapshot = null; };",
		"deferDraftTraffic([agents, policies, endpoints])",
		"dialog[open]:not([data-refresh-live])",
	} {
		if !strings.Contains(installer, required) {
			t.Errorf("traffic snapshot reuse is missing %q", required)
		}
	}
	for _, required := range []string{
		"export const trafficSnapshotTtl = 30_000;",
		"export function reusableTrafficSnapshot(",
		"if (snapshot.data !== data || snapshot.session !== session) return null;",
		"if (epoch <= snapshot.epoch) return null;",
		"if (!(now - snapshot.at < trafficSnapshotTtl)) return null;",
	} {
		if !strings.Contains(model, required) {
			t.Errorf("traffic snapshot model is missing %q", required)
		}
	}
	if !strings.Contains(forms, "traffic.invalidate?.();") {
		t.Error("traffic writes must drop the cached snapshot before refreshing")
	}
}
