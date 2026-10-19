package api

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"net/http"
	"testing"
)

func TestTrafficPageAPIAndBatchAuthorization(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "traffic api", core.EngineXray)
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	c, e := db.SaveAgentConfig(scoped, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "api", Content: `{"inbounds":[{"tag":"api","protocol":"http","port":21001}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`}, 0)
	if e != nil {
		t.Fatal(e)
	}
	var policies []core.PortTrafficPolicy
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &policies)
	if len(policies) != 0 {
		t.Fatal("draft visible")
	}
	task, e := db.CreateTask(scoped, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
	if e != nil {
		t.Fatal(e)
	}
	completeConfigScopeAPITask(t, ctx, db, task)
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &policies)
	if len(policies) != 1 || !policies[0].IsCurrentUser || policies[0].OwnerID != alice.userID || !policies[0].DeploymentConfirmed {
		t.Fatalf("wrong API ownership %+v", policies)
	}
	admin.call("GET", "/traffic-policies", nil, http.StatusOK, &policies)
	if len(policies) != 1 || policies[0].IsCurrentUser {
		t.Fatalf("administrator inherited user's port %+v", policies)
	}
	id := policies[0].ID
	request := core.TrafficBatchRequest{RequestID: "api-batch-request-001", Action: "reset", IDs: []string{id}}
	var response struct {
		Results []core.TrafficBatchResult `json:"results"`
	}
	bob.call("POST", "/traffic-policies/batch", request, http.StatusOK, &response)
	if len(response.Results) != 1 || response.Results[0].Status != "not_executed" {
		t.Fatalf("foreign target %+v", response)
	}
	alice.call("POST", "/traffic-policies/batch", request, http.StatusOK, &response)
	if response.Results[0].Status != "succeeded" {
		t.Fatalf("owner result %+v", response)
	}
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &policies)
	generation := policies[0].ResetGeneration
	alice.call("POST", "/traffic-policies/batch", request, http.StatusOK, &response)
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &policies)
	if policies[0].ResetGeneration != generation {
		t.Fatal("HTTP retry repeated reset")
	}
	request.Action = "delete"
	alice.call("POST", "/traffic-policies/batch", request, http.StatusConflict, nil)
	request.RequestID = "api-delete-request-01"
	alice.call("POST", "/traffic-policies/batch", request, http.StatusOK, &response)
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &policies)
	if len(policies) != 1 || policies[0].MonitoringEnabled {
		t.Fatalf("deletion lost tombstone %+v", policies)
	}
	var endpoints []core.PortTrafficEndpoint
	alice.call("GET", "/traffic-endpoints", nil, http.StatusOK, &endpoints)
	if len(endpoints) != 1 || !endpoints[0].IsCurrentUser {
		t.Fatalf("endpoint contract %+v", endpoints)
	}
}
