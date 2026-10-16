package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestFailedSingBoxInboundDeletion(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, intent := range []string{"validate", "deploy"} {
			name := "ordinary/" + intent
			if shared {
				name = "shared/" + intent
			}
			t.Run(name, func(t *testing.T) {
				db, ctx, _, owner, cdn := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "failed-inbound-delete", core.EngineSingBox)
				agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances)
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true, ServiceStatus: "inactive"}}}); err != nil {
					t.Fatal(err)
				}
				client := owner
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, agent, cdn, 443, 21001)
					client = cdn
				}
				base := "/agents/" + agent.ID + "/configs/sing-box"
				var plan serverconfig.Input
				client.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
				plan.Port, plan.TLSPublicPort, plan.TLSServerName = 21001, 443, "failed.example.test"
				var saved configMutationResult
				client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "intent": "deploy", "input": plan}, http.StatusOK, &saved)
				claimed, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || claimed == nil || claimed.ID != saved.Task.ID {
					t.Fatalf("claim failed deployment: %+v %v", claimed, err)
				}
				if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "TLS certificate not ready"}); err != nil {
					t.Fatal(err)
				}
				// No valid deployment exists. Deletion uses only the saved tag;
				// the old certificate, runtime and draft credentials are irrelevant.
				client.call("POST", base+"/server-inbounds", map[string]any{"operation": "delete", "original_tag": plan.Tag, "expected_version": saved.Config.Version, "intent": intent}, http.StatusOK, &saved)
				if len(serverconfig.ParseAll(core.EngineSingBox, saved.Config.Content)) != 0 || strings.Contains(saved.Config.Content, "v2ray_api") || strings.Contains(saved.Config.Content, "qch-trf-") {
					t.Fatalf("saved deletion retained inbound or generated statistics: %s", saved.Config.Content)
				}
				claimed, err = db.ClaimTask(ctx, agent.ID)
				if err != nil || claimed == nil || claimed.ID != saved.Task.ID {
					t.Fatalf("claim deletion: %+v %v", claimed, err)
				}
				if string(claimed.Action) != intent || len(claimed.TLSFrontends) != 0 || strings.Contains(claimed.ConfigContent, "v2ray_api") || shared != (claimed.SharedTrafficID != "") {
					t.Fatalf("deletion task retained failed runtime dependencies: %+v", claimed)
				}
				if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}); err != nil {
					t.Fatal(err)
				}
				var workspace struct {
					Config   *core.Config         `json:"config"`
					Inbounds []serverconfig.Input `json:"inbounds"`
				}
				client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if workspace.Config == nil || workspace.Config.Content != saved.Config.Content || len(workspace.Inbounds) != 0 {
					t.Fatalf("deleted inbound returned after reload: %+v", workspace)
				}
				// The same account can reuse the removed public/backend ports.
				client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
				if len(serverconfig.ParseAll(core.EngineSingBox, saved.Config.Content)) != 1 {
					t.Fatal("could not add a fresh preset after failed-deployment cleanup")
				}
			})
		}
	}
}
