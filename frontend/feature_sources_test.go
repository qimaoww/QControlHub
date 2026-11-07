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
