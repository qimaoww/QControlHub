package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestConfigManagementOwnerCASAndSnapshotsWithPostgreSQL(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001, 21002)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 22001)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var original core.Config
	alice.call("PUT", base, core.Config{Name: "single source", Content: "listeners: [{name: a, type: http, port: 21001}, {name: b, type: http, port: 21002}]\nrules: ['MATCH,DIRECT']\n"}, 200, &original)
	bob.call("PUT", base, core.Config{Name: "other source", Content: "listeners: [{name: a, type: http, port: 22001}]\nrules: ['MATCH,DIRECT']\n"}, 200, nil)
	var state struct {
		Version  int
		Disabled []serverconfig.ManagedResource
	}
	alice.call("GET", base+"/management", nil, 200, &state)
	if state.Version != 1 || len(state.Disabled) != 0 {
		t.Fatal("existing source has unexpected state")
	}
	body := map[string]any{"version": 1, "intent": "validate", "disabled": []serverconfig.ManagedResource{{Section: "listeners", Tag: "a"}}}
	var result configMutationResult
	alice.call("PUT", base+"/management", body, 200, &result)
	if result.Config.Content != original.Content || result.Config.Version != 2 || result.Task.ConfigVersion != 2 || result.Task.ConfigContent != "" {
		t.Fatal("state altered full source or leaked runtime task content")
	}
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	claim, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	if strings.Contains(claim.ConfigContent, "port: 21001") || !strings.Contains(claim.ConfigContent, "21002") || !claim.SharedInstance {
		t.Fatalf("runtime state or instance scope incorrect: %s", claim.ConfigContent)
	}
	if err = db.CompleteTask(ctx, agent.ID, claim.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	alice.call("PUT", base+"/management", body, http.StatusConflict, nil)
	body["version"] = 2
	body["disabled"] = []serverconfig.ManagedResource{{Section: "listeners", Tag: "missing"}}
	alice.call("PUT", base+"/management", body, http.StatusBadRequest, nil)
	current, err := db.AgentConfig(scoped, agent.ID, core.EngineMihomo)
	if err != nil || current.Version != 2 {
		t.Fatalf("failed state mutation changed revision: %+v %v", current, err)
	}
	bob.call("GET", base+"/management", nil, 200, &state)
	if state.Version != 1 || len(state.Disabled) != 0 {
		t.Fatal("another account inherited disabled objects")
	}
	admin.call("GET", base+"/management", nil, http.StatusNotFound, nil)
	body["owner_id"] = bob.userID
	alice.call("PUT", base+"/management", body, http.StatusBadRequest, nil)
	delete(body, "owner_id")
	restored, err := db.RestoreConfigRevision(scoped, original.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	restoredState, err := db.ConfigManagementState(scoped, original.ID, restored.Version)
	if err != nil || len(restoredState.Disabled) != 0 {
		t.Fatalf("restore retained newer disabled state: %+v %v", restoredState, err)
	}
	restored, err = db.RestoreConfigRevision(scoped, original.ID, 2, restored.Version)
	if err != nil {
		t.Fatal(err)
	}
	restoredState, err = db.ConfigManagementState(scoped, original.ID, restored.Version)
	if err != nil || len(restoredState.Disabled) != 1 || restoredState.Disabled[0].Tag != "a" {
		t.Fatalf("restore lost historical management state: %+v %v", restoredState, err)
	}
	if _, err = db.ConfigManagementState(store.WithConfigScope(ctx, bob.userID, false), original.ID, 2); err == nil {
		t.Fatal("cross-account revision state was exposed")
	}
}

func TestManagedOutboundAPIKeepsAccountSourceAndCAS(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 22001)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var saved core.Config
	alice.call("PUT", base, core.Config{Name: "outbound source", Content: "listeners: [{name: a, type: http, port: 21001}]\nproxies: [{name: old, type: http, server: 127.0.0.1, port: 443}]\nrules: ['MATCH,DIRECT']\n"}, 200, &saved)
	var fragment struct {
		Version  int
		Fragment string
	}
	alice.call("GET", base+"/outbounds/proxies/old", nil, 200, &fragment)
	if fragment.Version != saved.Version || !strings.Contains(fragment.Fragment, "443") {
		t.Fatal("authorized outbound fragment missing")
	}
	bob.call("GET", base+"/outbounds/proxies/old", nil, 404, nil)
	admin.call("GET", base+"/outbounds/proxies/old", nil, 404, nil)
	body := map[string]any{"version": saved.Version, "section": "proxies", "tag": "old", "operation": "modify", "fragment": "name: renamed\ntype: http\nserver: 127.0.0.1\nport: 8443\n", "intent": "validate"}
	var result configMutationResult
	alice.call("POST", base+"/outbounds", body, 200, &result)
	if result.Config.Version != saved.Version+1 || !strings.Contains(result.Config.Content, "8443") || !strings.Contains(result.Config.Content, "21001") || result.Task.ConfigContent != "" {
		t.Fatal("outbound edit lost unrelated listener or leaked task source")
	}
	alice.call("POST", base+"/outbounds", body, 409, nil)
	body["version"] = result.Config.Version
	body["operation"] = "delete"
	body["tag"] = "missing"
	alice.call("POST", base+"/outbounds", body, 400, nil)
	current, err := db.AgentConfig(store.WithConfigScope(ctx, alice.userID, false), agent.ID, core.EngineMihomo)
	if err != nil || current.Version != result.Config.Version {
		t.Fatal("failed outbound write left a revision")
	}
	body["owner_id"] = bob.userID
	alice.call("POST", base+"/outbounds", body, 400, nil)
}
