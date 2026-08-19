package frontend

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSPAConsoleSurfaceMatchesInitialRelease(t *testing.T) {
	script, err := os.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, required := range []string{
		`"client-access"`, `"live-config"`, `"archive-config"`,
		`machine-workspace`, `server-plan-form`, `field-form`,
		`revision-timeline`, `task-timeline`, `settings-section`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("SPA is missing initial console surface %q", required)
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
