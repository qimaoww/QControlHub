package store

import (
	"context"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Database-only accounting tests emulate the versioned Agent acknowledgement.
// Real service/configuration checks live in the Agent and live kernel suites.
func completeSharedTestTask(db *Store, ctx context.Context, agentID, taskID string, result core.TaskResultRequest) error {
	var task core.Task
	if err := db.pool.QueryRow(ctx, `SELECT engine,action,shared_instance,shared_traffic_id,COALESCE(config_id,''),COALESCE(config_version,0)
		FROM tasks WHERE id=$1`, taskID).Scan(&task.Engine, &task.Action, &task.SharedInstance, &task.SharedTrafficID, &task.ConfigID, &task.ConfigVersion); err != nil {
		return err
	}
	if task.SharedInstance && result.Success && (task.Action.ChangesInstance() || task.Action == core.ActionStatus) && result.InstanceStatus == nil {
		status := "active"
		if task.Action == core.ActionStop {
			status = "inactive"
		}
		applied := ""
		if task.Action == core.ActionDeploy {
			applied = taskID
		}
		result.InstanceStatus = &core.SharedInstanceStatus{Engine: task.Engine, ShareID: task.SharedTrafficID,
			ConfigID: task.ConfigID, ConfigVersion: task.ConfigVersion, TaskID: taskID, AppliedTaskID: applied, Status: status, ObservedAt: time.Now().UTC()}
	}
	return db.CompleteTask(ctx, agentID, taskID, result)
}
