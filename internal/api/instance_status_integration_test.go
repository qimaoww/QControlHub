package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestInboundEditAfterVerifiedInstanceRefresh(t *testing.T) {
	for index, engine := range core.AllEngines() {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", engine, shared), func(t *testing.T) {
				db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
				node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "instance-recovery", core.AllEngines()...)
				node.Features = append(node.Features, core.AgentFeatureInstanceStatus)
				runtime := map[core.Engine]core.RuntimeState{}
				for _, kind := range core.AllEngines() {
					runtime[kind] = core.RuntimeState{Installed: true, ServiceStatus: "active"}
				}
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: node.Features, Runtime: runtime}); err != nil {
					t.Fatal(err)
				}
				target := owner
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 32411)
					target = recipient
				}
				foreignEngine := core.AllEngines()[(index+1)%len(core.AllEngines())]
				newPlan := func(kind core.Engine) serverconfig.Input {
					t.Helper()
					var plan serverconfig.Input
					owner.call("POST", "/agents/"+node.ID+"/configs/"+string(kind)+"/plans", map[string]string{"protocol": "ss2022"}, http.StatusCreated, &plan)
					return plan
				}
				foreignPlan := newPlan(foreignEngine)
				foreignPlan.Port = 24441
				foreignBase := "/agents/" + node.ID + "/configs/" + string(foreignEngine)
				var foreign configMutationResult
				owner.call("POST", foreignBase+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": foreignPlan}, http.StatusOK, &foreign)
				completeConfigScopeAPITask(t, ctx, db, foreign.Task)
				retained := foreign.Config
				plan := newPlan(engine)
				plan.Port = 32411
				base := "/agents/" + node.ID + "/configs/" + string(engine)
				var saved configMutationResult
				target.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				foreignPlan.Port = 24442
				owner.call("POST", foreignBase+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": foreignPlan.Tag, "expected_version": foreign.Config.Version, "intent": "deploy", "input": foreignPlan}, http.StatusOK, &foreign)
				lease, err := db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != foreign.Task.ID {
					t.Fatalf("failure claim: %+v %v", lease, err)
				}
				if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: false, Error: "isolated preflight failure"}); err != nil {
					t.Fatal(err)
				}
				next := newPlan(engine)
				plan.Credential = next.Credential
				original := plan.Tag
				mutate := func(intent string, status int) {
					t.Helper()
					target.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": original, "expected_version": saved.Config.Version, "intent": intent, "input": plan}, status, &saved)
				}
				// Preserve the original draft version after each rejected operation.
				for _, intent := range []string{"validate", "deploy"} {
					target.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": original, "expected_version": saved.Config.Version, "intent": intent, "input": plan}, http.StatusConflict, nil)
				}
				var refresh core.Task
				owner.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: foreignEngine, Action: core.ActionStatus}, http.StatusCreated, &refresh)
				lease, err = db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != refresh.ID || lease.ConfigVersion != retained.Version || lease.ConfigID != retained.ID {
					t.Fatalf("refresh used wrong deployment: %+v %v", lease, err)
				}
				if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: "active", InstanceStatus: &core.SharedInstanceStatus{Engine: foreignEngine, TaskID: lease.ID, ConfigID: retained.ID, ConfigVersion: retained.Version, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
					t.Fatal(err)
				}
				stale := saved.Config.Version
				mutate("validate", http.StatusOK)
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				plan.Tag = "renamed"
				mutate("deploy", http.StatusOK)
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				var workspace struct {
					Inbounds []serverconfig.Input `json:"inbounds"`
				}
				target.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].Tag != "renamed" || workspace.Inbounds[0].Port != 32411 || workspace.Inbounds[0].Credential != plan.Credential {
					t.Fatal("authentication edit/rename did not replace the original inbound")
				}
				target.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": "renamed", "expected_version": stale, "intent": "deploy", "input": plan}, http.StatusConflict, nil)
			})
		}
	}
}
