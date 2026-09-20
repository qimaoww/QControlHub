package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeleteAgentBoundsBlockedCleanup(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "delete-owner")
	agent := sharedTestAgent(t, db, owner)
	config := sharedTestConfig(t, db, owner, agent.ID, 21001)
	blocker, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT id FROM configs WHERE id=$1 FOR UPDATE`, config.ID); err != nil {
		t.Fatal(err)
	}
	// No caller deadline: DeleteAgent itself must stop a blocked transaction.
	started := time.Now()
	err = db.DeleteAgent(context.WithoutCancel(owner), agent.ID)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "clear configs") {
		t.Fatalf("blocked deletion must identify the timed-out stage: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 35*time.Second {
		t.Fatalf("blocked deletion exceeded its deadline: %s", elapsed)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var revoked bool
	if err := db.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM agents WHERE id=$1`, agent.ID).Scan(&revoked); err != nil || revoked {
		t.Fatalf("timed-out cleanup must roll back identity revocation: revoked=%v err=%v", revoked, err)
	}
	if err := db.DeleteAgent(owner, agent.ID); err != nil {
		t.Fatalf("delete after releasing the lock: %v", err)
	}
}
