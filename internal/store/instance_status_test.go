package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestStatusSnapshotUsesRetainedManagementState(t *testing.T) {
	for _, input := range sharedEngineConfigs() {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", input.Engine, shared), func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				_, owner := sharedTestUser(t, db, ctx, "retained-state-owner")
				user, recipient := sharedTestUser(t, db, ctx, "retained-state-recipient")
				node := allEnginesSharedTestAgent(t, db, owner)
				scope := owner
				if shared {
					access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: node.ID, Engines: core.AllEngines(), Ports: []int{0}}}})
					if err != nil {
						t.Fatal(err)
					}
					acceptSharedTestInvitations(t, db, ctx, user.ID, access)
					scope = recipient
				}
				input.AgentID = node.ID
				config, err := db.SaveAgentConfig(scope, input, 0)
				if err != nil {
					t.Fatal(err)
				}
				objects, err := serverconfig.ManagedObjects(input.Engine, input.Content, serverconfig.ManagedState{})
				if err != nil {
					t.Fatal(err)
				}
				state := serverconfig.ManagedState{}
				for _, object := range objects {
					if object.Inbound {
						state.Disabled = append(state.Disabled, serverconfig.ManagedResource{Section: object.Section, Tag: object.Tag})
					}
				}
				if len(state.Disabled) == 0 {
					t.Fatal("fixture has no inbound")
				}
				retained, deploy, err := db.SaveAgentConfigManagement(scope, config, config.Version, state, core.ActionDeploy)
				if err != nil {
					t.Fatal(err)
				}
				lease, err := db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != deploy.ID {
					t.Fatalf("claim retained: %v", err)
				}
				deployedContent := lease.ConfigContent
				if plan, err := serverconfig.PrepareIndependentEgress(input.Engine, deployedContent); err != nil || plan.Source != "disabled" {
					t.Fatalf("retained runtime is not empty: %v", err)
				}
				if err := completeSharedTestTask(db, ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				if _, err := db.SaveAgentConfig(scope, retained, retained.Version); err != nil {
					t.Fatal(err)
				}
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureInstanceStatus}}); err != nil {
					t.Fatal(err)
				}
				refresh, err := db.CreateTask(scope, core.TaskRequest{AgentID: node.ID, Engine: input.Engine, Action: core.ActionStatus})
				if err != nil {
					t.Fatal(err)
				}
				lease, err = db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != refresh.ID || lease.ConfigID != retained.ID || lease.ConfigVersion != retained.Version || lease.ConfigContent != deployedContent {
					t.Fatalf("status did not reproduce retained compiled snapshot: %+v %v", lease, err)
				}
			})
		}
	}
}

// Reproduce legacy failed deployments through the actual transactional task
// lifecycle. Refresh must identify the retained deployment, not the saved draft.
func TestStatusRefreshRecoversFailedHostDeploymentAcrossEnginesAndInstances(t *testing.T) {
	for index, original := range sharedEngineConfigs() {
		for _, sharedTarget := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared-target=%v", original.Engine, sharedTarget), func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				_, owner := sharedTestUser(t, db, ctx, "investigation-owner")
				user, recipient := sharedTestUser(t, db, ctx, "investigation-recipient")
				node := allEnginesSharedTestAgent(t, db, owner)
				access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: node.ID, Engines: core.AllEngines(), Ports: []int{0}}}})
				if err != nil {
					t.Fatal(err)
				}
				acceptSharedTestInvitations(t, db, ctx, user.ID, access)
				targetContext := owner
				if sharedTarget {
					targetContext = recipient
				}
				finish := func(want core.Task, success bool) {
					t.Helper()
					lease, err := db.ClaimTask(ctx, node.ID)
					if err != nil || lease == nil || lease.ID != want.ID {
						t.Fatalf("claim: %+v %v", lease, err)
					}
					if err := completeSharedTestTask(db, ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: success}); err != nil {
						t.Fatal(err)
					}
				}
				deploy := func(config core.Config, success bool) {
					t.Helper()
					task, err := db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
					if err != nil {
						t.Fatal(err)
					}
					finish(task, success)
				}
				original.AgentID = node.ID
				foreign, err := db.SaveAgentConfig(owner, original, 0)
				if err != nil {
					t.Fatal(err)
				}
				deploy(foreign, true)
				retained := foreign
				targetIndex := 0
				if index == 0 {
					targetIndex = 1
				}
				target := sharedEngineConfigs()[targetIndex]
				target.AgentID = node.ID
				target.Content = strings.ReplaceAll(target.Content, fmt.Sprint(21001+targetIndex), "32411")
				target, err = db.SaveAgentConfig(targetContext, target, 0)
				if err != nil {
					t.Fatal(err)
				}
				first, err := db.CreateTask(targetContext, core.TaskRequest{AgentID: node.ID, Engine: target.Engine, ConfigID: target.ID, Action: core.ActionDeploy})
				if err != nil {
					t.Fatal(err)
				}
				finish(first, true)
				foreign.Content = strings.ReplaceAll(foreign.Content, fmt.Sprint(21001+index), fmt.Sprint(22001+index))
				foreign, err = db.SaveAgentConfig(owner, foreign, foreign.Version)
				if err != nil {
					t.Fatal(err)
				}
				deploy(foreign, false)
				denied := func(label string) {
					t.Helper()
					for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
						_, _, err := db.SaveAgentConfigAndTask(targetContext, target, target.Version, ConfigMutationOptions{Action: action})
						if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "无法确认实例的端口状态") {
							t.Fatalf("%s: expected reproduction for %s: %v", label, action, err)
						}
						var conflict *PortConflictError
						if !errors.As(err, &conflict) || (!sharedTarget && conflict.RecoveryEngine != string(foreign.Engine)) || (sharedTarget && conflict.RecoveryEngine != "") {
							t.Fatalf("status recovery crossed instance ownership: %+v", conflict)
						}
					}
					saved, err := db.AgentConfig(targetContext, node.ID, target.Engine)
					if err != nil || saved.Version != target.Version {
						t.Fatalf("failed save changed draft version: %+v %v", saved, err)
					}
					t.Log(label + ": unchanged-port validate/deploy blocked; transaction did not save or queue a task")
				}
				denied("after unrelated failed host deployment")
				if _, err := db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: foreign.Engine, Action: core.ActionStatus}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "升级 Agent") {
					t.Fatalf("legacy Agent did not receive recovery instructions: %v", err)
				}
				runtime := map[core.Engine]core.RuntimeState{}
				for _, engine := range core.AllEngines() {
					runtime[engine] = core.RuntimeState{Installed: true, ServiceStatus: "active"}
				}
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Runtime: runtime, Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureInstanceStatus}}); err != nil {
					t.Fatal(err)
				}
				denied("after active runtime heartbeat")
				refresh, err := db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: foreign.Engine, Action: core.ActionStatus})
				if err != nil {
					t.Fatal(err)
				}
				lease, err := db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != refresh.ID {
					t.Fatalf("status claim: %+v %v", lease, err)
				}
				if lease.ConfigID != retained.ID || lease.ConfigVersion != retained.Version || lease.ConfigContent != retained.Content {
					t.Fatalf("refresh used a draft: %+v", lease)
				}
				// Service liveness without a matching configuration never releases protection.
				if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: "active", InstanceStatus: &core.SharedInstanceStatus{Engine: foreign.Engine, TaskID: lease.ID, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
					t.Fatal(err)
				}
				denied("after status without verified configuration")
				refresh, err = db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: foreign.Engine, Action: core.ActionStatus})
				if err != nil {
					t.Fatal(err)
				}
				lease, err = db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != refresh.ID {
					t.Fatalf("recovery claim: %+v %v", lease, err)
				}
				if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: "active", InstanceStatus: &core.SharedInstanceStatus{Engine: foreign.Engine, TaskID: lease.ID, ConfigID: retained.ID, ConfigVersion: retained.Version, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
					t.Fatal(err)
				}
				var uncertain, configUncertain bool
				if err := db.pool.QueryRow(ctx, `SELECT uncertain,config_uncertain FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2`, node.ID, foreign.Engine).Scan(&uncertain, &configUncertain); err != nil || uncertain || configUncertain {
					t.Fatalf("refresh did not recover state: %v %v %v", uncertain, configUncertain, err)
				}

				for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
					saved, task, err := db.SaveAgentConfigAndTask(targetContext, target, target.Version, ConfigMutationOptions{Action: action})
					if err != nil {
						t.Fatalf("verified refresh did not restore %s: %v", action, err)
					}
					target = saved
					finish(task, true)
				}
				t.Log("after verified status refresh: unchanged-port validate/deploy both succeed")
			})
		}
	}
}

func TestFailedMainDeploymentObservationKeepsExactIdentityAndConflicts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "status-owner")
	node := allEnginesSharedTestAgent(t, db, owner)
	config := sharedEngineConfigs()[0]
	config.AgentID = node.ID
	config, err := db.SaveAgentConfig(owner, config, 0)
	if err != nil {
		t.Fatal(err)
	}
	finish := func(task core.Task, success bool, observation *core.SharedInstanceStatus) {
		t.Helper()
		lease, err := db.ClaimTask(ctx, node.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim: %+v %v", lease, err)
		}
		if observation != nil {
			observation.TaskID = lease.ID
			observation.ObservedAt = time.Now().UTC()
		}
		if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: success, InstanceStatus: observation}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	finish(first, true, nil)
	retained := config
	config.Content = strings.ReplaceAll(config.Content, "21001", "22001")
	config, err = db.SaveAgentConfig(owner, config, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	finish(failed, false, &core.SharedInstanceStatus{Engine: config.Engine, Status: "active", ConfigID: retained.ID, ConfigVersion: retained.Version})
	var uncertain, configUncertain bool
	if err := db.pool.QueryRow(ctx, `SELECT uncertain,config_uncertain FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2`, node.ID, config.Engine).Scan(&uncertain, &configUncertain); err != nil || uncertain || configUncertain {
		t.Fatalf("verified failed result left uncertainty: %v %v %v", uncertain, configUncertain, err)
	}
	other := sharedEngineConfigs()[1]
	other.AgentID = node.ID
	other.Content = strings.ReplaceAll(other.Content, "21002", "21001")
	other, err = db.SaveAgentConfig(owner, other, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		if _, err := db.CreateTask(owner, core.TaskRequest{AgentID: node.ID, Engine: other.Engine, ConfigID: other.ID, Action: action}); !errors.Is(err, ErrConflict) {
			t.Fatalf("retained listener allowed %s: %v", action, err)
		}
	}
	// A node manager's refresh must not export somebody else's retained body.
	otherUser, _ := sharedTestUser(t, db, ctx, "status-manager")
	otherContext := WithConfigScope(ctx, otherUser.ID, true)
	if _, err := db.pool.Exec(ctx, `UPDATE panel_users SET role='admin' WHERE id=$1`, otherUser.ID); err != nil {
		t.Fatal(err)
	}
	refresh, err := db.CreateTask(otherContext, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, node.ID)
	if err != nil || lease == nil || lease.ID != refresh.ID || lease.ConfigContent != "" || lease.ConfigID != "" {
		t.Fatalf("manager inherited configuration: %+v %v", lease, err)
	}
}

func TestSharedRefreshDoesNotRebindAnOlderOperationObservation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "refresh-owner")
	user, recipient := sharedTestUser(t, db, ctx, "refresh-recipient")
	node := allEnginesSharedTestAgent(t, db, owner)
	sharedTestAllocation(t, db, ctx, user.ID, node.ID, 0, 21001)
	config := sharedTestConfig(t, db, recipient, node.ID, 21001)
	deploy, err := db.CreateTask(recipient, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, node.ID)
	if err != nil || lease == nil || lease.ID != deploy.ID {
		t.Fatalf("deploy claim: %+v %v", lease, err)
	}
	if err := completeSharedTestTask(db, ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	for _, fresh := range []bool{false, true} {
		refresh, err := db.CreateTask(recipient, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, Action: core.ActionStatus})
		if err != nil {
			t.Fatal(err)
		}
		lease, err = db.ClaimTask(ctx, node.ID)
		if err != nil || lease == nil || lease.ID != refresh.ID {
			t.Fatalf("status claim: %+v %v", lease, err)
		}
		observation := core.SharedInstanceStatus{Engine: config.Engine, ShareID: lease.SharedTrafficID, TaskID: "tsk_older_operation", ConfigID: config.ID, ConfigVersion: config.Version, Status: "inactive", ObservedAt: time.Now().UTC()}
		if fresh {
			observation.TaskID = lease.ID // Identity bootstrapped by this status lease.
		}
		if err := db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, InstanceStatus: &observation}); err != nil {
			t.Fatal(err)
		}
		var running bool
		if err := db.pool.QueryRow(ctx, `SELECT running FROM agent_shared_instance_ownership WHERE agent_id=$1 AND engine=$2 AND share_id=$3`, node.ID, config.Engine, lease.SharedTrafficID).Scan(&running); err != nil || running == fresh {
			t.Fatalf("fresh=%v running=%v: %v", fresh, running, err)
		}
		if fresh {
			observation.TaskID = lease.ID // Agent keeps the bootstrapped sidecar's identity.
			observation.Status, observation.ObservedAt = "active", time.Now().UTC()
			if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: append(node.Features, core.AgentFeatureSharedCoreLifecycle), SharedInstances: []core.SharedInstanceStatus{observation}}); err != nil {
				t.Fatal(err)
			}
			if err := db.pool.QueryRow(ctx, `SELECT running FROM agent_shared_instance_ownership WHERE agent_id=$1 AND engine=$2 AND share_id=$3`, node.ID, config.Engine, lease.SharedTrafficID).Scan(&running); err != nil || !running {
				t.Fatalf("heartbeat after legacy identity recovery was discarded: running=%v %v", running, err)
			}
		}
	}
}
