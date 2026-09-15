package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) AgentPublicKey(ctx context.Context, id string) ([]byte, error) {
	var publicKey []byte
	err := s.pool.QueryRow(ctx, `SELECT public_key FROM agents WHERE id=$1 AND revoked_at IS NULL`, id).Scan(&publicKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return publicKey, nil
}

func (s *Store) RecordNonce(ctx context.Context, agentID, nonce string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_nonces (agent_id, nonce, expires_at) VALUES ($1,$2,$3)`, agentID, nonce, expiresAt)
	if isUniqueViolation(err) {
		return ErrReplay
	}
	return err
}

func (s *Store) CleanupNonces(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agent_nonces WHERE expires_at < now()`)
	return err
}
