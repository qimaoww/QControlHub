package webui

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestRecentTaskActivityCollapsesConsecutiveEquivalentBursts(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tasks := []core.Task{
		{ID: "read-1", AgentID: "agent", Action: core.ActionReadConfig, Engine: core.EngineMihomo, Status: core.TaskSucceeded, CreatedAt: now},
		{ID: "read-2", AgentID: "agent", Action: core.ActionReadConfig, Engine: core.EngineMihomo, Status: core.TaskSucceeded, CreatedAt: now.Add(-time.Minute)},
		{ID: "deploy", AgentID: "agent", Action: core.ActionDeploy, Engine: core.EngineMihomo, Status: core.TaskSucceeded, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "read-3", AgentID: "agent", Action: core.ActionReadConfig, Engine: core.EngineMihomo, Status: core.TaskSucceeded, CreatedAt: now.Add(-3 * time.Minute)},
	}
	groups := recentTaskActivity(tasks, 7)
	if len(groups) != 3 || groups[0].Task.ID != "read-1" || groups[0].Count != 2 || groups[1].Task.ID != "deploy" || groups[2].Count != 1 {
		t.Fatalf("task activity groups = %+v", groups)
	}
	if groups := recentTaskActivity(tasks, 1); len(groups) != 1 || groups[0].Count != 2 {
		t.Fatalf("limited task activity groups = %+v", groups)
	}
}
