package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	if err := requireAgentDeletion(ctx, s.pool, id); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var enrollmentID *string
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
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status='failed', error='agent identity was revoked', finished_at=now(), config_content=NULL, lease_id=NULL
		WHERE agent_id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='',updated_at=now()
			WHERE agent_id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_revisions WHERE config_id IN (SELECT id FROM configs WHERE agent_id=$1)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_usage WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_accounting WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_accounting_epochs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM substore_sync_items WHERE agent_id=$1`, id); err != nil {
		return err
	}
	// A partitioned core_logs table cannot carry ON DELETE CASCADE, so the
	// node's logs and their deduplication markers are removed here instead. The
	// covering index serves the lookup, so this stays a bounded delete.
	if _, err := tx.Exec(ctx, `DELETE FROM core_logs WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core_log_batches WHERE agent_id=$1`, id); err != nil {
		return err
	}
	legacyEnrollmentID := ""
	if enrollmentID != nil {
		legacyEnrollmentID = strings.TrimSpace(*enrollmentID)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM enrollment_tokens
		WHERE agent_id=$1 OR id=NULLIF($2,'')`, id, legacyEnrollmentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
