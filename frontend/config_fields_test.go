package frontend

import (
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
	// Transform the actual native editor: nested theme modules and shared field
	// models must receive the same build key as the application entrypoint.
	source := string(mustReadFrontendFile(t, "analytics/config-studio.js"))
	transformed := rewriteModuleCacheImports(t, source)
	for _, path := range []string{"./inbound-fields.js", "../modules/config-field-model.js", "./inbound-source.js"} {
		if !strings.Contains(source, path) || !strings.Contains(transformed, path+"?v=cache-test") {
			t.Errorf("native config editor dependency %s did not receive the release cache key", path)
		}
	}
}
