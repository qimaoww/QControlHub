package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestImportPreparationKeepsManualConfigurationAndHistory(t *testing.T) {
	db, ctx, admin, owner, shared := newConfigScopeAPIFixture(t)
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "import boundary", core.EngineXray)
	features := append(node.Features, core.AgentFeatureImportIsolation)
	heartbeat := func(available bool, reason string) {
		t.Helper()
		if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ExistingConfigAvailable: available, ExistingConfigUnsupportedReason: reason}}}); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat(false, "外部 Xray 配置未通过受保护路径与真实内核校验：permission denied")
	base := "/agents/" + node.ID + "/configs/xray"
	manual := `{"inbounds":[{"tag":"manual","listen":"127.0.0.1","port":21000,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}],"opaque":{"large":9007199254740993}}`
	var mutation configMutationResult
	owner.call("POST", base+"/source", map[string]any{"name": "manual", "content": manual, "intent": "deploy", "version": 0, "expected_instance_id": ""}, 200, &mutation)
	if !strings.Contains(mutation.Config.Content, "9007199254740993") || mutation.Task.ConfigVersion != mutation.Config.Version {
		t.Fatal("manual source or atomic task association changed")
	}
	manual = mutation.Config.Content
	completeConfigScopeAPITask(t, ctx, db, mutation.Task)
	// Source errors do not grant shared users host import rights.
	grantConfigScopeAPIAgent(t, ctx, db, node, shared, 21001)
	shared.call("POST", base+"/import", store.ConfigImportRequest{SnapshotTaskID: mutation.Task.ID, Name: "forbidden"}, http.StatusForbidden, nil)
	admin.call("GET", "/tasks/"+mutation.Task.ID, nil, http.StatusOK, nil)
	heartbeat(true, "")
	source := `{"inbounds":[{"tag":"imported","listen":"127.0.0.1","port":21002,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}],"opaque":{"large":9007199254740995}}`
	plan, err := serverconfig.PrepareIndependentEgress(core.EngineXray, source)
	if err != nil {
		t.Fatal(err)
	}
	var read core.Task
	owner.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: core.EngineXray, Action: core.ActionReadConfig}, 201, &read)
	claim, err := db.ClaimTask(ctx, node.ID)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if err = db.CompleteTask(ctx, node.ID, read.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: true, Output: source}); err != nil {
		t.Fatal(err)
	}
	request := store.ConfigImportRequest{SnapshotTaskID: read.ID, ExpectedVersion: mutation.Config.Version, Name: "imported"}
	var imported, repeated core.Task
	owner.call("POST", base+"/import", request, 201, &imported)
	owner.call("POST", base+"/import", request, 201, &repeated)
	if imported.ID != repeated.ID || !repeated.Reused {
		t.Fatal("duplicate import did not reuse exact intent")
	}
	assertManual := func() {
		t.Helper()
		cfg, err := db.AgentConfig(store.WithConfigScope(ctx, owner.userID, false), node.ID, core.EngineXray)
		if err != nil || cfg.Content != manual || cfg.Version != mutation.Config.Version {
			t.Fatalf("preparation changed manual configuration: %+v %v", cfg, err)
		}
	}
	assertManual()
	claim, err = db.ClaimTask(ctx, node.ID)
	if err != nil || claim == nil || claim.ID != imported.ID || claim.ConfigContent != plan.Content {
		t.Fatalf("import snapshot mismatch: %+v %v", claim, err)
	}
	if err = db.CompleteTask(ctx, node.ID, claim.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Error: "migration failed; rollback succeeded", InstanceStatus: &core.SharedInstanceStatus{Engine: core.EngineXray, TaskID: claim.ID, ConfigID: mutation.Config.ID, ConfigVersion: mutation.Config.Version, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	assertManual()
	var retry core.Task
	owner.call("POST", "/tasks/"+imported.ID+"/retry", nil, 201, &retry)
	assertManual()
	claim, err = db.ClaimTask(ctx, node.ID)
	if err != nil || claim == nil || claim.ConfigContent != plan.Content {
		t.Fatalf("retry lost pinned source: %+v %v", claim, err)
	}
	if err = db.CompleteTask(ctx, node.ID, claim.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := db.AgentConfig(store.WithConfigScope(ctx, owner.userID, false), node.ID, core.EngineXray)
	source, sourceErr := serverconfig.PresetAccountingSource(core.EngineXray, plan.Content)
	if err != nil || sourceErr != nil || cfg.Content != source || cfg.Version != mutation.Config.Version+1 {
		t.Fatalf("successful import not committed atomically: %+v %v", cfg, err)
	}
	var diagnostic core.TaskDiagnostic
	owner.call("GET", "/tasks/"+imported.ID+"/diagnostics", nil, 200, &diagnostic)
	if !strings.Contains(diagnostic.Error, "migration failed") {
		t.Fatal("successful retry erased previous failure")
	}
	owner.call("GET", "/tasks/"+retry.ID+"/diagnostics", nil, 200, &diagnostic)
	if diagnostic.RetryOf != imported.ID {
		t.Fatal("retry association lost")
	}
}

func TestPanelFailuresAreDurableScopedNonExecutableTasks(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	detail := "validation stage: field.port\n" + strings.Repeat("中文诊断/long_without_spaces_", 6000) + "\npassword=fixture-secret\nAuthorization: Bearer fixture-token\nlast-cause"
	input := core.PanelFailure{Operation: "配置字段校验", Stage: "form_validation", Summary: "端口校验失败", Detail: detail, Field: "port"}
	var first, second struct {
		ID string `json:"id"`
	}
	alice.call("POST", "/tasks/failures", input, 201, &first)
	alice.call("POST", "/tasks/failures", input, 201, &second)
	if first.ID == "" || second.ID != first.ID {
		t.Fatal("refresh failure deduplication failed")
	}
	var task core.Task
	alice.call("GET", "/tasks/"+first.ID, nil, 200, &task)
	if task.AgentID != "" || task.Engine != "" || task.Attempt != 0 || task.StartedAt != nil || task.Status != core.TaskFailed || task.Action != core.ActionPanelFailure {
		t.Fatalf("invented execution target: %+v", task)
	}
	bob.call("GET", "/tasks/"+first.ID+"/diagnostics", nil, 404, nil)
	admin.call("GET", "/tasks/"+first.ID+"/diagnostics", nil, 404, nil)
	alice.call("POST", "/tasks/"+first.ID+"/retry", nil, 409, nil)
	var diagnostic core.TaskDiagnostic
	alice.call("GET", "/tasks/"+first.ID+"/diagnostics", nil, 200, &diagnostic)
	if diagnostic.Error != core.DiagnosticText(detail) || strings.Contains(diagnostic.Output, "fixture-secret") || strings.Contains(diagnostic.Output, "fixture-token") || diagnostic.Occurrences != 2 {
		t.Fatalf("diagnostic len=%d expected=%d occurrences=%d secret=%t token=%t tail=%q", len(diagnostic.Error), len(core.DiagnosticText(detail)), diagnostic.Occurrences, strings.Contains(diagnostic.Output, "fixture-secret"), strings.Contains(diagnostic.Output, "fixture-token"), diagnostic.Error[max(0, len(diagnostic.Error)-180):])
	}
	var failure map[string]any
	alice.call("POST", "/tasks", map[string]string{"action": "invalid"}, 404, &failure)
	if failure["failure_saved"] != true || failure["failure_task_id"] == nil || failure["request_id"] == nil {
		t.Fatalf("pre-task API rejection was not retained: %+v", failure)
	}
	if _, err := db.GetTask(store.WithConfigScope(ctx, alice.userID, false), first.ID); err != nil {
		t.Fatal(err)
	}
	// A rejected retention request reports its own failure without creating a recursive task.
	var before, after []core.Task
	alice.call("GET", "/tasks?limit=500", nil, 200, &before)
	alice.call("POST", "/tasks/failures", core.PanelFailure{}, 400, nil)
	alice.call("GET", "/tasks?limit=500", nil, 200, &after)
	if len(before) != len(after) {
		t.Fatal("retention failure recursively created a task")
	}
}

func TestPanelFailureResourceRevocationHidesDiagnostics(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	input := core.PanelFailure{Operation: "共享实例配置校验", Stage: "form_validation", Summary: "校验失败", Detail: "private shared-instance diagnostic", AgentID: agent.ID, Engine: core.EngineMihomo}
	var record struct {
		ID string `json:"id"`
	}
	alice.call("POST", "/tasks/failures", input, 201, &record)
	var bound core.Task
	alice.call("GET", "/tasks/"+record.ID, nil, 200, &bound)
	if !bound.SharedInstance || bound.SharedTrafficID == "" || bound.AgentID != agent.ID || bound.Engine != input.Engine {
		t.Fatalf("failure lost real shared instance: %+v", bound)
	}
	alice.call("GET", "/tasks/"+record.ID+"/diagnostics", nil, 200, nil)
	bob.call("GET", "/tasks/"+record.ID+"/diagnostics", nil, 404, nil)
	access, err := db.UserAgentAccess(ctx, alice.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SetUserAgentAccess(ctx, alice.userID, core.AgentAccessRequest{Isolated: true, Revision: access.Revision}); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/tasks/"+record.ID, nil, 404, nil)
	alice.call("GET", "/tasks/"+record.ID+"?view=status", nil, 404, nil)
	alice.call("GET", "/tasks/"+record.ID+"/diagnostics", nil, 404, nil)
	var tasks []core.Task
	alice.call("GET", "/tasks", nil, 200, &tasks)
	for _, task := range tasks {
		if task.ID == record.ID {
			t.Fatal("revoked diagnostic remains in task list")
		}
	}
}

func TestChunkedTaskDiagnosticsSurviveSummaryLimitsAndLeaseRetry(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "diagnostic node", core.EngineXray)
	var task core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: core.EngineXray, Action: core.ActionStatus}, 201, &task)
	claim, err := db.ClaimTask(ctx, node.ID)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	full := core.TaskDiagnostic{Output: strings.Repeat("中文输出\n", 20000) + "output-tail", Error: "root failure\n" + strings.Repeat("nested_cause_without_spaces", 6000) + "\nrollback failed\nerror-tail\ntoken=secret-fixture"}
	content, _ := json.Marshal(full)
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	// Split JSON at ASCII boundaries; protocol accepts arbitrary UTF-8 chunk boundaries.
	parts := []string{}
	for len(content) > 0 {
		n := min(len(content), 16000)
		for n > 0 && content[n-1] >= 128 {
			n--
		}
		parts = append(parts, string(content[:n]))
		content = content[n:]
	}
	for i, part := range parts {
		if err = db.UploadTaskDiagnostic(ctx, node.ID, task.ID, core.TaskDiagnosticChunk{LeaseID: claim.LeaseID, Index: i, Count: len(parts), Digest: hash, Content: part}); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Output: "short summary", Error: "short error", DiagnosticDigest: hash}); err != nil {
		t.Fatal(err)
	}
	var d core.TaskDiagnostic
	alice.call("GET", "/tasks/"+task.ID+"/diagnostics", nil, 200, &d)
	if d.Output != core.DiagnosticText(full.Output) || d.Error != core.DiagnosticText(full.Error) || d.Source != "agent-diagnostic-v1" {
		t.Fatalf("diagnostic output=%d/%d error=%d/%d source=%q tail=%q", len(d.Output), len(core.DiagnosticText(full.Output)), len(d.Error), len(core.DiagnosticText(full.Error)), d.Source, d.Error[max(0, len(d.Error)-180):])
	}
	bob.call("GET", "/tasks/"+task.ID+"/diagnostics", nil, 404, nil)
	var retry core.Task
	alice.call("POST", "/tasks/"+task.ID+"/retry", nil, 201, &retry)
	completeConfigScopeAPITask(t, ctx, db, retry)
	alice.call("GET", "/tasks/"+task.ID+"/diagnostics", nil, 200, &d)
	if !strings.HasSuffix(d.Output, "output-tail") || !strings.Contains(d.Error, "error-tail") {
		t.Fatal("successful retry removed old evidence")
	}
}

// Failure records are durable task entries but must never enter scheduling.
func executionTasks(t *testing.T, tasks []core.Task) []core.Task {
	t.Helper()
	var result []core.Task
	for _, task := range tasks {
		if task.Action != core.ActionPanelFailure {
			result = append(result, task)
			continue
		}
		if task.Status != core.TaskFailed || task.Attempt != 0 || task.StartedAt != nil || task.LeaseID != "" {
			t.Fatalf("failure record acquired execution state: %+v", task)
		}
	}
	return result
}

func TestFailureRetentionDatabaseFaultPreservesOriginalResponse(t *testing.T) {
	base := os.Getenv("QCH_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	connection, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	_, err = connection.Exec(ctx, `CREATE FUNCTION refuse_diagnostic() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='panel-failure' THEN RAISE EXCEPTION 'fixture retention unavailable'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER refuse_diagnostic BEFORE INSERT ON tasks FOR EACH ROW EXECUTE FUNCTION refuse_diagnostic()`)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("f", 48)
	handler := New(db, Config{AdminToken: token}).Handler()
	check := func() {
		t.Helper()
		request := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"unknown_field":true}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var body struct {
			Error, Detail string
			Saved         bool   `json:"failure_saved"`
			Retention     string `json:"retention_error"`
			TaskID        string `json:"failure_task_id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != 400 || !strings.Contains(body.Detail, "unknown_field") || body.Saved || body.TaskID != "" || !strings.Contains(body.Retention, "未能保存到任务") {
			t.Fatalf("original failure replaced or false retention: %s", response.Body.String())
		}
	}
	check()
	var count int
	if err = connection.QueryRow(ctx, `SELECT count(*) FROM tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retention recursively scheduled: %d %v", count, err)
	}
	db.Close()
	check()
}
