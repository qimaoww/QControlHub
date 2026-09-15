package api

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

// notifyTaskFailure asynchronously delivers a task.failed webhook event when a
// webhook URL is configured. Delivery is best-effort and never blocks the
// agent connection loop.
func (s *Server) notifyTaskFailure(agentID, taskID string, result core.TaskResultRequest) {
	if result.Success {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		settings, err := s.store.TaskPanelSettings(ctx, taskID)
		if err != nil || !settings.NotifyTaskFailed || strings.TrimSpace(settings.WebhookURL) == "" {
			return
		}
		task, err := s.store.GetTask(ctx, taskID)
		if err != nil {
			slog.Warn("load task for failure webhook", "task_id", taskID, "error", err)
			return
		}
		agentName, _ := s.store.AgentName(ctx, agentID)
		event := notify.TaskFailedEvent(task, agentName, result.Error)
		if err := s.notifier.Send(ctx, settings.WebhookURL, event); err != nil {
			slog.Warn("deliver task failure webhook", "task_id", taskID, "error", err)
		}
	}()
}
