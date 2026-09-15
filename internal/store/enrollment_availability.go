package store

import (
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// EnrollmentTokenUsable checks an add-node credential without consuming it.
// Reusable node credentials remain valid until explicitly deleted.
func (s *Store) EnrollmentTokenUsable(ctx context.Context, rawToken string) bool {
	rawToken = strings.TrimSpace(rawToken)
	if len(rawToken) < 32 {
		return false
	}
	digest := sha256.Sum256([]byte(rawToken))
	var expiresAt *time.Time
	var maxUses, usedCount int
	var reusable bool
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT expires_at,max_uses,used_count,reusable,revoked_at
		FROM enrollment_tokens WHERE token_hash=$1
			AND (owner_id='' OR starts_with(owner_id,'token_') OR EXISTS(
				SELECT 1 FROM panel_users u WHERE u.id=enrollment_tokens.owner_id AND NOT u.disabled))`, digest[:]).Scan(&expiresAt, &maxUses, &usedCount, &reusable, &revokedAt)
	return err == nil && revokedAt == nil && (reusable || (expiresAt != nil && usedCount < maxUses && time.Now().Before(*expiresAt)))
}

func (s *Store) ListEnrollmentTokens(ctx context.Context) ([]core.EnrollmentToken, error) {
	args := []any{}
	where := ownerClause(ctx, "owner_id", &args)
	where += hiddenAgentClause(ctx, "enrollment_tokens.agent_id", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,expires_at,max_uses,used_count,reusable,created_at,revoked_at,
		       token_ciphertext,token_hash
		FROM enrollment_tokens WHERE true`+where+` ORDER BY created_at DESC LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.EnrollmentToken, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var value core.EnrollmentToken
		var ciphertext *string
		var storedDigest []byte
		if err := rows.Scan(&value.ID, &value.AgentID, &value.Name, &value.ExpiresAt, &value.MaxUses, &value.UsedCount, &value.Reusable, &value.CreatedAt, &value.RevokedAt, &ciphertext, &storedDigest); err != nil {
			return nil, err
		}
		_, recoverErr := s.recoverEnrollmentToken(ciphertext, storedDigest)
		value.Recoverable = recoverErr == nil && enrollmentTokenCommandActive(value.ExpiresAt, value.MaxUses, value.UsedCount, value.Reusable, value.RevokedAt, now)
		result = append(result, value)
	}
	return result, rows.Err()
}

func enrollmentTokenCommandActive(expiresAt *time.Time, maxUses, usedCount int, reusable bool, revokedAt *time.Time, now time.Time) bool {
	return revokedAt == nil && (expiresAt == nil || expiresAt.After(now)) && (reusable || usedCount < maxUses)
}

// ListEnrollmentCommandAvailability returns a non-secret projection for the
// currently recoverable command of each active Agent. A bad key, damaged
// ciphertext, digest-only legacy row, revoked row, and expired row all remain
// unavailable; another valid credential for the same Agent may still qualify.
func (s *Store) ListEnrollmentCommandAvailability(ctx context.Context, agentIDs []string) (map[string]bool, error) {
	available := make(map[string]bool)
	if len(agentIDs) == 0 {
		return available, nil
	}
	query, args := enrollmentAvailabilityQuery(ctx, agentIDs)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return s.scanEnrollmentCommandAvailability(rows)
}

const enrollmentCommandAvailabilitySQL = `
		SELECT agent_id,token_ciphertext,token_hash
		FROM enrollment_tokens
		WHERE ($1::text[] IS NULL OR agent_id=ANY($1::text[])) AND revoked_at IS NULL
		  AND agent_id IN (SELECT id FROM agents WHERE revoked_at IS NULL)
		  AND token_ciphertext IS NOT NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND (reusable OR used_count<max_uses)
`

func enrollmentAvailabilityQuery(ctx context.Context, agentIDs []string) (string, []any) {
	args := []any{agentIDs}
	where := ownerClause(ctx, "owner_id", &args)
	return enrollmentCommandAvailabilitySQL + where + ` ORDER BY created_at DESC`, args
}

func (s *Store) scanEnrollmentCommandAvailability(rows pgx.Rows) (map[string]bool, error) {
	defer rows.Close()
	available := make(map[string]bool)
	for rows.Next() {
		var agentID string
		var ciphertext *string
		var storedDigest []byte
		if err := rows.Scan(&agentID, &ciphertext, &storedDigest); err != nil {
			return nil, err
		}
		if _, already := available[agentID]; already {
			continue
		}
		if _, err := s.recoverEnrollmentToken(ciphertext, storedDigest); err == nil {
			available[agentID] = true
		}
	}
	return available, rows.Err()
}
