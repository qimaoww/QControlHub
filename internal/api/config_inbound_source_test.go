package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestNativeInboundEditingUsesActualSourceAndScopedCAS(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
			t.Run(string(engine)+map[bool]string{false: "/owner", true: "/shared"}[shared], func(t *testing.T) {
				db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "native", engine)
				client, foreign := owner, recipient
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001, 23101)
					client, foreign = recipient, owner
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				sources := map[core.Engine]string{
					core.EngineXray:            `{"inbounds":[{"tag":"manual","protocol":"socks","listen":"0.0.0.0","port":21001,"settings":{"auth":"password","accounts":[{"user":"a","pass":"test-a"},{"user":"b","pass":"test-b"}]}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`,
					core.EngineSingBox:         `{"inbounds":[{"tag":"manual","type":"socks","listen":"0.0.0.0","listen_port":21001,"users":[{"username":"a","password":"test-a"},{"username":"b","password":"test-b"}]}],"outbounds":[{"tag":"direct","type":"direct"}]}`,
					core.EngineMihomo:          "listeners:\n  - name: manual\n    type: socks\n    listen: 0.0.0.0\n    port: 21001\n    users:\n      - username: a\n        password: test-a\nrules:\n  - MATCH,DIRECT\n",
					core.EngineShadowsocksRust: `{"servers":[{"id":"manual","server":"0.0.0.0","server_port":21001,"method":"aes-256-gcm","password":"test-a"}],"dns":"1.1.1.1"}`,
				}
				var config core.Config
				content := sources[engine]
				if shared { // Existing shared listener restrictions still apply.
					content = strings.ReplaceAll(strings.ReplaceAll(content, `"socks"`, `"http"`), "type: socks", "type: http")
				}
				client.call("PUT", base, map[string]any{"name": "manual", "engine": engine, "content": content, "version": 0}, http.StatusOK, &config)
				var source struct {
					Version  int    `json:"version"`
					Fragment string `json:"fragment"`
				}
				client.call("GET", base+"/server-inbounds/manual", nil, http.StatusOK, &source)
				foreign.call("GET", base+"/server-inbounds/manual", nil, http.StatusNotFound, nil)
				fragment := strings.Replace(source.Fragment, "test-a", "test-edited", 1)
				body := map[string]any{"operation": "modify", "original_tag": "manual", "expected_version": source.Version, "intent": "deploy", "fragment": fragment}
				var result configMutationResult
				client.call("POST", base+"/server-inbounds", body, http.StatusOK, &result)
				if result.Config.Version != config.Version+1 || result.Task.ConfigVersion != result.Config.Version || result.Task.SharedInstance != shared || !strings.Contains(result.Config.Content, "test-edited") {
					t.Fatal("native edit lost exact save/task/instance association")
				}
				client.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				foreign.call("GET", "/tasks/"+result.Task.ID, nil, http.StatusNotFound, nil)
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				var unchanged core.Task
				client.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionReadManagedConfig}, http.StatusCreated, &unchanged)
				unchangedLease, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || unchangedLease == nil || unchangedLease.ID != unchanged.ID {
					t.Fatal("unchanged read not claimed")
				}
				if err := db.CompleteTask(ctx, agent.ID, unchanged.ID, core.TaskResultRequest{LeaseID: unchangedLease.LeaseID, Success: true, Output: result.Config.Content}); err != nil {
					t.Fatal(err)
				}
				var applied agentConfigWorkspaceResource
				client.call("GET", base+"/workspace/snapshots/"+unchanged.ID, nil, http.StatusOK, &applied)
				if applied.Deployment.State != "applied" {
					t.Fatal("unchanged read incorrectly reset an applied deployment")
				}
				// An operator edits the actual instance file after deployment. Read it
				// through the existing Agent task, then edit that snapshot, not the
				// stale panel source. The task and snapshot remain instance scoped.
				actual := strings.Replace(result.Config.Content, "test-edited", "test-from-agent", 1)
				actualInbound, _, err := serverconfig.InboundSource(engine, actual, "manual")
				if err != nil {
					t.Fatal(err)
				}
				actual, err = serverconfig.ReplaceInboundSource(engine, actual, "manual", strings.Replace(actualInbound, "21001", "23101", 1))
				if err != nil {
					t.Fatal(err)
				}
				var read core.Task
				client.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionReadManagedConfig}, http.StatusCreated, &read)
				lease, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || lease == nil || lease.ID != read.ID || lease.SharedInstance != shared {
					t.Fatalf("read target: %v", err)
				}
				if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: actual}); err != nil {
					t.Fatal(err)
				}
				var preview agentConfigWorkspaceResource
				previewPath := base + "/workspace/snapshots/" + read.ID
				client.call("GET", previewPath, nil, http.StatusOK, &preview)
				if preview.Config.Content != actual || preview.Config.Version != result.Config.Version || preview.Deployment.State != "pending" {
					t.Fatal("read preview used saved content or claimed deployment")
				}
				foreign.call("GET", previewPath, nil, http.StatusNotFound, nil)
				client.call("GET", base+"/server-inbounds/manual?snapshot="+read.ID, nil, http.StatusOK, &source)
				if !strings.Contains(source.Fragment, "test-from-agent") {
					t.Fatal("inbound editor did not reverse-read actual configuration")
				}
				body["fragment"] = strings.Replace(source.Fragment, "test-from-agent", "test-read-edited", 1)
				body["expected_version"], body["snapshot_task_id"] = result.Config.Version, read.ID
				client.call("POST", base+"/server-inbounds", body, http.StatusOK, &result)
				if !strings.Contains(result.Config.Content, "test-read-edited") || result.Task.ConfigVersion != result.Config.Version || result.Task.SharedInstance != shared {
					t.Fatal("read-edit-deploy lost snapshot or target")
				}
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				// Deployment retires its source snapshot; it cannot be reused.
				client.call("GET", previewPath, nil, http.StatusNotFound, nil)
				if shared {
					body["expected_version"] = result.Config.Version
					delete(body, "snapshot_task_id")
					body["fragment"] = strings.Replace(fragment, "21001", "21002", 1)
					client.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				}
			})
		}
	}
}

func TestInboundSnapshotEditingBeforeFirstPanelSave(t *testing.T) {
	db, ctx, _, owner, foreign := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "first-native-read", core.EngineXray)
	base := "/agents/" + agent.ID + "/configs/xray"
	var read core.Task
	owner.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionReadManagedConfig}, http.StatusCreated, &read)
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != read.ID {
		t.Fatal("read was not claimed")
	}
	content := `{"inbounds":[{"tag":"manual","protocol":"http","port":21001,"settings":{"accounts":[{"user":"a","pass":"test-original"}]}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: content}); err != nil {
		t.Fatal(err)
	}
	var preview agentConfigWorkspaceResource
	owner.call("GET", base+"/workspace/snapshots/"+read.ID, nil, http.StatusOK, &preview)
	if preview.Config == nil || preview.Config.ID != "" || preview.Config.Version != 0 || len(preview.ManagedObjects) == 0 {
		t.Fatal("first read lost editable source")
	}
	foreign.call("GET", base+"/workspace/snapshots/"+read.ID, nil, http.StatusNotFound, nil)
	var source struct {
		Version  int    `json:"version"`
		Fragment string `json:"fragment"`
	}
	owner.call("GET", base+"/server-inbounds/manual?snapshot="+read.ID, nil, http.StatusOK, &source)
	var result configMutationResult
	owner.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": "manual", "fragment": strings.Replace(source.Fragment, "test-original", "test-edited", 1), "expected_version": 0, "snapshot_task_id": read.ID, "intent": "deploy"}, http.StatusOK, &result)
	if result.Config.Version != 1 || result.Task.ConfigID != result.Config.ID || result.Task.ConfigVersion != 1 || !strings.Contains(result.Config.Content, "test-edited") {
		t.Fatal("initial read-edit-deploy lost atomic save")
	}
}

func TestNativeInboundEditKeepsMatchingClientMetadata(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "owner", true: "shared"}[shared], func(t *testing.T) {
			db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "native-client-fields", core.EngineXray)
			client := owner
			if shared {
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001)
				client = recipient
			}
			base := "/agents/" + agent.ID + "/configs/xray"
			protocol, ok := serverconfig.FindProtocol(core.EngineXray, "vmess")
			if !ok {
				t.Fatal("VMess preset missing")
			}
			plan, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			plan.TLSEnabled, plan.RealityEnabled, plan.TLSMode, plan.TLSPublicPort, plan.Flow = false, false, "", 0, ""
			plan.TLSServerName, plan.CertificatePath, plan.PrivateKeyPath = "", "", ""
			plan.Tag, plan.Port, plan.Transport, plan.TransportPath, plan.TransportHost = "native-ws", 21001, "websocket", "/native", "host.example.test"
			var saved configMutationResult
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
			completeConfigScopeAPITask(t, ctx, db, saved.Task)
			metadata, err := db.ConfigClientMetadata(ctx, saved.Config.ID, saved.Config.Version)
			if err != nil || metadata[plan.Tag] == "" {
				t.Fatal("client metadata fixture missing")
			}
			fragment, _, err := serverconfig.InboundSource(core.EngineXray, saved.Config.Content, plan.Tag)
			if err != nil {
				t.Fatal(err)
			}
			var native map[string]json.RawMessage
			if err := json.Unmarshal([]byte(fragment), &native); err != nil {
				t.Fatal(err)
			}
			native["sniffing"] = json.RawMessage(`{"enabled":true,"destOverride":["http","tls"],"routeOnly":true}`)
			updated, err := json.Marshal(native)
			if err != nil {
				t.Fatal(err)
			}
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": saved.Config.Version, "intent": "deploy", "fragment": string(updated)}, http.StatusOK, &saved)
			var workspace agentConfigWorkspaceResource
			client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].TransportHost != plan.TransportHost {
				t.Fatal("native edit lost matching detached client fields")
			}
		})
	}
}
