package frontend

import (
	"strings"
	"testing"
)

func TestSPAConsoleSurfaceMatchesInitialRelease(t *testing.T) {
	// Check the shipped owners directly: a retired renderer must never satisfy
	// an otherwise missing native workflow through a repository-wide scan.
	owners := map[string][]string{
		"analytics/app.js": {
			`["client-access",`, `["substore-sync",`, `["live-config",`, `["archive-config",`,
			`["node-settings",`, `["settings",`, `field("用户名", "username", ""`,
			`api("/auth/login"`, `api("/auth/logout"`, `mountTheme(`,
		},
		"analytics/nodes.js": {
			`enrollmentInstallCommand(created)`, `ctx.dialog('QAgent 安装命令'`,
			`await copyText(value)`, `ctx.api('/enrollment-tokens')`,
			`/enrollment-tokens/${pathID(token.id)}/command`, `method: 'DELETE'`,
			`token.command_available`, `已接入的节点不会被卸载`, `/enrollment-command`,
			`ctx.can('enrollment.manage')`, `ctx.can('host.manage',agent)`,
			`createServiceActionGuard(ctx.can)`, `m.cpu_percent`, `agent.last_seen`,
		},
		"analytics/config-studio.js": {
			`field('协议','protocol'`, `保存并部署`, `await taskDetail(ctx,result.task.id)`,
			`section('入站配置'`, `section('出站配置'`, `通用配置项`,
		},
		"analytics/live-config.js":      {`保存历史`, `/revisions?limit=100`, `恢复此档案`, `配置源码`},
		"analytics/operations.js":       {`export async function tasksPage`, `export function taskDetail`, `taskRetryState(task)`, `output.firstChild`},
		"analytics/api-management.js":   {`section("API Key"`, `"/settings/api-keys"`, `field("模式", "mode"`},
		"analytics/settings.js":         {`保存设置`, `ctx.pageActions(submit)`, `section("API"`},
		"analytics/themes.js":           {`createThemePreference`, `ctx.switchTheme`, `theme.id === "analytics"`},
		"modules/enrollment-command.js": {`X-QControlHub-Enrollment`, `/install-agent.sh`, `created?.token`, `created?.name`},
	}
	var scripts strings.Builder
	for path, markers := range owners {
		content := string(mustReadFrontendFile(t, path))
		scripts.WriteString(content)
		for _, required := range markers {
			if !strings.Contains(content, required) {
				t.Errorf("native console owner %s is missing %q", path, required)
			}
		}
	}
	content := scripts.String()
	for _, forbidden := range []string{
		"/ui/", "注册码", "入网码", "命令有效期", "旧安装命令会立即失效", "重新生成后旧命令立即失效",
		"生成新安装命令", "data-reinstall-agent", "login-theme-toggle", "qcontrolhub-color-theme",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("native console exposes retired route/control/wording %q", forbidden)
		}
	}
	// Reading existing enrollment commands must keep the token and does not
	// depend on regenerating credentials; real repeated reads are browser tested.
	nodes := string(mustReadFrontendFile(t, "analytics/nodes.js"))
	start := strings.Index(nodes, "token.command_available && button('安装命令'")
	if start < 0 {
		t.Fatal("existing command action is missing")
	}
	end := strings.Index(nodes[start:], "button('删除记录'")
	if end <= 0 {
		t.Fatal("existing command action boundary is missing")
	}
	read := nodes[start : start+end]
	if strings.Contains(read, `ctx.api('/enrollment-tokens',`) || !strings.Contains(read, `box.close(); command(created)`) {
		t.Fatal("reading an installation command must display the existing credential without regenerating it")
	}
}
