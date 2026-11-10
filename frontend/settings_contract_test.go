package frontend

import (
	"strings"
	"testing"
)

func TestSettingsDoesNotRepeatTheContextNavigation(t *testing.T) {
	content := string(mustReadFrontendFile(t, "analytics/settings.js"))
	for _, duplicate := range []string{`settings-hero`, `settings-category-nav`, `设置分类`, `data-save-settings`} {
		if strings.Contains(content, duplicate) {
			t.Errorf("native settings repeats retired navigation/control %q", duplicate)
		}
	}
	for _, required := range []string{
		`ctx.form(content, "保存设置"`, `form.id = "panel-settings-form"`,
		`submit.setAttribute("form", form.id)`, `ctx.pageActions(submit)`,
		`saveState.setAttribute("role", "status")`, `baseline = submitted`,
		`保存失败 · 有未保存更改`, `snapshot() !== baseline`, `sequence !== refreshSequence`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("native settings save contract missing %q", required)
		}
	}
	if strings.Count(content, `ctx.form(content, "保存设置"`) != 1 {
		t.Error("settings must create one complete form and one save action")
	}
}

func TestCoreLogStoragePolicyNavigationAndVisibility(t *testing.T) {
	appContent := frontendSources(t, "analytics/app.js", "modules/routes.js", "analytics/appearance.js")
	for _, required := range []string{
		`["settings", "面板设置", "settings", "settings.read"]`,
		`"settings-engines": "settings"`, `"settings-data": "settings"`,
		`"settings-certificates": "settings"`, `"settings-api-keys": "api"`,
		`applyAppearance(result)`, `document.documentElement.style.fontSize`,
	} {
		if !strings.Contains(appContent, required) {
			t.Errorf("native settings navigation/appearance missing %q", required)
		}
	}
	settingsContent := string(mustReadFrontendFile(t, "analytics/settings.js"))
	for _, group := range []string{"基础设置", "默认内核能力", "节点上报", "任务", "日志", "数据清理", "证书与地址库", "部署状态", "API"} {
		if !strings.Contains(settingsContent, `section("`+group+`"`) {
			t.Errorf("native settings omits group %q", group)
		}
	}
	if strings.Contains(settingsContent, `api("/users")`) || strings.Contains(settingsContent, "用户管理") {
		t.Error("settings must not load/render user management")
	}
	for _, required := range []string{
		`input("ui_font_scale", "文字大小"`, `[90, "较小"]`, `[100, "标准"]`, `[110, "较大"]`,
		`"ui_font_scale", "client_connection_retention_days"`, `body[name] = Number(body[name])`,
		`ctx.session.role === "admin" && writable`, `ctx.can("settings.manage")`,
	} {
		if !strings.Contains(settingsContent, required) {
			t.Errorf("native settings typography/permission contract missing %q", required)
		}
	}

	for _, notification := range []string{"notificationSettings", "/settings/notifications", "Webhook", "notify_"} {
		if strings.Contains(settingsContent, notification) {
			t.Errorf("panel settings still contains notification concern %q", notification)
		}
	}
	for _, required := range []string{`["notifications", "通知", "activity", "settings.read"]`, `notifications: ['notifications', 'notificationsPage']`, `"settings-notify": "notifications"`} {
		if !strings.Contains(appContent, required) {
			t.Errorf("independent notification route missing %q", required)
		}
	}
	notificationContent := string(mustReadFrontendFile(t, "analytics/notification-settings.js"))
	for _, title := range []string{"通知渠道", "通知规则", "通知模板", "投递记录", "通知服务状态"} {
		if !strings.Contains(notificationContent, `section("`+title+`"`) {
			t.Errorf("notification group missing %s", title)
		}
	}
	module := []byte(frontendSources(t, "analytics/observability.js", "modules/core-log-model.js"))
	moduleContent := string(module)
	if !strings.Contains(moduleContent, `})[value] || "保存策略不可见"`) {
		t.Error("core-log storage policy must be explicit when settings are unavailable")
	}
	if !strings.Contains(moduleContent, `coreLogStoragePolicy(data.settings, ctx.can('settings.read'))`) {
		t.Error("core-log storage policy visibility must follow the independent settings.read permission")
	}
	if strings.Contains(moduleContent, `state.data.settings?.core_log_minimum_level || "debug"`) {
		t.Error("core-log readers without settings permission must not see a fabricated debug policy")
	}
}
