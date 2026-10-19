package store

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) RetryEditor(ctx context.Context, id string) (core.Task, core.Config, error) {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return core.Task{}, core.Config{}, err
	}
	if (task.Status != core.TaskFailed && task.Status != core.TaskCanceled) || (task.Action != core.ActionValidate && task.Action != core.ActionDeploy) || task.ConfigID == "" {
		return core.Task{}, core.Config{}, fmt.Errorf("%w: only failed or canceled configuration validation/deployment tasks support edited retry", ErrConflict)
	}
	if _, err := sharedTaskRetryOwner(ctx, s.pool, task); err != nil {
		return core.Task{}, core.Config{}, err
	}
	configs, err := s.ListConfigRevisions(ctx, task.ConfigID, 1)
	if err != nil {
		return core.Task{}, core.Config{}, err
	}
	if len(configs) == 0 {
		return core.Task{}, core.Config{}, ErrNotFound
	}
	if configs[0].Engine != task.Engine {
		return core.Task{}, core.Config{}, fmt.Errorf("%w: configuration engine changed", ErrConflict)
	}
	return task, configs[0], nil
}

// SaveEditedRetry commits exactly one new revision and one immutable task, or
// neither. Configuration identity and task intent come from the scoped record.
func (s *Store) SaveEditedRetry(ctx context.Context, id, content string, version int) (core.Config, core.Task, error) {
	previous, input, err := s.RetryEditor(ctx, id)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	if input.Version != version {
		return core.Config{}, core.Task{}, fmt.Errorf("%w: 配置已变化，请重新打开编辑后重试", ErrConflict)
	}
	input.Content = content
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	defer tx.Rollback(ctx)
	shareID, err := sharedTaskRetryOwner(ctx, tx, previous)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	var configIDs []string
	if scopeForConfig(ctx).Admin {
		configIDs = append(configIDs, input.ID)
	}
	// Lock an administrator's foreign configuration owner before the edit,
	// matching task submission and purge's account-before-resource order.
	if err = lockAgentUser(ctx, tx, configIDs...); err != nil {
		return core.Config{}, core.Task{}, err
	}
	permission := core.PermissionConfigsWrite
	if input.AgentID != "" {
		permission = core.PermissionAgentConfigWrite
	}
	if !scopeForConfig(ctx).Admin {
		readPermission := core.PermissionConfigsRead
		if input.AgentID != "" {
			readPermission = core.PermissionAgentConfigRead
		}
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM panel_users WHERE id=$1 AND (disabled OR (role<>'admin' AND (NOT permissions ? $2 OR NOT permissions ? $3))))`, scopeForConfig(ctx).OwnerID, permission, readPermission).Scan(&allowed); err != nil {
			return core.Config{}, core.Task{}, err
		}
		if !allowed {
			return core.Config{}, core.Task{}, ErrForbidden
		}
	}
	var saved core.Config
	if input.AgentID != "" {
		saved, err = s.saveAgentConfigTx(ctx, tx, input, version, nil)
	} else {
		input.Version = version
		saved, err = s.updateConfigTx(ctx, tx, input.ID, input)
	}
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	task, err := s.createTaskTx(ctx, tx, core.TaskRequest{AgentID: previous.AgentID, Engine: previous.Engine, Action: previous.Action, ConfigID: saved.ID, ExpectedConfigVersion: saved.Version, InstallIfMissing: previous.InstallIfMissing}, 0)
	if err != nil {
		return core.Config{}, core.Task{}, err
	}
	if previous.SharedInstance && (!task.SharedInstance || task.SharedTrafficID != shareID) {
		return core.Config{}, core.Task{}, fmt.Errorf("%w: 共享实例已变化，请从本人配置重新提交操作", ErrConflict)
	}
	if err = tx.Commit(ctx); err != nil {
		return core.Config{}, core.Task{}, err
	}
	s.signalTaskReady(task.AgentID)
	return saved, task, nil
}
