package frontend

import (
	"crypto/sha256"
	"fmt"
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
		`"client-access"`, `"live-config"`, `"archive-config"`,
		`machine-workspace`, `server-plan-form`, `field-form`,
		`revision-timeline`, `task-timeline`, `settings-section`,
		`node-settings`, `内核配置预设`, `node-settings-tabs`, `复制 Agent 安装命令`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial console surface %q", required)
		}
	}
	for _, required := range []string{
		`data-theme-toggle`, `qcontrolhub-color-theme`, `login-theme-toggle`,
		`app.style.display = "contents"`, `X-QControlHub-Enrollment`,
		`/install-agent.sh`, `执行记录`, `手动配置`, `系统设置`,
		`data-delete-enrollment`, `可重复安装`, `删除添加命令`,
		`enrollment-token`, `生成新安装命令`,
		`heartbeat, percent`, `serviceActionDisabled, trafficChart, renderConfigDiff`,
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
	if !strings.Contains(content, "已有安装命令会继续有效") || !strings.Contains(content, "已有命令继续有效") {
		t.Error("SPA does not explain that existing Agent install commands remain valid")
	}
}

func TestInitialConsoleStylesRemainExactOutsideApprovedExtensions(t *testing.T) {
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	const extensionMarker = "/* Deployment command dialog v48: a scoped extension to the initial console surface. */"
	parts := strings.SplitN(string(styles), extensionMarker, 2)
	if len(parts) != 2 {
		t.Fatalf("app.css is missing approved deployment dialog extension marker")
	}
	// Base hash covers the initial release plus the approved v56 revision that
	// reserved the compact media queries for coarse-pointer devices so desktop
	// zoom keeps one fixed layout.
	const expected = "b967be66daf4078b69fdc204cf88105800f424dae4aed74ec3e5807b47a3ff4c"
	if actual := fmt.Sprintf("%x", sha256.Sum256([]byte(parts[0]))); actual != expected {
		t.Fatalf("base app.css hash = %s, want initial release hash %s", actual, expected)
	}
	for _, required := range []string{".deploy-command-modal", ".deploy-command-input", ".deploy-command-copy"} {
		if !strings.Contains(parts[1], required) {
			t.Errorf("deployment dialog extension is missing %q", required)
		}
	}
}

func TestStaticAssetsUseBuildGeneratedCacheKeys(t *testing.T) {
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(index)
	for _, required := range []string{
		`/assets/app.css?v=__QCH_CSS_VERSION__`,
		`/assets/app.js?v=__QCH_JS_VERSION__`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("index.html is missing cache key placeholder %q", required)
		}
	}
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"__QCH_CSS_VERSION__", "__QCH_JS_VERSION__"} {
		if !strings.Contains(string(dockerfile), placeholder) {
			t.Errorf("Dockerfile does not replace %s", placeholder)
		}
	}
	if !strings.Contains(string(dockerfile), `modules/[^\"]+\\.js`) || !strings.Contains(string(dockerfile), `?v=${js_version}`) {
		t.Error("Dockerfile does not add the aggregate JavaScript cache key to module imports")
	}
	if !strings.Contains(string(dockerfile), `js_content_version`) || !strings.Contains(string(dockerfile), `${VERSION}`) {
		t.Error("Dockerfile JavaScript cache key must include both content and release version")
	}
}

func TestSPAModulesArePublished(t *testing.T) {
	for _, name := range []string{
		"dashboard.js",
		"agents.js",
		"client-access.js",
		"configs.js",
		"tasks.js",
		"traffic.js",
		"settings.js",
		"../module_smoke.mjs",
	} {
		if _, err := os.Stat(filepath.Join("modules", name)); err != nil {
			t.Errorf("missing SPA module %s: %v", name, err)
		}
	}
}

func TestTaskPollingKeepsTheScrollContainerStable(t *testing.T) {
	tasks, err := os.ReadFile("modules/tasks.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(tasks)
	for _, required := range []string{
		`tasks({ background: true })`,
		`reconcileTaskTimeline`,
		`captureTaskAnchor`,
		`restoreTaskAnchor`,
		`taskRenderSignature`,
		`syncTaskAgentFilter`,
		`data-task-age`,
		`data-task-timing`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("task polling is missing in-place refresh contract %q", required)
		}
	}
	if strings.Contains(content, `setTimeout(() => tasks(),`) {
		t.Error("task polling must not rebuild the complete application shell")
	}
}
