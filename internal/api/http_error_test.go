package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestStoreSchemaErrorResponseAndSafeCorrelationLog(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	pgError := &pgconn.PgError{Code: "23514", ConstraintName: "tasks_check", TableName: "tasks", SchemaName: "fixture", Message: "private SQL input password", Detail: "private SQL parameters and postgresql://secret:password@db"}
	err := fmt.Errorf("wrapped: %w", &store.TaskSchemaError{AgentID: "agt_fixture", Action: core.ActionIPQuality, Cause: pgError})
	recorder := httptest.NewRecorder()
	writeStoreError(recorder, err)
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || body["code"] != "task_schema_incompatible" || body["stage"] != "task_creation" || !strings.Contains(body["error"], "尚未进入 Agent") {
		t.Fatalf("response=%d %v", recorder.Code, body)
	}
	id := body["request_id"]
	if id == "" || recorder.Header().Get("X-Request-ID") != id {
		t.Fatal("missing correlation ID")
	}
	var log map[string]any
	if err := json.Unmarshal(logs.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	if log["request_id"] != id || log["sqlstate"] != "23514" || log["constraint"] != "tasks_check" || log["agent_id"] != "agt_fixture" || log["action"] != "ip-quality" || log["stage"] != "task_creation" {
		t.Fatalf("diagnostic log=%v", log)
	}
	for _, private := range []string{"private SQL", "postgresql://", "password"} {
		if strings.Contains(recorder.Body.String()+logs.String(), private) {
			t.Fatalf("error leaked %s", private)
		}
	}
	// A check violation outside the validated task insert is still an ordinary
	// server failure. It must never receive the migration-specific business code.
	for _, constraint := range []string{"tasks_status_check", "tasks_install_if_missing_check", "reports_result_check", "tasks_check"} {
		response := httptest.NewRecorder()
		writeStoreError(response, &pgconn.PgError{Code: "23514", TableName: "tasks", ConstraintName: constraint})
		if strings.Contains(response.Body.String(), "task_schema_incompatible") || response.Code != 500 {
			t.Fatalf("overclassified %s: %s", constraint, response.Body.String())
		}
	}
}
