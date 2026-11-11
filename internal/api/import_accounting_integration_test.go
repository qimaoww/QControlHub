package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestImportedAccountingSurvivesEditingAndSharedIsolation(t *testing.T) {
	for engine, source := range map[core.Engine]string{
		core.EngineXray:            `{"inbounds":[{"protocol":"socks","port":"21001-21002","listen":"127.0.0.1","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom"},{"protocol":"blackhole","tag":"blocked"}],"routing":{"rules":[{"type":"field","domain":["blocked.test"],"outboundTag":"blocked"}]}}`,
		core.EngineSingBox:         `{"inbounds":[{"type":"socks","listen_port":21001},{"type":"http","tag":"kept","listen_port":21002}],"outbounds":[{"type":"direct"}],"route":{"rules":[{"domain":["blocked.test"],"action":"reject"}]}}`,
		core.EngineMihomo:          "listeners: [{type: socks, port: 21001}, {name: kept, type: http, port: 21002}]\nrules: ['DOMAIN,blocked.test,REJECT','MATCH,DIRECT']\n",
		core.EngineShadowsocksRust: `{"servers":[{"server_port":21001,"method":"aes-256-gcm","password":"fixture"},{"server_port":21002,"method":"aes-256-gcm","password":"fixture"}]}`,
	} {
		t.Run(string(engine), func(t *testing.T) {
			db, ctx, admin, owner, shared := newConfigScopeAPIFixture(t)
			node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "import accounting", engine)
			features := append(node.Features, core.AgentFeatureImportIsolation, core.AgentFeatureImportDependencies)
			if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true, ExistingConfigAvailable: true}}}); err != nil {
				t.Fatal(err)
			}
			base := "/agents/" + node.ID + "/configs/" + string(engine)
			grantConfigScopeAPIAgent(t, ctx, db, node, shared, 22001)
			var read core.Task
			owner.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: engine, Action: core.ActionReadConfig}, 201, &read)
			claimed, err := db.ClaimTask(ctx, node.ID)
			if err != nil || claimed == nil {
				t.Fatal(err)
			}
			digest := strings.Repeat("a", 64)
			if err := db.CompleteTask(ctx, node.ID, read.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: source, ImportSourceDigest: digest}); err != nil {
				t.Fatal(err)
			}
			shared.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, 404, nil)
			request := store.ConfigImportRequest{SnapshotTaskID: read.ID, Name: "imported"}
			shared.call("POST", base+"/import", request, http.StatusForbidden, nil)
			var task, repeated core.Task
			owner.call("POST", base+"/import", request, 201, &task)
			owner.call("POST", base+"/import", request, 201, &repeated)
			if repeated.ID != task.ID || !repeated.Reused {
				t.Fatal("duplicate conversion queued another migration")
			}
			owner.call("GET", base, nil, 404, nil)
			expected, err := serverconfig.PrepareIndependentEgress(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			claimed, err = db.ClaimTask(ctx, node.ID)
			if err != nil || claimed == nil || claimed.ConfigContent != expected.Content || claimed.ImportSourceDigest != digest {
				t.Fatalf("converted execution lost provenance: %+v %v", claimed, err)
			}
			if err := db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Error: "isolated rollback completed", InstanceStatus: &core.SharedInstanceStatus{Engine: engine, TaskID: claimed.ID, Status: "inactive", ObservedAt: time.Now().UTC()}}); err != nil {
				t.Fatal(err)
			}
			owner.call("GET", base, nil, 404, nil)
			owner.call("POST", "/tasks/"+task.ID+"/retry", nil, 201, &task)
			completeConfigScopeAPITask(t, ctx, db, task)
			var saved core.Config
			owner.call("GET", base, nil, 200, &saved)
			if saved.Content != expected.Content || saved.Version != 1 {
				t.Fatal("import did not save its executed plan")
			}
			admin.call("GET", "/tasks/"+task.ID, nil, 200, nil)
			assertPlan := func(config core.Config, count int) {
				t.Helper()
				plan, err := serverconfig.PrepareIndependentEgress(engine, config.Content)
				if err != nil || plan.Content != config.Content || len(plan.Ports) != count {
					t.Fatalf("stale plan after mutation: %+v %v", plan.Ports, err)
				}
			}
			assertPlan(saved, 2)
			protocol := serverconfig.ProtocolSS2022
			p, ok := serverconfig.FindProtocol(engine, protocol)
			if !ok {
				t.Fatal("missing fixture preset")
			}
			input, err := serverconfig.NewPlan(p)
			if err != nil {
				t.Fatal(err)
			}
			input.Port, input.Tag = 21003, "added"
			for _, operation := range []string{"add", "modify", "delete"} {
				original := ""
				if operation != "add" {
					original = input.Tag
				}
				if operation == "modify" {
					input.Tag = "edited"
					input.Port = 21004
				}
				var result configMutationResult
				owner.call("POST", base+"/server-inbounds", map[string]any{"operation": operation, "original_tag": original, "expected_version": saved.Version, "intent": "validate", "input": input}, 200, &result)
				count := 3
				if operation == "delete" {
					count = 2
				}
				saved = result.Config
				assertPlan(saved, count)
				completeConfigScopeAPITask(t, ctx, db, result.Task)
			}
			for _, intent := range []string{"validate", "deploy"} {
				var result configMutationResult
				owner.call("POST", base+"/source", map[string]any{"name": "imported", "version": saved.Version, "content": saved.Content, "intent": intent, "expected_instance_id": ""}, 200, &result)
				saved = result.Config
				assertPlan(saved, 2)
				completeConfigScopeAPITask(t, ctx, db, result.Task)
			}
			var revisions []core.Config
			owner.call("GET", "/configs/"+saved.ID+"/revisions", nil, 200, &revisions)
			if len(revisions) != saved.Version {
				t.Fatalf("lost same-day saves: %d != %d", len(revisions), saved.Version)
			}
			// The shared instance uses its own source and allocated port on the same node.
			var privateSource string
			if engine == core.EngineXray {
				privateSource = `{"inbounds":[{"protocol":"http","port":22001,"settings":{}}],"outbounds":[{"protocol":"freedom"}]}`
			} else if engine == core.EngineSingBox {
				privateSource = `{"inbounds":[{"type":"socks","listen_port":22001}],"outbounds":[{"type":"direct"}]}`
			} else if engine == core.EngineMihomo {
				privateSource = "listeners: [{type: http, port: 22001}]\nrules: ['MATCH,DIRECT']\n"
			} else {
				privateSource = `{"server_port":22001,"method":"aes-256-gcm","password":"fixture"}`
			}
			var private configMutationResult
			shared.call("POST", base+"/source", map[string]any{"name": "shared", "version": 0, "content": privateSource, "intent": "deploy"}, 200, &private)
			completeConfigScopeAPITask(t, ctx, db, private.Task)
			var after core.Config
			owner.call("GET", base, nil, 200, &after)
			if after.ID != saved.ID || after.Content != saved.Content || after.Version != saved.Version || private.Config.ID == saved.ID || !private.Task.SharedInstance {
				t.Fatal("shared conversion modified host ownership")
			}
			assertPlan(private.Config, 1)
		})
	}
}

func TestImportAccountingRejectsWithoutPartialSave(t *testing.T) {
	db, ctx, _, owner, shared := newConfigScopeAPIFixture(t)
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "rejected import", core.EngineXray)
	features := append(node.Features, core.AgentFeatureImportIsolation, core.AgentFeatureImportDependencies)
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ExistingConfigAvailable: true}}}); err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + node.ID + "/configs/xray"
	grantConfigScopeAPIAgent(t, ctx, db, node, shared, 21002)
	var private configMutationResult
	shared.call("POST", base+"/source", map[string]any{
		"name": "occupied shared port", "version": 0, "intent": "deploy",
		"content": `{"inbounds":[{"protocol":"http","port":21002}],"outbounds":[{"protocol":"freedom"}]}`,
	}, 200, &private)
	completeConfigScopeAPITask(t, ctx, db, private.Task)
	for _, tt := range []struct {
		inbounds, field string
		status          int
	}{
		{`[{"port":"invalid","tag":"a"}]`, "inbounds[0].port", 400},
		{`[{"port":21001,"tag":5}]`, "inbounds[0].tag", 400},
		{`[{"port":21001,"tag":"a"},{"port":21001,"tag":"b","listen":"127.0.0.2"}]`, "inbounds[1].port", 400},
		{`[{"port":"21001-21002","protocol":"http"}]`, "21002", 409},
	} {
		source := `{"inbounds":` + tt.inbounds + `,"outbounds":[{"protocol":"freedom"}]}`
		var read core.Task
		owner.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: core.EngineXray, Action: core.ActionReadConfig}, 201, &read)
		claimed, err := db.ClaimTask(ctx, node.ID)
		if err != nil || claimed == nil {
			t.Fatal(err)
		}
		if err := db.CompleteTask(ctx, node.ID, read.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: source, ImportSourceDigest: strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
		body := owner.call("POST", base+"/import", store.ConfigImportRequest{SnapshotTaskID: read.ID, Name: "must not save"}, tt.status, nil)
		if !strings.Contains(string(body), tt.field) {
			t.Fatalf("no actionable field: %s", body)
		}
		owner.call("GET", base, nil, 404, nil)
		if task, err := db.ClaimTask(ctx, node.ID); err != nil || task != nil {
			t.Fatalf("rejected import queued execution: %+v %v", task, err)
		}
	}
}
