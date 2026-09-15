package frontend

import (
	"strings"
	"testing"
)

func TestTaskPollingKeepsTheScrollContainerStable(t *testing.T) {
	tasks := []byte(frontendFeatureSources(t, "modules/tasks.js"))
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
