package store

import (
	"context"
)

func (s *Store) DeleteEnrollmentToken(ctx context.Context, id string) error {
	args := []any{id}
	where := ownerClause(ctx, "owner_id", &args)
	where += hiddenAgentClause(ctx, "enrollment_tokens.agent_id", &args)
	command, err := s.pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE id=$1`+where, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CleanupExpiredEnrollmentTokens removes credentials that can no longer be
// used, including one-shot credentials that exhausted their use count. This
// also erases protected ciphertext instead of retaining an expired secret.
func (s *Store) CleanupExpiredEnrollmentTokens(ctx context.Context) (int64, error) {
	command, err := s.pool.Exec(ctx, `
		DELETE FROM enrollment_tokens
		WHERE revoked_at IS NOT NULL
		   OR (expires_at IS NOT NULL AND expires_at <= now())
		   OR (reusable = FALSE AND used_count >= max_uses)`)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}
