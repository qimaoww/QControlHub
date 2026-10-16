package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedCorePreparationRevocationAndIsolation(t *testing.T) {
	for _, change := range []string{"execute", "share", "disabled", "feature"} {
		for _, running := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "/queued", true: "/running"}[running], func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				agent := sharedTestAgent(t, db, ctx)
				if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["preset-auto-install-v1","shared-core-instances-v1","shared-core-prepare-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
					t.Fatal(err)
				}
				user, account := sharedTestUser(t, db, ctx, "prepare-user")
				sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 21001)
				config := core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "private prepare", Content: "listeners: [{name: a, type: trojan, port: 21001}]\nrules: ['MATCH,DIRECT']\n"}
				saved, task, err := db.SaveAgentConfigAndTask(account, config, 0, ConfigMutationOptions{Action: core.ActionDeploy, InstallIfMissing: true})
				if err != nil {
					t.Fatal(err)
				}
				// The snapshot and sharing identity survive ordinary duplicate requests.
				request := core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: task.Action, ConfigID: saved.ID, ExpectedConfigVersion: saved.Version, InstallIfMissing: true}
				reused, err := db.CreateTask(account, request)
				if err != nil || !reused.Reused || reused.ID != task.ID {
					t.Fatalf("duplicate preparation: %+v %v", reused, err)
				}
				if running {
					claimed, err := db.ClaimTask(ctx, agent.ID)
					if err != nil || claimed == nil || claimed.ID != task.ID {
						t.Fatalf("claim: %+v %v", claimed, err)
					}
				}
				var sql string
				switch change {
				case "execute":
					sql = `UPDATE panel_users SET permissions=permissions-'tasks.execute' WHERE id=$1`
				case "disabled":
					sql = `UPDATE panel_users SET disabled=true WHERE id=$1`
				case "share":
					sql = `UPDATE agent_shares SET enabled=false WHERE user_id=$1`
				case "feature":
					sql = `UPDATE agents SET features=features-'shared-core-prepare-v1' WHERE owner_id<>$1`
				}
				if _, err := db.pool.Exec(ctx, sql, user.ID); err != nil {
					t.Fatal(err)
				}
				// Execution resolves durable authorization again after queueing.
				var next *core.Task
				if running {
					next, err = db.RunningTask(ctx, agent.ID)
				} else {
					next, err = db.ClaimTask(ctx, agent.ID)
				}
				if err != nil || next != nil {
					t.Fatalf("revoked preparation delivered: %+v %v", next, err)
				}
				ended, err := db.GetTask(ctx, task.ID)
				want := core.TaskCanceled
				if running {
					want = core.TaskFailed
				}
				if err != nil || ended.Status != want {
					t.Fatalf("authorization result: %+v %v", ended, err)
				}
			})
		}
	}
}

func TestSharedCorePreparationSerializesUsersAndKeepsSnapshots(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["preset-auto-install-v1","shared-core-instances-v1","shared-core-prepare-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	alice, aliceCtx := sharedTestUser(t, db, ctx, "prepare-alice")
	bob, bobCtx := sharedTestUser(t, db, ctx, "prepare-bob")
	sharedTestAllocation(t, db, ctx, alice.ID, agent.ID, 0, 21001)
	sharedTestAllocation(t, db, ctx, bob.ID, agent.ID, 0, 21002)
	a := sharedTestConfig(t, db, aliceCtx, agent.ID, 21001)
	b := sharedTestConfig(t, db, bobCtx, agent.ID, 21002)
	makeTask := func(config core.Config, owner string) core.Task {
		task, err := db.CreateTask(WithConfigScope(ctx, owner, false), core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, ExpectedConfigVersion: config.Version, Action: core.ActionDeploy, InstallIfMissing: true})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	at, bt := makeTask(a, alice.ID), makeTask(b, bob.ID)
	if at.SharedTrafficID == bt.SharedTrafficID || !at.SharedInstance || !bt.SharedInstance {
		t.Fatal("shared instances collapsed")
	}
	if _, err := db.GetTask(aliceCtx, bt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user task visible: %v", err)
	}
	first, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || first == nil || first.ID != at.ID || first.ConfigContent != a.Content {
		t.Fatalf("first immutable task: %+v %v", first, err)
	}
	if next, err := db.ClaimTask(ctx, agent.ID); err != nil || next != nil {
		t.Fatalf("concurrent install: %+v %v", next, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, at.ID, core.TaskResultRequest{LeaseID: first.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	second, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || second == nil || second.ID != bt.ID || second.ConfigContent != b.Content {
		t.Fatalf("second immutable task: %+v %v", second, err)
	}
	var base int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM agent_engine_ownership WHERE agent_id=$1`, agent.ID).Scan(&base); err != nil || base != 0 {
		t.Fatalf("private task changed host ownership: %d %v", base, err)
	}
}
