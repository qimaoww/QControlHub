package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) CreateEnrollmentToken(ctx context.Context, request core.EnrollmentTokenRequest) (core.EnrollmentTokenCreated, error) {
	return s.createEnrollmentToken(ctx, request, false)
}

// CreateProtectedEnrollmentToken persists a recoverable enrollment credential
// only after sealing it with the configured encryption key. The hash remains
// the sole value used for Agent authentication.
func (s *Store) CreateProtectedEnrollmentToken(ctx context.Context, request core.EnrollmentTokenRequest) (core.EnrollmentTokenCreated, error) {
	return s.createEnrollmentToken(ctx, request, true)
}

func (s *Store) createEnrollmentToken(ctx context.Context, request core.EnrollmentTokenRequest, protect bool) (core.EnrollmentTokenCreated, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	created, err := s.createEnrollmentTokenWithExecutor(ctx, tx, request, protect)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	return created, nil
}

// CreateProtectedEnrollmentTokenWithAudit commits the recoverable credential
// and its disclosure audit entry in one PostgreSQL transaction. A failed
// audit insert rolls back the credential before it becomes visible.
func (s *Store) CreateProtectedEnrollmentTokenWithAudit(ctx context.Context, request core.EnrollmentTokenRequest, entry core.AuditLogEntry) (core.EnrollmentTokenCreated, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	created, err := s.createEnrollmentTokenWithExecutor(ctx, tx, request, true)
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	if entry.Target == "" {
		entry.Target = created.ID
	}
	if err := recordAuditWithExecutor(ctx, tx, entry); err != nil {
		return core.EnrollmentTokenCreated{}, fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	return created, nil
}

func (s *Store) createEnrollmentTokenWithExecutor(ctx context.Context, executor storeExecutor, request core.EnrollmentTokenRequest, protect bool) (core.EnrollmentTokenCreated, error) {
	ownerID := scopeForConfig(ctx).OwnerID
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "Add node"
	}
	if utf8.RuneCountInString(name) > 100 {
		return core.EnrollmentTokenCreated{}, fmt.Errorf("%w: add-node name exceeds 100 characters", ErrInvalid)
	}
	if !request.Reusable {
		if request.TTLMinutes == 0 {
			request.TTLMinutes = 15
		}
		if request.TTLMinutes < 1 || request.TTLMinutes > 1440 {
			return core.EnrollmentTokenCreated{}, fmt.Errorf("%w: enrollment token lifetime must be between 1 and 1440 minutes", ErrInvalid)
		}
		if request.MaxUses == 0 {
			request.MaxUses = 1
		}
		if request.MaxUses < 1 || request.MaxUses > 50 {
			return core.EnrollmentTokenCreated{}, fmt.Errorf("%w: enrollment token max uses must be between 1 and 50", ErrInvalid)
		}
	}
	if request.Reusable {
		var exists bool
		if err := executor.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM enrollment_tokens
				WHERE reusable=TRUE AND revoked_at IS NULL AND lower(name)=lower($1) AND owner_id=$2
				UNION ALL
				SELECT 1 FROM agents
				WHERE revoked_at IS NULL AND lower(name)=lower($1) AND owner_id=$2
			)`, name, ownerID).Scan(&exists); err != nil {
			return core.EnrollmentTokenCreated{}, err
		}
		if exists {
			return core.EnrollmentTokenCreated{}, ErrConflict
		}
	}
	id, err := core.NewID("enr")
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	rawToken, err := core.NewToken()
	if err != nil {
		return core.EnrollmentTokenCreated{}, err
	}
	digest := sha256.Sum256([]byte(rawToken))
	var tokenCiphertext *string
	if protect {
		sealed, err := s.encryptEnrollmentToken(rawToken)
		if err != nil {
			return core.EnrollmentTokenCreated{}, err
		}
		tokenCiphertext = &sealed
	}
	now := time.Now().UTC()
	value := core.EnrollmentToken{
		ID: id, Name: name, MaxUses: request.MaxUses, UsedCount: 0, Reusable: request.Reusable, Recoverable: protect, CreatedAt: now,
	}
	if !request.Reusable {
		expiresAt := now.Add(time.Duration(request.TTLMinutes) * time.Minute)
		value.ExpiresAt = &expiresAt
	}
	_, err = executor.Exec(ctx, `
		INSERT INTO enrollment_tokens (id,name,token_hash,token_ciphertext,expires_at,max_uses,used_count,reusable,created_at,owner_id,admin_hidden)
		VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$10)`,
		value.ID, value.Name, digest[:], tokenCiphertext, value.ExpiresAt, value.MaxUses, value.Reusable, value.CreatedAt, ownerID, request.AdminHidden)
	if err != nil {
		return core.EnrollmentTokenCreated{}, mapError(err)
	}
	return core.EnrollmentTokenCreated{EnrollmentToken: value, Token: rawToken}, nil
}
