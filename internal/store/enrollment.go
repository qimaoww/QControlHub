package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) EnrollAgent(ctx context.Context, request core.EnrollRequest, enrollmentToken string) (core.Agent, error) {
	id, err := core.NewID("agt")
	if err != nil {
		return core.Agent{}, err
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(request.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return core.Agent{}, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalid)
	}
	supported := append([]core.Engine{}, request.Capabilities...)
	supportedCapabilities, _ := json.Marshal(supported)
	features, _ := json.Marshal(request.Features)
	if len(request.Features) == 0 {
		features = []byte(`[]`)
	}
	labels, _ := json.Marshal(request.Labels)
	runtimeState := []byte(`{}`)
	enrolledAt := time.Now().UTC()
	lastSeen := time.Unix(0, 0).UTC()
	if len(enrollmentToken) < 32 {
		return core.Agent{}, ErrNotFound
	}
	tokenDigest := sha256.Sum256([]byte(enrollmentToken))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Agent{}, err
	}
	defer tx.Rollback(ctx)
	var enrollmentID, enrollmentName, enrollmentOwner string
	var enrollmentAgentID *string
	var reusable, adminHidden bool
	err = tx.QueryRow(ctx, `
		UPDATE enrollment_tokens SET used_count=used_count+1
		WHERE token_hash=$1 AND revoked_at IS NULL
		  AND (reusable OR (expires_at>now() AND used_count<max_uses))
		  AND (owner_id='' OR starts_with(owner_id,'token_') OR EXISTS(
			SELECT 1 FROM panel_users u WHERE u.id=enrollment_tokens.owner_id AND NOT u.disabled))
		RETURNING id,name,reusable,agent_id,owner_id,admin_hidden`, tokenDigest[:]).Scan(&enrollmentID, &enrollmentName, &reusable, &enrollmentAgentID, &enrollmentOwner, &adminHidden)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Agent{}, ErrNotFound
	}
	if err != nil {
		return core.Agent{}, err
	}
	name := strings.TrimSpace(request.Name)
	reinstalled := false
	if reusable {
		if name != enrollmentName {
			return core.Agent{}, ErrNotFound
		}
		boundAgentID := ""
		if enrollmentAgentID != nil {
			boundAgentID = strings.TrimSpace(*enrollmentAgentID)
		}
		if boundAgentID != "" {
			err = tx.QueryRow(ctx, `SELECT id,name,admin_hidden FROM agents WHERE id=$1 AND owner_id=$2 AND revoked_at IS NULL FOR UPDATE`, boundAgentID, enrollmentOwner).Scan(&id, &name, &adminHidden)
		} else {
			err = tx.QueryRow(ctx, `SELECT id,name,admin_hidden FROM agents WHERE enrollment_id=$1 AND owner_id=$2 AND revoked_at IS NULL FOR UPDATE`, enrollmentID, enrollmentOwner).Scan(&id, &name, &adminHidden)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if boundAgentID != "" {
				return core.Agent{}, ErrNotFound
			}
			err = nil
		} else if err != nil {
			return core.Agent{}, err
		} else {
			reinstalled = true
		}
	}
	if reinstalled {
		// Keep the node's selection through reinstalls, bounded by the newly
		// declared support of the Agent binary/environment.
		var selected []core.Engine
		if err := tx.QueryRow(ctx, `SELECT capabilities FROM agents WHERE id=$1`, id).Scan(&selected); err != nil {
			return core.Agent{}, err
		}
		request.Capabilities = core.IntersectEngines(selected, request.Capabilities)
	} else {
		settings, err := s.panelSettingsForOwner(ctx, tx, enrollmentOwner)
		if err != nil {
			return core.Agent{}, err
		}
		request.Capabilities = core.IntersectEngines(settings.DefaultAgentEngines, request.Capabilities)
	}
	capabilities, _ := json.Marshal(request.Capabilities)
	if reinstalled {
		// The credential's original name authenticates the reinstall above;
		// retain the row-locked panel name instead of reverting a custom rename.
		// The stored metrics snapshot and the observed address describe the
		// previous installation, so a first report from the new one starts from
		// a clean slate rather than inheriting stale probe results.
		_, err = tx.Exec(ctx, `
			UPDATE agents SET name=$2,version=$3,os=$4,arch=$5,capabilities=$6,features=$7,labels=$8,runtime=$9,
				observed_public_ip='',public_key=$10,last_seen=$11,enrolled_at=$12,revoked_at=NULL
			WHERE id=$1`, id, name, strings.TrimSpace(request.Version), strings.TrimSpace(request.OS), strings.TrimSpace(request.Arch),
			capabilities, features, labels, runtimeState, publicKey, lastSeen, enrolledAt)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM agent_live_state WHERE agent_id=$1`, id)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM agent_nonces WHERE agent_id=$1`, id)
		}
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO agents (id,name,version,os,arch,capabilities,features,labels,runtime,public_key,last_seen,enrolled_at,enrollment_id,owner_id,admin_hidden)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			id, name, strings.TrimSpace(request.Version), strings.TrimSpace(request.OS), strings.TrimSpace(request.Arch),
			capabilities, features, labels, runtimeState, publicKey, lastSeen, enrolledAt, nullableEnrollmentID(reusable, enrollmentID), enrollmentOwner, adminHidden)
	}
	if err != nil {
		return core.Agent{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET supported_capabilities=$2 WHERE id=$1`, id, supportedCapabilities); err != nil {
		return core.Agent{}, err
	}
	if reusable {
		result, bindErr := tx.Exec(ctx, `
			UPDATE enrollment_tokens SET agent_id=$2
			WHERE id=$1 AND (agent_id IS NULL OR agent_id=$2)`, enrollmentID, id)
		if bindErr != nil {
			return core.Agent{}, mapError(bindErr)
		}
		if result.RowsAffected() == 0 {
			return core.Agent{}, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Agent{}, err
	}
	return core.Agent{
		SupportedCapabilities: supported,
		AdminHidden:           adminHidden,
		ID:                    id, Name: name, Version: request.Version,
		OwnerID: enrollmentOwner,
		OS:      request.OS, Arch: request.Arch, Capabilities: append([]core.Engine{}, request.Capabilities...), Features: append([]string(nil), request.Features...),
		Labels: cloneLabels(request.Labels), Runtime: map[core.Engine]core.RuntimeState{},
		LastSeen: lastSeen, EnrolledAt: enrolledAt, Status: "offline", Reinstalled: reinstalled,
	}, nil
}

func nullableEnrollmentID(reusable bool, enrollmentID string) any {
	if reusable {
		return enrollmentID
	}
	return nil
}

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
