package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func completeConfigCacheRead(t *testing.T, ctx context.Context, db *Store, agentID string, action core.Action) core.Task {
	t.Helper()
	created, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agentID, Engine: core.EngineMihomo, Action: action})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agentID)
	if err != nil || claimed == nil || claimed.ID != created.ID {
		t.Fatalf("claim read: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agentID, created.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, Output: "mixed-port: 7890\nmode: rule\n",
	}); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestConfigReadCacheInvalidatesBeforeMutationDispatch(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	for _, test := range []struct {
		name    string
		action  core.Action
		install bool
		mutates bool
		success bool
		cancel  bool
		expire  bool
	}{
		{name: "deploy success", action: core.ActionDeploy, mutates: true, success: true},
		{name: "deploy failure", action: core.ActionDeploy, mutates: true},
		{name: "lost deployment result", action: core.ActionDeploy, mutates: true, expire: true},
		{name: "install failure", action: core.ActionInstall, mutates: true},
		{name: "import failure", action: core.ActionImportExisting, mutates: true},
		{name: "automatic install then failed validation", action: core.ActionValidate, install: true, mutates: true},
		{name: "read-only validation", action: core.ActionValidate},
		{name: "canceled before dispatch", action: core.ActionDeploy, mutates: true, cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			agent, enrollmentID := enrollTaskTestAgent(t, ctx, db)
			defer cleanupTaskTestAgent(db, agent.ID, enrollmentID)
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["preset-auto-install-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
			reads := []core.Task{
				completeConfigCacheRead(t, ctx, db, agent.ID, core.ActionReadConfig),
				completeConfigCacheRead(t, ctx, db, agent.ID, core.ActionReadManagedConfig),
			}
			config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo,
				Name: "cache boundary", Content: "listeners: [{name: cache, type: socks, port: 21001}]\nrules: ['MATCH,DIRECT']\n",
			}, 0)
			if err != nil {
				t.Fatal(err)
			}
			request := core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: test.action, InstallIfMissing: test.install}
			if test.action != core.ActionInstall {
				request.ConfigID, request.ExpectedConfigVersion = config.ID, config.Version
			} else {
				request.CoreVersion = core.CoreVersionStable
			}
			task, err := db.CreateTask(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			assertCache := func(want bool) {
				t.Helper()
				for _, read := range reads {
					recent, err := db.RecentReadTask(ctx, agent.ID, core.EngineMihomo, read.Action, 10*time.Minute)
					if want && (err != nil || recent.ID != read.ID) {
						t.Errorf("lost reusable %s snapshot: %+v %v", read.Action, recent, err)
					} else if !want && !errors.Is(err, ErrNotFound) {
						t.Errorf("reused %s snapshot across mutation: %+v %v", read.Action, recent, err)
					}
				}
			}
			assertCache(!test.mutates)
			if test.cancel {
				if err := db.CancelTask(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
				assertCache(true)
				return
			}
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil || claimed.ID != task.ID {
				t.Fatalf("claim mutation: %+v %v", claimed, err)
			}
			assertCache(!test.mutates)
			if test.expire {
				setTaskStartedAt(t, ctx, db, task.ID, time.Now().Add(-time.Hour))
				if err := db.RequeueStaleTasks(ctx, time.Minute, time.Minute, 1); err != nil {
					t.Fatal(err)
				}
			} else if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{
				LeaseID: claimed.LeaseID, Success: test.success, Error: "execution outcome fixture",
			}); err != nil {
				t.Fatal(err)
			}
			assertCache(!test.mutates)
			if test.mutates {
				for _, read := range reads {
					if _, err := db.ReadTaskConfigSnapshot(ctx, read.ID, agent.ID, core.EngineMihomo); !errors.Is(err, ErrNotFound) {
						t.Errorf("pre-mutation %s snapshot remains fetchable: %v", read.Action, err)
					}
				}
			}
		})
	}
}

func TestFreshConfigReadDoesNotJoinBeforeQueuedMutation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	readRequest := core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionReadManagedConfig}
	first, err := db.CreateTask(ctx, readRequest)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != first.ID {
		t.Fatalf("claim first read: %+v %v", claimed, err)
	}
	config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo,
		Name: "queued deployment", Content: "listeners: [{name: cache, type: socks, port: 21001}]\nrules: ['MATCH,DIRECT']\n",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	deploy, err := db.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := db.CreateTask(ctx, readRequest)
	if err != nil || fresh.ID == first.ID || fresh.Reused {
		t.Fatalf("fresh read joined work ahead of queued deployment: %+v %v", fresh, err)
	}
	joined, err := db.CreateTask(ctx, readRequest)
	if err != nil || joined.ID != fresh.ID || !joined.Reused {
		t.Fatalf("concurrent fresh requests did not coalesce after deployment: %+v %v", joined, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, first.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, Output: "mixed-port: 7890\n",
	}); err != nil {
		t.Fatal(err)
	}
	for _, next := range []core.Task{deploy, fresh} {
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != next.ID {
			t.Fatalf("task order lost: %+v %v, want %s", lease, err, next.ID)
		}
		if err := db.CompleteTask(ctx, agent.ID, next.ID, core.TaskResultRequest{
			LeaseID: lease.LeaseID, Success: true, Output: config.Content,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot, err := db.ReadTaskConfigSnapshot(ctx, fresh.ID, agent.ID, config.Engine); err != nil || snapshot != config.Content {
		t.Fatalf("fresh read did not observe post-deployment content: %q %v", snapshot, err)
	}
}

func TestConfigReadCacheUsesCurrentScopeAndEligibility(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, owner := sharedTestUser(t, db, ctx, "read-cache-owner")
	agent, _ := enrollTaskTestAgent(t, owner, db)
	read := completeConfigCacheRead(t, owner, db, agent.ID, core.ActionReadManagedConfig)
	request := core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: read.Action}
	select {
	case <-db.TaskReady(agent.ID):
	default:
	}
	cached, err := db.CreateTaskWithReadCache(owner, request, 10*time.Minute)
	if err != nil || !cached.Reused || cached.ID != read.ID || cached.ConfigContent != "" {
		t.Fatalf("owner cache hit: %+v %v", cached, err)
	}
	select {
	case <-db.TaskReady(agent.ID):
		t.Fatal("cache hit woke the Agent")
	default:
	}
	// Even an administrator uses their own read cache, not another workspace's.
	other := WithConfigScope(ctx, "token_other-admin", true)
	fresh, err := db.CreateTaskWithReadCache(other, request, 10*time.Minute)
	if err != nil || fresh.Reused || fresh.ID == read.ID {
		t.Fatalf("cross-principal cache reuse: %+v %v", fresh, err)
	}
	if err := db.CancelTask(other, fresh.ID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, update, restore string
		id                    string
		want                  error
	}{
		{"permission revoked", `UPDATE panel_users SET permissions=permissions-'tasks.execute' WHERE id=$1`,
			`UPDATE panel_users SET permissions=permissions||'["tasks.execute"]'::jsonb WHERE id=$1`, user.ID, ErrForbidden},
		{"account disabled", `UPDATE panel_users SET disabled=true WHERE id=$1`,
			`UPDATE panel_users SET disabled=false WHERE id=$1`, user.ID, ErrNotFound},
		{"managed read feature removed", `UPDATE agents SET features=features-'managed-config-read-v1' WHERE id=$1`,
			`UPDATE agents SET features=features||'["managed-config-read-v1"]'::jsonb WHERE id=$1`, agent.ID, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.pool.Exec(ctx, test.update, test.id); err != nil {
				t.Fatal(err)
			}
			defer db.pool.Exec(ctx, test.restore, test.id)
			if _, err := db.CreateTaskWithReadCache(owner, request, 10*time.Minute); !errors.Is(err, test.want) {
				t.Fatalf("cache bypassed current eligibility: %v, want %v", err, test.want)
			}
			var count int
			if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE agent_id=$1`, agent.ID).Scan(&count); err != nil || count != 2 {
				t.Fatalf("rejected cache request created fallback work: count=%d %v", count, err)
			}
		})
	}
	transition, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: request.Engine, Action: core.ActionStop})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET capability_transition=true WHERE id=$1`, transition.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTaskWithReadCache(owner, request, 10*time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("cache bypassed pending capability transition: %v", err)
	}
}

func TestConfigReadCacheExpirationAndSourceIsolation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	other, _ := enrollTaskTestAgent(t, ctx, db)
	reads := []core.Task{
		completeConfigCacheRead(t, ctx, db, agent.ID, core.ActionReadConfig),
		completeConfigCacheRead(t, ctx, db, agent.ID, core.ActionReadManagedConfig),
		completeConfigCacheRead(t, ctx, db, other.ID, core.ActionReadManagedConfig),
	}
	for _, read := range reads {
		request := core.TaskRequest{AgentID: read.AgentID, Engine: read.Engine, Action: read.Action}
		cached, err := db.CreateTaskWithReadCache(ctx, request, 10*time.Minute)
		if err != nil || cached.ID != read.ID || !cached.Reused {
			t.Fatalf("cache crossed node/source boundary: %+v %v", cached, err)
		}
		if _, err := db.pool.Exec(ctx, `UPDATE tasks SET finished_at=now()-interval '601 seconds' WHERE id=$1`, read.ID); err != nil {
			t.Fatal(err)
		}
		fresh, err := db.CreateTaskWithReadCache(ctx, request, 10*time.Minute)
		if err != nil || fresh.ID == read.ID || fresh.Reused || fresh.Status != core.TaskPending {
			t.Fatalf("expired snapshot reused: %+v %v", fresh, err)
		}
		if err := db.CancelTask(ctx, fresh.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateTaskWithReadCache(ctx, core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus,
	}, time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-read task accepted cache preference: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.RecentReadTask(canceled, agent.ID, core.EngineMihomo, core.ActionReadConfig, time.Minute); errors.Is(err, ErrNotFound) || err == nil {
		t.Fatalf("database/context failure was disguised as a cache miss: %v", err)
	}
}
