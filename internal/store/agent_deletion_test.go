package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeleteAgentDoesNotWaitForCleanup(t *testing.T) {
	for _, locked := range []string{"configs", "tasks"} {
		t.Run(locked, func(t *testing.T) {
			db, ctx, databaseURL := isolatedConfigScopeStore(t)
			_, owner := sharedTestUser(t, db, ctx, "delete-owner")
			agent := sharedTestAgent(t, db, owner)
			credential, err := db.CreateAgentEnrollmentToken(owner, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			config := sharedTestConfig(t, db, owner, agent.ID, 21001)
			task, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: config.ID})
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := db.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			lockedID := config.ID
			stage := "clear configs"
			if locked == "tasks" {
				lockedID, stage = task.ID, "fail pending tasks"
			}
			if _, err := blocker.Exec(ctx, `SELECT id FROM `+locked+` WHERE id=$1 FOR UPDATE`, lockedID); err != nil {
				t.Fatal(err)
			}

			// The history lock stays held throughout deletion and the first cleanup.
			// A synchronous cleanup would hit this short caller deadline and fail.
			request, cancel := context.WithTimeout(owner, 2*time.Second)
			err = db.DeleteAgent(request, agent.ID)
			cancel()
			if err != nil {
				t.Fatalf("history lock blocked deletion: %v", err)
			}
			assertDeletedAgentHidden(t, db, owner, agent.ID, config.ID)
			if db.EnrollmentTokenUsable(ctx, credential.Token) {
				t.Fatal("old enrollment credential remains usable")
			}
			if _, err := db.AgentPublicKey(ctx, agent.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("revoked identity authenticates: %v", err)
			}
			if task, err := db.RunningTask(ctx, agent.ID); err != nil || task != nil {
				t.Fatalf("deleted agent resumes work: %+v %v", task, err)
			}

			// A different deletion must progress even when the first job is locked.
			other := sharedTestAgent(t, db, owner)
			if err := db.DeleteAgent(owner, other.ID); err != nil {
				t.Fatal(err)
			}
			if err := db.CleanupDeletedAgents(ctx); err == nil || !strings.Contains(err.Error(), stage) {
				t.Fatalf("expected failed cleanup stage %q: %v", stage, err)
			}
			assertDeletedAgentHidden(t, db, owner, agent.ID, config.ID)
			var queued, delayed bool
			if err := db.pool.QueryRow(ctx, `SELECT retry_at>now() FROM agent_deletion_jobs WHERE agent_id=$1`, agent.ID).Scan(&delayed); err != nil || !delayed {
				t.Fatalf("failed job was lost or has no retry delay: %v %v", delayed, err)
			}
			if err := db.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_deletion_jobs WHERE agent_id=$1)`, other.ID).Scan(&queued); err != nil || queued {
				t.Fatalf("locked job blocked another deletion: %v %v", queued, err)
			}
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := db.pool.Exec(ctx, `UPDATE agent_deletion_jobs SET retry_at=now() WHERE agent_id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}

			// Close the request's store; a fresh process/pool must recover the job.
			db.Close()
			restarted, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if err := restarted.CleanupDeletedAgents(ctx); err != nil {
				t.Fatal(err)
			}
			var erased bool
			if err := restarted.pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND content='' FROM configs WHERE id=$1`, config.ID).Scan(&erased); err != nil || !erased {
				t.Fatalf("configuration not erased after retry: %v %v", erased, err)
			}
			var revisions int
			if err := restarted.pool.QueryRow(ctx, `SELECT count(*) FROM config_revisions WHERE config_id=$1`, config.ID).Scan(&revisions); err != nil || revisions != 0 {
				t.Fatalf("revisions not erased: %d %v", revisions, err)
			}
			stored, err := restarted.GetTaskState(ctx, task.ID)
			if err != nil || stored.Status != core.TaskFailed {
				t.Fatalf("pending deployment was not failed: %+v %v", stored, err)
			}
			if err := restarted.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_deletion_jobs WHERE agent_id=$1)`, agent.ID).Scan(&queued); err != nil || queued {
				t.Fatalf("completed job remains queued: %v %v", queued, err)
			}
			if err := restarted.CleanupDeletedAgents(ctx); err != nil {
				t.Fatalf("repeat cleanup: %v", err)
			}
		})
	}
}

func assertDeletedAgentHidden(t *testing.T, db *Store, ctx context.Context, agentID, configID string) {
	t.Helper()
	if _, err := db.GetAgent(ctx, agentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted agent remains visible: %v", err)
	}
	if configs, err := db.ListAgentConfigs(ctx); err != nil || len(configs) != 0 {
		t.Fatalf("deleted configs remain visible: %+v %v", configs, err)
	}
	if _, err := db.ConfigRevision(ctx, configID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted revision remains readable: %v", err)
	}
	if overview, err := db.Overview(ctx); err != nil || overview.Agents != 0 || overview.NodeConfigs != 0 {
		t.Fatalf("deleted data remains in overview: %+v %v", overview, err)
	}
}

func TestAgentCleanupSkipsClaimedJobs(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if err := db.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	worker, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Rollback(ctx)
	if _, err := worker.Exec(ctx, `SELECT agent_id FROM agent_deletion_jobs WHERE agent_id=$1 FOR UPDATE`, agent.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := db.CleanupDeletedAgents(short); err != nil {
		t.Fatalf("waited on another worker's claim: %v", err)
	}
	if err := worker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAgentRollsBackWithoutDurableJob(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	credential, err := db.CreateAgentEnrollmentToken(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `LOCK TABLE agent_deletion_jobs IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if err := db.DeleteAgent(request, agent.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unqueued deletion must fail: %v", err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AgentPublicKey(ctx, agent.ID); err != nil {
		t.Fatalf("failed deletion revoked identity: %v", err)
	}
	if !db.EnrollmentTokenUsable(ctx, credential.Token) {
		t.Fatal("failed deletion erased enrollment credential")
	}
	if err := db.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("retry deletion: %v", err)
	}
	if err := db.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAgentDeletionQueueUpgrade(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	if _, err := db.pool.Exec(ctx, `DROP TABLE agent_deletion_jobs;
		DELETE FROM qcontrolhub_schema_migrations;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES (65)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	agent := sharedTestAgent(t, upgraded, ctx)
	if err := upgraded.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("queue deletion after upgrade: %v", err)
	}
	if err := upgraded.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedAgentCannotCompleteRunningTask(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	config := sharedTestConfig(t, db, ctx, agent.ID, 21001)
	created, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	running, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || running == nil {
		t.Fatalf("claim task: %+v %v", running, err)
	}
	if err := db.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteTask(ctx, agent.ID, running.ID, core.TaskResultRequest{LeaseID: running.LeaseID, Success: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("accepted a late result from revoked identity: %v", err)
	}
	if _, err := db.AgentConfig(ctx, agent.ID, core.EngineMihomo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("system read exposed revoked config: %v", err)
	}
	if err := db.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetTaskState(ctx, created.ID)
	if err != nil || stored.Status != core.TaskFailed {
		t.Fatalf("running task after cleanup: %+v %v", stored, err)
	}
}
