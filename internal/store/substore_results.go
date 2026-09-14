package store

import (
	"context"
	"strings"
)

func (s *Store) RecordSubStoreSyncResult(ctx context.Context, targetID string, syncErr error) error {
	status := "success"
	message := ""
	if syncErr != nil {
		status = "failed"
		message = truncate(strings.TrimSpace(syncErr.Error()), 500)
	}
	args := []any{status, message, strings.TrimSpace(targetID)}
	ownerWhere := workspaceOwnerClause(ctx, "owner_id", &args)
	command, err := s.pool.Exec(ctx, `
		UPDATE substore_sync_targets SET
			last_synced_at=now(),last_sync_status=$1,last_sync_error=$2,updated_at=now()
		WHERE id=$3`+ownerWhere, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
