package frontend

import (
	"strings"
	"testing"
)

func TestSettingsDoesNotRepeatTheContextNavigation(t *testing.T) {
	settings := []byte(frontendFeatureSources(t, "modules/settings.js"))
	content := string(settings)
	for _, duplicate := range []string{`class="settings-hero"`, `class="settings-category-nav"`, `aria-label="设置分类"`} {
		if strings.Contains(content, duplicate) {
			t.Errorf("settings workspace repeats context navigation with %q", duplicate)
		}
	}
	if !strings.Contains(content, `class="settings-savebar"`) || !strings.Contains(content, `data-settings-state`) {
		t.Fatal("settings save bar must retain the saved revision state")
	}
}

func TestCoreLogStoragePolicyNavigationAndVisibility(t *testing.T) {
	appContent := frontendSources(t, "modules/shell-context.js", "modules/routes.js", "modules/shell-appearance.js")
	for _, required := range []string{
		`href="#settings-engines"><span>01</span>默认内核能力`,
		`href="#settings-basic"><span>02</span>基础设置`,
		`href="#settings-runtime"><span>03</span>任务与同步`,
		`href="#settings-data"><span>04</span>数据与日志`,
		`href="#settings-notify"><span>05</span>事件通知`,
		`href="#settings-komari"><span>06</span>Komari 联动`,
		`href="#settings-deployment"><span>08</span>部署状态`,
		`"settings-engines": "settings"`,
		`"settings-data": "settings"`,
	} {
		if !strings.Contains(appContent, required) {
			t.Errorf("core-log storage settings navigation is missing %q", required)
		}
	}
	settingsModule := []byte(frontendFeatureSources(t, "modules/settings.js"))
	settingsContent := string(settingsModule)
	if strings.Contains(settingsContent, `api("/users")`) || strings.Contains(settingsContent, "用户管理") {
		t.Error("settings page must not load or render user permission management")
	}
	if strings.Count(settingsContent, `data-save-settings`) != 2 {
		t.Error("settings page must render and bind exactly one save button")
	}
	for _, required := range []string{
		`select(item, "ui_font_scale", "界面字号"`,
		`[90, "小"]`, `[100, "正常"]`, `[110, "大"]`,
		`ui_font_scale: number("ui_font_scale")`,
		`applyUIFontScale?.(saved.ui_font_scale)`,
	} {
		if !strings.Contains(settingsContent, required) {
			t.Errorf("settings page is missing configurable typography marker %q", required)
		}
	}
	if !strings.Contains(appContent, `--ui-font-scale`) || !strings.Contains(appContent, `uiFontScaleOptions`) {
		t.Error("app shell must apply the persisted typography scale")
	}

	module := []byte(frontendFeatureSources(t, "modules/core-logs.js"))
	moduleContent := string(module)
	if !strings.Contains(moduleContent, `})[value] || "保存策略不可见"`) {
		t.Error("core-log storage policy must be explicit when settings are unavailable")
	}
	if !strings.Contains(moduleContent, `can("settings.read")`) {
		t.Error("core-log storage policy visibility must follow the independent settings.read permission")
	}
	if strings.Contains(moduleContent, `state.data.settings?.core_log_minimum_level || "debug"`) {
		t.Error("core-log readers without settings permission must not see a fabricated debug policy")
	}
}
