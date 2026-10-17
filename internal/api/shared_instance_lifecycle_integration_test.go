package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedInstanceAPIScopesStateReadsVersionsAndForgedTargets(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "independent-instance-api")
	if err := db.SetAgentClientAddress(ctx, agent.ID, "edge.example.test"); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001, 21003)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var configs [2]core.Config
	for i, client := range []configScopeAPIClient{alice, bob} {
		client.call("PUT", base, configScopeAPIConfig(t, 21001+i), http.StatusOK, &configs[i])
		var task core.Task
		client.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: configs[i].ID}, http.StatusCreated, &task)
		completeConfigScopeAPITask(t, ctx, db, task)
	}
	var own core.Agent
	body := alice.call("GET", base+"/state", nil, http.StatusOK, &own)
	if own.CanManage || own.Runtime[core.EngineMihomo].Instance.DeployedConfigID != configs[0].ID || bytes.Contains(body, []byte(configs[1].ID)) || bytes.Contains(body, []byte("content")) {
		t.Fatalf("state projection exposed another instance or configuration body: %s", body)
	}
	for _, field := range []string{"shared_traffic_id", "instance_id", "owner_id", "service", "config_path"} {
		alice.call("POST", "/tasks", map[string]any{"agent_id": agent.ID, "engine": "mihomo", "action": "stop", field: "foreign-target"}, http.StatusBadRequest, nil)
	}
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStart, ConfigID: configs[1].ID}, http.StatusBadRequest, nil)
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: configs[1].ID}, http.StatusNotFound, nil)
	draft := configScopeAPIConfig(t, 21003)
	draft.Version = 1
	alice.call("PUT", base, draft, http.StatusOK, nil)
	var stop core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop}, http.StatusCreated, &stop)
	completeConfigScopeAPITask(t, ctx, db, stop)
	var entries []clientAccessEntry
	alice.call("GET", "/client-access", nil, http.StatusOK, &entries)
	if len(entries) != 1 || entries[0].ConfigVersion != 1 || entries[0].ServiceStatus != "inactive" || len(entries[0].Profiles) != 1 || entries[0].Profiles[0].Port != 21001 {
		t.Fatalf("stopped client used a draft or lost deployment: %+v", entries)
	}
	var canceled core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop}, http.StatusCreated, &canceled)
	alice.call("DELETE", "/tasks/"+canceled.ID, nil, http.StatusNoContent, nil)
	admin.call("POST", "/tasks/"+canceled.ID+"/retry", nil, http.StatusForbidden, nil)
	var retried core.Task
	alice.call("POST", "/tasks/"+canceled.ID+"/retry", nil, http.StatusCreated, &retried)
	if !retried.SharedInstance || retried.ConfigID != configs[0].ID || retried.ConfigVersion != 1 {
		t.Fatalf("instance retry became a host operation: %+v", retried)
	}
	completeConfigScopeAPITask(t, ctx, db, retried)
	var read core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionReadManagedConfig}, http.StatusCreated, &read)
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || !lease.SharedInstance || lease.SharedTrafficID != own.Runtime[core.EngineMihomo].Instance.ID {
		t.Fatalf("read target: %+v %v", lease, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: configs[0].Content}); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusOK, nil)
	bob.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusNotFound, nil)
	var cached core.Task
	alice.call("POST", "/tasks", map[string]any{"agent_id": agent.ID, "engine": "mihomo", "action": "read-managed-config", "prefer_cached": true}, http.StatusOK, &cached)
	if cached.ID != read.ID {
		t.Fatal("own read cache not reused")
	}
	bob.call("POST", "/tasks", map[string]any{"agent_id": agent.ID, "engine": "mihomo", "action": "read-managed-config", "prefer_cached": true}, http.StatusCreated, &cached)
	if cached.ID == read.ID || cached.SharedTrafficID == read.SharedTrafficID {
		t.Fatal("cross-user read cache reused")
	}
}

func TestSharedInstanceOldAgentCannotFallBackToHostService(t *testing.T) {
	db, ctx, admin, recipient, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "legacy-agent-api")
	grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001)
	legacy := []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: legacy, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var saved core.Config
	recipient.call("PUT", base, configScopeAPIConfig(t, 21001), http.StatusOK, &saved)
	for _, action := range []core.Action{core.ActionStart, core.ActionStop, core.ActionRestart, core.ActionReadManagedConfig, core.ActionStatus, core.ActionDeploy} {
		request := core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: action}
		if action == core.ActionDeploy {
			request.ConfigID = saved.ID
		}
		body := recipient.call("POST", "/tasks", request, http.StatusConflict, nil)
		if !strings.Contains(string(body), "升级") {
			t.Fatalf("missing actionable upgrade message: %s", body)
		}
	}
}
