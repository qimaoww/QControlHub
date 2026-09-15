package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// CreateAgentEnrollmentToken adds a reusable credential for an existing agent.
// Existing credentials remain valid and the plaintext token is returned once.
func (s *Store) CreateAgentEnrollmentToken(ctx context.Context, agentID string) (core.EnrollmentTokenCreated, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return core.EnrollmentTokenCreated{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	defer tx.Rollback(ctx)
	created, err := s.createAgentEnrollmentTokenTx(ctx, tx, agentID)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	return created, nil
}

// CreateAgentEnrollmentTokenWithAudit atomically persists an Agent-bound
// enrollment credential and its creation audit entry.
func (s *Store) CreateAgentEnrollmentTokenWithAudit(ctx context.Context, agentID string, entry core.AuditLogEntry) (core.EnrollmentTokenCreated, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return core.EnrollmentTokenCreated{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	defer tx.Rollback(ctx)
	created, err := s.createAgentEnrollmentTokenTx(ctx, tx, agentID)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	if entry.Detail == "" {
		entry.Detail = created.ID
	}
	if err := recordAuditWithExecutor(ctx, tx, entry); err != nil {
		return core.EnrollmentTokenCreated{}, fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	return created, nil
}

func (s *Store) createAgentEnrollmentTokenTx(ctx context.Context, tx pgx.Tx, agentID string) (core.EnrollmentTokenCreated, error) {
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	// Administrators can issue a command for another user's node. Serialize
	// that issuance with purge before locking the Agent or inserting a token.
	if scopeForConfig(ctx).Admin {
		rows, err := tx.Query(ctx, `SELECT id FROM panel_users
			WHERE id=(SELECT owner_id FROM agents WHERE id=$1) FOR SHARE`, agentID)
		if err != nil {
			return core.EnrollmentTokenCreated{}, err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return core.EnrollmentTokenCreated{}, err
		}
	}
	if err := requireAgentAdministration(ctx, tx, agentID); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	rawToken, err := core.NewToken()
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	digest := sha256.Sum256([]byte(rawToken))
	sealed, err := s.encryptEnrollmentToken(rawToken)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	now := time.Now().UTC()

	var name, ownerID string
	if err := tx.QueryRow(ctx, `
		SELECT name,owner_id FROM agents
		WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&name, &ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.EnrollmentTokenCreated{}, ErrNotFound
		}
		return core.EnrollmentTokenCreated{}, err
	}

	value := core.EnrollmentToken{
		AgentID: agentID, Name: strings.TrimSpace(name), MaxUses: 0, UsedCount: 0,
		Reusable: true, Recoverable: true, CreatedAt: now,
	}
	if value.Name == "" {
		return core.EnrollmentTokenCreated{}, fmt.Errorf("%w: agent name is empty", ErrInvalid)
	}
	value.ID, err = core.NewID("enr")
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO enrollment_tokens
			(id,agent_id,name,token_hash,token_ciphertext,expires_at,max_uses,used_count,reusable,created_at,owner_id)
		VALUES ($1,$2,$3,$4,$5,NULL,0,0,TRUE,$6,$7)`,
		value.ID, value.AgentID, value.Name, digest[:], sealed, now, ownerID); err != nil {
		return core.EnrollmentTokenCreated{}, mapError(err)
	}
	return core.EnrollmentTokenCreated{EnrollmentToken: value, Token: rawToken}, nil
}
