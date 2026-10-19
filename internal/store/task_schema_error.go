package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// TaskSchemaError means a validated task was rejected by a known action/engine
// schema rule. Other check violations keep their original error semantics.
type TaskSchemaError struct {
	AgentID string
	Action  core.Action
	Cause   error
}

func (e *TaskSchemaError) Error() string {
	var pgError *pgconn.PgError
	if errors.As(e.Cause, &pgError) {
		return fmt.Sprintf("create %s task for %s: incompatible task schema (SQLSTATE %s, constraint %s)",
			e.Action, e.AgentID, pgError.Code, pgError.ConstraintName)
	}
	return "task creation failed: incompatible task schema"
}
func (e *TaskSchemaError) Unwrap() error { return e.Cause }

func taskInsertError(err error, task core.Task) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23514" && pgError.TableName == "tasks" {
		switch pgError.ConstraintName {
		case "tasks_check", "tasks_action_check", "tasks_engine_check":
			return &TaskSchemaError{AgentID: task.AgentID, Action: task.Action, Cause: err}
		}
	}
	return mapError(err)
}
