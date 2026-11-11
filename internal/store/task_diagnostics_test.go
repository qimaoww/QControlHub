package store

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestRefreshFailureDeduplicatesRequestLabelsWithoutLosingNewCauses(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_refresh-owner", false)
	input := core.PanelFailure{Operation: "任务读取", Resource: "/tasks", Stage: "api_request", HTTPStatus: 502, Summary: "代理响应失败", RequestID: "proxy-first", Detail: "upstream unavailable\n（请求 ID：proxy-first）"}
	first, err := db.RecordPanelFailure(owner, input)
	if err != nil {
		t.Fatal(err)
	}
	input.RequestID, input.Detail = "proxy-second", "upstream unavailable\n（请求 ID：proxy-second）"
	second, err := db.RecordPanelFailure(owner, input)
	if err != nil || first != second {
		t.Fatalf("request label created another task: %s %s %v", first, second, err)
	}
	diagnostic, err := db.TaskDiagnostic(owner, first)
	if err != nil || diagnostic.Error != "upstream unavailable\n（请求 ID：proxy-first）" || diagnostic.Occurrences != 2 || !strings.Contains(diagnostic.Output, "proxy-first") {
		t.Fatalf("original request or occurrence lost: %+v %v", diagnostic, err)
	}
	input.Detail = "upstream certificate expired\n（请求 ID：proxy-second）"
	changed, err := db.RecordPanelFailure(owner, input)
	if err != nil || changed == first {
		t.Fatalf("new cause was merged into old failure: %s %v", changed, err)
	}
}

func TestLeaseRecoveryPreservesEachDiagnosticAttempt(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	setTaskStartedAt(t, ctx, db, task.ID, time.Now().Add(-time.Hour))
	if err := db.RequeueStaleTasks(ctx, time.Minute, time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	second, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || second == nil || second.Attempt != 2 {
		t.Fatalf("second lease: %+v %v", second, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: second.LeaseID, ExecutedAttempt: 1, Success: true, Output: "cached original execution result"}); err != nil {
		t.Fatal(err)
	}
	current, err := db.TaskDiagnostic(ctx, task.ID)
	if err != nil || len(current.Attempts) != 2 || current.Attempts[1].ExecutedAttempt != 1 || current.Output != "cached original execution result" {
		t.Fatalf("current diagnostic: %+v %v", current, err)
	}
	prior, err := db.TaskDiagnostic(ctx, task.ID, 1)
	if err != nil || prior.Source != "lease-expired" || !strings.Contains(prior.Error, "结果不确定") {
		t.Fatalf("prior diagnostic lost after success: %+v %v", prior, err)
	}
	if _, err := db.TaskDiagnostic(ctx, task.ID, 3); err == nil {
		t.Fatal("invented attempt accepted")
	}
}

func TestPanelFailureDeduplicationBoundaries(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_dedup-owner", false)
	input := core.PanelFailure{Operation: "POST /configs", Resource: "/configs", Stage: "validation", HTTPStatus: 400, Code: "invalid_port", Field: "port", Summary: "校验失败", Detail: "invalid value"}
	ids := make(chan string, 20)
	errs := make(chan error, 20)
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() { defer group.Done(); id, err := db.RecordPanelFailure(owner, input); ids <- id; errs <- err }()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("same concurrent cause created multiple records")
		}
	}
	diagnostic, err := db.TaskDiagnostic(owner, first)
	if err != nil || diagnostic.Occurrences != 20 {
		t.Fatalf("lost occurrence count: %+v %v", diagnostic, err)
	}
	for _, different := range []core.PanelFailure{
		{Operation: input.Operation, Resource: input.Resource, Stage: input.Stage, HTTPStatus: 400, Code: "permission_denied", Field: input.Field, Summary: input.Summary, Detail: input.Detail},
		{Operation: input.Operation, Resource: input.Resource, Stage: input.Stage, HTTPStatus: 400, Code: input.Code, Field: "tls_public_port", Summary: input.Summary, Detail: input.Detail},
	} {
		id, err := db.RecordPanelFailure(owner, different)
		if err != nil || id == first {
			t.Fatalf("different cause/field merged: %s %v", id, err)
		}
	}
	other, err := db.RecordPanelFailure(WithConfigScope(ctx, "token_dedup-other", false), input)
	if err != nil || other == first {
		t.Fatalf("owners merged: %s %v", other, err)
	}
	if _, err = db.pool.Exec(ctx, "UPDATE tasks SET created_at=now()-interval '6 minutes' WHERE id=$1", first); err != nil {
		t.Fatal(err)
	}
	next, err := db.RecordPanelFailure(owner, input)
	if err != nil || next == first {
		t.Fatalf("original five-minute window was widened: %s %v", next, err)
	}
}
