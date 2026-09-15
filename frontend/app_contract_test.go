package frontend

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPAConsoleSurfaceMatchesInitialRelease(t *testing.T) {
	var scripts strings.Builder
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".js" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scripts.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	content := scripts.String()
	for _, required := range []string{
		`"client-access"`, `"substore-sync"`, `"live-config"`, `"archive-config"`,
		`machine-workspace`, `server-plan-form`, `field-form`,
		`revision-timeline`, `task-timeline`, `settings-section`,
		`node-settings`, `内核配置预设`, `node-settings-tabs`, `查看安装部署命令`, `复制 Agent 安装命令`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial console surface %q", required)
		}
	}
	for _, required := range []string{
		`data-theme-toggle`, `qcontrolhub-color-theme`, `login-theme-toggle`,
		`app.style.display = "contents"`, `X-QControlHub-Enrollment`,
		`/install-agent.sh`, `执行记录`, `入站操作`, `高级字段`, `系统设置`,
		`data-delete-enrollment`, `可重复安装`, `删除添加命令`,
		`enrollment-token`, `/enrollment-command`,
		`heartbeat, percent`, `serviceActionDisabled`, `trafficChart`, `renderConfigDiff`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial visual/installation contract %q", required)
		}
	}
	if strings.Contains(content, "/ui/") {
		t.Error("SPA must use the JSON API instead of legacy HTML form routes")
	}
	for _, forbidden := range []string{"注册码", "入网码", "命令有效期"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SPA still exposes deprecated add-node wording %q", forbidden)
		}
	}
	if strings.Contains(content, "旧安装命令会立即失效") || strings.Contains(content, "重新生成后旧命令立即失效") {
		t.Error("SPA must keep existing Agent install commands valid when another command is generated")
	}
	for _, forbidden := range []string{"生成新安装命令", "data-reinstall-agent"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SPA must not expose single-node credential generation control %q", forbidden)
		}
	}
	if !strings.Contains(content, "命令可重复查看") {
		t.Error("SPA does not explain that existing Agent install commands remain readable")
	}
}
