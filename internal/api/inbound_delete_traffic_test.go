package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestDeleteAndManualEditRecoverVerifiedAccounting(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		for _, operation := range []string{"delete", "source", "manual", "conflict"} {
			t.Run(string(engine)+"/"+operation, func(t *testing.T) {
				db, ctx, _, owner, _ := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "accounting-recovery", engine)
				content := `{"inbounds":[{"tag":"keep","protocol":"socks","port":21001,"settings":{"auth":"noauth"}},{"tag":"remove","protocol":"socks","port":21002,"settings":{"auth":"noauth"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["custom.test"],"outboundTag":"direct"}]}}`
				if engine == core.EngineSingBox {
					content = `{"inbounds":[{"tag":"keep","type":"socks","listen_port":21001},{"tag":"remove","type":"socks","listen_port":21002}],"outbounds":[{"tag":"direct","type":"direct"}],"route":{"rules":[{"domain":["custom.test"],"outbound":"direct"}]}}`
				}
				plan, err := serverconfig.PrepareAccounting(engine, content)
				if err != nil {
					t.Fatal(err)
				}
				// Save the verifiable predecessor and a stale historical manual edit.
				scoped := store.WithConfigScope(ctx, owner.userID, false)
				saved, err := db.SaveAgentConfig(scoped, core.Config{AgentID: agent.ID, Name: "recovery", Engine: engine, Content: plan.Content}, 0)
				if err != nil {
					t.Fatal(err)
				}
				saved.Content = strings.Replace(saved.Content, `"port": 21002`, `"port": 21003`, 1)
				saved.Content = strings.Replace(saved.Content, `"listen_port": 21002`, `"listen_port": 21003`, 1)
				if operation == "conflict" {
					saved.Content = strings.Replace(saved.Content, `"tag": "qch-trf-21002-`, `"unexpected": true, "tag": "qch-trf-21002-`, 1)
				}
				saved, err = db.SaveAgentConfig(scoped, saved, 1)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := serverconfig.PresetAccountingSource(engine, saved.Content); err == nil {
					t.Fatal("fixture must reproduce old accounting rejection")
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				if operation == "manual" {
					var updated core.Config
					owner.call("PUT", base, core.Config{AgentID: agent.ID, Engine: engine, Version: 2, Name: saved.Name, Content: saved.Content}, http.StatusOK, &updated)
					if strings.Contains(updated.Content, "qch-trf-21002-") || !strings.Contains(updated.Content, "qch-trf-21003-") {
						t.Fatal("manual save retained old generated port")
					}
				} else if operation == "source" {
					var result configMutationResult
					owner.call("POST", base+"/source", map[string]any{"engine": engine, "version": 2, "name": saved.Name, "content": saved.Content, "intent": "validate"}, http.StatusOK, &result)
					if strings.Contains(result.Config.Content, "qch-trf-21002-") {
						t.Fatal("source retained old generation")
					}
				} else {
					status := http.StatusOK
					if operation == "conflict" {
						status = http.StatusBadRequest
					}
					body := owner.call("POST", base+"/server-inbounds", map[string]any{"operation": "delete", "original_tag": "remove", "expected_version": 2, "intent": "deploy"}, status, nil)
					if operation == "conflict" {
						if !strings.Contains(string(body), "删除入站失败") {
							t.Fatalf("wrong operation feedback: %s", body)
						}
						return
					}
					owner.call("GET", base, nil, http.StatusOK, &saved)
					if len(serverconfig.DiscoverTrafficPorts(engine, saved.Content)) != 1 || !strings.Contains(saved.Content, "custom.test") || strings.Contains(saved.Content, "qch-trf-21002-") {
						t.Fatal("delete lost custom route or retained removed stats")
					}
					lease, err := db.ClaimTask(ctx, agent.ID)
					if err != nil || lease == nil {
						t.Fatalf("claim %v", err)
					}
					if err := db.CompleteTask(ctx, agent.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
						t.Fatal(err)
					}
					var endpoints []core.PortTrafficEndpoint
					owner.call("GET", "/traffic-endpoints", nil, http.StatusOK, &endpoints)
					if len(endpoints) != 1 || endpoints[0].Port != 21001 {
						t.Fatalf("wrong applied response %+v", endpoints)
					}
				}
			})
		}
	}
}
