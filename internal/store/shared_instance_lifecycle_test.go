package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedInstanceLifecycleKeepsAppliedRevisionAndOtherServices(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "instance-owner")
	a, alice := sharedTestUser(t, db, ctx, "instance-alice")
	b, bob := sharedTestUser(t, db, ctx, "instance-bob")
	agent := allEnginesSharedTestAgent(t, db, owner)
	allocate := func(user core.User, ports []int) string {
		t.Helper()
		access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineXray}, Ports: ports, LimitBytes: 10000}}})
		if err != nil {
			t.Fatal(err)
		}
		return acceptSharedTestInvitations(t, db, ctx, user.ID, access).Shares[0].ID
	}
	aShare, bShare := allocate(a, []int{21001, 21002}), allocate(b, []int{22001})
	finish := func(task core.Task, result core.TaskResultRequest) {
		t.Helper()
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim %s: %+v %v", task.ID, lease, err)
		}
		result.LeaseID = lease.LeaseID
		if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, result); err != nil {
			t.Fatal(err)
		}
	}
	deploy := func(account context.Context, port int) (core.Config, core.Task) {
		t.Helper()
		content := strings.ReplaceAll(sharedEngineConfigs()[1].Content, "21002", strconv.Itoa(port))
		config, task, err := db.SaveAgentConfigAndTask(account, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "同名配置", Content: content}, 0, ConfigMutationOptions{Action: core.ActionDeploy})
		if err != nil {
			t.Fatal(err)
		}
		finish(task, core.TaskResultRequest{Success: true})
		return config, task
	}
	base, _ := deploy(owner, 23001)
	aConfig, firstDeploy := deploy(alice, 21001)
	bConfig, _ := deploy(bob, 22001)
	visible := func(account context.Context) core.RuntimeState {
		t.Helper()
		node, err := db.GetAgent(account, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		return node.Runtime[core.EngineXray]
	}
	if got := visible(alice); got.ServiceStatus != "active" || got.Instance.ID != aShare || got.Instance.DeployedConfigID != aConfig.ID {
		t.Fatalf("Alice identity: %+v", got)
	}
	if got := visible(bob); got.ServiceStatus != "active" || got.Instance.ID != bShare || got.Instance.DeployedConfigID != bConfig.ID {
		t.Fatalf("Bob identity: %+v", got)
	}
	for account, config := range map[context.Context]core.Config{owner: base, alice: aConfig, bob: bConfig} {
		latest, err := db.LatestDeployments(account)
		if err != nil || len(latest) != 1 || latest[0].ConfigID != config.ID {
			t.Fatalf("deployment leaked: %+v %v", latest, err)
		}
	}
	updated := aConfig
	updated.Content = strings.ReplaceAll(updated.Content, "21001", "21002")
	if _, err := db.SaveAgentConfig(alice, updated, 1); err != nil {
		t.Fatal(err)
	}
	if got := visible(alice).Instance; got.SavedVersion != 2 || got.DeployedVersion != 1 {
		t.Fatalf("draft replaced running version: %+v", got)
	}
	stop, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionStop})
	if err != nil || !stop.SharedInstance || stop.SharedTrafficID != aShare {
		t.Fatalf("stop target: %+v %v", stop, err)
	}
	duplicate, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionStop})
	if err != nil || duplicate.ID != stop.ID || !duplicate.Reused {
		t.Fatalf("duplicate click was not coalesced: %+v %v", duplicate, err)
	}
	if _, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionRestart}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting operation accepted: %v", err)
	}
	finish(stop, core.TaskResultRequest{Success: true, TrafficSettled: true})
	if got := visible(alice); got.ServiceStatus != "inactive" || got.Instance.DeployedVersion != 1 {
		t.Fatalf("stop forgot applied version: %+v", got)
	}
	profiles, err := db.DeployedConfigs(alice)
	if err != nil || len(profiles) != 1 || profiles[0].Config.Version != 1 || !strings.Contains(profiles[0].Config.Content, "21001") {
		t.Fatalf("stopped client projection: %+v %v", profiles, err)
	}
	start, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionStart})
	if err != nil || start.ConfigID != aConfig.ID || start.ConfigVersion != 1 {
		t.Fatalf("start bound newer draft: %+v %v", start, err)
	}
	finish(start, core.TaskResultRequest{Success: true})
	failed, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionDeploy, ConfigID: aConfig.ID, ExpectedConfigVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	finish(failed, core.TaskResultRequest{Error: "配置校验失败，已恢复旧配置", InstanceStatus: &core.SharedInstanceStatus{Engine: core.EngineXray, ShareID: aShare, Status: "active", ConfigID: aConfig.ID, ConfigVersion: 1, AppliedTaskID: firstDeploy.ID, TaskID: failed.ID, ObservedAt: time.Now().UTC()}})
	if got := visible(alice); got.ServiceStatus != "active" || got.Instance.ConfigUncertain || got.Instance.DeployedVersion != 1 || got.Instance.Operation.Status != core.TaskFailed {
		t.Fatalf("failed deployment hid rollback: %+v", got)
	}
	// A delayed pre-start sample cannot turn the current instance off.
	node, _ := db.GetAgent(owner, agent.ID)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: node.Features, Runtime: node.Runtime, SharedInstances: []core.SharedInstanceStatus{{Engine: core.EngineXray, ShareID: aShare, Status: "inactive", TaskID: stop.ID, ObservedAt: time.Now().Add(-time.Minute)}}}); err != nil {
		t.Fatal(err)
	}
	if visible(alice).ServiceStatus != "active" || visible(bob).ServiceStatus != "active" {
		t.Fatal("late status or Alice lifecycle changed another instance")
	}
	var baseID string
	if err := db.pool.QueryRow(ctx, `SELECT config_id FROM agent_engine_ownership WHERE agent_id=$1 AND engine='xray'`, agent.ID).Scan(&baseID); err != nil || baseID != base.ID {
		t.Fatalf("shared lifecycle wrote base identity: %s %v", baseID, err)
	}
}

func TestSharedInstanceLifecycleRechecksAuthorizationAtEveryLease(t *testing.T) {
	for _, change := range []string{"revoke", "disable-user", "withdraw-engine", "downgrade-agent"} {
		for _, running := range []bool{false, true} {
			t.Run(change+"/running="+strconv.FormatBool(running), func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				_, owner := sharedTestUser(t, db, ctx, "lease-owner")
				user, recipient := sharedTestUser(t, db, ctx, "lease-recipient")
				agent := allEnginesSharedTestAgent(t, db, owner)
				allocation := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 10000, 21001)
				config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
				deployment, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
				if err != nil {
					t.Fatal(err)
				}
				lease, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || lease == nil {
					t.Fatalf("deploy claim: %+v %v", lease, err)
				}
				if err := completeSharedTestTask(db, ctx, agent.ID, deployment.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				task, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionRestart})
				if err != nil {
					t.Fatal(err)
				}
				if running {
					lease, err = db.ClaimTask(ctx, agent.ID)
					if err != nil || lease == nil || lease.ID != task.ID {
						t.Fatalf("restart claim: %+v %v", lease, err)
					}
				}
				switch change {
				case "revoke":
					_, err = db.pool.Exec(ctx, `UPDATE agent_shares SET enabled=false WHERE id=$1`, allocation.Shares[0].ID)
				case "disable-user":
					_, err = db.SetUserDisabled(ctx, user.ID, true)
				case "withdraw-engine":
					_, err = db.pool.Exec(ctx, `UPDATE agent_shares SET engines=ARRAY['xray']::text[] WHERE id=$1`, allocation.Shares[0].ID)
				case "downgrade-agent":
					_, err = db.pool.Exec(ctx, `UPDATE agents SET features=features-'shared-core-lifecycle-v1' WHERE id=$1`, agent.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				var next *core.Task
				if running {
					next, err = db.RunningTask(ctx, agent.ID)
				} else {
					next, err = db.ClaimTask(ctx, agent.ID)
				}
				if err != nil || next != nil {
					t.Fatalf("unauthorized task dispatched: %+v %v", next, err)
				}
				stored, err := db.GetTask(ctx, task.ID)
				want := core.TaskCanceled
				if running {
					want = core.TaskFailed
				}
				if err != nil || stored.Status != want || stored.ConfigContent != "" {
					t.Fatalf("authorization did not invalidate lease: %+v %v", stored, err)
				}
				if running && db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}) == nil {
					t.Fatal("delayed revoked result accepted")
				}
				if _, err := db.RetryTask(recipient, task.ID); err == nil {
					t.Fatal("retry bypassed changed authorization")
				}
				if _, err := db.RetryTask(ctx, task.ID); !errors.Is(err, ErrForbidden) {
					t.Fatalf("administrator retry reinterpreted an instance as a host task: %v", err)
				}
			})
		}
	}
}

func TestSharedInstanceRetryPreservesRecipientAndAllocation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "retry-instance-owner")
	user, recipient := sharedTestUser(t, db, ctx, "retry-instance-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	allocation := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 10000, 21001)
	config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
	deployment, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CancelTask(recipient, deployment.ID); err != nil {
		t.Fatal(err)
	}
	adminRetry, err := db.RetryTask(ctx, deployment.ID)
	if err != nil || !adminRetry.SharedInstance || adminRetry.SharedTrafficID != allocation.Shares[0].ID || adminRetry.ConfigID != config.ID {
		t.Fatalf("administrator configuration retry changed target: %+v %v", adminRetry, err)
	}
	if err := db.CancelTask(ctx, adminRetry.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := db.RetryTask(recipient, deployment.ID)
	if err != nil || !retry.SharedInstance || retry.SharedTrafficID != allocation.Shares[0].ID || retry.ConfigID != config.ID || retry.ConfigVersion != config.Version {
		t.Fatalf("recipient retry changed target: %+v %v", retry, err)
	}
	if err := db.CancelTask(recipient, retry.ID); err != nil {
		t.Fatal(err)
	}
	// An obsolete task may refer to a previous allocation. Even an accepted
	// new allocation must not silently change the instance during a retry.
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET shared_traffic_id='shr_0000000000000000' WHERE id=$1`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RetryTask(recipient, deployment.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry silently retargeted a replaced allocation: %v", err)
	}
	if _, _, err := db.SaveEditedRetry(recipient, deployment.ID, config.Content, config.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("edited retry silently retargeted a replaced allocation: %v", err)
	}
	stored, err := db.AgentConfig(recipient, agent.ID, config.Engine)
	if err != nil || stored.Version != config.Version {
		t.Fatalf("rejected retry persisted an edit: %+v %v", stored, err)
	}
	var pending int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE agent_id=$1 AND status='pending'`, agent.ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("rejected retry enqueued a task: %d %v", pending, err)
	}
}

func TestSharedEmptyDeploymentSucceedsWithInactiveExactRevision(t *testing.T) {
	for engine, content := range map[core.Engine]string{
		core.EngineMihomo:          "listeners: []\nrules: ['MATCH,DIRECT']\n",
		core.EngineXray:            `{"inbounds":[],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`,
		core.EngineSingBox:         `{"inbounds":[],"outbounds":[{"tag":"direct","type":"direct"}],"route":{"final":"direct"}}`,
		core.EngineShadowsocksRust: `{"servers":[]}`,
	} {
		t.Run(string(engine), func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			_, owner := sharedTestUser(t, db, ctx, "empty-owner")
			user, recipient := sharedTestUser(t, db, ctx, "empty-recipient")
			agent := allEnginesSharedTestAgent(t, db, owner)
			access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{engine}, Ports: []int{21001}}}})
			if err != nil {
				t.Fatal(err)
			}
			shareID := acceptSharedTestInvitations(t, db, ctx, user.ID, access).Shares[0].ID
			config, task, err := db.SaveAgentConfigAndTask(recipient, core.Config{AgentID: agent.ID, Engine: engine, Name: "empty", Content: content}, 0, ConfigMutationOptions{Action: core.ActionDeploy})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || lease == nil {
				t.Fatalf("claim: %+v %v", lease, err)
			}
			result := core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true,
				InstanceStatus: &core.SharedInstanceStatus{Engine: engine, ShareID: shareID, ConfigID: config.ID, ConfigVersion: config.Version,
					TaskID: task.ID, AppliedTaskID: task.ID, Status: "inactive", ObservedAt: time.Now().UTC()}}
			if err := db.CompleteTask(ctx, agent.ID, task.ID, result); err != nil {
				t.Fatal(err)
			}
			finished, err := db.GetTask(recipient, task.ID)
			if err != nil || finished.Status != core.TaskSucceeded {
				t.Fatalf("empty deployment was falsely failed: %+v %v", finished, err)
			}
			node, err := db.GetAgent(recipient, agent.ID)
			if err != nil || node.Runtime[engine].ServiceStatus != "inactive" || node.Runtime[engine].Instance.DeployedVersion != config.Version {
				t.Fatalf("empty deployment identity/status: %+v %v", node.Runtime[engine], err)
			}
		})
	}
}

func TestMigratePreviousSharedSchemasPreserveRevisionLedgerAndBaseIdentity(t *testing.T) {
	for _, version := range []int{75, 76} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			checkPreviousSharedSchema(t, version)
		})
	}
}

func checkPreviousSharedSchema(t *testing.T, version int) {
	db, ctx, database := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "upgrade-owner")
	user, recipient := sharedTestUser(t, db, ctx, "upgrade-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	allocation := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 10000, 21001, 21002)
	var base, shared core.Config
	for _, account := range []context.Context{owner, recipient} {
		port := 23001
		if account == recipient {
			port = 21001
		}
		config := sharedTestConfig(t, db, account, agent.ID, port)
		task, err := db.CreateTask(account, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil {
			t.Fatal(err)
		}
		if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
		if account == owner {
			base = config
		} else {
			shared = config
		}
	}
	shared.Content = strings.ReplaceAll(shared.Content, "21001", "21002")
	if _, err := db.SaveAgentConfig(recipient, shared, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123 WHERE id=$1`, allocation.Shares[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM agent_engine_ownership;
		ALTER TABLE agent_shared_instance_ownership DROP COLUMN service_status,DROP COLUMN status_reason,
		DROP COLUMN observed_at,DROP COLUMN sampled_at,DROP COLUMN last_task_id,DROP COLUMN applied_task_id,DROP COLUMN deployed_at;
			DELETE FROM qcontrolhub_schema_migrations WHERE version>=`+strconv.Itoa(version)+`;
			INSERT INTO qcontrolhub_schema_migrations(version) VALUES(`+strconv.Itoa(version)+`) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	node, err := migrated.GetAgent(recipient, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := node.Runtime[core.EngineMihomo]
	if runtime.ServiceStatus != "unknown" || runtime.Instance.DeployedConfigID != shared.ID || runtime.Instance.DeployedVersion != 1 || runtime.Instance.SavedVersion != 2 || node.SharedAllocation.UsedBytes != 123 {
		t.Fatalf("upgrade lost version, freshness, or ledger: %+v %+v", runtime, node.SharedAllocation)
	}
	var baseID string
	if err := migrated.pool.QueryRow(ctx, `SELECT config_id FROM agent_engine_ownership WHERE agent_id=$1 AND engine='mihomo'`, agent.ID).Scan(&baseID); err != nil || baseID != base.ID {
		t.Fatalf("migration attributed shared config to base: %s %v", baseID, err)
	}
}
