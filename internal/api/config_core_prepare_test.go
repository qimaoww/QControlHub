package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestConfigSourceFirstInstallAcrossEnginesAndInstances(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, shared := range []bool{false, true} {
			name := string(engine) + map[bool]string{false: "/owner", true: "/shared"}[shared]
			t.Run(name, func(t *testing.T) {
				db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "source-first-install", engine)
				features := append(agent.Features, core.AgentFeaturePresetAutoInstall, core.AgentFeatureSharedCorePrepare)
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: false}}}); err != nil {
					t.Fatal(err)
				}
				client := owner
				instance := ""
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001)
					client = recipient
					access, err := db.UserAgentAccess(ctx, client.userID)
					if err != nil {
						t.Fatal(err)
					}
					instance = access.Shares[0].ID
				}
				protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolSS2022)
				input, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Port = 21001
				content, err := serverconfig.Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				var result configMutationResult
				// Save-only validation retains its existing API semantics.
				client.call("POST", base+"/source", map[string]any{"name": "source-first", "content": content, "version": 0, "intent": "validate", "expected_instance_id": instance}, http.StatusOK, &result)
				if result.Task.InstallIfMissing {
					t.Fatal("ordinary validation unexpectedly prepared a core")
				}
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				var first core.Config
				client.call("GET", base, nil, http.StatusOK, &first)
				// Deploy an already saved source while the binary is still missing.
				payload := map[string]any{"name": "source-first", "content": first.Content, "version": first.Version, "intent": "deploy", "install_if_missing": true, "expected_instance_id": instance}
				client.call("POST", base+"/source", payload, http.StatusOK, &result)
				if !result.Task.InstallIfMissing || result.Task.SharedInstance != shared || result.Task.SharedTrafficID != instance || result.Task.ConfigVersion != result.Config.Version {
					t.Fatalf("wrong preparation target: %+v", result.Task)
				}
				// Repeating the same submission cannot create another revision/task.
				client.call("POST", base+"/source", payload, http.StatusConflict, nil)
				lease, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || lease == nil || lease.ID != result.Task.ID {
					t.Fatalf("claim: %+v %v", lease, err)
				}
				if err := db.CompleteTask(ctx, agent.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Error: "configuration validation failed after installation", TrafficSettled: true}); err != nil {
					t.Fatal(err)
				}
				retry, err := db.RetryTask(store.WithConfigScope(ctx, client.userID, false), lease.ID)
				if err != nil || retry.ConfigVersion != lease.ConfigVersion || retry.SharedTrafficID != instance || !retry.InstallIfMissing {
					t.Fatalf("retry lost reviewed target: %+v %v", retry, err)
				}
				completeConfigScopeAPITask(t, ctx, db, retry)
				// The archive entry uses the controlled exact-revision
				// configuration task API; it must not create a second configuration record.
				var archived core.Task
				client.call("POST", "/configs/"+result.Config.ID+"/tasks", map[string]any{"agent_id": agent.ID, "action": "validate", "expected_config_version": result.Config.Version, "install_if_missing": true, "expected_instance_id": instance}, http.StatusCreated, &archived)
				if !archived.InstallIfMissing || archived.SharedTrafficID != instance || archived.ConfigVersion != result.Config.Version {
					t.Fatal("archive preparation lost scope or snapshot")
				}
				completeConfigScopeAPITask(t, ctx, db, archived)
				other := recipient
				if shared {
					other = owner
				}
				archivePath := "/configs/" + result.Config.ID + "/tasks"
				other.call("POST", archivePath, map[string]any{"agent_id": agent.ID, "action": "deploy", "expected_config_version": result.Config.Version, "expected_instance_id": instance, "install_if_missing": true}, http.StatusNotFound, nil)
				client.call("POST", archivePath, map[string]any{"agent_id": agent.ID, "action": "restart", "expected_config_version": result.Config.Version, "expected_instance_id": instance, "install_if_missing": true}, http.StatusBadRequest, nil)
				client.call("POST", archivePath, map[string]any{"agent_id": agent.ID, "action": "deploy", "expected_config_version": result.Config.Version, "expected_instance_id": "different-instance", "install_if_missing": true}, http.StatusConflict, nil)

				client.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": input.Tag, "expected_version": result.Config.Version, "intent": "deploy", "install_if_missing": true, "input": input, "expected_instance_id": instance}, http.StatusOK, &result)
				if !result.Task.InstallIfMissing || result.Task.SharedTrafficID != instance {
					t.Fatal("preset edit lost preparation")
				}
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				fragment, _, err := serverconfig.InboundSource(engine, result.Config.Content, input.Tag)
				if err != nil {
					t.Fatal(err)
				}
				client.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": input.Tag, "expected_version": result.Config.Version, "intent": "deploy", "install_if_missing": true, "fragment": fragment, "expected_instance_id": instance}, http.StatusOK, &result)
				if !result.Task.InstallIfMissing {
					t.Fatal("native inbound edit lost preparation")
				}
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				var revisions []core.Config
				client.call("GET", "/configs/"+result.Config.ID+"/revisions", nil, http.StatusOK, &revisions)
				if len(revisions) != 4 {
					t.Fatalf("lost distinct saves: %d", len(revisions))
				}
				for _, revision := range revisions {
					if revision.UpdatedAt.IsZero() {
						t.Fatal("archive has no real save time")
					}
				}
				// v1 Agents can activate defaults during preparation; refuse their tasks.
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: append(agent.Features, "preset-auto-install-v1", core.AgentFeatureSharedCorePrepare), Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: false}}}); err != nil {
					t.Fatal(err)
				}
				client.call("POST", archivePath, map[string]any{"agent_id": agent.ID, "action": "deploy", "expected_config_version": result.Config.Version, "expected_instance_id": instance, "install_if_missing": true}, http.StatusConflict, nil)
				client.call("POST", base+"/source", map[string]any{"name": "old-agent", "content": result.Config.Content, "version": result.Config.Version, "intent": "deploy", "install_if_missing": true, "expected_instance_id": instance}, http.StatusConflict, nil)
				var unchanged core.Config
				client.call("GET", base, nil, http.StatusOK, &unchanged)
				if unchanged.Version != result.Config.Version {
					t.Fatal("failed preparation saved a revision")
				}
			})
		}
	}
}
