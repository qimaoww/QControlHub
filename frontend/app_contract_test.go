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
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial console surface %q", required)
		}
	}
	for _, required := range []string{
		`data-theme-toggle`, `qcontrolhub-color-theme`, `login-theme-toggle`,
		`app.style.display = "contents"`, `X-QControlHub-Enrollment`,
		`/install-agent.sh`, `执行记录`, `手动配置`, `系统设置`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial visual/installation contract %q", required)
		}
	}
	if strings.Contains(content, "/ui/") {
		t.Error("SPA must use the JSON API instead of legacy HTML form routes")
	}
}

func TestInitialConsoleStylesRemainExact(t *testing.T) {
	styles, err := os.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	const expected = "fb8d54418db5a8511ab88196525aff163b9655f4e45412f5dd05a942ed0e219e"
	if actual := fmt.Sprintf("%x", sha256.Sum256(styles)); actual != expected {
		t.Fatalf("app.css hash = %s, want initial release hash %s", actual, expected)
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
		"settings.js",
		"../module_smoke.mjs",
	} {
		if _, err := os.Stat(filepath.Join("modules", name)); err != nil {
			t.Errorf("missing SPA module %s: %v", name, err)
		}
	}
}
