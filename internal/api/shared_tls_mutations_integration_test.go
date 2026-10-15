package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSharedTLSCertificateEditsAndFinalInboundDeletion(t *testing.T) {
	for _, unrestricted := range []bool{false, true} {
		name := "allocated"
		if unrestricted {
			name = "unrestricted"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _, owner, cdn := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "shared-tls-edits", core.EngineSingBox)
			agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true, ServiceStatus: "inactive"}}}); err != nil {
				t.Fatal(err)
			}
			ports := []int{443, 21001, 21002}
			if unrestricted {
				ports = []int{0}
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, cdn, ports...)
			base := "/agents/" + agent.ID + "/configs/sing-box"
			var plan serverconfig.Input
			cdn.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
			plan.Port, plan.TLSPublicPort, plan.TLSServerName = 21001, 443, "first.example.test"
			var saved configMutationResult
			mutate := func(operation, original string) {
				t.Helper()
				cdn.call("POST", base+"/server-inbounds", map[string]any{"operation": operation, "original_tag": original, "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
				if saved.Task.SharedTrafficID == "" || !saved.Task.SharedInstance {
					t.Fatal("shared deployment lost its instance")
				}
				claimed, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || claimed == nil || claimed.ID != saved.Task.ID {
					t.Fatalf("claim: %+v %v", claimed, err)
				}
				endpoints, err := serverconfig.SharedTrafficEndpoints(core.EngineSingBox, saved.Config.Content)
				if err != nil || len(claimed.TLSFrontends) != len(endpoints) {
					t.Fatalf("TLS routes do not match remaining inbounds: %d %+v %v", len(claimed.TLSFrontends), endpoints, err)
				}
				if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}); err != nil {
					t.Fatal(err)
				}
			}
			mutate("add", "")
			for _, mode := range []string{"nginx", "caddy", "nginx"} {
				plan.TLSMode, plan.TLSServerName = mode, mode+".example.test"
				mutate("modify", plan.Tag)
			}
			first := plan
			plan.Tag, plan.Port, plan.TransportPath = "second", 21002, "/second"
			mutate("add", "")
			plan = first
			mutate("delete", first.Tag)
			plan.Tag = "second"
			mutate("delete", "second")
			endpoints, err := serverconfig.SharedTrafficEndpoints(core.EngineSingBox, saved.Config.Content)
			if err != nil || len(endpoints) != 0 {
				t.Fatalf("final deletion: %v %v", endpoints, err)
			}
			var workspace struct {
				Inbounds []serverconfig.Input `json:"inbounds"`
			}
			cdn.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if len(workspace.Inbounds) != 0 {
				t.Fatal("last inbound remains")
			}
			plan = first
			mutate("add", "")
		})
	}
}

func TestSharedTLSPortConflictRetainsRevisionAndAllowsRetry(t *testing.T) {
	for _, unrestricted := range []bool{false, true} {
		name := "allocated"
		if unrestricted {
			name = "unrestricted"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _, owner, cdn := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "shared-tls-port-conflicts", core.EngineSingBox)
			agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true, ServiceStatus: "inactive"}}}); err != nil {
				t.Fatal(err)
			}
			ports := []int{21001, 8443}
			if unrestricted {
				ports = []int{0}
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, cdn, ports...)
			base := "/agents/" + agent.ID + "/configs/sing-box"
			var plan serverconfig.Input
			cdn.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
			plan.Port, plan.TLSPublicPort, plan.TLSServerName = 21001, 443, "cdn.example.test"
			request := map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}
			assertConflict := func() {
				t.Helper()
				var conflict struct {
					Code, Field string
					Port        int
				}
				cdn.call("POST", base+"/server-inbounds", request, http.StatusConflict, &conflict)
				if conflict.Code != "port_conflict" || conflict.Field != "tls_public_port" || conflict.Port != 443 {
					t.Fatalf("public port conflict misclassified: %+v", conflict)
				}
				var workspace struct{ Config *core.Config }
				cdn.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if workspace.Config != nil {
					t.Fatal("rejected port conflict committed a draft")
				}
				var tasks []core.Task
				cdn.call("GET", "/tasks?agent_id="+agent.ID, nil, http.StatusOK, &tasks)
				if len(tasks) != 0 {
					t.Fatal("rejected port conflict created a task")
				}
			}
			if !unrestricted {
				assertConflict() // The public port has not been allocated.
			}
			ownerPlan := plan
			ownerPlan.Port, ownerPlan.TLSServerName = 22001, "owner.example.test"
			var owned configMutationResult
			owner.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": ownerPlan}, http.StatusOK, &owned)
			completeConfigScopeAPITask(t, ctx, db, owned.Task)
			assertConflict() // Another account has a live frontend on 443.
			plan.TLSPublicPort = 8443
			request["input"] = plan
			var saved configMutationResult
			cdn.call("POST", base+"/server-inbounds", request, http.StatusOK, &saved)
			completeConfigScopeAPITask(t, ctx, db, saved.Task)
			request["operation"], request["original_tag"] = "modify", plan.Tag
			var stale struct{ Code string }
			cdn.call("POST", base+"/server-inbounds", request, http.StatusConflict, &stale)
			if stale.Code == "port_conflict" {
				t.Fatal("stale revision was presented as a recoverable port conflict")
			}
			request["expected_version"] = saved.Config.Version
			plan.TLSMode = "nginx"
			request["input"] = plan
			cdn.call("POST", base+"/server-inbounds", request, http.StatusOK, &saved)
			completeConfigScopeAPITask(t, ctx, db, saved.Task)
		})
	}
}
