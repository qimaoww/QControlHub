package frontend

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestConfigFieldStudioSmoke(t *testing.T) {
	output, err := exec.Command("node", "config_fields_smoke.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("config field studio smoke failed: %v\n%s", err, output)
	}
}

func TestConfigFieldModuleCacheInvalidation(t *testing.T) {
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	// The main app already has versioned imports; the shared field renderer is
	// imported by modules themselves and must receive that same release key.
	for _, line := range strings.Split(string(dockerfile), "\n") {
		if strings.Contains(line, "find /usr/share/nginx/html/assets/modules -name '*.js'") &&
			strings.Contains(line, "sed -i -E") && strings.Contains(line, "?v=${js_version}") {
			return
		}
	}
	t.Fatal("nested module imports must use the release cache key to avoid stale field renderers")
}
