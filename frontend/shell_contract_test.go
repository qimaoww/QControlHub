package frontend

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSidebarNavigationUsesWorkflowOrderAndResponsiveGrouping(t *testing.T) {
	content := frontendSources(t, "modules/shell-view.js", "modules/shell-icons.js")
	start := strings.Index(content, "  const links = [")
	if start < 0 {
		t.Fatal("shell-view.js is missing the sidebar navigation list")
	}
	end := strings.Index(content[start:], "  ];\n  const linkPermissions")
	if end < 0 {
		t.Fatal("shell-view.js sidebar navigation list has no closing boundary")
	}
	navigation := content[start : start+end]
	previous := -1
	for _, route := range []string{
		"dashboard", "node-settings", "live-config",
		"client-access", "substore-sync", "traffic", "core-logs", "tasks",
	} {
		position := strings.Index(navigation, `"`+route+`"`)
		if position < 0 {
			t.Errorf("sidebar navigation is missing route %q", route)
			continue
		}
		if position <= previous {
			t.Errorf("sidebar route %q is outside the expected workflow order", route)
		}
		previous = position
	}
	if strings.Contains(navigation, `"settings"`) {
		t.Error("settings must remain separated from the primary desktop navigation")
	}
	if strings.Contains(navigation, `"agents"`) || strings.Contains(content, `["agents", "内核预设"]`) {
		t.Error("preset actions belong in the configuration page, not standalone desktop or mobile navigation")
	}
	for _, required := range []string{
		`const dockIcons = Object.freeze({`,
		`["node-settings", "节点", dockIcons.server]`,
		`["live-config", "配置", dockIcons.fileCode]`,
		`["client-access", "客户端", dockIcons.monitorSmartphone]`,
		`["substore-sync", "同步", dockIcons.refreshCw, true]`,
		`["traffic", "流量", dockIcons.chart, true]`,
		`["core-logs", "日志", dockIcons.logs, true]`,
		`["tasks", "任务", dockIcons.listChecks, true]`,
		`class="dock-settings`,
		`mobileMoreRoutes.some(([id]) => activeDockRoute(id))`,
		`summary class="${mobileMoreActive ? "active" : ""}"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("sidebar navigation is missing responsive icon contract %q", required)
		}
	}
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`.dock-nav .dock-mobile-secondary{display:none}`,
		`.app-dock{left:0;right:0;bottom:0;height:calc(64px + env(safe-area-inset-bottom));display:grid;grid-template-columns:repeat(5,minmax(0,1fr))`,
		`.dock-nav{display:contents!important}`,
		`.dock-nav a,.mobile-account-menu>summary{display:flex;align-items:center;justify-content:center;flex-direction:column;gap:1px`,
		`,.page-agents .release-channel-options label{min-height:44px}`,
		`.mobile-account-menu>summary.active`,
		`.mobile-account-menu a,.mobile-account-menu button`,
		`.page-access-control .access-control-card form.is-dirty>footer{position:fixed;z-index:85;right:8px;bottom:calc(64px + env(safe-area-inset-bottom));left:8px`,
	} {
		if !strings.Contains(string(styles), required) {
			t.Errorf("sidebar styles are missing responsive grouping contract %q", required)
		}
	}
}

func TestLoginDoesNotPreFillAdministratorUsername(t *testing.T) {
	app, err := os.ReadFile("modules/login-page.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(app)
	if strings.Contains(content, `name="username" type="text" value="admin"`) {
		t.Fatal("login username must not be prefilled with admin")
	}
	if !strings.Contains(content, `name="username" type="text" autocomplete="username"`) {
		t.Fatal("login username must retain browser autocomplete support")
	}
	if !strings.Contains(content, `<label>用户名<input name="username"`) {
		t.Fatal("login username must have a persistent label, not only a placeholder")
	}
}

func TestRouteModulesLoadOnDemandAndWarmFromNavigationIntent(t *testing.T) {
	app := string(mustReadFrontendFile(t, "app.js"))
	for _, module := range []string{
		"dashboard", "agents", "client-access", "substore-sync", "configs",
		"tasks", "core-logs", "traffic", "access-control", "system-bbr",
		"settings", "users",
	} {
		lazyImport := fmt.Sprintf(`import("./modules/%s.js")`, module)
		if !strings.Contains(app, lazyImport) {
			t.Errorf("route module %q is not loaded lazily", module)
		}
		staticImport := fmt.Sprintf(`from "./modules/%s.js"`, module)
		if strings.Contains(app, staticImport) {
			t.Errorf("route module %q is still part of the eager application graph", module)
		}
	}
	warmup := string(mustReadFrontendFile(t, "modules/route-warmup.js"))
	for _, required := range []string{
		`createRouteModuleLoader({`,
		`createRouteWarmup(routeModules)`,
		`bindNavigationPreload();`,
		`routeModules.preload(routeModuleNames[route] || "dashboard")`,
		`document.addEventListener("pointerover", preloadNavigationRoute`,
		`document.addEventListener("pointerdown", preloadNavigationRoute`,
		`document.addEventListener("focusin", preloadNavigationRoute`,
		`window.requestIdleCallback(warm, { timeout: 1500 })`,
		`connection?.saveData`,
		`connection?.effectiveType?.includes("2g")`,
		`const routeModulePromise = routeModules.load(`,
		`const sharedDataPromise = Promise.all([`,
	} {
		if !strings.Contains(app+warmup, required) {
			t.Errorf("route loading performance contract is missing %q", required)
		}
	}
}
