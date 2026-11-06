package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestXrayAPIMigrationDeleteAndRestore(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, remaining := range []bool{false, true} {
			for _, intent := range []string{"validate", "deploy"} {
				t.Run(fmt.Sprintf("shared=%t/remaining=%t/%s", shared, remaining, intent), func(t *testing.T) {
					db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
					agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "legacy-api", core.EngineXray)
					agent.Features = append(agent.Features, core.AgentFeatureSharedCoreInstances)
					if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}}}); err != nil {
						t.Fatal(err)
					}
					client := owner
					if shared {
						grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 8443, 21001)
						client = recipient
					}
					port := 40152
					if shared {
						port = 10085
					}
					keep := ""
					if remaining {
						keep = `,{"tag":"keep","protocol":"http","port":21001}`
					}
					content := fmt.Sprintf(`{"api":{"tag":"api","services":["HandlerService","LoggerService","StatsService"]},"inbounds":[{"tag":"api","protocol":"dokodemo-door","listen":"127.0.0.1","port":%d,"settings":{"address":"127.0.0.1"}},{"tag":"VLESS-REALITY-8443.json","protocol":"vless","port":8443,"settings":{"clients":[{"id":"b3625453-d453-4ce5-8abc-9ce94fc44087"}]}}%s],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"},{"type":"field","domain":["example.test"],"outboundTag":"direct"}]}}`, port, keep)
					plan, err := serverconfig.PrepareAccounting(core.EngineXray, content)
					if err != nil {
						t.Fatal(err)
					}
					scoped := store.WithConfigScope(ctx, client.userID, false)
					saved, err := db.SaveAgentConfig(scoped, core.Config{AgentID: agent.ID, Name: "legacy API", Engine: core.EngineXray, Content: plan.Content}, 0)
					if err != nil {
						t.Fatal(err)
					}
					base := "/agents/" + agent.ID + "/configs/xray"
					payload := map[string]any{"operation": "delete", "original_tag": "VLESS-REALITY-8443.json", "expected_version": 1, "intent": intent}
					// Consent is necessary; failure must roll back revision and task together.
					client.call("POST", base+"/server-inbounds", payload, http.StatusBadRequest, nil)
					var unchanged core.Config
					client.call("GET", base, nil, http.StatusOK, &unchanged)
					if unchanged.Version != 1 || unchanged.Content != saved.Content {
						t.Fatal("rejected migration changed the draft")
					}
					payload["disable_mutable_api"] = true
					var result configMutationResult
					client.call("POST", base+"/server-inbounds", payload, http.StatusOK, &result)
					if strings.Contains(result.Config.Content, "HandlerService") || strings.Contains(result.Config.Content, "8443") || !strings.Contains(result.Config.Content, "LoggerService") || !strings.Contains(result.Config.Content, "example.test") {
						t.Fatalf("migration contract: mutable=%t targetToken=%t logger=%t domain=%t remainingPorts=%+v", strings.Contains(result.Config.Content, "HandlerService"), strings.Contains(result.Config.Content, "8443"), strings.Contains(result.Config.Content, "LoggerService"), strings.Contains(result.Config.Content, "example.test"), serverconfig.DiscoverTrafficPorts(core.EngineXray, result.Config.Content))
					}
					want := 0
					if remaining {
						want = 1
					}
					if len(serverconfig.DiscoverTrafficPorts(core.EngineXray, result.Config.Content)) != want {
						t.Fatal("wrong remaining proxy set")
					}
					lease, err := db.ClaimTask(ctx, agent.ID)
					if err != nil || lease == nil || lease.ID != result.Task.ID || lease.SharedInstance != shared || lease.ConfigID != saved.ID || string(lease.Action) != intent {
						t.Fatalf("wrong execution target: %+v %v", lease, err)
					}
					if err := db.CompleteTask(ctx, agent.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: false, Error: "isolated failure"}); err != nil {
						t.Fatal(err)
					}
					var failed core.Task
					client.call("GET", "/tasks/"+lease.ID, nil, http.StatusOK, &failed)
					if failed.Status != core.TaskFailed {
						t.Fatal("execution failure reported as success")
					}
					var retried core.Task
					client.call("POST", "/tasks/"+lease.ID+"/retry", nil, http.StatusCreated, &retried)
					if retried.ConfigID != saved.ID || retried.ConfigVersion != 2 || retried.SharedInstance != shared {
						t.Fatal("retry changed configuration or instance identity")
					}
					completeConfigScopeAPITask(t, ctx, db, retried)
					// A stale deletion cannot resubmit. Restore has its own consent and CAS.
					client.call("POST", base+"/server-inbounds", payload, http.StatusConflict, nil)
					restore := "/configs/" + saved.ID + "/revisions/1/restore"
					client.call("POST", restore, map[string]any{"expected_version": 2}, http.StatusBadRequest, nil)
					client.call("POST", restore, map[string]any{"expected_version": 1, "disable_mutable_api": true}, http.StatusConflict, nil)
					var restored core.Config
					client.call("POST", restore, map[string]any{"expected_version": 2, "disable_mutable_api": true}, http.StatusOK, &restored)
					if restored.Version != 3 || strings.Contains(restored.Content, "HandlerService") || !strings.Contains(restored.Content, "8443") {
						t.Fatal("restore did not create a safe new archive")
					}
					var original core.Config
					client.call("GET", "/configs/"+saved.ID+"/revisions/1", nil, http.StatusOK, &original)
					if original.Content != saved.Content {
						t.Fatal("migration overwrote the source archive")
					}
					// A different user on the same node never gets this workspace's history.
					foreign := recipient
					if shared {
						foreign = owner
					}
					foreign.call("GET", "/configs/"+saved.ID+"/revisions", nil, http.StatusNotFound, nil)
					foreign.call("POST", restore, map[string]int{"expected_version": 3}, http.StatusNotFound, nil)
				})
			}
		}
	}
}
