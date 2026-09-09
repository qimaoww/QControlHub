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
		Name: "traffic sync", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray},
		PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)),
	}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, []core.PortTrafficEndpoint{{
		AgentID: agent.ID, Engine: core.EngineXray, Name: "stale", Port: 443, Protocol: core.TrafficProtocolTCP,
	}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Engine: core.EngineXray, Name: "new listener",
		Content: `{"inbounds":[{"tag":"new","port":8443,"protocol":"vless"}],"outbounds":[{"protocol":"freedom"}]}`,
	}, 0); err != nil {
		t.Fatal(err)
	}
	const token = "traffic-sync-test-admin-token"
	server := New(db, Config{AdminToken: token})
	handler := server.Handler()
	sync := func() []string {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/traffic-endpoints/sync", strings.NewReader(`{"selections":[{"agent_id":"`+agent.ID+`","port":8443}]}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var result struct {
			ChangedAgents []string `json:"changed_agents"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("sync: %d %s, %v", response.Code, response.Body.String(), err)
		}
		return result.ChangedAgents
	}
	if changed := sync(); len(changed) != 1 || changed[0] != agent.ID {
		t.Fatalf("changed agents = %v", changed)
	}
	server.refreshPortTrafficMonitoring(ctx, agent.ID)
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 || policies[0].Port != 443 || policies[1].Port != 8443 {
		t.Fatalf("manual sync/reconnect deleted a port: %+v, %v", policies, err)
	}
	if changed := sync(); len(changed) != 0 {
		t.Fatalf("idempotent sync reconnects agents: %v", changed)
	}
	// Configuration-change refreshes still remove stale discovered monitors.
	server.refreshPortTrafficMonitoring(ctx, "")
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 8443 {
		t.Fatalf("configuration refresh did not prune: %+v, %v", policies, err)
	}
	if _, err := db.DeletePortTrafficMonitoring(ctx, policies[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "selection", Content: `{"inbounds":[{"tag":"selected","port":9443,"protocol":"vless"},{"tag":"unselected","port":10443,"protocol":"vless"}],"outbounds":[{"protocol":"freedom"}]}`}, 1); err != nil {
		t.Fatal(err)
	}
	preview := httptest.NewRequest(http.MethodGet, "/api/v1/traffic-endpoints/sync", nil)
	preview.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, preview)
	var candidates struct {
		Candidates []core.TrafficSyncCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &candidates); err != nil || response.Code != http.StatusOK || len(candidates.Candidates) != 3 {
		t.Fatalf("preview: %d %s %v", response.Code, response.Body.String(), err)
	}
	if candidates.Candidates[0].Kind != "deleted" || candidates.Candidates[0].Port != 8443 {
		t.Fatalf("missing tombstone: %+v", candidates)
	}
	if policies, _ := db.AgentPortTrafficPolicies(ctx, agent.ID); len(policies) != 0 {
		t.Fatal("preview wrote policies")
	}
	cancelled := false
	updates := server.registerConnection(agent.ID, "selected-sync-test", func() { cancelled = true })
	defer server.unregisterConnection(agent.ID, "selected-sync-test")
	selected := httptest.NewRequest(http.MethodPost, "/api/v1/traffic-endpoints/sync", strings.NewReader(`{"selections":[{"agent_id":"`+agent.ID+`","port":8443},{"agent_id":"`+agent.ID+`","port":9443}]}`))
	selected.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, selected)
	if response.Code != http.StatusOK {
		t.Fatalf("selected: %d %s", response.Code, response.Body.String())
	}
	if cancelled {
		t.Fatal("selective sync forced reconnect discovery")
	}
	select {
	case <-updates:
	default:
		t.Fatal("live session not notified")
	}
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 || policies[0].Port != 8443 || policies[1].Port != 9443 {
		t.Fatalf("selected scope: %+v %v", policies, err)
	}
	for _, body := range []string{"", `{}`, `{"selections":[]}`, `{"selections":[{"agent_id":"` + agent.ID + `","port":10443,"name":"untrusted"}]}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/traffic-endpoints/sync", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid selection accepted: %s => %d", body, response.Code)
		}
	}
}
