package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) ListTasks(ctx context.Context, agentID string, limit int) ([]core.Task, error) {
	return s.ListTasksFiltered(ctx, agentID, "", "", limit)
}

func (s *Store) ListTasksFiltered(ctx context.Context, agentID string, status core.TaskStatus, action core.Action, limit int) ([]core.Task, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	// Keep optional filters out of the SQL entirely when unused. An OR against
	// a parameter can hide selective indexes once pgx reuses a generic plan.
	where := "true"
	args := make([]any, 0, 4)
	for _, filter := range []struct{ column, value string }{
		{"agent_id", agentID}, {"status", string(status)}, {"action", string(action)},
	} {
		if filter.value != "" {
			args = append(args, filter.value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
		}
	}
	where += ownerClause(ctx, "owner_id", &args)
	where += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing,shared_instance
		FROM tasks
		WHERE `+where+fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]core.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows, false)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, id string) (core.Task, error) {
	return s.getTask(ctx, id, false)
}

// GetTaskState omits the potentially large execution log from frequent
// status polls. The full task endpoint still returns that log on demand.
func (s *Store) GetTaskState(ctx context.Context, id string) (core.Task, error) {
	return s.getTask(ctx, id, true)
}

func (s *Store) getTask(ctx context.Context, id string, stateOnly bool) (core.Task, error) {
	output := "COALESCE(output,'')"
	if stateOnly {
		output = "''"
	}
	args := []any{id}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	ownerWhere += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	row := s.pool.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       `+output+`,COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,install_if_missing,shared_instance
		FROM tasks WHERE id=$1`+ownerWhere, args...)
	task, err := scanTask(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, ErrNotFound
	}
	return task, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner, includeContent bool) (core.Task, error) {
	var task core.Task
	var err error
	var tcpSettingsJSON []byte
	if includeContent {
		var mainlandPoliciesJSON []byte
		err = row.Scan(&task.ID, &task.AgentID, &task.Action, &task.Engine, &task.ConfigID, &task.ConfigVersion,
			&task.ConfigContent, &mainlandPoliciesJSON, &task.CoreVersion, &task.CoreSource, &task.Status, &task.Attempt, &task.LeaseID, &task.Output, &task.Error,
			&task.CreatedAt, &task.StartedAt, &task.FinishedAt, &tcpSettingsJSON, &task.SharedTrafficID, &task.CNIPSource, &task.InstallIfMissing, &task.SharedInstance)
		if err == nil && len(mainlandPoliciesJSON) > 0 {
			err = json.Unmarshal(mainlandPoliciesJSON, &task.MainlandAccessPolicies)
		}
	} else {
		err = row.Scan(&task.ID, &task.AgentID, &task.Action, &task.Engine, &task.ConfigID, &task.ConfigVersion,
			&task.CoreVersion, &task.CoreSource, &task.Status, &task.Attempt, &task.Output, &task.Error,
			&task.CreatedAt, &task.StartedAt, &task.FinishedAt, &tcpSettingsJSON, &task.InstallIfMissing, &task.SharedInstance)
	}
	if err == nil && len(tcpSettingsJSON) > 0 {
		err = json.Unmarshal(tcpSettingsJSON, &task.TCPSettings)
	}
	return task, err
}
