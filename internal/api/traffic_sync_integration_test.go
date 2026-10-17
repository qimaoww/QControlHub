package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestTrafficSyncPreservesPortsThroughReconnectWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "traffic sync"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name: "traffic sync", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, Features: []string{core.AgentFeatureIndependentEgress},
		PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)),
	}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}

	const token = "traffic-sync-test-admin-token"
	server := New(db, Config{AdminToken: token})
	handler := server.Handler()
	call := func(method, body string) (int, []byte) {
		t.Helper()
		request := httptest.NewRequest(method, "/api/v1/traffic-endpoints/sync", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Header().Get("Cache-Control") != "no-store" && response.Code == http.StatusOK {
			t.Fatal("cacheable scan")
		}
		return response.Code, response.Body.Bytes()
	}
	preview := func() store.TrafficSyncPreview {
		t.Helper()
		code, body := call(http.MethodGet, "")
		var p store.TrafficSyncPreview
		if err := json.Unmarshal(body, &p); err != nil || code != 200 {
			t.Fatalf("preview %d %s %v", code, body, err)
		}
		return p
	}
	sync := func(snapshot string, port int) int {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"snapshot": snapshot, "selections": []core.TrafficSyncSelection{{AgentID: agent.ID, Port: port}}})
		code, _ := call(http.MethodPost, string(body))
		return code
	}
	c, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "listener", Content: `{"inbounds":[{"tag":"stable","port":8443,"protocol":"vless"}],"outbounds":[{"protocol":"freedom"}]}`}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pending := preview()
	if len(pending.Candidates) != 1 || pending.Candidates[0].DeploymentStatus != "pending" || sync(pending.Snapshot, 8443) != 409 {
		t.Fatalf("undeployed scan: %+v", pending)
	}
	deploy := func() {
		t.Helper()
		task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil {
			t.Fatalf("claim: %+v %v", lease, err)
		}
		if err = db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}); err != nil {
			t.Fatal(err)
		}
	}
	deploy()
	p := preview()
	if len(p.Endpoints) != 1 || p.Endpoints[0].Port != 8443 {
		t.Fatalf("applied scan: %+v", p)
	}
	if sync(p.Snapshot, 8443) != 200 || sync(p.Snapshot, 8443) != 200 {
		t.Fatal("sync is not idempotent")
	}
	c.Content = strings.ReplaceAll(c.Content, "8443", "9443")
	c, err = db.SaveAgentConfig(ctx, c, 1)
	if err != nil {
		t.Fatal(err)
	}
	if sync(p.Snapshot, 8443) != 409 {
		t.Fatal("stale config scan accepted")
	}
	server.refreshSavedAgentTrafficMonitoring(ctx, agent.ID)
	server.refreshPortTrafficMonitoring(ctx, agent.ID)
	active, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(active) != 1 || active[0].Port != 8443 {
		t.Fatalf("draft/reconnect switched rules: %+v %v", active, err)
	}
	deploy()
	p = preview()
	if len(p.Endpoints) != 1 || p.Endpoints[0].Port != 9443 || sync(p.Snapshot, 9443) != 200 {
		t.Fatalf("replacement scan: %+v", p)
	}
	if sync(p.Snapshot, 8443) != 409 {
		t.Fatal("retired port reactivated")
	}
	for _, body := range []string{"", `{}`, `{"selections":[]}`, `{"snapshot":"bad","selections":[{"agent_id":"` + agent.ID + `","port":9443,"name":"untrusted"}]}`} {
		if code, _ := call(http.MethodPost, body); code != 400 {
			t.Fatalf("invalid body accepted: %s %d", body, code)
		}
	}
}
