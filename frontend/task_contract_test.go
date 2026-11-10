package frontend

import (
	"strings"
	"testing"
)

func TestTaskPollingKeepsTheScrollContainerStable(t *testing.T) {
	content := string(mustReadFrontendFile(t, "analytics/operations.js"))
	for _, required := range []string{
		`rowKey: row => row.taskID`,
		`taskTable.updateRows`,
		`syncAgents()`,
		`taskTiming(task)`,
		`patch(context, taskContext(ctx, task, targetAgent))`,
		`selection.setBaseAndExtent`,
		`if(follow)output.scrollTop=output.scrollHeight`,
		`poll(ctx, refresh, pollDelay)`,
		`if (Date.now() - agentsLoadedAt < 30000) return;`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("task polling is missing in-place refresh contract %q", required)
		}
	}
	if strings.Contains(content, `setTimeout(() => tasks(),`) {
		t.Error("task polling must not rebuild the complete application shell")
	}
	// Actual list/detail focus, selection, scrolling, pagination, single-timer,
	// cache, permission and stale-read behavior runs in both native task modes.
	patching := string(mustReadFrontendFile(t, "analytics/live-dom.js"))
	for _, required := range []string{`isActive: () => ctx.current()`, `current.moveBefore`, `if (control) return current`} {
		if !strings.Contains(patching, required) {
			t.Errorf("native keyed polling is missing %q", required)
		}
	}
}
