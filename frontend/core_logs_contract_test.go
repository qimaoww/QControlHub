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
	view := frontendSources(t, "modules/core-log-view.js")
	sidebar := frontendSources(t, "modules/shell-context.js")
	if strings.Contains(view, `select name="agent_id"`) {
		t.Error("core-log filters must not duplicate the node selector in the sidebar")
	}
	if !strings.Contains(view, `select name="instance_id"`) || !strings.Contains(sidebar, `data-core-log-agent`) {
		t.Error("core-log instance filter and sidebar node selection must both remain available")
	}
	for _, required := range []string{
		`data-core-log-engine`,
		`data-core-log-level`,
		`renderLocalFilters({ q: search.value })`,
		`compositionend`,
		`!event.isComposing`,
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
	if strings.Count(content, "rememberSelection();") < 5 {
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
	if !strings.Contains(content, `--ui-font-scale:1;`) {
		t.Fatal("frontend typography must expose the shared scale fallback")
	}
	for _, required := range []string{
		`--core-log-grid:168px 130px 72px 150px minmax(360px,1fr)`,
		`grid-template-columns:var(--core-log-grid);align-items:center`,
		`.core-log-row time,.core-log-row span{min-width:0;overflow:hidden;font-size:var(--text-small)`,
		`.core-log-row pre{min-width:0;margin:0;color:var(--ink-2);font:var(--text-small)/var(--leading-body)`,
		`.core-log-filter-group,.core-log-filters label{display:grid;gap:7px;min-width:0;color:var(--muted);font-size:var(--text-small)`,
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
		`.core-log-filter-fields{display:grid;grid-template-columns:repeat(12,minmax(0,1fr))`,
		`.core-log-filter-tools{display:grid;grid-template-columns:minmax(0,24em) max(104px,7.5em) max-content`,
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

func TestCoreLogSourceStatesNameTheBrokenSource(t *testing.T) {
	module := frontendSources(t, "modules/core-log-source-status.js", "modules/core-log-view.js")
	for _, required := range []string{
		`coreLogSourceState`,
		`coreLogSourceStatus(relevant`,
		`coreLogGuardStatus`,
		`"not-configured"`,
		`"未配置"`,
		`部署后自动采集`,
		`采集已就绪，尚未收到日志`,
		`采集正常，最近日志 `,
		`部分来源采集失败`,
		`读取失败`,
		`coreLogInstanceName`,
		`last_log_at`,
		`core_log_last_at`,
		`core_log_source`,
		`coreLogLastSeen`,
	} {
		if !strings.Contains(module, required) {
			t.Errorf("core-log source states are missing %q", required)
		}
	}
	// A healthy engine must never inherit a sibling's failure: the old
	// aggregate read every runtime state on the node instead of the filter.
	for _, obsolete := range []string{
		`sourceStates.includes("failed")`,
		`暂无匹配记录`,
		`采集正常，等待新的日志`,
		`日志采集等待中`,
	} {
		if strings.Contains(module, obsolete) {
			t.Errorf("core-log source states still aggregate across engines with %q", obsolete)
		}
	}
}

func TestCoreLogStatusAggregatesOnlyTheActiveFilterScope(t *testing.T) {
	view := frontendSources(t, "modules/core-log-view.js")
	for _, required := range []string{
		`const relevant = selectedSources.filter(source => !filters.engine || source.engine === filters.engine)`,
		`coreLogSourceStatus(relevant, {`,
		`filtered: Boolean(filters.q || filters.level)`,
		`scopeStatus.noticeTitle`,
		`scopeStatus.meta`,
		`const instanceName = coreLogInstanceName`,
	} {
		if !strings.Contains(view, required) {
			t.Errorf("core-log status is not scoped to the active filters: missing %q", required)
		}
	}
	statuses := frontendSources(t, "modules/core-log-source-status.js")
	// A failed source names its node, engine and instance instead of a generic
	// user-facing code that cannot be located on the node.
	for _, required := range []string{
		`source?.name`,
		`engineName(source?.engine || "")`,
		`coreLogInstanceName(source)`,
		`source.username || "共享实例"`,
		`source.instance_id`,
	} {
		if !strings.Contains(statuses, required) {
			t.Errorf("core-log failure location is missing %q", required)
		}
	}
	for _, state := range []string{
		`case "active":`,
		`case "waiting":`,
		`case "not-configured":`,
		`case "failed":`,
		`case "offline":`,
		`case "unsupported":`,
		`case "status-unavailable":`,
		`case "not-installed":`,
	} {
		if !strings.Contains(statuses, state) {
			t.Errorf("core-log source state dispatch is missing %q", state)
		}
	}
}

func TestCoreLogStatusShowsReportedTransportAndLastDelivery(t *testing.T) {
	statuses := frontendSources(t, "modules/core-log-source-status.js")
	for _, required := range []string{
		`"journal-host": "journal（主机）"`,
		`"systemd-fallback": "systemd（回退）"`,
		`coreLogTransportName`,
		`reported.length === states.length && transports.length === 1`,
		`最近日志 `,
		`coreLogLastSeen(newestAt, now)`,
	} {
		if !strings.Contains(statuses, required) {
			t.Errorf("core-log source metadata is missing %q", required)
		}
	}
	view := frontendSources(t, "modules/core-log-view.js")
	if !strings.Contains(view, `scopeStatus.meta`) {
		t.Error("the log status line must render the reported transport and last delivery")
	}
}
