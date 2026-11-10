package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTaskFailureSurvivesStoreRestartWithoutEnteringScheduling(t *testing.T) {
	db, ctx, url := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_diagnostic-owner", false)
	original := "字段校验失败\n" + strings.Repeat("中文_without_spaces_", 10000) + "\npassword=private-fixture\nlast-cause"
	id, err := db.RecordPanelFailure(owner, core.PanelFailure{Operation: "配置校验", Stage: "form_validation", Summary: "校验失败", Detail: original})
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.Overview(ctx)
	if err != nil || before.TasksFailed != 0 {
		t.Fatalf("diagnostic changed executable statistics: %+v %v", before, err)
	}
	db.Close()
	reopened, err := OpenWithConfigKey(ctx, url, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	diagnostic, err := reopened.TaskDiagnostic(owner, id)
	if err != nil || diagnostic.Error != core.DiagnosticText(original) {
		t.Fatalf("diagnostic changed after restart: %v", err)
	}
	if strings.Contains(diagnostic.Output, "last-cause") {
		t.Fatal("metadata duplicates the full diagnostic body")
	}
	task, err := reopened.GetTask(owner, id)
	if err != nil || task.AgentID != "" || task.Attempt != 0 || task.Status != core.TaskFailed || task.StartedAt != nil {
		t.Fatalf("false execution identity: %+v %v", task, err)
	}
	if _, err = reopened.RetryTask(owner, id); err == nil {
		t.Fatal("failure record entered execution retry")
	}
}

func TestSafeFailureDiagnosticNeverExposesDatabaseRows(t *testing.T) {
	pg := &pgconn.PgError{Code: "23514", Message: "private-row-value", Detail: "password=fixture-private", SchemaName: "public", TableName: "configs", ConstraintName: "configs_guard"}
	detail := SafeFailureDiagnostic(fmt.Errorf("save configuration: %w", pg))
	if strings.Contains(detail, "private") || !strings.Contains(detail, "save configuration") || !strings.Contains(detail, "SQLSTATE=23514") || !strings.Contains(detail, "configs_guard") {
		t.Fatalf("unsafe or missing diagnostic context: %s", detail)
	}
}
