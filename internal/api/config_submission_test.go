package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestEmptySourceRestoreDeploymentAcrossEnginesAndInstances(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, shared := range []bool{false, true} {
			name := string(engine) + "/owner"
			if shared {
				name = string(engine) + "/shared"
			}
			t.Run(name, func(t *testing.T) {
				db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
				node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "reset-restore", engine)
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: node.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true, ServiceStatus: "active"}}}); err != nil {
					t.Fatal(err)
				}
				client := owner
				instance := ""
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 21001)
					client = recipient
					access, err := db.UserAgentAccess(ctx, client.userID)
					if err != nil {
						t.Fatal(err)
					}
					instance = access.Shares[0].ID
				}
				protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolSS2022)
				plan, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				plan.Port = 21001
				original, err := serverconfig.Generate(engine, plan)
				if err != nil {
					t.Fatal(err)
				}
				base := "/agents/" + node.ID + "/configs/" + string(engine)
				var first, saved core.Config
				client.call("PUT", base, core.Config{Name: "original", Content: original}, http.StatusOK, &first)
				empty := "{}\n"
				if engine == core.EngineMihomo {
					empty = "listeners: []\nrules: ['MATCH,DIRECT']\n"
				}
				client.call("PUT", base, core.Config{Name: "empty reset", Content: empty, Version: first.Version}, http.StatusOK, &saved)
				deploy := func(content string, version int, disabled bool) configMutationResult {
					t.Helper()
					var result configMutationResult
					client.call("POST", base+"/source", map[string]any{"name": "restorable source", "content": content, "version": version, "intent": "deploy", "expected_instance_id": instance}, http.StatusOK, &result)
					lease, err := db.ClaimTask(ctx, node.ID)
					if err != nil || lease == nil || lease.ID != result.Task.ID || lease.ConfigVersion != result.Config.Version || lease.SharedInstance != shared {
						t.Fatalf("deployment snapshot mismatch: %+v %v", lease, err)
					}
					compiled, err := serverconfig.PrepareIndependentEgress(engine, lease.ConfigContent)
					if err != nil || (compiled.Source == "disabled") != disabled {
						t.Fatalf("incorrect empty/live plan: %+v %v", compiled, err)
					}
					if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}); err != nil {
						t.Fatal(err)
					}
					return result
				}
				reset := deploy(empty, saved.Version, true)
				var restored core.Config
				client.call("POST", "/configs/"+first.ID+"/revisions/1/restore", map[string]any{"expected_version": reset.Config.Version}, http.StatusOK, &restored)
				if restored.Content != original || restored.Version != reset.Config.Version+1 {
					t.Fatal("restore lost original source or failed to create a revision")
				}
				live := deploy(restored.Content, restored.Version, false)
				var current core.Config
				client.call("GET", base, nil, http.StatusOK, &current)
				if current.Content != live.Config.Content || current.Version != live.Config.Version || !reflect.DeepEqual(serverconfig.ParseAll(engine, current.Content), serverconfig.ParseAll(engine, original)) {
					t.Fatal("restored deployment changed saved source")
				}
				var archive core.Config
				client.call("GET", "/configs/"+first.ID+"/revisions/1", nil, http.StatusOK, &archive)
				if archive.Content != original {
					t.Fatal("restore overwrote original archive")
				}
			})
		}
	}
}

func TestSourceSubmissionRepairsBrokenSavedAccounting(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "owner"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "source-repair", core.EngineSingBox)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true, ServiceStatus: "active"}}}); err != nil {
				t.Fatal(err)
			}
			client, foreign := owner, recipient
			if shared {
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001)
				client, foreign = recipient, owner
			}
			base := "/agents/" + agent.ID + "/configs/sing-box"
			instanceID := ""
			if shared {
				access, err := db.UserAgentAccess(ctx, client.userID)
				if err != nil {
					t.Fatal(err)
				}
				instanceID = access.Shares[0].ID
			}
			var saved core.Config
			client.call("PUT", base, core.Config{Name: "broken saved source", Content: `{}`}, http.StatusOK, &saved)
			// The error must reject the transaction, without creating a revision/task.
			submit := func(content string, version int, status int) configMutationResult {
				t.Helper()
				var result configMutationResult
				client.call("POST", base+"/source", map[string]any{"name": "source repair", "content": content, "version": version, "intent": "deploy", "expected_instance_id": instanceID}, status, &result)
				return result
			}
			submit(`{"inbounds":[{"type":"http","tag":"missing-exit","listen_port":21001}]}`, saved.Version, http.StatusBadRequest)
			var current core.Config
			client.call("GET", base, nil, http.StatusOK, &current)
			if current.Version != saved.Version || current.Content != saved.Content {
				t.Fatal("rejected source was saved")
			}
			var tasks []core.Task
			client.call("GET", "/tasks?agent_id="+agent.ID, nil, http.StatusOK, &tasks)
			if len(executionTasks(t, tasks)) != 0 {
				t.Fatal("rejected source created a task")
			}
			source := `{"log":{"level":"warn"},"inbounds":[{"type":"http","tag":"edited","listen":"127.0.0.1","listen_port":21001}],"outbounds":[{"type":"direct","tag":"original"}],"route":{"rules":[{"domain":["example.test"],"outbound":"original"}]}}`
			plan, err := serverconfig.PrepareAccounting(core.EngineSingBox, source)
			if err != nil {
				t.Fatal(err)
			}
			client.call("POST", base+"/source", map[string]any{"name": "wrong instance", "content": plan.Content, "version": saved.Version, "intent": "deploy", "expected_instance_id": "stale-instance"}, http.StatusConflict, nil)
			client.call("GET", base, nil, http.StatusOK, &current)
			if current.Version != saved.Version {
				t.Fatal("instance conflict saved a revision")
			}
			result := submit(plan.Content, saved.Version, http.StatusOK)
			if result.Config.Version != saved.Version+1 || result.Task.ConfigID != saved.ID || result.Task.ConfigVersion != result.Config.Version || result.Task.SharedInstance != shared {
				t.Fatal("repair lost exact revision or instance")
			}
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil || claimed.ID != result.Task.ID {
				t.Fatalf("claim repaired task: %v", err)
			}
			if !strings.Contains(claimed.ConfigContent, `"tag": "edited"`) || serverconfig.ValidateIndependentEgress(core.EngineSingBox, claimed.ConfigContent) != nil {
				t.Fatal("task snapshot did not contain valid new source")
			}
			completion := core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}
			if shared {
				completion.InstanceStatus = &core.SharedInstanceStatus{Engine: claimed.Engine, ShareID: claimed.SharedTrafficID, ConfigID: claimed.ConfigID, ConfigVersion: claimed.ConfigVersion, TaskID: claimed.ID, AppliedTaskID: claimed.ID, Status: "active"}
			}
			if err := db.CompleteTask(ctx, agent.ID, claimed.ID, completion); err != nil {
				t.Fatal(err)
			}
			var receipt core.Task
			client.call("GET", "/tasks/"+claimed.ID+"?view=status", nil, http.StatusOK, &receipt)
			if receipt.Status != core.TaskSucceeded || receipt.SharedTrafficID != instanceID || receipt.SharedInstance != shared {
				t.Fatal("task receipt lost completion")
			}
			foreign.call("GET", "/tasks/"+claimed.ID, nil, http.StatusNotFound, nil)
			admin.call("GET", base, nil, http.StatusNotFound, nil)
			var revisions []core.Config
			client.call("GET", "/configs/"+saved.ID+"/revisions", nil, http.StatusOK, &revisions)
			if len(revisions) != 2 || revisions[0].Version != result.Config.Version || !revisions[0].UpdatedAt.After(saved.UpdatedAt) {
				t.Fatal("repair did not preserve separately dated archives")
			}
			client.call("GET", "/configs/"+saved.ID+"/revisions/1", nil, http.StatusOK, &current)
			if current.Content != saved.Content {
				t.Fatal("repair modified original archive")
			}
			submit(plan.Content, saved.Version, http.StatusConflict)
		})
	}
}

func TestConfigSubmissionReceiptAndOfflineReality(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "owner"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "submission", core.EngineXray)
			client, foreign := owner, recipient
			if shared {
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001)
				client, foreign = recipient, owner
			}
			base := "/agents/" + agent.ID + "/configs/xray"
			submit := func(suffix string, value any, id string, status int) configMutationResult {
				t.Helper()
				encoded, _ := json.Marshal(value)
				request := httptest.NewRequest("POST", "/api/v1"+base+suffix, bytes.NewReader(encoded))
				request.AddCookie(client.cookie)
				request.Header.Set(csrfHeader, client.csrf)
				request.Header.Set("X-Config-Task-ID", id)
				response := httptest.NewRecorder()
				client.handler.ServeHTTP(response, request)
				if response.Code != status {
					t.Fatalf("submission: %d %s, want %d", response.Code, response.Body.String(), status)
				}
				var result configMutationResult
				if status == http.StatusOK {
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
				}
				return result
			}
			// First source save and its validation task commit together.
			source := map[string]any{"name": "source", "content": `{"inbounds":[],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`, "version": 0, "intent": "validate"}
			submit("/source", source, "invalid", http.StatusBadRequest)
			client.call("GET", base, nil, http.StatusNotFound, nil)
			result := submit("/source", source, "tsk_0123456789abcdef", http.StatusOK)
			if result.Task.ID != "tsk_0123456789abcdef" || result.Task.ConfigVersion != result.Config.Version {
				t.Fatal("task receipt lost exact revision")
			}
			// Read-only reconciliation works after the caller loses the POST response.
			var receipt core.Task
			client.call("GET", "/tasks/"+result.Task.ID+"?view=status", nil, http.StatusOK, &receipt)
			if receipt.ConfigID != result.Config.ID || receipt.ConfigVersion != result.Config.Version {
				t.Fatal("receipt does not identify saved config")
			}
			foreign.call("GET", "/tasks/"+result.Task.ID, nil, http.StatusNotFound, nil)
			var claimed *core.Task
			var err error
			claimed, err = db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil {
				t.Fatalf("claim: %v", err)
			}
			if err = db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
				t.Fatal(err)
			}
			// Syntactically valid but deliberately unresolvable camouflage target.
			protocol, _ := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolVLESSXHTTP)
			plan, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			plan.Tag, plan.Port, plan.RealityServerName, plan.RealityTarget = "reality", 21001, "unreachable.example.invalid", "unreachable.example.invalid:443"
			result = submit("/server-inbounds", map[string]any{"operation": "add", "expected_version": 1, "intent": "validate", "input": plan}, "tsk_1123456789abcdef", http.StatusOK)
			if result.Task.ID != "tsk_1123456789abcdef" || result.Config.Version != 2 {
				t.Fatal("offline target blocked atomic save")
			}
			// Invalid local credentials still fail before either revision or task.
			plan.Credential = "invalid-uuid"
			submit("/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": 2, "intent": "deploy", "input": plan}, "tsk_2123456789abcdef", http.StatusBadRequest)
			var current core.Config
			client.call("GET", base, nil, http.StatusOK, &current)
			if current.Version != 2 {
				t.Fatal("invalid local input saved a revision")
			}
			// Duplicate/stale sends cannot enqueue another deployment.
			source["version"] = 1
			submit("/source", source, "tsk_3123456789abcdef", http.StatusConflict)
		})
	}
}
