package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func waitForPurgeBlockedBy(t *testing.T, db *Store, ctx context.Context, pid uint32) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		if err := db.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid))
		)`, int32(pid)).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("purge did not reach the expected database lock")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestPurgeUserPreservesTaskAndTrafficLockOrder(t *testing.T) {
	for _, firstLock := range []string{"agent", "share"} {
		t.Run(firstLock, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			user, owner := sharedTestUser(t, db, ctx, "locked-owner")
			recipient, recipientCtx := sharedTestUser(t, db, ctx, "locked-recipient")
			agent := sharedTestAgent(t, db, owner)
			access := sharedTestAllocation(t, db, ctx, recipient.ID, agent.ID, 0, 21001)
			config := sharedTestConfig(t, db, recipientCtx, agent.ID, 21001)
			task, err := db.CreateTask(recipientCtx, core.TaskRequest{
				AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy,
			})
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := db.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			if firstLock == "agent" {
				_, err = blocker.Exec(ctx, "SELECT id FROM agents WHERE id=$1 FOR UPDATE", agent.ID)
			} else {
				_, err = blocker.Exec(ctx, "SELECT id FROM agent_shares WHERE id=$1 FOR NO KEY UPDATE", access.Shares[0].ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := db.PurgeUser(ctx, user.ID)
				finished <- err
			}()
			waitForPurgeBlockedBy(t, db, ctx, blocker.Conn().PgConn().PID())
			if _, err := blocker.Exec(ctx, "SET LOCAL lock_timeout='500ms'"); err != nil {
				t.Fatal(err)
			}
			// Simulate the next lock in Agent dispatch or a traffic report.
			// Purge must not hold that row while waiting on the first lock.
			if firstLock == "agent" {
				_, err = blocker.Exec(ctx, "SELECT id FROM tasks WHERE id=$1 FOR UPDATE", task.ID)
			} else {
				_, err = blocker.Exec(ctx, "SELECT id FROM port_traffic_policies WHERE share_id=$1 FOR UPDATE", access.Shares[0].ID)
			}
			if err != nil {
				t.Fatalf("purge inverted the %s lock order: %v", firstLock, err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-finished; err != nil {
				t.Fatalf("purge after releasing %s: %v", firstLock, err)
			}
		})
	}
}

func TestPurgeUserIncludesEnrollmentCommittedWhileWaiting(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, owner := sharedTestUser(t, db, ctx, "enrolling-owner")
	token, err := db.CreateEnrollmentToken(owner, core.EnrollmentTokenRequest{Name: "enrolling-node", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer enrollment.Rollback(ctx)
	// EnrollAgent consumes the token before inserting the Agent. Pause at
	// that boundary while purge starts, then commit the newly enrolled node.
	if _, err := enrollment.Exec(ctx, "UPDATE enrollment_tokens SET used_count=used_count+1 WHERE id=$1", token.ID); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		user PurgedUser
		err  error
	}
	finished := make(chan outcome, 1)
	go func() {
		user, err := db.PurgeUser(ctx, user.ID)
		finished <- outcome{user, err}
	}()
	waitForPurgeBlockedBy(t, db, ctx, enrollment.Conn().PgConn().PID())
	agentID, err := core.NewID("agt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Exec(ctx, `INSERT INTO agents
		(id,name,os,arch,capabilities,public_key,last_seen,enrolled_at,enrollment_id,owner_id)
		VALUES ($1,'enrolling-node','linux','amd64','["mihomo"]',$2,now(),now(),$3,$4)`,
		agentID, testEnrollmentPublicKey(t), token.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Exec(ctx, "UPDATE enrollment_tokens SET agent_id=$2 WHERE id=$1", token.ID, agentID); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-finished
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !slices.Contains(result.user.AgentIDs, agentID) {
		t.Fatal("purge omitted the node enrolled while it waited for the credential")
	}
	if agent, err := db.GetAgent(ctx, agentID); err != nil || agent.OwnerID != "" {
		t.Fatalf("concurrent enrollment left an orphan owner: %+v %v", agent, err)
	}
	if db.EnrollmentTokenUsable(ctx, token.Token) {
		t.Fatal("concurrent enrollment kept the deleted owner's credential")
	}
}
