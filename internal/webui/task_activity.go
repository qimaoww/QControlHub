package webui

import (
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type taskActivityGroup struct {
	Task  core.Task
	Count int
}

func recentTaskActivity(tasks []core.Task, limit int) []taskActivityGroup {
	if limit < 1 {
		return nil
	}
	groups := make([]taskActivityGroup, 0, limit)
	for _, task := range tasks {
		if len(groups) > 0 {
			latest := &groups[len(groups)-1]
			if sameTaskActivity(latest.Task, task) && latest.Task.CreatedAt.Sub(task.CreatedAt) <= 10*time.Minute {
				latest.Count++
				continue
			}
		}
		if len(groups) == limit {
			break
		}
		groups = append(groups, taskActivityGroup{Task: task, Count: 1})
	}
	return groups
}

func sameTaskActivity(left, right core.Task) bool {
	return left.AgentID == right.AgentID &&
		left.Action == right.Action &&
		left.Engine == right.Engine &&
		left.Status == right.Status &&
		left.Simulated == right.Simulated
}
