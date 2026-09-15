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
