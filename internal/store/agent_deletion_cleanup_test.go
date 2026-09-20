package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentCleanupCommitsBatchesBeforeBlockedLaterStage(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	other := sharedTestAgent(t, db, ctx)
	now := time.Now().UTC()
	// Two partitions for the deleted node and one for a live node. Physical
	// tuple offsets overlap between partitions and must not cross-delete.
	for _, seed := range []struct {
		id, batch string
		at        time.Time
	}{
		{agent.ID, "cleanup-old", now.Add(-24 * time.Hour)},
		{agent.ID, "cleanup-current", now},
		{other.ID, "cleanup-other", now.Add(24 * time.Hour)},
	} {
		if _, err := db.pool.Exec(ctx, `INSERT INTO core_log_batches(id,agent_id,received_at) VALUES ($1,$2,$3)`, seed.batch, seed.id, seed.at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
            SELECT $1,n,$2,'mihomo','info','cleanup fixture',$3,$3 FROM generate_series(1,$4::int) n`, seed.batch, seed.id, seed.at, agentCleanupBatchSize+10); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if progress, err := db.cleanupDeletedAgentBatch(ctx, agent.ID); err != nil || !progress {
		t.Fatalf("first cleanup batch: %v %v", progress, err)
	}
	var remaining, preserved int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE agent_id=$1),count(*) FILTER (WHERE agent_id=$2) FROM core_logs`, agent.ID, other.ID).Scan(&remaining, &preserved); err != nil {
		t.Fatal(err)
	}
	if remaining != agentCleanupBatchSize+20 || preserved != agentCleanupBatchSize+10 {
		t.Fatalf("unbounded or cross-partition deletion: remaining=%d preserved=%d", remaining, preserved)
	}

	blocker, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT id FROM core_log_batches WHERE agent_id=$1 FOR UPDATE`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CleanupDeletedAgents(ctx); err == nil || !strings.Contains(err.Error(), "delete core log batches") {
		t.Fatalf("expected blocked batch-marker cleanup: %v", err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_logs WHERE agent_id=$1`, agent.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("later failure rolled back completed log erasure: %d %v", remaining, err)
	}
	var queued bool
	if err := db.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_deletion_jobs WHERE agent_id=$1)`, agent.ID).Scan(&queued); err != nil || !queued {
		t.Fatalf("unfinished job lost: %v %v", queued, err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_deletion_jobs SET retry_at=now() WHERE agent_id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_log_batches WHERE agent_id=$1`, agent.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("retry left batch markers: %d %v", remaining, err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_logs WHERE agent_id=$1`, other.ID).Scan(&preserved); err != nil || preserved != agentCleanupBatchSize+10 {
		t.Fatalf("retry erased live node logs: %d %v", preserved, err)
	}
}

func TestDeletedAgentCannotUploadLogsDuringCleanup(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if err := db.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	batch := core.CoreLogBatch{ID: "log_1234567890abcdef", Entries: []core.CoreLogEntry{
		{Engine: core.EngineMihomo, Level: "info", Message: "late upload", LoggedAt: time.Now().UTC()},
	}}
	if err := db.StoreCoreLogs(ctx, agent.ID, batch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("accepted logs during cleanup: %v", err)
	}
	if err := db.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, batch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recreated logs after cleanup: %v", err)
	}
	var rows int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_log_batches WHERE agent_id=$1`, agent.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rejected upload left a batch marker: %d %v", rows, err)
	}
}
