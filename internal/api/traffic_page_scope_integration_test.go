package api

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestTrafficPageMetadataUsesTrafficPermissionAndResourceScope(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	own, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "alice private traffic")
	foreign, _ := ownedConfigScopeAPIAgent(t, ctx, db, bob, "bob private traffic")
	shared := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, shared, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, shared, bob, 21002)
	for _, agent := range []core.Agent{own, foreign, shared} {
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: append(agent.Features, "port-traffic-v1"), Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	admin.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": []string{"traffic.read", "traffic.manage"}}, http.StatusOK, nil)
	aliceID := alice.userID
	alice, _ = managementLogin(t, alice.handler, "alice", "config scope password fixture", http.StatusOK)
	alice.userID = aliceID
	alice.call("GET", "/agents", nil, http.StatusForbidden, nil)
	var page struct {
		Agents []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			CanManage bool   `json:"can_manage"`
		} `json:"agents"`
		Policies []core.PortTrafficPolicy `json:"policies"`
	}
	body := alice.call("GET", "/traffic-policies?view=page&owner_id="+bob.userID+"&agent_id="+foreign.ID, nil, http.StatusOK, &page)
	if len(page.Agents) != 2 || bytes.Contains(body, []byte(foreign.ID)) || bytes.Contains(body, []byte(foreign.Name)) {
		t.Fatalf("traffic scope did not isolate A's nodes: %s", body)
	}
	for _, source := range page.Agents {
		if source.CanManage != (source.ID == own.ID) {
			t.Fatalf("owned/shared node confused: %+v", source)
		}
	}
	for _, field := range []string{`"metrics"`, `"runtime"`, `"labels"`, `"owner_id"`, `"content"`} {
		if bytes.Contains(body, []byte(field)) {
			t.Fatalf("traffic page exposed unrelated metadata %s: %s", field, body)
		}
	}
	// Minimal permissions still allow a host policy on the owned node. The
	// same global permission must not grant host quota edits on a shared node.
	policy := core.PortTrafficPolicyRequest{AgentID: own.ID, Name: "owned policy", Engine: core.EngineMihomo, Port: 21011,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Now().UTC(), LimitBytes: 1 << 30}
	alice.call("POST", "/traffic-policies", policy, http.StatusCreated, nil)
	policy.AgentID = shared.ID
	alice.call("POST", "/traffic-policies", policy, http.StatusForbidden, nil)
	// Existing integrations continue receiving an array, not the page envelope.
	var legacy []core.PortTrafficPolicy
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &legacy)
	if len(legacy) != 0 {
		t.Fatalf("undeployed policy visible: %+v", legacy)
	}
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	c, err := db.SaveAgentConfig(scoped, core.Config{AgentID: own.ID, Engine: core.EngineMihomo, Name: "traffic-only account", Content: "listeners: [{name: own, type: http, port: 21011}]\n"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture maintenance deploys Alice's configuration; her session retains
	// only traffic permissions throughout every HTTP assertion.
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: own.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	completeConfigScopeAPITask(t, ctx, db, task)
	alice.call("GET", "/traffic-policies?view=page", nil, http.StatusOK, &page)
	if len(page.Policies) != 1 || !page.Policies[0].IsCurrentUser || page.Policies[0].OwnerID != alice.userID || !page.Policies[0].DeploymentConfirmed {
		t.Fatalf("traffic-only account lost ownership metadata: %+v", page.Policies)
	}
	alice.call("GET", "/traffic-policies", nil, http.StatusOK, &legacy)
	if len(legacy) != 1 || legacy[0].AgentID != own.ID {
		t.Fatalf("legacy traffic response: %+v", legacy)
	}
}
