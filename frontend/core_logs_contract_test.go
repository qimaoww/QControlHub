package frontend

import (
	"strings"
	"testing"
)

func TestCoreLogsLabelFollowsAdvertisedFeature(t *testing.T) {
	statuses := frontendSources(t, "modules/core-log-source-status.js")
	// The authenticated source directory now owns feature availability; a node
	// sidebar must not infer a healthy shared collector from another instance.
	for _, required := range []string{`case "unsupported":`, `Agent 功能不足`, `请升级 Agent`, `case "status-unavailable":`, `此 Agent 无法核验来源状态`} {
		if !strings.Contains(statuses, required) {
			t.Errorf("source feature availability missing %q", required)
		}
	}
	view := frontendSources(t, "analytics/observability.js")
	for _, required := range []string{`ctx.api("/core-log-sources"`, `coreLogSourceState(source)`, `state.title`} {
		if !strings.Contains(view, required) {
			t.Errorf("native source feature presentation missing %q", required)
		}
	}
	if strings.Contains(view, `source.status === "active" ? "采集正常"`) {
		t.Error("undelivered sources cannot be called healthy")
	}
}

func TestCoreLogsUseImmediateFiltersAndSidebarNodeScope(t *testing.T) {
	view := frontendSources(t, "analytics/observability.js")
	for _, required := range []string{
		`field("节点", "agent_id"`, `field("使用用户 / 实例", "instance_id"`,
		`field("内核", "engine"`, `field("级别", "level"`, `field("关键词", "q"`,
		`field("每内核上限", "limit"`, `coreLogFilterLimits.map`,
		`controls.addEventListener("change", changed)`, `controls.elements.q.addEventListener("input"`,
		`compositionend`, `!event.isComposing`, `requestAnimationFrame(changed)`,
		`button("清除筛选"`, `button(automatic ? "暂停更新" : "自动更新"`,
		`filterCoreLogEntries(entries, filters)`, `page * 200`,
		`record.agent_id`, `source ? instanceLabel(source)`, `engineName(record.engine)`,
		`date(record.logged_at)`, `redactDiagnostic(record.message)`,
	} {
		if !strings.Contains(view, required) {
			t.Errorf("native immediate/scoped log filtering missing %q", required)
		}
	}
	if strings.Count(view, `field("节点", "agent_id"`) != 1 {
		t.Error("logs must expose one node selector")
	}
	if strings.Contains(view, `node("button", { type: "submit"`) {
		t.Error("immediate log filters must not add a deferred apply button")
	}
}

func TestCoreLogPageSelectionPersistsInBrowserStorage(t *testing.T) {
	content := frontendSources(t, "analytics/observability.js", "modules/core-log-selection.js")
	for _, required := range []string{
		`createCoreLogSelection({ state: ctx.state`, `selection.restoreSelection()`,
		`...data.coreLogFilters`, `data.coreLogAutoRefresh !== false`,
		`selection.rememberSelection()`, `data.coreLogFilters = { ...filters }`,
		`if (state.data.coreLogFilters) return`, `savedCoreLogPreferences`, `saveCoreLogPreferences`,
		`filters = normalizeCoreLogScope(filters, sources); remember()`,
		`automatic = !automatic; remember()`, `page = 0; anchor = null; remember()`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native log selection lifecycle missing %q", required)
		}
	}
	store := frontendSources(t, "modules/core-log-preferences.js")
	for _, required := range []string{
		`export const coreLogPreferenceKey = "qcontrolhub:core-log-preferences"`,
		`export const coreLogFilterLimits = [100, 200, 500, 1000, 2000];`,
		`autoRefresh: record.auto_refresh !== false`, `filters.q = source.q.slice(0, keywordLength);`,
	} {
		if !strings.Contains(store, required) {
			t.Errorf("validated log preference schema missing %q", required)
		}
	}
}

func TestCoreLogsUseReadableAlignedDesktopGrid(t *testing.T) {
	stylesheet := frontendSources(t, "analytics/theme-source.css")
	for _, required := range []string{
		`.a-code, .a-editor textarea`, `font-size: var(--text-label)`, `line-height: 1.75`,
		`.a-code { max-height: 55dvh; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere`,
		`.a-log-message { white-space: pre-wrap`, `.a-filter-form > .a-form-fields > .a-form-grid { display: contents; }`,
	} {
		if !strings.Contains(stylesheet, required) {
			t.Errorf("native log typography/alignment missing %q", required)
		}
	}
	view := frontendSources(t, "analytics/observability.js", "analytics/observability-table.js")
	for _, required := range []string{`observationStream("内核日志")`, `data-log-entry`, `a-card-body a-stack`, `a-code a-log-message`, `node("time"`, `a-muted`} {
		if !strings.Contains(view, required) {
			t.Errorf("mixed log stream attribution missing %q", required)
		}
	}
	if strings.Contains(view, `style=`) {
		t.Error("log alignment must not use inline offsets")
	}
}

func TestCoreLogsResizeWithinTheirWorkspace(t *testing.T) {
	styles := frontendSources(t, "analytics/theme-source.css")
	for _, required := range []string{
		`.a-filter-form { display: flex; align-items: flex-end; flex-wrap: wrap`,
		`.a-filter-form .a-field, .a-filter-fields > .a-field`, `min-width: 0`,
		`.a-filter-form .a-field:has([name=q])`, `.a-filter-form .a-field:has([name=limit])`,
		`grid-template-columns: minmax(0,1fr)`, `overflow-wrap: anywhere`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("native responsive log workspace missing %q", required)
		}
	}
	browser := frontendSources(t, "browser/core-log-filter-layout.mjs", "browser/core-logs.mjs")
	for _, required := range []string{`scrollWidth`, `innerWidth`, `getBoundingClientRect()`, `longLogs`, `text selection`, `pagination`} {
		if !strings.Contains(browser, required) {
			t.Errorf("actual responsive log browser coverage missing %q", required)
		}
	}
}

func TestCoreLogSourceStatesNameTheBrokenSource(t *testing.T) {
	content := frontendSources(t, "modules/core-log-source-status.js", "analytics/observability.js", "analytics/source-status.js")
	for _, required := range []string{
		`coreLogSourceState`, `coreLogSourceStatus(relevant`, `"not-configured"`, `"未配置"`,
		`部署后自动采集`, `采集已就绪，尚未收到日志`, `采集正常，最近日志 `,
		`部分来源采集失败`, `读取失败`, `coreLogInstanceName`, `last_log_at`,
		`core_log_last_at`, `core_log_source`, `coreLogLastSeen`,
		`state.lastAt ? date(state.lastAt)`, `state.key === "active"`,
		`来源采集状态`, `ctx.dialog("来源采集状态"`, `row.detail && button("详情"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("scoped native source status missing %q", required)
		}
	}
	for _, obsolete := range []string{`sourceStates.includes("failed")`, `采集正常，等待新的日志`, `日志采集等待中`} {
		if strings.Contains(content, obsolete) {
			t.Errorf("obsolete aggregate source state %q", obsolete)
		}
	}
}

func TestCoreLogStatusAggregatesOnlyTheActiveFilterScope(t *testing.T) {
	view := frontendSources(t, "analytics/observability.js")
	for _, required := range []string{
		`source.agent_id === filters.agent_id`, `source.instance_id === filters.instance_id`, `source.engine === filters.engine`,
		`coreLogSourceStatus(relevant, { engineName, filtered: Boolean(filters.q || filters.level) })`,
		`collectionState.emptyTitle`, `collection.update(relevant.map`,
	} {
		if !strings.Contains(view, required) {
			t.Errorf("source status must follow all active filters: missing %q", required)
		}
	}
	statuses := frontendSources(t, "modules/core-log-source-status.js")
	for _, required := range []string{
		`source?.name`, `engineName(source?.engine || "")`, `coreLogInstanceName(source)`,
		`source.username || "共享实例"`, `source.instance_id`,
		`case "active":`, `case "waiting":`, `case "not-configured":`, `case "failed":`,
		`case "offline":`, `case "unsupported":`, `case "status-unavailable":`, `case "not-installed":`,
	} {
		if !strings.Contains(statuses, required) {
			t.Errorf("source status/location dispatch missing %q", required)
		}
	}
}

func TestCoreLogStatusShowsReportedTransportAndLastDelivery(t *testing.T) {
	statuses := frontendSources(t, "modules/core-log-source-status.js")
	for _, required := range []string{
		`"journal-host": "journal（主机）"`, `"systemd-fallback": "systemd（回退）"`,
		`coreLogTransportName`, `reported.length === states.length && transports.length === 1`,
		`最近日志 `, `coreLogLastSeen(newestAt, now)`,
	} {
		if !strings.Contains(statuses, required) {
			t.Errorf("source transport/delivery model missing %q", required)
		}
	}
	view := frontendSources(t, "analytics/observability.js", "analytics/source-status.js")
	for _, required := range []string{`transport: state.transport`, `lastSeen: state.lastAt ? date(state.lastAt)`, `row.transport && muted(row.transport)`, `row.lastSeen || lastSeenEmpty`} {
		if !strings.Contains(view, required) {
			t.Errorf("native collection dialog must show actual source metadata: missing %q", required)
		}
	}
}
