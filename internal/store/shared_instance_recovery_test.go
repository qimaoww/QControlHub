package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedInstanceAutomaticStart(t *testing.T) {
	db, ctx, database := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "recovery-owner")
	agent := allEnginesSharedTestAgent(t, db, owner)
	node, err := db.GetAgent(owner, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	features := node.Features
	finish := func(task core.Task, success bool, status string, applied string) {
		t.Helper()
		obs := core.SharedInstanceStatus{ShareID: task.SharedTrafficID, Engine: task.Engine, ConfigID: task.ConfigID,
			ConfigVersion: task.ConfigVersion, AppliedTaskID: applied, TaskID: task.ID, Status: status, ObservedAt: time.Now().UTC()}
		if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: success, InstanceStatus: &obs}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func() *core.Task {
		t.Helper()
		task, err := db.ClaimTask(ctx, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	deploy := func(name string, port int) (context.Context, core.Config, core.Task) {
		t.Helper()
		user, actor := sharedTestUser(t, db, ctx, name)
		sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, port)
		config := sharedTestConfig(t, db, actor, agent.ID, port)
		task, err := db.CreateTask(actor, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
		if err != nil {
			t.Fatal(err)
		}
		lease := claim()
		if lease == nil || lease.ID != task.ID {
			t.Fatalf("missing deploy %+v", lease)
		}
		finish(*lease, true, "active", lease.ID)
		return actor, config, *lease
	}
	alice, config, a := deploy("recovery-alice", 21001)
	bob, bConfig, b := deploy("recovery-bob", 21002)
	stop, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: b.Engine, Action: core.ActionStop})
	if err != nil {
		t.Fatal(err)
	}
	lease := claim()
	if lease == nil || lease.ID != stop.ID {
		t.Fatal("missing manual stop")
	}
	finish(*lease, true, "inactive", b.ID)
	// A newer saved draft has no bearing on the automatic start snapshot.
	draft := config
	draft.Content = strings.ReplaceAll(draft.Content, "21001", "21003")
	if _, err := db.SaveAgentConfig(alice, draft, config.Version); err != nil {
		t.Fatal(err)
	}

	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("policies %+v: %v", policies, err)
	}
	var reportProcess, expired bool
	heartbeat := func(healthy bool, taskID string) {
		t.Helper()
		now := time.Now().UTC()
		hb := core.HeartbeatRequest{Features: features, Runtime: node.Runtime}
		for _, task := range []core.Task{a, b} {
			last := taskID
			if task.ID == b.ID {
				last = stop.ID
			}
			hb.SharedInstances = append(hb.SharedInstances, core.SharedInstanceStatus{ShareID: task.SharedTrafficID, Engine: task.Engine, ConfigID: task.ConfigID, ConfigVersion: task.ConfigVersion,
				AppliedTaskID: task.ID, TaskID: last, Status: "inactive", ObservedAt: now})
		}
		for _, p := range policies {
			start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, now)
			hb.TrafficUsage = append(hb.TrafficUsage, core.PortTrafficUsage{PolicyID: p.ID, ShareID: p.SharedQuota.ID, ResetGeneration: p.ResetGeneration,
				CollectedAt: now, CounterEpoch: strings.Repeat("a", 32), PeriodStart: start, PeriodEnd: end, EnforcementAvailable: healthy || p.SharedQuota.ID == b.SharedTrafficID})
			if reportProcess {
				hb.TrafficUsage[len(hb.TrafficUsage)-1].ProcessUsage = &core.SharedProcessUsage{Epoch: strings.Repeat("b", 32)}
			}
		}
		// The metrics ticker can deliver the identical snapshot just before the heartbeat.
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, hb.TrafficUsage, now); err != nil {
			t.Fatal(err)
		}
		if expired {
			if _, err := db.pool.Exec(ctx, `UPDATE port_traffic_policies SET last_reported_at=now()-interval '1 day' WHERE agent_id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Heartbeat(ctx, agent.ID, hb); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a v107 upgrade with Alice already stopped after a deployed
	// listener and Bob explicitly stopped. Both observed states are inactive.
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shared_instance_ownership SET running=false,service_status='inactive';
		ALTER TABLE agent_shared_instance_ownership DROP COLUMN desired_running,DROP COLUMN next_auto_start;
		ALTER TABLE tasks DROP COLUMN shared_auto_start;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=108;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(107) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenWithConfigKey(ctx, database, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	heartbeat(true, a.ID)
	if task := claim(); task != nil {
		t.Fatalf("older Agent received automatic start: %+v", task)
	}
	features = append(features, core.AgentFeatureSharedCoreRecovery, core.AgentFeatureSharedProcessTraffic)
	heartbeat(true, a.ID)
	if task := claim(); task != nil {
		t.Fatal("pre-upgrade metrics authorized a process-metered recovery")
	}
	reportProcess, expired = true, true
	heartbeat(true, a.ID)
	if task := claim(); task != nil {
		t.Fatal("expired duplicate authorized recovery")
	}
	expired = false
	heartbeat(false, a.ID)
	if task := claim(); task != nil {
		t.Fatalf("unhealthy or manually stopped allocation started: %+v", task)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=limit_bytes WHERE id=$1`, a.SharedTrafficID); err != nil {
		t.Fatal(err)
	}
	heartbeat(true, a.ID)
	if task := claim(); task != nil {
		t.Fatal("exhausted allocation was started")
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=0 WHERE id=$1`, a.SharedTrafficID); err != nil {
		t.Fatal(err)
	}
	heartbeat(true, stop.ID)
	if task := claim(); task != nil {
		t.Fatal("stale task observation triggered a start")
	}
	heartbeat(true, a.ID)
	heartbeat(true, a.ID)
	lease = claim()
	if lease == nil || lease.Action != core.ActionStart || !lease.SharedInstance || lease.SharedTrafficID != a.SharedTrafficID || lease.ConfigVersion != config.Version || !strings.Contains(lease.ConfigContent, "21001") {
		t.Fatalf("wrong recovery task: %+v", lease)
	}
	finish(*lease, false, "inactive", a.ID)
	heartbeat(true, lease.ID)
	if task := claim(); task != nil {
		t.Fatal("failed recovery bypassed cooldown")
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shared_instance_ownership SET next_auto_start=now() WHERE share_id=$1`, a.SharedTrafficID); err != nil {
		t.Fatal(err)
	}
	heartbeat(true, lease.ID)
	lease = claim()
	if lease == nil || lease.SharedTrafficID != a.SharedTrafficID {
		t.Fatalf("recovery did not retry: %+v", lease)
	}
	finish(*lease, true, "active", a.ID)
	if task := claim(); task != nil {
		t.Fatalf("duplicate or cross-instance task: %+v", task)
	}
	// A new stop takes precedence over a queued recovery. A downgraded
	// Agent must also lose any recovery lease prepared by a newer session.
	for _, scenario := range []string{"downgrade", "stop"} {
		if _, err := db.pool.Exec(ctx, `UPDATE agent_shared_instance_ownership SET next_auto_start=now() WHERE share_id=$1`, a.SharedTrafficID); err != nil {
			t.Fatal(err)
		}
		heartbeat(true, lease.ID)
		var queuedID string
		if err := db.pool.QueryRow(ctx, `SELECT id FROM tasks WHERE shared_traffic_id=$1 AND shared_auto_start AND status='pending'`, a.SharedTrafficID).Scan(&queuedID); err != nil {
			t.Fatal(err)
		}
		if scenario == "downgrade" {
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features-$2::text WHERE id=$1`, agent.ID, core.AgentFeatureSharedCoreRecovery); err != nil {
				t.Fatal(err)
			}
			if got := claim(); got != nil {
				t.Fatal("downgraded Agent claimed a recovery task")
			}
		} else {
			explicit, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: a.Engine, Action: core.ActionStop})
			if err != nil {
				t.Fatal(err)
			}
			got := claim()
			if got == nil || got.ID != explicit.ID {
				t.Fatalf("queued recovery blocked stop: %+v", got)
			}
			finish(*got, true, "inactive", a.ID)
		}
		canceled, err := db.GetTask(alice, queuedID)
		if err != nil || canceled.Status != core.TaskCanceled {
			t.Fatalf("%s recovery task: %+v %v", scenario, canceled, err)
		}
	}

	// Explicitly deploying an unchanged stopped binding must actually run it.
	_, fresh, err := db.SaveAgentConfigAndTask(bob, bConfig, bConfig.Version, ConfigMutationOptions{Action: core.ActionDeploy, ReuseUnchangedBinding: true})
	if err != nil || fresh.Reused || fresh.ID == b.ID {
		t.Fatalf("stopped binding reused stale success: %+v %v", fresh, err)
	}
	lease = claim()
	if lease == nil || lease.ID != fresh.ID {
		t.Fatalf("missing binding redeploy: %+v", lease)
	}
	finish(*lease, true, "active", fresh.ID)
	_, same, err := db.SaveAgentConfigAndTask(bob, bConfig, bConfig.Version, ConfigMutationOptions{Action: core.ActionDeploy, ReuseUnchangedBinding: true})
	if err != nil || !same.Reused {
		t.Fatalf("running binding lost reuse: %+v %v", same, err)
	}

}
