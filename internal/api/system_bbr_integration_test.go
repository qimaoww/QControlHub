package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestSystemTCPAPIAndTaskLifecycle(t *testing.T) {
	url := os.Getenv("QCH_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	credential, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "system TCP"})
	if err != nil {
		t.Fatal(err)
	}
	agent := enrollTaskAPIAgent(t, ctx, db, credential.Token, "external-bbr")
	server := New(db, Config{AdminToken: "tcp-admin", OperatorTokens: []string{"tcp-operator"}, ReadonlyTokens: []string{"tcp-reader"}})
	handler := server.Handler()
	call := func(method, path, token string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(encoded))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	input := core.TaskRequest{AgentID: agent.ID, Action: core.ActionConfigureTCP, TCPSettings: core.TCPSettings{"net.ipv4.tcp_ecn": "1", "net.ipv4.tcp_rmem": "4096\t131072 33554432"}}
	call("GET", "/system-tcp/parameters", "tcp-reader", nil, 200)
	call("POST", "/tasks", "tcp-reader", input, 403)
	call("POST", "/tasks", "tcp-operator", input, 403)
	call("POST", "/tasks", "tcp-admin", input, 409) // old Agent lacks the feature
	metrics := core.HostMetrics{BBR: &core.SystemBBRStatus{
		Available: true, CollectedAt: time.Now().UTC(), CongestionControl: "bbr", DefaultQdisc: "fq_codel", Persistence: "unmanaged",
		Parameters: map[string]string{"net.ipv4.tcp_congestion_control": "bbr", "net.ipv4.tcp_ecn": "2"},
	}}
	beat := core.HeartbeatRequest{Features: []string{core.AgentFeatureSystemBBR}, Metrics: &metrics}
	if err := db.Heartbeat(ctx, agent.ID, beat); err != nil {
		t.Fatal(err)
	}
	var agents []core.Agent
	if err := json.Unmarshal(call("GET", "/agents", "tcp-reader", nil, 200).Body.Bytes(), &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Metrics.BBR.CongestionControl != "bbr" || agents[0].Metrics.BBR.Persistence != "unmanaged" {
		t.Fatalf("external BBR lost: %+v", agents)
	}
	for _, bad := range []core.TaskRequest{
		{AgentID: agent.ID, Action: core.ActionConfigureTCP, TCPSettings: core.TCPSettings{"kernel.sysrq": "1"}},
		{AgentID: agent.ID, Action: core.ActionConfigureTCP, Engine: core.EngineMihomo, TCPSettings: input.TCPSettings},
		{AgentID: agent.ID, Action: core.ActionEnableBBR, TCPSettings: input.TCPSettings},
		{AgentID: agent.ID, Action: core.ActionConfigureTCP},
	} {
		call("POST", "/tasks", "tcp-admin", bad, 400)
	}
	var task core.Task
	if err := json.Unmarshal(call("POST", "/tasks", "tcp-admin", input, 201).Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	normalized, _ := core.NormalizeTCPSettings(input.TCPSettings)
	if !reflect.DeepEqual(task.TCPSettings, normalized) {
		t.Fatal(task)
	}
	call("POST", "/tasks", "tcp-admin", input, 200) // identical active request is idempotent
	call("POST", "/tasks", "tcp-admin", core.TaskRequest{AgentID: agent.ID, Action: core.ActionEnableBBR}, 409)
	fetched, err := db.GetTask(ctx, task.ID)
	if err != nil || !reflect.DeepEqual(fetched.TCPSettings, normalized) {
		t.Fatalf("stored TCP payload: %+v %v", fetched, err)
	}
	listed, err := db.ListTasksFiltered(ctx, agent.ID, "", core.ActionConfigureTCP, 10)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0].TCPSettings, normalized) {
		t.Fatalf("listed TCP payload: %+v %v", listed, err)
	}
	// A downgraded/newly reconnected legacy Agent must never claim new actions.
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("legacy claim: %+v %v", claimed, err)
	}
	if err := db.Heartbeat(ctx, agent.ID, beat); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || !reflect.DeepEqual(claimed.TCPSettings, normalized) {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	running, err := db.RunningTask(ctx, agent.ID)
	if err != nil || running == nil || !reflect.DeepEqual(running.TCPSettings, normalized) {
		t.Fatalf("reconnect: %+v %v", running, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: running.LeaseID, Error: "simulated write failure"}); err != nil {
		t.Fatal(err)
	}
	call("POST", "/tasks/"+task.ID+"/retry", "tcp-operator", nil, 403)
	var retried core.Task
	if err := json.Unmarshal(call("POST", "/tasks/"+task.ID+"/retry", "tcp-admin", nil, 201).Body.Bytes(), &retried); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retried.TCPSettings, normalized) {
		t.Fatal("retry lost saved TCP selection")
	}
	if _, err := db.ClaimTask(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	if resumed, err := db.RunningTask(ctx, agent.ID); err != nil || resumed != nil {
		t.Fatalf("downgraded resume: %+v %v", resumed, err)
	}
	failed, _ := db.GetTask(ctx, retried.ID)
	if failed.Status != core.TaskFailed {
		t.Fatal("incompatible running task not failed")
	}
	// Mutating offline nodes must fail, not unexpectedly execute days later.
	connection, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	// One busy node must remain visible even after another node has produced
	// more than a global history page's worth of completed tuning tasks.
	var pending core.Task
	if err := db.Heartbeat(ctx, agent.ID, beat); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(call("POST", "/tasks", "tcp-admin", input, 201).Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	_, err = connection.Exec(ctx, `
		INSERT INTO agents(id,name,version,os,arch,capabilities,features,public_key,last_seen,enrolled_at)
		VALUES('agt_tcp_history','history','test','linux','amd64','[]','["system-bbr-v1"]',decode(repeat('02',32),'hex'),now(),now());
		INSERT INTO tasks(id,agent_id,action,engine,status,created_at,output)
		SELECT 'tsk_tcp_history_' || i,'agt_tcp_history','enable-bbr','','succeeded',now()+i*interval '1 second','full output belongs on the task page'
		FROM generate_series(1,200) i;
	`)
	if err != nil {
		t.Fatal(err)
	}
	readLatest := func(path string) []core.Task {
		t.Helper()
		var tasks []core.Task
		if err := json.Unmarshal(call("GET", path, "tcp-reader", nil, 200).Body.Bytes(), &tasks); err != nil {
			t.Fatal(err)
		}
		return tasks
	}
	latest := readLatest("/system-tcp/tasks")
	if len(latest) != 2 {
		t.Fatalf("latest TCP task set: %+v", latest)
	}
	for _, task := range latest {
		if task.Output != "" || (task.AgentID == agent.ID && (task.ID != pending.ID || task.Status != core.TaskPending || !reflect.DeepEqual(task.TCPSettings, normalized))) ||
			(task.AgentID == "agt_tcp_history" && task.ID != "tsk_tcp_history_200") {
			t.Fatalf("incorrect latest TCP task: %+v", task)
		}
	}
	if filtered := readLatest("/system-tcp/tasks?agent_id=" + agent.ID); len(filtered) != 1 || filtered[0].ID != pending.ID {
		t.Fatalf("node filter: %+v", filtered)
	}
	if missing := readLatest("/system-tcp/tasks?agent_id=nonexistent"); len(missing) != 0 {
		t.Fatal("unknown node returned another node's tasks")
	}
	call("GET", "/system-tcp/tasks", "invalid-token", nil, 401)
	if _, err := connection.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id='agt_tcp_history'`); err != nil {
		t.Fatal(err)
	}
	if latest = readLatest("/system-tcp/tasks"); len(latest) != 1 || latest[0].ID != pending.ID {
		t.Fatalf("revoked node visible: %+v", latest)
	}
	if _, err := connection.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '1 day' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	call("POST", "/tasks", "tcp-admin", input, 409)
}
