package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTaskInsertSchemaErrorClassification(t *testing.T) {
	for _, test := range []struct {
		code, table, constraint string
		want                    bool
	}{
		{"23514", "tasks", "tasks_check", true},
		{"23514", "tasks", "tasks_action_check", true},
		{"23514", "tasks", "tasks_engine_check", true},
		{"23514", "tasks", "tasks_install_if_missing_check", false},
		{"23514", "tasks", "tasks_status_check", false},
		{"23514", "reports", "tasks_check", false},
		{"23505", "tasks", "tasks_check", false},
	} {
		original := &pgconn.PgError{Code: test.code, TableName: test.table, ConstraintName: test.constraint}
		err := taskInsertError(original, core.Task{AgentID: "agent", Action: core.ActionIPQuality})
		var schemaError *TaskSchemaError
		if errors.As(err, &schemaError) != test.want {
			t.Errorf("%+v: %v", test, err)
		}
		if test.want && (!errors.Is(err, original) || schemaError.AgentID != "agent" || schemaError.Action != core.ActionIPQuality) {
			t.Fatal("lost original diagnostics/context")
		}
	}
}
