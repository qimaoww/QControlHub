package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentIsolationAPIRevocationAndAllocationRevision(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
		Features: []string{core.AgentFeatureSharedTraffic, "port-traffic-v1"},
		Runtime:  map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	var initial core.AgentAccess
	alice.call("GET", "/agent-access", nil, http.StatusOK, &initial)
	alice.call("GET", "/users/"+bob.userID+"/agent-access", nil, http.StatusForbidden, nil)
	admin.call("PUT", "/users/"+alice.userID+"/agent-access", map[string]any{"isolated": true}, http.StatusBadRequest, nil)
	var access core.AgentAccess
	input := core.AgentAccessRequest{Revision: initial.Revision, Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, LimitBytes: 1000, Ports: []int{21001}}}}
	admin.call("PUT", "/users/"+alice.userID+"/agent-access", input, http.StatusOK, &access)
	admin.call("PUT", "/users/"+alice.userID+"/agent-access", input, http.StatusConflict, nil)
	// A quota update must take effect in the same authenticated session.
	var own core.AgentAccess
	alice.call("GET", "/agent-access", nil, http.StatusOK, &own)
	if !own.Isolated || own.Shares[0].LimitBytes != 1000 {
		t.Fatalf("cached session lost current sharing state: %+v", own)
	}
	alice.call("PUT", "/agents/"+agent.ID+"/name", map[string]string{"name": "stolen"}, http.StatusForbidden, nil)
	alice.call("PUT", "/substore-sync/settings", map[string]string{"endpoint_url": "http://127.0.0.1/private"}, http.StatusOK, nil)
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop}, http.StatusForbidden, nil)
	// Personal integration settings and groups do not grant host operations.
	alice.call("POST", "/substore-sync/targets", map[string]string{"display_name": "private", "sync_format": "mihomo"}, http.StatusCreated, nil)
	admin.call("PUT", "/users/"+alice.userID+"/agent-access", core.AgentAccessRequest{Revision: access.Revision, Isolated: true}, http.StatusOK, &access)
	var agents []core.Agent
	alice.call("GET", "/agents", nil, http.StatusOK, &agents)
	if len(agents) != 0 {
		t.Fatalf("revocation leaked node list: %+v", agents)
	}
	alice.call("GET", "/agents/"+agent.ID+"/configs", nil, http.StatusNotFound, nil)
	alice.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusNotFound, nil)
	alice.call("GET", "/agent-access", nil, http.StatusOK, &own)
	if len(own.Shares) != 1 || own.Shares[0].Enabled {
		t.Fatalf("revoked allocation disappeared/reset: %+v", own)
	}
}
