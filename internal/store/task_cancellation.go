package store

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) CancelTask(ctx context.Context, id string) error {
	args := []any{id}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	ownerWhere += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	command, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status='canceled',error='canceled by administrator',finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE id=$1 AND status='pending'`+ownerWhere, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1`+ownerWhere+`)`, args...).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return fmt.Errorf("%w: only pending tasks can be canceled", ErrConflict)
}

func (s *Store) RetryTask(ctx context.Context, id string) (core.Task, error) {
	previous, err := s.GetTask(ctx, id)
	if err != nil {
		return core.Task{}, err
	}
	if previous.Status != core.TaskFailed && previous.Status != core.TaskCanceled {
		return core.Task{}, fmt.Errorf("%w: only failed or canceled tasks can be retried", ErrConflict)
	}
	var transition bool
	if err := s.pool.QueryRow(ctx, `SELECT capability_transition FROM tasks WHERE id=$1`, id).Scan(&transition); err != nil {
		return core.Task{}, err
	}
	if transition {
		change, err := s.ChangeAgentEngineCapability(ctx, previous.AgentID, previous.Engine, previous.Action == core.ActionStart)
		if err != nil {
			return core.Task{}, err
		}
		if change.TaskID == "" {
			return core.Task{}, fmt.Errorf("%w: 节点能力已更新，无需重试启停任务", ErrConflict)
		}
		return s.GetTask(ctx, change.TaskID)
	}
	expectedVersion := 0
	if previous.InstallIfMissing {
		// Never silently deploy a newer draft when retrying a partially
		// completed install + configuration operation.
		expectedVersion = previous.ConfigVersion
	}
	return s.CreateTask(ctx, core.TaskRequest{
		InstallIfMissing: previous.InstallIfMissing, ExpectedConfigVersion: expectedVersion,
		TCPSettings: previous.TCPSettings,
		AgentID:     previous.AgentID, Action: previous.Action, Engine: previous.Engine,
		ConfigID: previous.ConfigID, CoreVersion: previous.CoreVersion, CoreSource: previous.CoreSource,
	})
}
