package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CleanupDeletedAgents processes a bounded batch of durable deletion jobs.
// A failed job remains queued, including across process restarts. Retry delays
// prevent a locked node from starving later deletions; other workers skip claims.
func (s *Store) CleanupDeletedAgents(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT agent_id FROM agent_deletion_jobs
        WHERE retry_at<=now() ORDER BY retry_at,agent_id LIMIT 16`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		if err := s.cleanupDeletedAgent(ctx, id); err != nil {
			failures = append(failures, err)
			if _, retryErr := s.pool.Exec(ctx, `WITH available AS (
                SELECT agent_id FROM agent_deletion_jobs WHERE agent_id=$1 FOR UPDATE SKIP LOCKED
            ) UPDATE agent_deletion_jobs SET retry_at=now()+interval '1 minute'
              WHERE agent_id IN (SELECT agent_id FROM available)`, id); retryErr != nil {
				return errors.Join(append(failures, retryErr)...)
			}
		}
	}
	return errors.Join(failures...)
}

// Bound each transaction by row count as well as time. Committed batches
// survive a later lock timeout or shutdown, so large histories make progress.
const agentCleanupBatchSize = 256

type agentCleanupStep struct {
	stage string
	query string
}

var agentCleanupSteps = []agentCleanupStep{
	{"fail pending tasks", `UPDATE tasks SET status='failed', error='agent identity was revoked',
        finished_at=now(), config_content=NULL, lease_id=NULL
        WHERE id IN (SELECT id FROM tasks WHERE agent_id=$1 AND status IN ('pending','running') LIMIT $2)`},
	{"clear configs", `UPDATE configs SET deleted_at=now(),content='',updated_at=now()
        WHERE id IN (SELECT id FROM configs WHERE agent_id=$1 AND deleted_at IS NULL LIMIT $2)`},
	{"delete config revisions", `DELETE FROM config_revisions WHERE ctid IN
        (SELECT ctid FROM config_revisions WHERE config_id IN (SELECT id FROM configs WHERE agent_id=$1) LIMIT $2)`},
	{"delete daily usage", `DELETE FROM port_traffic_daily_usage WHERE ctid IN
        (SELECT ctid FROM port_traffic_daily_usage WHERE agent_id=$1 LIMIT $2)`},
	{"delete daily accounting", `DELETE FROM port_traffic_daily_accounting WHERE ctid IN
        (SELECT ctid FROM port_traffic_daily_accounting WHERE agent_id=$1 LIMIT $2)`},
	{"delete accounting epochs", `DELETE FROM port_traffic_accounting_epochs WHERE ctid IN
        (SELECT ctid FROM port_traffic_accounting_epochs WHERE agent_id=$1 LIMIT $2)`},
	{"delete traffic policies", `DELETE FROM port_traffic_policies WHERE ctid IN
        (SELECT ctid FROM port_traffic_policies WHERE agent_id=$1 LIMIT $2)`},
	{"delete SubStore selections", `DELETE FROM substore_sync_items WHERE ctid IN
        (SELECT ctid FROM substore_sync_items WHERE agent_id=$1 LIMIT $2)`},
	// Use the partitioned primary key for indexed lookup of each selected row.
	{"delete core logs", `DELETE FROM core_logs WHERE (id,received_at) IN
        (SELECT id,received_at FROM core_logs WHERE agent_id=$1 LIMIT $2)`},
	// Entries must be gone before their referenced batch markers are removed.
	{"delete core log batches", `DELETE FROM core_log_batches WHERE ctid IN
        (SELECT ctid FROM core_log_batches WHERE agent_id=$1 LIMIT $2)`},
}

func (s *Store) cleanupDeletedAgent(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		progressed, err := s.cleanupDeletedAgentBatch(ctx, id)
		if err != nil || !progressed {
			return err
		}
	}
}

func (s *Store) cleanupDeletedAgentBatch(ctx context.Context, id string) (progressed bool, err error) {
	stage := "claim cleanup"
	defer func() {
		if err != nil {
			err = fmt.Errorf("clean deleted agent %s (%s): %w", id, stage, err)
		}
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='1s'`); err != nil {
		return false, err
	}
	var claimed string
	err = tx.QueryRow(ctx, `SELECT agent_id FROM agent_deletion_jobs
        WHERE agent_id=$1 AND retry_at<=now() FOR UPDATE SKIP LOCKED`, id).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, step := range agentCleanupSteps {
		stage = step.stage
		command, err := tx.Exec(ctx, step.query, id, agentCleanupBatchSize)
		if err != nil {
			return false, err
		}
		if command.RowsAffected() == 0 {
			continue
		}
		// Commit before moving to another table. A blocked later stage must
		// not undo this batch. Move unfinished work behind older queue entries.
		if _, err := tx.Exec(ctx, `UPDATE agent_deletion_jobs SET retry_at=now() WHERE agent_id=$1`, id); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	stage = "complete cleanup"
	if _, err := tx.Exec(ctx, `DELETE FROM agent_deletion_jobs WHERE agent_id=$1`, id); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}
