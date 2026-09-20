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
			if _, retryErr := s.pool.Exec(ctx, `UPDATE agent_deletion_jobs
                SET retry_at=now()+interval '1 minute' WHERE agent_id=$1`, id); retryErr != nil {
				return errors.Join(append(failures, retryErr)...)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Store) cleanupDeletedAgent(ctx context.Context, id string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stage := "claim cleanup"
	defer func() {
		if err != nil {
			err = fmt.Errorf("clean deleted agent %s (%s): %w", id, stage, err)
		}
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var claimed string
	err = tx.QueryRow(ctx, `SELECT agent_id FROM agent_deletion_jobs
        WHERE agent_id=$1 AND retry_at<=now() FOR UPDATE SKIP LOCKED`, id).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Do not occupy a connection for the full deadline on a locked history row.
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='1s'`); err != nil {
		return err
	}
	stage = "fail pending tasks"
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status='failed', error='agent identity was revoked', finished_at=now(), config_content=NULL, lease_id=NULL
		WHERE agent_id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return err
	}
	stage = "clear configs"
	_, err = tx.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='',updated_at=now()
			WHERE agent_id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	stage = "delete config revisions"
	if _, err := tx.Exec(ctx, `DELETE FROM config_revisions WHERE config_id IN (SELECT id FROM configs WHERE agent_id=$1)`, id); err != nil {
		return err
	}
	stage = "delete daily usage"
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_usage WHERE agent_id=$1`, id); err != nil {
		return err
	}
	stage = "delete daily accounting"
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_accounting WHERE agent_id=$1`, id); err != nil {
		return err
	}
	stage = "delete accounting epochs"
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_accounting_epochs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	stage = "delete traffic policies"
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE agent_id=$1`, id); err != nil {
		return err
	}
	stage = "delete SubStore selections"
	if _, err := tx.Exec(ctx, `DELETE FROM substore_sync_items WHERE agent_id=$1`, id); err != nil {
		return err
	}
	// Delete log entries before their batch markers to satisfy the foreign key.
	stage = "delete core logs"
	if _, err := tx.Exec(ctx, `DELETE FROM core_logs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	stage = "delete core log batches"
	if _, err := tx.Exec(ctx, `DELETE FROM core_log_batches WHERE agent_id=$1`, id); err != nil {
		return err
	}

	stage = "complete cleanup"
	if _, err := tx.Exec(ctx, `DELETE FROM agent_deletion_jobs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
