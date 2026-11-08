package frontend

import (
	"strings"
	"testing"
)

// These contracts follow the shipped runtime. The classic shell's DOM and
// request orchestration must not stand in for native refresh behavior.
func TestRefreshPathsUseStableViewsAndScopedCoordinators(t *testing.T) {
	contracts := map[string][]string{
		"analytics/app.js": {
			"const bound = state.session;",
			"isCurrent: () => state.session === bound && !lifetime.signal.aborted",
			"routeAbort?.abort();",
			"const signal = routeAbort.signal, epoch = ++routeEpoch;",
			"signal: options.signal || signal",
			"current: () => !signal.aborted && epoch === routeEpoch && Boolean(state.session)",
			"const epoch = routeEpoch, read = pageRefresh;",
			"if (epoch === routeEpoch && error.name !== 'AbortError')",
			"if (pending || refreshButton.disabled) return;",
			"if (context.current())",
			"if (previousRoute !== route) motion?.page(stage)",
			"if (pending) { announce(",
			"if (dirty && !await confirm(",
			"if (attempt !== navigation) return;",
			"state.session = null;",
			"state.data = {};",
			"watch.stop();",
			"box.close()",
			"box.addEventListener(\"close\", () => resolve(false)",
			"lifetime.abort(); routeAbort?.abort(); watch.stop();",
		},
		"modules/account-request.js": {
			"headers[\"X-QControlHub-CSRF\"] = session.csrf_token",
			"credentials: \"same-origin\", cache: \"no-store\"",
			"{ isLogin: path === \"/auth/login\", isCurrent, onUnauthorized",
		},
		"modules/errors.js": {
			"if (!isCurrent()) throw new DOMException(\"Account changed\", \"AbortError\")",
			"options?.signal?.aborted",
		},
		"analytics/live-dom.js": {
			"export function patch(current, fresh)",
			"if (current.data !== fresh.data) current.data = fresh.data",
			"if (current.getAttribute(attr.name) !== attr.value)",
			"if (control) return current;",
			"current.tagName === 'DETAILS' && attr.name === 'open'",
			"const keyed = new Map(",
			"current.moveBefore(child, current.childNodes[index] || null)",
			"current.childNodes[index] !== child",
			"export function poll(ctx, run, delay = 2000)",
			"if (!ctx.current() || running) return;",
			"if (!document.hidden) await run();",
			"if (ctx.current()) schedule()",
			"ctx.signal.addEventListener('abort', stop",
		},
		"analytics/nodes.js": {
			"if (!ctx.current() || sequence !== loadSequence) return;",
			"if (dragging.active()) return;",
			"patch(results.firstElementChild, fresh)",
			"dialogUpdates.forEach(update => update())",
			"liveTable.updateRows(rows)",
			"poll({ ...ctx, onRefreshError: failedRefresh }, load)",
			"if (error.status === 403",
			"agents = []; selected.clear(); draw(true)",
		},
		"analytics/client-access.js": {
			"if (!ctx.current() || sequence !== readSequence) return;",
			"patch(result, node('div'",
			"row.key = clientAccessRecordKey(entry, item)",
			"card.dataset.refreshKey = group.agent_id",
			"ctx.onRefresh(reload)",
			"ctx.setDirty(true)",
		},
		"analytics/observability.js": {
			"createPoller({", "readCoreLogWindow(ctx.api, params",
			"previous: entries", "filterCoreLogEntries(entries, filters)",
			"ctx.signal.addEventListener(\"abort\"", "serial === revision",
			"if (page && anchor)", "刷新失败，保留上次数据",
		},
		"analytics/operations.js": {
			"patch(resourceGrid,", "patch(overviewGrid,", "patch(trafficTotals,",
			"rowKey: row => row[5].dataset.taskId",
			"const sequence = ++readSequence",
			"if (!ctx.current() || sequence !== readSequence) return;",
			"if (!ctx.current() || requests.get(key) !== controller) return;",
			"AbortSignal.any([ctx.signal, controller.signal])",
			"readings.delete(key); states.set(key, \"denied\")",
			"刷新失败 · 保留上次采样", "更新失败，保留上次数据",
			"query(\"/traffic-usage\", { month })",
			"can(\"panel-metrics.read\")", "\"/panel-metrics\"",
			"orderNodesBySavedOrder(", "poll(ctx, refresh, pollDelay)",
		},
		"analytics/traffic.js": {
			"createPoller({", "bindNodeOrdering(ctx, results",
			"if (!ctx.current() || expected !== readSequence)",
			"!dragging.active() && !document.querySelector('dialog.a-dialog[open]')",
			"readController?.abort(); poller.stop();",
			"details.update(policy, byID.get(policy.agent_id))",
			"[401, 403, 404].includes(error.status)",
			"delete data.analyticsTrafficSnapshot",
			"trafficRateForDisplay(", "'auto_block'", "刷新失败，保留上次数据",
		},
		"analytics/traffic-table.js": {
			"if (card) patch(card, fresh)", "else patch(old, next)",
		},
	}
	for path, markers := range contracts {
		content := string(mustReadFrontendFile(t, path))
		for _, marker := range markers {
			if !strings.Contains(content, marker) {
				t.Errorf("%s native refresh contract is missing %q", path, marker)
			}
		}
	}
}

func TestPanelReadsAndNodeRegionsAvoidPerRenderRequests(t *testing.T) {
	app := string(mustReadFrontendFile(t, "analytics/app.js"))
	if !strings.Contains(app, "can('settings.read') && !state.data.settings ? context.api('/settings') : null") {
		t.Error("the native shell must reuse the current account's settings across route changes")
	}
	if !strings.Contains(app, "state.data = {};") {
		t.Error("authorization and login changes must invalidate the shell's account data")
	}
	// Overview is owned and polled by the dashboard, not refetched by every
	// shell render. Cache TTLs in the retired shell are no longer its contract.
	if strings.Contains(app, "api('/overview')") || strings.Contains(app, `api("/overview")`) {
		t.Error("the native shell must not fetch dashboard data for unrelated routes")
	}
	regions := string(mustReadFrontendFile(t, "analytics/node-region.js"))
	for _, marker := range []string{
		"geoRegionDetails(agent?.labels?.region_code) || geoRegionDetails(agent?.region_code)",
		"Object.hasOwn(agent, 'region_code')",
		"ctx.state.data.agentRegions ||= new Map()",
		"let entry = cache.get(key)", "cache.set(key, entry)",
		"ctx.api(`/agents/${pathID(agent.id)}/region`)",
		"ctx.current() && flag.isConnected",
		"if (cache.get(key) === entry) cache.delete(key)",
	} {
		if !strings.Contains(regions, marker) {
			t.Errorf("native region cache is missing %q", marker)
		}
	}
}
