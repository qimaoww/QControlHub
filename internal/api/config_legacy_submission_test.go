package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestSourceSubmissionRepairsArchivedAlternateSingBoxBackend(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "owner"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "legacy-source", core.EngineSingBox)
			client, foreign := owner, recipient
			if shared {
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001, 22001)
				client, foreign = recipient, owner
			}
			base := "/agents/" + agent.ID + "/configs/sing-box"
			const source = `{"inbounds":[{"type":"http","tag":"listener","listen":"127.0.0.1","listen_port":21001}],"outbounds":[{"type":"direct","tag":"original"}],"route":{"rules":[{"domain":["example.test"],"outbound":"original"}]}}`
			plan, err := serverconfig.PrepareAccounting(core.EngineSingBox, source)
			if err != nil {
				t.Fatal(err)
			}
			scope := store.WithConfigScope(ctx, client.userID, false)
			saved, err := db.SaveAgentConfig(scope, core.Config{AgentID: agent.ID, Engine: core.EngineSingBox, Name: "legacy", Content: plan.Content}, 0)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := serverconfig.PrepareMarkedSingBoxAccounting(plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			stale := strings.Replace(runtime.Content, `"listen_port": 21001`, `"listen_port": 22001`, 1)
			saved.Content = stale
			saved, err = db.SaveAgentConfig(scope, saved, saved.Version)
			if err != nil {
				t.Fatal(err)
			}
			// Reject both an empty draft and an altered reserved clone without
			// advancing the archive or leaving a task behind.
			for _, content := range []string{`{}`, strings.Replace(stale, `"routing_mark":`, `"unexpected": true, "routing_mark":`, 1)} {
				client.call("POST", base+"/source", map[string]any{"name": "legacy", "version": saved.Version, "intent": "deploy", "content": content}, http.StatusBadRequest, nil)
			}
			var revisions []core.Config
			client.call("GET", "/configs/"+saved.ID+"/revisions", nil, http.StatusOK, &revisions)
			var tasks []core.Task
			client.call("GET", "/tasks?agent_id="+agent.ID, nil, http.StatusOK, &tasks)
			if len(revisions) != 2 || len(tasks) != 0 {
				t.Fatal("rejected source committed a revision or task")
			}
			var result configMutationResult
			client.call("POST", base+"/source", map[string]any{"name": "repaired", "version": saved.Version, "intent": "deploy", "content": stale}, http.StatusOK, &result)
			if result.Config.ID != saved.ID || result.Config.Version != saved.Version+1 || result.Task.SharedInstance != shared {
				t.Fatal("repair changed the configuration or instance identity")
			}
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil || claimed.ID != result.Task.ID {
				t.Fatalf("claim repaired task: %v", err)
			}
			plan, err = serverconfig.PrepareIndependentEgress(core.EngineSingBox, claimed.ConfigContent)
			if err != nil || len(plan.Ports) != 1 || plan.Ports[0].Port != 22001 || !strings.Contains(claimed.ConfigContent, "example.test") {
				t.Fatalf("task did not use the repaired source: %v", err)
			}
			foreign.call("GET", "/configs/"+saved.ID+"/revisions", nil, http.StatusNotFound, nil)
			admin.call("GET", base, nil, http.StatusNotFound, nil)
			client.call("GET", base, nil, http.StatusOK, &saved)
			var workspace agentConfigWorkspaceResource
			client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if workspace.Config.Content != saved.Content || workspace.AccountingError != "" {
				t.Fatal("reopening did not retain the verified repair")
			}
		})
	}
}

func TestXHTTPTLSAdditionPreservesMainlandPolicyResources(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		for _, shared := range []bool{false, true} {
			name := string(engine) + "/owner"
			if shared {
				name = string(engine) + "/shared"
			}
			t.Run(name, func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "mainland-merge", engine)
				agent.Features = append(agent.Features, core.AgentFeatureXHTTPTLS, core.AgentFeatureXHTTP)
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
					t.Fatal(err)
				}
				client, foreign := owner, recipient
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001, 21002)
					client, foreign = recipient, owner
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				var saved core.Config
				content := "listeners: [{name: existing, type: http, listen: 127.0.0.1, port: 21001}]\nrules: ['DOMAIN,example.test,DIRECT', 'MATCH,DIRECT']\n"
				if engine == core.EngineXray {
					content = `{"inbounds":[{"tag":"existing","protocol":"http","listen":"127.0.0.1","port":21001}],"outbounds":[{"tag":"original","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["example.test"],"outboundTag":"original"}]}}`
				}
				client.call("PUT", base, core.Config{Name: "existing", Content: content}, http.StatusOK, &saved)
				protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolXHTTPTLS)
				input, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Port, input.Listen = "xhttp", 21002, "127.0.0.1"
				input.TLSMode, input.TLSServerName = "native", "fixture.example.test"
				input.CertificatePath, input.PrivateKeyPath = "/tmp/qch-fixture.crt", "/tmp/qch-fixture.key"
				input.BlockMainlandDestination, input.BlockMainlandSource = true, true
				var result configMutationResult
				client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": saved.Version, "intent": "deploy", "input": input}, http.StatusOK, &result)
				if result.Config.Version != saved.Version+1 || result.Task.SharedInstance != shared {
					t.Fatal("addition lost transactional version or instance")
				}
				policies := serverconfig.DiscoverMainlandAccessPolicies(engine, result.Config.Content)
				if len(policies) != 2 || policies[0].BlockMainlandDestination || policies[0].BlockMainlandSource || !policies[1].BlockMainlandDestination || !policies[1].BlockMainlandSource {
					t.Fatal("mainland policy was lost or widened to the existing listener")
				}
				source, err := serverconfig.PresetAccountingSource(engine, result.Config.Content)
				if err != nil || !strings.Contains(source, "example.test") || engine == core.EngineMihomo && (!strings.Contains(source, "DOMAIN,example.test,DIRECT") || !strings.Contains(source, "MATCH,DIRECT")) {
					t.Fatalf("custom routing changed: %v", err)
				}
				claimed, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || claimed == nil || claimed.ID != result.Task.ID || serverconfig.ValidateIndependentEgress(engine, claimed.ConfigContent) != nil {
					t.Fatalf("new inbound did not reach the correct valid task snapshot: %v", err)
				}
				foreign.call("GET", "/configs/"+result.Config.ID+"/revisions", nil, http.StatusNotFound, nil)
				admin.call("GET", base, nil, http.StatusNotFound, nil)
			})
		}
	}
}
