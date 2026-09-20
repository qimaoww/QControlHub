package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) DeleteAgent(ctx context.Context, id string) (err error) {
	// HTTP write deadlines do not cancel database work. Bound lock waits and
	// cleanup so a stalled deletion releases its transaction and returns an error.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stage := "authorize"
	defer func() {
		if err != nil {
			err = fmt.Errorf("delete agent %s (%s): %w", id, stage, err)
		}
	}()
	if err := requireAgentDeletion(ctx, s.pool, id); err != nil {
		return err
	}
	stage = "begin transaction"
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var enrollmentID *string
	stage = "revoke identity"
	err = tx.QueryRow(ctx, `
		UPDATE agents SET revoked_at=now()
		WHERE id=$1 AND revoked_at IS NULL
		RETURNING enrollment_id`, id).Scan(&enrollmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
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
	// A partitioned core_logs table cannot carry ON DELETE CASCADE, so the
	// node's logs and their deduplication markers are removed here instead. The
	// covering index serves the lookup, so this stays a bounded delete.
	stage = "delete core logs"
	if _, err := tx.Exec(ctx, `DELETE FROM core_logs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	stage = "delete core log batches"
	if _, err := tx.Exec(ctx, `DELETE FROM core_log_batches WHERE agent_id=$1`, id); err != nil {
		return err
	}
	legacyEnrollmentID := ""
	if enrollmentID != nil {
		legacyEnrollmentID = strings.TrimSpace(*enrollmentID)
	}
	stage = "delete enrollment credentials"
	if _, err := tx.Exec(ctx, `
		DELETE FROM enrollment_tokens
		WHERE agent_id=$1 OR id=NULLIF($2,'')`, id, legacyEnrollmentID); err != nil {
		return err
	}
	stage = "commit"
	return tx.Commit(ctx)
}
