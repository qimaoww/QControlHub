package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestImportSourceSelectionPinsProvenanceAndAuthorization(t *testing.T) {
	db, ctx, _, owner, shared := newConfigScopeAPIFixture(t)
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "source selection", core.EngineXray)
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	features := append(node.Features, core.AgentFeatureImportIsolation, core.AgentFeatureImportDependencies)
	sources := []core.ExistingCoreSource{{ID: first, Service: "custom-one.service", ConfigPath: "/srv/host secret/config.json", Status: "active"}, {ID: second, Service: "custom-two.service", ConfigDirectory: "/opt/host config", Status: "inactive"}}
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ExistingConfigAvailable: true, ExistingSources: sources}}}); err != nil {
		t.Fatal(err)
	}
	create := func(id string, status int) core.Task {
		t.Helper()
		var task core.Task
		owner.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: core.EngineXray, Action: core.ActionReadConfig, ExistingSourceID: id}, status, &task)
		return task
	}
	create("", http.StatusBadRequest)
	create(strings.Repeat("c", 64), http.StatusConflict)
	grantConfigScopeAPIAgent(t, ctx, db, node, shared, 21001)
	for _, path := range []string{"/agents", "/agents/" + node.ID + "/configs/xray/state", "/agents/" + node.ID + "/configs/xray/workspace"} {
		body := shared.call("GET", path, nil, http.StatusOK, nil)
		for _, forbidden := range []string{"existing_sources", "existing_config_available\":true", "custom-one.service", "custom-two.service", "/srv/host secret", "/opt/host config", first, second} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("shared response %s exposes host discovery: %s", path, forbidden)
			}
		}
	}
	shared.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: core.EngineXray, Action: core.ActionReadConfig, ExistingSourceID: first}, http.StatusForbidden, nil)
	shared.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: core.EngineXray, Action: core.ActionImportExisting}, http.StatusForbidden, nil)
	read := create(first, http.StatusCreated)
	create(second, http.StatusConflict)
	claim, err := db.ClaimTask(ctx, node.ID)
	if err != nil || claim == nil || claim.ExistingSourceID != first {
		t.Fatalf("source did not reach Agent: %+v %v", claim, err)
	}
	dependency := core.ImportDependency{Field: "routing.rules[0].ip[0]", Source: "/srv/规则 one.dat", Resolved: "/srv/archive/规则 one.dat", Destination: "/usr/local/lib/qagent/cores/imports/digest/file.dat", Kind: "input"}
	content := `{"inbounds":[],"outbounds":[],"opaque":{"large":9007199254740995}}`
	digest := strings.Repeat("d", 64)
	if err := db.CompleteTask(ctx, node.ID, read.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: true, Output: content, ImportSourceDigest: digest, ImportDependencies: []core.ImportDependency{dependency}}); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Content      string                  `json:"content"`
		Dependencies []core.ImportDependency `json:"dependencies"`
	}
	owner.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusOK, &snapshot)
	if snapshot.Content != content || len(snapshot.Dependencies) != 1 || snapshot.Dependencies[0] != dependency {
		t.Fatalf("protected snapshot lost manifest: %+v", snapshot)
	}
	shared.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusNotFound, nil)
	shared.call("POST", "/agents/"+node.ID+"/configs/xray/import", store.ConfigImportRequest{SnapshotTaskID: read.ID, Name: "denied shared import"}, http.StatusForbidden, nil)
	var task core.Task
	owner.call("POST", "/agents/"+node.ID+"/configs/xray/import", store.ConfigImportRequest{SnapshotTaskID: read.ID, Name: "selected service"}, http.StatusCreated, &task)
	claim, err = db.ClaimTask(ctx, node.ID)
	if err != nil || claim == nil || claim.ExistingSourceID != first || claim.ImportSourceDigest != digest || claim.ConfigContent != content {
		state, _ := db.GetTask(ctx, task.ID)
		t.Fatalf("import lost source binding: %+v %v state=%+v", claim, err, state)
	}
	if err := db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: false, Error: "fixture stop before takeover"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AgentConfig(store.WithConfigScope(ctx, owner.userID, false), node.ID, core.EngineXray); err == nil {
		t.Fatal("failed import saved configuration")
	}
}
