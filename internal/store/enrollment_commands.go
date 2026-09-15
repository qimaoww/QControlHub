package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// EnrollmentCommandForAgent returns an already-persisted credential without
// creating, consuming, rotating, or otherwise mutating enrollment state.
func (s *Store) EnrollmentCommandForAgent(ctx context.Context, agentID string) (core.EnrollmentTokenCreated, error) {
	if err := requireAgentAdministration(ctx, s.pool, agentID); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return core.EnrollmentTokenCreated{}, ErrInvalid
	}
	args := []any{agentID}
	where := ownerClause(ctx, "owner_id", &args)
	return s.readEnrollmentCommand(ctx, `
		SELECT id,COALESCE(agent_id,''),name,expires_at,max_uses,used_count,reusable,created_at,revoked_at,token_ciphertext,token_hash
		FROM enrollment_tokens
		WHERE agent_id=$1 AND revoked_at IS NULL AND token_ciphertext IS NOT NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND (reusable OR used_count<max_uses)
		`+where+` ORDER BY created_at DESC`, args...)
}

// EnrollmentCommandByID reveals one explicitly selected add-node record.
func (s *Store) EnrollmentCommandByID(ctx context.Context, id string) (core.EnrollmentTokenCreated, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return core.EnrollmentTokenCreated{}, ErrInvalid
	}
	args := []any{id}
	where := ownerClause(ctx, "owner_id", &args)
	where += hiddenAgentClause(ctx, "enrollment_tokens.agent_id", &args)
	return s.readEnrollmentCommand(ctx, `
		SELECT id,COALESCE(agent_id,''),name,expires_at,max_uses,used_count,reusable,created_at,revoked_at,token_ciphertext,token_hash
		FROM enrollment_tokens
		WHERE id=$1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND (reusable OR used_count<max_uses)`+where, args...)
}

func (s *Store) readEnrollmentCommand(ctx context.Context, query string, args ...any) (core.EnrollmentTokenCreated, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	defer rows.Close()
	var unavailable bool
	for rows.Next() {
		var value core.EnrollmentTokenCreated
		var ciphertext *string
		var storedDigest []byte
		if err := rows.Scan(
			&value.ID, &value.AgentID, &value.Name, &value.ExpiresAt, &value.MaxUses,
			&value.UsedCount, &value.Reusable, &value.CreatedAt, &value.RevokedAt, &ciphertext, &storedDigest,
		); err != nil {
			return core.EnrollmentTokenCreated{}, err
		}
		value.Token, err = s.recoverEnrollmentToken(ciphertext, storedDigest)
		if err == nil {
			value.Recoverable = true
			return value, nil
		}
		if errors.Is(err, ErrSecretUnavailable) {
			unavailable = true
			continue
		}
		return core.EnrollmentTokenCreated{}, err
	}
	if err := rows.Err(); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	if unavailable {
		return core.EnrollmentTokenCreated{}, ErrSecretUnavailable
	}
	return core.EnrollmentTokenCreated{}, ErrNotFound
}

func (s *Store) recoverEnrollmentToken(ciphertext *string, storedDigest []byte) (string, error) {
	if ciphertext == nil || strings.TrimSpace(*ciphertext) == "" {
		return "", fmt.Errorf("%w: legacy digest-only credential cannot be recovered", ErrSecretUnavailable)
	}
	if s.cryptor == nil {
		return "", fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required", ErrSecretUnavailable)
	}
	token, err := s.cryptor.decrypt(*ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSecretUnavailable, err)
	}
	digest := sha256.Sum256([]byte(token))
	if len(storedDigest) != len(digest) || subtle.ConstantTimeCompare(storedDigest, digest[:]) != 1 {
		return "", fmt.Errorf("%w: encrypted credential digest mismatch", ErrSecretUnavailable)
	}
	return token, nil
}
