package frontend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep source-level contracts scoped to their explicit owners. Do not traverse
// arbitrary dependencies: another feature must not satisfy a missing marker.
func frontendFeatureSources(t *testing.T, route string) string {
	t.Helper()
	owners := map[string][]string{
		"modules/settings.js": {
			"modules/settings.js", "modules/settings-view.js", "modules/settings-bindings.js",
		},
		"modules/dashboard.js": {
			"modules/dashboard.js", "modules/dashboard-model.js", "modules/dashboard-view.js", "modules/dashboard-bindings.js",
		},
		"modules/client-access.js": {
			"modules/client-access.js", "modules/client-access-model.js", "modules/client-access-view.js",
			"modules/client-access-results.js", "modules/client-access-bindings.js", "modules/client-access-profiles.js",
			"modules/client-clipboard.js", "modules/card-masonry.js",
		},
		"modules/substore-sync.js": {
			"modules/substore-sync.js", "modules/substore-model.js", "modules/substore-view.js",
			"modules/substore-bindings.js", "modules/substore-selections.js", "modules/substore-targets.js", "modules/card-masonry.js",
		},
		"modules/system-bbr.js": {
			"modules/system-bbr.js", "modules/system-bbr-model.js", "modules/system-bbr-view.js", "modules/system-bbr-editor.js",
		},
		"modules/traffic.js": {
			"modules/traffic.js", "modules/traffic-model.js", "modules/traffic-view.js", "modules/traffic-accounting-view.js",
			"modules/traffic-bindings.js", "modules/traffic-forms.js", "modules/traffic-form-view.js", "modules/traffic-form-model.js",
			"modules/traffic-order.js", "modules/traffic-card-interactions.js", "modules/traffic-sync-dialog.js",
		},
		"modules/core-logs.js": {
			"modules/core-logs.js", "modules/core-log-model.js", "modules/core-log-view.js",
			"modules/core-log-selection.js", "modules/core-log-bindings.js", "modules/core-log-source-status.js",
		},
		"modules/tasks.js": {
			"modules/tasks.js", "modules/task-model.js", "modules/task-view.js", "modules/task-bindings.js", "modules/task-timeline.js",
		},
	}
	if paths, ok := owners[route]; ok {
		return frontendSources(t, paths...)
	}
	return frontendSources(t, route)
}

func mustReadFrontendFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func frontendSources(t *testing.T, names ...string) string {
	t.Helper()
	var source strings.Builder
	for _, name := range names {
		source.Write(mustReadFrontendFile(t, name))
		source.WriteByte('\n')
	}
	return source.String()
}
