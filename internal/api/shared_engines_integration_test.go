package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedEngineAPICannotBypassAllocatedCapabilities(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "engine-api", core.AllEngines()...)
	var sharing core.AgentSharing
	alice.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	alice.call("PUT", "/agents/"+agent.ID+"/sharing", core.AgentSharingRequest{Revision: sharing.Revision,
		Shares: []core.AgentSharingRecipient{{Username: "bob", Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}}},
		http.StatusOK, &sharing)
	bob.call("POST", "/agent-access/"+sharing.Shares[0].ID+"/response", core.AgentShareResponseRequest{
		Revision: sharing.Shares[0].InvitationRevision, Decision: "accept",
	}, http.StatusOK, nil)
	var agents []core.Agent
	bob.call("GET", "/agents", nil, http.StatusOK, &agents)
	if len(agents) != 1 || !slices.Equal(agents[0].Capabilities, []core.Engine{core.EngineMihomo}) ||
		!slices.Equal(agents[0].SupportedCapabilities, agents[0].Capabilities) {
		t.Fatalf("unassigned engines exposed: %+v", agents)
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		base := "/agents/" + agent.ID + "/configs/" + string(engine)
		for _, path := range []string{base, base + "/workspace", base + "/files", base + "/fields/log"} {
			bob.call("GET", path, nil, http.StatusNotFound, nil)
		}
		bob.call("POST", base+"/plans", map[string]string{"protocol": "http"}, http.StatusNotFound, nil)
		bob.call("PUT", base, map[string]any{"name": "unassigned", "content": "{}", "expected_version": 0}, http.StatusNotFound, nil)
		bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionStatus}, http.StatusNotFound, nil)
	}
	var config core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusOK, &config)
	bob.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, ConfigID: config.ID, Action: core.ActionValidate,
	}, http.StatusCreated, nil)
	// Archive IDs and the generic task endpoint must not bypass the path gate.
	var archived core.Config
	bob.call("POST", "/configs", core.Config{Name: "unassigned archive", Engine: core.EngineXray,
		Content: `{"inbounds":[{"tag":"a","protocol":"http","port":21001}],"outbounds":[{"protocol":"freedom"}]}`},
		http.StatusCreated, &archived)
	bob.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineXray, ConfigID: archived.ID, Action: core.ActionDeploy,
	}, http.StatusNotFound, nil)
}

func TestConfigValidationAPIRequiresIndependentEgress(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "egress-api")
	var config core.Config
	// Saving an editable draft is allowed; it is not proof of deployability.
	alice.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", map[string]any{
		"name": "global-route", "content": "mode: direct\nlisteners: [{name: a, type: http, port: 21001}]\n",
		"version": 0,
	}, http.StatusOK, &config)
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		response := auditPR183Request(alice, "POST", "/tasks", core.TaskRequest{
			AgentID: agent.ID, Engine: core.EngineMihomo, ConfigID: config.ID, Action: action,
		})
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "独立出口校验失败") {
			t.Fatalf("%s bypassed independent exits: %d %s", action, response.Code, response.Body.String())
		}
	}
}

func TestSharedPoliciesStayRevokedUntilCurrentHeartbeat(t *testing.T) {
	policies := []core.PortTrafficPolicy{{Engine: core.EngineMihomo, SharedQuota: &core.SharedTrafficQuota{
		ID: "shr_0000000000000001", Engines: []core.Engine{core.EngineMihomo},
	}}}
	pending := trafficPoliciesForSession(policies, false)
	if !pending[0].SharedQuota.Revoked || policies[0].SharedQuota.Revoked {
		t.Fatal("unverified session enabled sharing or mutated the durable policy")
	}
	verified := trafficPoliciesForSession(policies, true)
	if !verified[0].SharedQuota.AllowsEngine(core.EngineMihomo) || verified[0].SharedQuota.AllowsEngine(core.EngineXray) {
		t.Fatal("verified session lost its exact engine allocation")
	}
}
