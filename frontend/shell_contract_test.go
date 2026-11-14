package frontend

import (
	"fmt"
	"strings"
	"testing"
)

func TestSidebarNavigationUsesWorkflowOrderAndResponsiveGrouping(t *testing.T) {
	content := string(mustReadFrontendFile(t, "analytics/app.js"))
	start, end := strings.Index(content, "const destinations = ["), strings.Index(content, "const pageModules =")
	if start < 0 || end <= start {
		t.Fatal("native navigation groups are missing")
	}
	navigation := content[start:end]
	previous := -1
	for _, group := range []string{`["概览"`, `["节点与配置"`, `["运维"`, `["面板"`} {
		position := strings.Index(navigation, group)
		if position <= previous {
			t.Errorf("native navigation group out of order: %q", group)
		}
		previous = position
	}
	for _, route := range []string{"dashboard", "topology", "node-settings", "live-config", "archive-config", "client-access", "substore-sync", "certificates", "tasks", "core-logs", "client-connections", "traffic", "system-bbr", "ip-quality", "settings", "users", "my-quota", "themes"} {
		if strings.Count(navigation, `["`+route+`",`) != 1 {
			t.Errorf("native navigation must expose exactly one %s destination", route)
		}
	}
	for _, forbidden := range []string{`"agents"`, `"access-control"`, `"agent-config"`} {
		if strings.Contains(navigation, forbidden) {
			t.Errorf("duplicate configuration navigation: %s", forbidden)
		}
	}
	for _, required := range []string{`const allowed = entries.filter`, `can(permission)`, `rail(true)`, `"aria-label": "主导航"`, `"aria-current": route === id ? "page" : null`, `matchMedia("(max-width: 56rem)")`, `openDialog(opened)`, `navigationToggle?.setAttribute("aria-expanded", "false")`} {
		if !strings.Contains(content, required) {
			t.Errorf("native navigation permission/responsive contract missing %q", required)
		}
	}
	styles := string(mustReadFrontendFile(t, "analytics/theme.css"))
	for _, required := range []string{`.a-rail nav {`, `overflow: auto`, `.a-drawer { width: min(288px, 88vw)`, `.a-layout > .a-rail { display: none; }`, `.a-drawer .a-rail`, `min-height: 44px`} {
		if !strings.Contains(styles, required) {
			t.Errorf("native navigation styles missing %q", required)
		}
	}
}

func TestLoginDoesNotPreFillAdministratorUsername(t *testing.T) {
	content := frontendSources(t, "analytics/app.js", "analytics/ui.js")
	for _, required := range []string{`field("用户名", "username", "", { autocomplete: "username", required: true`, `field("密码 / 管理令牌", "token", "", { type: "password", autocomplete: "current-password", required: true`, `node("label"`, `node("span", {}, label)`} {
		if !strings.Contains(content, required) {
			t.Errorf("native login label/autocomplete contract missing %q", required)
		}
	}
	if strings.Contains(content, `field("用户名", "username", "admin"`) {
		t.Fatal("login username must not be prefilled with admin")
	}
}

func TestRouteModulesLoadOnDemandAndWarmFromNavigationIntent(t *testing.T) {
	app := string(mustReadFrontendFile(t, "analytics/app.js"))
	for _, module := range []string{
		"operations", "nodes", "client-access", "substore", "configs", "live-config",
		"observability", "traffic", "tcp", "settings", "users", "themes", "ip-quality", "certificates",
	} {
		lazyImport := fmt.Sprintf(`import('./%s.js')`, module)
		if !strings.Contains(app, lazyImport) {
			t.Errorf("route module %q is not loaded lazily", module)
		}
		if strings.Contains(app, fmt.Sprintf(`from "./%s.js"`, module)) || strings.Contains(app, fmt.Sprintf(`from './%s.js'`, module)) {
			t.Errorf("route module %q is still part of the eager application graph", module)
		}
	}
	warmup := string(mustReadFrontendFile(t, "modules/route-warmup.js"))
	for _, required := range []string{
		`createRouteModuleLoader({`,
		`createRouteWarmup(routeModules, { moduleName: pageModuleName, signal: lifetime.signal })`,
		`bindNavigationPreload();`,
		`routeModules.preload(moduleName(route))`,
		`document.addEventListener("pointerover", preloadNavigationRoute`,
		`document.addEventListener("pointerdown", preloadNavigationRoute`,
		`document.addEventListener("focusin", preloadNavigationRoute`,
		`window.requestIdleCallback(warm, { timeout: 1500 })`,
		`connection?.saveData`,
		`connection?.effectiveType?.includes("2g")`,
		`const module = await routeModules.load(descriptor[0]);`,
		`void context.api('/settings').catch(error => {`,
		`routeModules.load(descriptor[0])`,
		`if (!context.current()) return;`,
		`signal?.addEventListener('abort', stopRouteWarmup`,
	} {
		if !strings.Contains(app+warmup, required) {
			t.Errorf("route loading performance contract is missing %q", required)
		}
	}
}

func TestWorkspaceRouteCrumbsLinkToModuleEntries(t *testing.T) {
	content := frontendSources(t, "analytics/app.js", "modules/routes.js")
	// The native rail and header replace the old breadcrumb layout. Preserve
	// the actual destinations, permission fallback and selected-route context.
	for _, required := range []string{
		`className: "a-brand", href: "#dashboard"`,
		`if (route === "dashboard" && !can("overview.read")) route = "my-quota"`,
		`["agents", "agent-config", "access-control"].includes(route)`, `route = "live-config"`,
		`shellTitle.textContent = title`, `aria-current`, `link.hash ===`,
		`if (hash.startsWith("preset-node-")) return "live-config"`,
		`["live-config", "实时配置"`, `["archive-config", "配置档案"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native workspace entry routing is missing %q", required)
		}
	}
}
