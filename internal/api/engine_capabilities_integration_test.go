package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestAgentEngineCapabilityAPI(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.InitializeDefaultAgentEngines(ctx, []core.Engine{}); err != nil {
		t.Fatal(err)
	}
	credential, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "capabilities", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "capabilities", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	admin, reader := strings.Repeat("a", 48), strings.Repeat("r", 48)
	server := New(db, Config{AdminToken: admin})
	server.roleTokens[sha256.Sum256([]byte(reader))] = tokenPrincipal{Role: core.RoleUser, Permissions: []core.Permission{core.PermissionAgentsRead}}
	handler := server.Handler()
	put := func(id, engine, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id+"/capabilities/"+engine, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		id, engine, token, body string
		status                  int
	}{
		{agent.ID, "xray", "", `{"enabled":true}`, 401},
		{agent.ID, "xray", reader, `{"enabled":true}`, 403},
		{agent.ID, "xray", admin, `{}`, 400},
		{agent.ID, "xray", admin, `{"enabled":null}`, 400},
		{agent.ID, "xray", admin, `{"enabled":"true"}`, 400},
		{agent.ID, "invalid", admin, `{"enabled":true}`, 400},
		{"missing", "xray", admin, `{"enabled":true}`, 404},
		{agent.ID, "xray", admin, `{"enabled":true}`, 200},
		{agent.ID, "xray", admin, `{"enabled":true}`, 200},
		{agent.ID, "xray", admin, `{"enabled":false}`, 200},
	} {
		got := put(tc.id, tc.engine, tc.token, tc.body)
		if got.Code != tc.status {
			t.Fatalf("%s %s: %d %s, want %d", tc.engine, tc.body, got.Code, got.Body.String(), tc.status)
		}
	}
	got, err := db.GetAgent(ctx, agent.ID)
	if err != nil || len(got.Capabilities) != 0 || len(got.SupportedCapabilities) != 4 {
		t.Fatalf("saved capability state: %+v %v", got, err)
	}
	assertUnsafeSwitchRejected := func(body string, enabled bool) {
		t.Helper()
		const reason = "wrapper 不在安全支持范围"
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {ServiceStatus: "active", ExistingConfigUnsupportedReason: reason}}}); err != nil {
			t.Fatal(err)
		}
		response := put(agent.ID, "xray", admin, body)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), reason) {
			t.Fatalf("unsafe switch response: %d %s", response.Code, response.Body.String())
		}
		got, err := db.GetAgent(ctx, agent.ID)
		if err != nil || (len(got.Capabilities) == 1) != enabled {
			t.Fatalf("unsafe switch changed capability: %+v %v", got, err)
		}
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}}}); err != nil {
			t.Fatal(err)
		}
	}
	assertUnsafeSwitchRejected(`{"enabled":true}`, false)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "inactive"}}}); err != nil {
		t.Fatal(err)
	}
	response := put(agent.ID, "xray", admin, `{"enabled":true}`)
	var change store.EngineCapabilityChange
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &change) != nil || change.Enabled || change.TaskID == "" {
		t.Fatalf("installed start response: %d %s", response.Code, response.Body.String())
	}
	if response := put(agent.ID, "xray", admin, `{"enabled":true}`); response.Code != http.StatusConflict {
		t.Fatalf("overlapping start: %d %s", response.Code, response.Body.String())
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.Action != core.ActionStart {
		t.Fatalf("start dispatch: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	assertUnsafeSwitchRejected(`{"enabled":false}`, true)
	response = put(agent.ID, "xray", admin, `{"enabled":false}`)
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &change) != nil || !change.Enabled || change.TaskID == "" {
		t.Fatalf("installed stop response: %d %s", response.Code, response.Body.String())
	}
	claimed, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.Action != core.ActionStop {
		t.Fatalf("stop dispatch: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if got := put(agent.ID, "xray", admin, `{"enabled":true}`); got.Code != 404 {
		t.Fatalf("revoked node: %d", got.Code)
	}
}
