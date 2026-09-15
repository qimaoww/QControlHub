package frontend

import (
	"os"
	"strings"
	"testing"
)

func TestCoreLogsLabelFollowsAdvertisedFeature(t *testing.T) {
	app, err := os.ReadFile("modules/shell-context.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(app)
	for _, required := range []string{
		`(agent.features || []).includes("core-logs-v1")`,
		`"集中日志已启用"`,
		`"需升级 Agent"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-logs sidebar is missing %q", required)
		}
	}
	if strings.Contains(content, `agent.features.includes("core-logs-v1") ? "需升级 Agent"`) {
		t.Error("core-logs sidebar must enable streaming only when core-logs-v1 is advertised")
	}
}

func TestCoreLogsUseImmediateFiltersAndSidebarNodeScope(t *testing.T) {
	module := []byte(frontendFeatureSources(t, "modules/core-logs.js"))
	content := string(module)
	for _, required := range []string{
		`data-core-log-engine`,
		`data-core-log-level`,
		`renderLocalFilters({ q: event.currentTarget.value })`,
		`data-toggle-core-log-refresh`,
		`core-log-columns`,
		`每内核上限<select name="limit">`,
		`每种内核分别取最新日志，不共用总条数上限`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-log immediate filtering is missing %q", required)
		}
	}
	for _, duplicatedControl := range []string{
		`name="agent_id"`,
		`type="submit"`,
	} {
		if strings.Contains(content, duplicatedControl) {
			t.Errorf("core-log workspace still contains duplicated or deferred control %q", duplicatedControl)
		}
	}
}

func TestCoreLogPageSelectionPersistsInBrowserStorage(t *testing.T) {
	module := []byte(frontendFeatureSources(t, "modules/core-logs.js"))
	content := string(module)
	for _, required := range []string{
		`savedCoreLogPreferences`,
		`saveCoreLogPreferences`,
		`restoreSelection()`,
		`rememberSelection()`,
		`coreLogFilterLimits.map((limit) =>`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-log selection persistence is missing %q", required)
		}
	}
	if strings.Count(content, "rememberSelection();") != 5 {
		t.Errorf("log controls and revoked-node fallback must persist selection; got %d call sites", strings.Count(content, "rememberSelection();"))
	}
	store, err := os.ReadFile("modules/core-log-preferences.js")
	if err != nil {
		t.Fatal(err)
	}
	storeContent := string(store)
	if !strings.Contains(storeContent, `export const coreLogPreferenceKey = "qcontrolhub:core-log-preferences"`) {
		t.Error("core-log selection must keep one stable browser-storage key")
	}
	for _, required := range []string{
		`export const coreLogFilterLimits = [100, 200, 500, 1000, 2000];`,
		`autoRefresh: record.auto_refresh !== false`,
		`filters.q = source.q.slice(0, keywordLength);`,
	} {
		if !strings.Contains(storeContent, required) {
			t.Errorf("core-log preference store is missing %q", required)
		}
	}
}

func TestCoreLogsUseReadableAlignedDesktopGrid(t *testing.T) {
	stylesheet, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	content := string(stylesheet)
	if !strings.Contains(content, `:root{--ui-font-scale:1}`) {
		t.Fatal("frontend typography must expose the shared scale fallback")
	}
	for _, required := range []string{
		`--core-log-grid:168px 130px 72px 150px minmax(360px,1fr)`,
		`grid-template-columns:var(--core-log-grid);align-items:center`,
		`.core-log-row time,.core-log-row span{min-width:0;overflow:hidden;font-size:calc(11px * var(--ui-font-scale))`,
		`.core-log-row pre{min-width:0;margin:0;color:var(--ink-2);font:calc(11px * var(--ui-font-scale))/1.6`,
		`.core-log-filter-group,.core-log-filters label{display:grid;gap:7px;min-width:0;color:var(--muted);font-size:calc(11px * var(--ui-font-scale))`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-log readable alignment contract is missing %q", required)
		}
	}
	for _, obsolete := range []string{
		`.core-log-row time{padding-top:3px`,
		`.core-log-level{padding-top:3px`,
		`.core-log-agent{padding-top:3px`,
	} {
		if strings.Contains(content, obsolete) {
			t.Errorf("core-log columns still use manual baseline offset %q", obsolete)
		}
	}
}

func TestCoreLogsResizeWithinTheirWorkspace(t *testing.T) {
	stylesheet, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	content := string(stylesheet)
	for _, required := range []string{
		`@media(pointer:fine){.app-body.page-core-logs .desktop-app{min-width:0}}`,
		`.core-log-workspace{min-width:0;container-type:inline-size}`,
		`.core-log-filters{display:flex;align-items:flex-end;flex-wrap:wrap}`,
		`.core-log-stream{--core-log-grid:minmax(130px,.85fr)`,
		`width:100%;max-width:100%;min-width:0`,
		`@container(max-width:840px){.core-log-columns{display:none}`,
		`.core-log-row{grid-template-columns:minmax(140px,1fr) auto auto`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("core-log responsive workspace is missing %q", required)
		}
	}
}
