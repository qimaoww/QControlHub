package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrReplay            = errors.New("replayed request")
	ErrInvalid           = errors.New("invalid input")
	ErrSecretUnavailable = errors.New("protected enrollment credential unavailable")
	ErrAuditUnavailable  = errors.New("audit unavailable")
)

type Store struct {
	pool       *pgxpool.Pool
	cryptor    *configCryptor
	taskWakeMu sync.Mutex
	taskWakes  map[string]chan struct{}
}

type storeExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const currentSchemaVersion = 33

func Open(ctx context.Context, databaseURL string, allowInsecureRemote bool) (*Store, error) {
	return OpenWithConfigKey(ctx, databaseURL, allowInsecureRemote, "")
}

// OpenWithConfigKey opens the store and enables at-rest configuration
// encryption when a non-empty key is supplied. Existing plaintext rows keep
// working transparently; new writes are sealed with AES-256-GCM.
func OpenWithConfigKey(ctx context.Context, databaseURL string, allowInsecureRemote bool, configKey string) (*Store, error) {
	return OpenWithConfigKeyring(ctx, databaseURL, allowInsecureRemote, configKey, nil)
}

// OpenWithConfigKeyring uses configKey for new encrypted writes and previous
// keys only to decrypt data written before an intentional key rotation.
func OpenWithConfigKeyring(ctx context.Context, databaseURL string, allowInsecureRemote bool, configKey string, previousKeys []string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("QCH_DATABASE_URL is required")
	}
	configKey = strings.TrimSpace(configKey)
	if configKey != "" && len([]byte(configKey)) < minConfigEncryptionKeyBytes {
		return nil, fmt.Errorf("QCH_CONFIG_ENCRYPTION_KEY must be at least %d bytes", minConfigEncryptionKeyBytes)
	}
	if strings.TrimSpace(configKey) == "" {
		for _, previousKey := range previousKeys {
			if strings.TrimSpace(previousKey) != "" {
				return nil, errors.New("QCH_CONFIG_ENCRYPTION_KEY is required when previous encryption keys are configured")
			}
		}
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL URL: %w", err)
	}
	if !localDatabaseHost(config.ConnConfig.Host) && !allowInsecureRemote {
		tlsConfig := config.ConnConfig.TLSConfig
		verifyFull := tlsConfig != nil && !tlsConfig.InsecureSkipVerify && tlsConfig.ServerName != "" && len(config.ConnConfig.Fallbacks) == 0
		if !verifyFull {
			return nil, errors.New("remote PostgreSQL connections must use sslmode=verify-full without cleartext fallback; set QCH_ALLOW_INSECURE_DATABASE=true only on a trusted development network")
		}
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	cryptor, err := newConfigCryptorKeyring(append([]string{configKey}, previousKeys...))
	if err != nil {
		return nil, err
	}
	if err := cryptor.verify(); err != nil {
		return nil, err
	}
	result := &Store{pool: pool, cryptor: cryptor}
	if err := result.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return result, nil
}

func (s *Store) encryptEnrollmentToken(rawToken string) (string, error) {
	if s.cryptor == nil {
		return "", fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required", ErrSecretUnavailable)
	}
	sealed, err := s.cryptor.encrypt(rawToken)
	if err != nil {
		return "", fmt.Errorf("%w: encrypt enrollment credential: %v", ErrSecretUnavailable, err)
	}
	return sealed, nil
}

func localDatabaseHost(host string) bool {
	if strings.HasPrefix(host, "/") || strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(0x52464f524745)); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer connection.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(0x52464f524745))
	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("initialize schema migration ledger: %w", err)
	}
	var appliedVersion int
	if err := connection.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM qcontrolhub_schema_migrations`).Scan(&appliedVersion); err != nil {
		return fmt.Errorf("read schema migration version: %w", err)
	}
	if appliedVersion > currentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than this QControlHub binary supports (%d)", appliedVersion, currentSchemaVersion)
	}
	if appliedVersion == currentSchemaVersion {
		return nil
	}

	tx, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply PostgreSQL schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations (version) VALUES ($1)`, currentSchemaVersion); err != nil {
		return fmt.Errorf("record schema migration version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

func (s *Store) EnrollAgent(ctx context.Context, request core.EnrollRequest, enrollmentToken string) (core.Agent, error) {
	id, err := core.NewID("agt")
	if err != nil {
		return core.Agent{}, err
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(request.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return core.Agent{}, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalid)
	}
	capabilities, _ := json.Marshal(request.Capabilities)
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
	var enrollmentID, enrollmentName string
	var enrollmentAgentID *string
	var reusable bool
	err = tx.QueryRow(ctx, `
		UPDATE enrollment_tokens SET used_count=used_count+1
		WHERE token_hash=$1 AND revoked_at IS NULL
		  AND (reusable OR (expires_at>now() AND used_count<max_uses))
		RETURNING id,name,reusable,agent_id`, tokenDigest[:]).Scan(&enrollmentID, &enrollmentName, &reusable, &enrollmentAgentID)
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
			err = tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, boundAgentID).Scan(&id)
		} else {
			err = tx.QueryRow(ctx, `SELECT id FROM agents WHERE enrollment_id=$1 AND revoked_at IS NULL FOR UPDATE`, enrollmentID).Scan(&id)
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
		_, err = tx.Exec(ctx, `
			UPDATE agents SET name=$2,version=$3,os=$4,arch=$5,capabilities=$6,features=$7,labels=$8,runtime=$9,
				metrics='{}'::jsonb,public_key=$10,last_seen=$11,enrolled_at=$12,revoked_at=NULL
			WHERE id=$1`, id, name, strings.TrimSpace(request.Version), strings.TrimSpace(request.OS), strings.TrimSpace(request.Arch),
			capabilities, features, labels, runtimeState, publicKey, lastSeen, enrolledAt)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM agent_nonces WHERE agent_id=$1`, id)
		}
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO agents (id,name,version,os,arch,capabilities,features,labels,runtime,public_key,last_seen,enrolled_at,enrollment_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			id, name, strings.TrimSpace(request.Version), strings.TrimSpace(request.OS), strings.TrimSpace(request.Arch),
			capabilities, features, labels, runtimeState, publicKey, lastSeen, enrolledAt, nullableEnrollmentID(reusable, enrollmentID))
	}
	if err != nil {
		return core.Agent{}, mapError(err)
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
		ID: id, Name: name, Version: request.Version,
		OS: request.OS, Arch: request.Arch, Capabilities: append([]core.Engine(nil), request.Capabilities...), Features: append([]string(nil), request.Features...),
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
	return s.createEnrollmentTokenWithExecutor(ctx, s.pool, request, protect)
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
				WHERE reusable=TRUE AND revoked_at IS NULL AND lower(name)=lower($1)
				UNION ALL
				SELECT 1 FROM agents
				WHERE revoked_at IS NULL AND lower(name)=lower($1)
			)`, name).Scan(&exists); err != nil {
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
		INSERT INTO enrollment_tokens (id,name,token_hash,token_ciphertext,expires_at,max_uses,used_count,reusable,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8)`,
		value.ID, value.Name, digest[:], tokenCiphertext, value.ExpiresAt, value.MaxUses, value.Reusable, value.CreatedAt)
	if err != nil {
		return core.EnrollmentTokenCreated{}, mapError(err)
	}
	return core.EnrollmentTokenCreated{EnrollmentToken: value, Token: rawToken}, nil
}

// EnrollmentCommandForAgent returns an already-persisted credential without
// creating, consuming, rotating, or otherwise mutating enrollment state.
func (s *Store) EnrollmentCommandForAgent(ctx context.Context, agentID string) (core.EnrollmentTokenCreated, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return core.EnrollmentTokenCreated{}, ErrInvalid
	}
	return s.readEnrollmentCommand(ctx, `
		SELECT id,COALESCE(agent_id,''),name,expires_at,max_uses,used_count,reusable,created_at,revoked_at,token_ciphertext,token_hash
		FROM enrollment_tokens
		WHERE agent_id=$1 AND revoked_at IS NULL AND token_ciphertext IS NOT NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND (reusable OR used_count<max_uses)
		ORDER BY created_at DESC`, agentID)
}

// EnrollmentCommandByID reveals one explicitly selected add-node record.
func (s *Store) EnrollmentCommandByID(ctx context.Context, id string) (core.EnrollmentTokenCreated, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return core.EnrollmentTokenCreated{}, ErrInvalid
	}
	return s.readEnrollmentCommand(ctx, `
		SELECT id,COALESCE(agent_id,''),name,expires_at,max_uses,used_count,reusable,created_at,revoked_at,token_ciphertext,token_hash
		FROM enrollment_tokens
		WHERE id=$1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND (reusable OR used_count<max_uses)`, id)
}

func (s *Store) readEnrollmentCommand(ctx context.Context, query, id string) (core.EnrollmentTokenCreated, error) {
	rows, err := s.pool.Query(ctx, query, id)
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

	var name string
	if err := tx.QueryRow(ctx, `
		SELECT name FROM agents
		WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&name); err != nil {
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
			(id,agent_id,name,token_hash,token_ciphertext,expires_at,max_uses,used_count,reusable,created_at)
		VALUES ($1,$2,$3,$4,$5,NULL,0,0,TRUE,$6)`,
		value.ID, value.AgentID, value.Name, digest[:], sealed, now); err != nil {
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
		FROM enrollment_tokens WHERE token_hash=$1`, digest[:]).Scan(&expiresAt, &maxUses, &usedCount, &reusable, &revokedAt)
	return err == nil && revokedAt == nil && (reusable || (expiresAt != nil && usedCount < maxUses && time.Now().Before(*expiresAt)))
}

func (s *Store) ListEnrollmentTokens(ctx context.Context) ([]core.EnrollmentToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,expires_at,max_uses,used_count,reusable,created_at,revoked_at,
		       token_ciphertext,token_hash
		FROM enrollment_tokens ORDER BY created_at DESC LIMIT 100`)
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
	rows, err := s.pool.Query(ctx, `
		SELECT agent_id,token_ciphertext,token_hash
		FROM enrollment_tokens
		WHERE agent_id=ANY($1::text[]) AND revoked_at IS NULL
		  AND token_ciphertext IS NOT NULL
		  AND (expires_at IS NULL OR expires_at>now()) AND (reusable OR used_count<max_uses)
		ORDER BY created_at DESC`, agentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	command, err := s.pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE id=$1`, id)
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

// Heartbeat records a complete authenticated Agent heartbeat. The advertised
// features are authoritative: an empty, omitted, or [] feature list clears any
// stale value (for example a previous session's mihomo-development-source-v1),
// so a legacy Agent that reconnects cannot inherit a stale capability the
// control plane would otherwise use to dispatch a mirror task. Metrics-only
// refreshes go through UpdateAgentMetrics, which never touches features.
func (s *Store) Heartbeat(ctx context.Context, id string, heartbeat core.HeartbeatRequest) error {
	return s.HeartbeatWithPublicIPProbeTrust(ctx, id, heartbeat, PublicIPProbeTrust{})
}

// HeartbeatWithPublicIPProbeTrust records a complete heartbeat while binding
// managed public-IP provenance to capability and configuration established by
// the current authenticated WSS session.
func (s *Store) HeartbeatWithPublicIPProbeTrust(ctx context.Context, id string, heartbeat core.HeartbeatRequest, trust PublicIPProbeTrust) error {
	receivedAt := time.Now().UTC()
	heartbeat.Version = strings.TrimSpace(heartbeat.Version)
	heartbeat.OS = strings.TrimSpace(heartbeat.OS)
	heartbeat.Arch = strings.TrimSpace(heartbeat.Arch)
	if utf8.RuneCountInString(heartbeat.Version) > 100 {
		return fmt.Errorf("%w: agent version exceeds 100 characters", ErrInvalid)
	}
	if utf8.RuneCountInString(heartbeat.OS) > 50 || utf8.RuneCountInString(heartbeat.Arch) > 50 {
		return fmt.Errorf("%w: agent OS and architecture must not exceed 50 characters", ErrInvalid)
	}
	runtimeState, err := json.Marshal(heartbeat.Runtime)
	if err != nil {
		return err
	}
	if heartbeat.Metrics != nil {
		metrics := *heartbeat.Metrics
		applyPublicIPProbeTrust(&metrics, trust)
		heartbeat.Metrics = &metrics
	}
	metricsState, err := encodeHeartbeatMetrics(heartbeat.Metrics, receivedAt)
	if err != nil {
		return err
	}
	featuresState, err := json.Marshal(heartbeat.Features)
	if err != nil {
		return err
	}
	if len(heartbeat.Features) == 0 {
		featuresState = []byte(`[]`)
	}
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now(), version=CASE WHEN $2='' THEN version ELSE $2 END, runtime=$3,
			                  metrics=CASE
			                    WHEN $4::jsonb IS NULL THEN metrics - 'public_ipv4' - 'public_ipv6' - 'public_ipv4_source' - 'public_ipv6_source'
					WHEN $4::jsonb ? 'network_interfaces' OR NOT (metrics ? 'network_interfaces') THEN $4::jsonb
			                    ELSE $4::jsonb || jsonb_build_object('network_interfaces', metrics->'network_interfaces')
			                  END,
			                  features=$5::jsonb,
			                  os=CASE WHEN $6='' THEN os ELSE $6 END,
			                  arch=CASE WHEN $7='' THEN arch ELSE $7 END
			WHERE id=$1 AND revoked_at IS NULL`, id, heartbeat.Version, runtimeState, metricsState, featuresState, heartbeat.OS, heartbeat.Arch)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.UpdatePortTrafficUsage(ctx, id, heartbeat.TrafficUsage, receivedAt)
}

// UpdateAgentMetrics refreshes only the live metrics snapshot from the
// high-frequency metrics pushes. The push proves liveness, so last_seen is
// refreshed as well, while version, runtime, and features stay untouched.
func (s *Store) UpdateAgentMetrics(ctx context.Context, id string, metrics core.HostMetrics) error {
	return s.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, id, metrics, PublicIPProbeTrust{})
}

// UpdateAgentMetricsWithPublicIPProbeTrust applies the same current-session
// provenance constraint to metrics-only refreshes without changing persisted
// features.
func (s *Store) UpdateAgentMetricsWithPublicIPProbeTrust(ctx context.Context, id string, metrics core.HostMetrics, trust PublicIPProbeTrust) error {
	applyPublicIPProbeTrust(&metrics, trust)
	metricsState, err := encodeHeartbeatMetrics(&metrics, time.Now().UTC())
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
			UPDATE agents SET last_seen=now(), metrics=CASE
			  WHEN $2::jsonb ? 'network_interfaces' OR NOT (metrics ? 'network_interfaces') THEN $2::jsonb
			  ELSE $2::jsonb || jsonb_build_object('network_interfaces', metrics->'network_interfaces')
			END
			WHERE id=$1 AND revoked_at IS NULL`, id, metricsState)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAgentObservedPublicIP stores the authenticated WSS peer address in
// the existing metrics snapshot without disturbing Agent-reported counters or
// default-route interfaces. An empty value removes a stale observation so the
// client address resolver falls back to the current interface snapshot.
func (s *Store) UpdateAgentObservedPublicIP(ctx context.Context, id, address string) error {
	address = strings.TrimSpace(address)
	if address != "" {
		address = authn.NormalizePublicIP(address)
		if address == "" {
			return fmt.Errorf("%w: invalid observed public agent address", ErrInvalid)
		}
		parsed, err := netip.ParseAddr(address)
		if err != nil || netpolicy.IsCloudflareAddress(parsed) {
			address = ""
		}
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET metrics=CASE
			WHEN $2='' THEN metrics - 'observed_public_ip'
			ELSE jsonb_set(metrics, '{observed_public_ip}', to_jsonb($2::text), true)
		END
		WHERE id=$1 AND revoked_at IS NULL`, id, address)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListAgents(ctx context.Context) ([]core.Agent, error) {
	rows, err := s.pool.Query(ctx, `
			SELECT id,name,version,os,arch,capabilities,features,labels,runtime,metrics,last_seen,enrolled_at
		FROM agents WHERE revoked_at IS NULL ORDER BY enrolled_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := make([]core.Agent, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var agent core.Agent
		var capabilities, features, labels, runtimeState, metricsState []byte
		if err := rows.Scan(&agent.ID, &agent.Name, &agent.Version, &agent.OS, &agent.Arch, &capabilities, &features, &labels, &runtimeState, &metricsState, &agent.LastSeen, &agent.EnrolledAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &agent.Capabilities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(features, &agent.Features); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labels, &agent.Labels); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(runtimeState, &agent.Runtime); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metricsState, &agent.Metrics); err != nil {
			return nil, err
		}
		if agent.LastSeen.After(now.Add(-45 * time.Second)) {
			agent.Status = "online"
		} else {
			agent.Status = "offline"
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
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
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE agent_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM substore_sync_items WHERE agent_id=$1`, id); err != nil {
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

// AgentName returns the display name of an active registered agent.
func (s *Store) AgentName(ctx context.Context, id string) (string, error) {
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM agents WHERE id=$1 AND revoked_at IS NULL`, id).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) CreateConfig(ctx context.Context, input core.Config) (core.Config, error) {
	if input.AgentID != "" {
		return core.Config{}, fmt.Errorf("%w: node-owned configurations must use the agent configuration workflow", ErrInvalid)
	}
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	id, err := core.NewID("cfg")
	if err != nil {
		return core.Config{}, err
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	now := time.Now().UTC()
	config := core.Config{
		ID: id, AgentID: input.AgentID, Name: name, Description: description,
		Engine: input.Engine, Content: input.Content, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
			INSERT INTO configs (id,agent_id,name,description,engine,content,version,created_at,updated_at)
		VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$8)`,
		config.ID, config.AgentID, config.Name, config.Description, config.Engine, storedContent, config.Version, now)
	if err != nil {
		return core.Config{}, mapError(err)
	}
	if err := s.insertConfigRevision(ctx, tx, config); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return config, nil
}

func (s *Store) UpdateConfig(ctx context.Context, id string, input core.Config) (core.Config, error) {
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	if input.Version < 1 {
		return core.Config{}, fmt.Errorf("%w: configuration version is required", ErrInvalid)
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	var config core.Config
	err = tx.QueryRow(ctx, `
		UPDATE configs SET name=$2,description=$3,engine=$4,content=$5,version=version+1,updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL AND version=$6
		RETURNING id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at`,
		id, name, description, input.Engine, storedContent, input.Version).Scan(
		&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content, &config.Version, &config.CreatedAt, &config.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if existsErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM configs WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL)`, id).Scan(&exists); existsErr != nil {
				return core.Config{}, existsErr
			}
			if exists {
				return core.Config{}, fmt.Errorf("%w: configuration changed; reload before saving", ErrConflict)
			}
			return core.Config{}, ErrNotFound
		}
		return core.Config{}, mapError(err)
	}
	config.Content, err = s.decryptContent(config.Content)
	if err != nil {
		return core.Config{}, err
	}
	if err := s.insertConfigRevision(ctx, tx, config); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return config, nil
}

func (s *Store) DeleteConfig(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='' WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status='failed',error='configuration was deleted before dispatch',finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE config_id=$1 AND status='pending'`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListConfigs(ctx context.Context) ([]core.Config, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at
		FROM configs WHERE deleted_at IS NULL AND agent_id IS NULL ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]core.Config, 0)
	for rows.Next() {
		var config core.Config
		if err := rows.Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content, &config.Version, &config.CreatedAt, &config.UpdatedAt); err != nil {
			return nil, err
		}
		config.Content, err = s.decryptContent(config.Content)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (s *Store) ExistingConfigIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	existing := make(map[string]bool)
	if len(ids) == 0 {
		return existing, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM configs WHERE deleted_at IS NULL AND id=ANY($1::text[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

func (s *Store) CreateTask(ctx context.Context, request core.TaskRequest) (core.Task, error) {
	if !request.Action.Valid() {
		return core.Task{}, fmt.Errorf("%w: unsupported action %q", ErrInvalid, request.Action)
	}
	if request.Action == core.ActionUpgradeAgent {
		if request.Engine != "" || request.ConfigID != "" || request.CoreVersion != "" {
			return core.Task{}, fmt.Errorf("%w: agent upgrade tasks cannot reference an engine, configuration, or core version", ErrInvalid)
		}
	} else if !request.Engine.Valid() {
		return core.Task{}, fmt.Errorf("%w: unsupported engine %q", ErrInvalid, request.Engine)
	}
	if request.Action == core.ActionInstall {
		normalizedVersion, versionErr := core.NormalizeCoreVersionSelector(request.CoreVersion)
		if versionErr != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, versionErr)
		}
		request.CoreVersion = normalizedVersion
		source, sourceErr := core.NormalizeCoreSource(request.Engine, normalizedVersion, request.CoreSource)
		if sourceErr != nil {
			return core.Task{}, fmt.Errorf("%w: %v", ErrInvalid, sourceErr)
		}
		request.CoreSource = source
		if request.ConfigID != "" {
			return core.Task{}, fmt.Errorf("%w: install tasks cannot reference a configuration", ErrInvalid)
		}
	} else {
		if request.CoreSource != "" {
			return core.Task{}, fmt.Errorf("%w: core source is only applicable to Mihomo development installs", ErrInvalid)
		}
		request.CoreSource = ""
		request.CoreVersion = ""
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Task{}, err
	}
	defer tx.Rollback(ctx)
	var capabilitiesJSON, featuresJSON, runtimeJSON []byte
	if err := tx.QueryRow(ctx, `SELECT capabilities,features,runtime FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, request.AgentID).Scan(&capabilitiesJSON, &featuresJSON, &runtimeJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Task{}, fmt.Errorf("agent: %w", ErrNotFound)
		}
		return core.Task{}, err
	}
	var capabilities []core.Engine
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		return core.Task{}, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return core.Task{}, err
	}
	var runtime map[core.Engine]core.RuntimeState
	if err := json.Unmarshal(runtimeJSON, &runtime); err != nil {
		return core.Task{}, err
	}
	if request.Action == core.ActionUpgradeAgent && !containsFeature(features, core.AgentFeatureSelfUpgrade) {
		return core.Task{}, fmt.Errorf("%w: this Agent does not support remote upgrades; run the current one-click installation once", ErrConflict)
	}
	if request.Action != core.ActionUpgradeAgent && !containsEngine(capabilities, request.Engine) {
		return core.Task{}, fmt.Errorf("%w: agent does not advertise the requested engine", ErrInvalid)
	}
	if request.Action != core.ActionUpgradeAgent {
		if reason := strings.TrimSpace(runtime[request.Engine].ExistingConfigUnsupportedReason); reason != "" {
			return core.Task{}, fmt.Errorf("%w: %s core tasks are disabled because an existing service could not be mapped safely: %s", ErrConflict, request.Engine, reason)
		}
	}
	if request.Action == core.ActionReadManagedConfig && !containsFeature(features, core.AgentFeatureManagedConfigRead) {
		return core.Task{}, fmt.Errorf("%w: this Agent cannot read the managed configuration independently; upgrade the Agent through the panel first", ErrConflict)
	}
	if request.Action == core.ActionInstall && request.Engine == core.EngineMihomo &&
		request.CoreVersion == core.CoreVersionDevelopment &&
		request.CoreSource == string(core.CoreSourceMirror) &&
		!containsFeature(features, core.AgentFeatureMihomoDevelopmentSource) {
		return core.Task{}, fmt.Errorf("%w: this Agent does not support the Mihomo Alpha mirror source; upgrade the Agent through the panel first", ErrConflict)
	}

	task := core.Task{
		AgentID: request.AgentID, Action: request.Action, Engine: request.Engine,
		ConfigID: request.ConfigID, CoreVersion: request.CoreVersion, CoreSource: request.CoreSource,
		Status: core.TaskPending, CreatedAt: time.Now().UTC(),
	}
	if request.Action == core.ActionDeploy || request.Action == core.ActionValidate || request.Action == core.ActionImportExisting {
		var configEngine core.Engine
		var configAgentID string
		err := tx.QueryRow(ctx, `SELECT engine,content,version,COALESCE(agent_id,'') FROM configs WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, request.ConfigID).Scan(&configEngine, &task.ConfigContent, &task.ConfigVersion, &configAgentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Task{}, fmt.Errorf("configuration: %w", ErrNotFound)
		}
		if err != nil {
			return core.Task{}, err
		}
		if configEngine != request.Engine {
			return core.Task{}, fmt.Errorf("%w: task engine does not match configuration engine", ErrInvalid)
		}
		if request.Action == core.ActionImportExisting && configAgentID != request.AgentID {
			return core.Task{}, fmt.Errorf("%w: existing service migration requires this agent's saved snapshot", ErrInvalid)
		}
		if configAgentID != "" && configAgentID != request.AgentID {
			return core.Task{}, fmt.Errorf("%w: node-owned configuration cannot be deployed to another agent", ErrInvalid)
		}
		task.ConfigContent, err = s.decryptContent(task.ConfigContent)
		if err != nil {
			return core.Task{}, err
		}
	} else {
		task.ConfigID = ""
	}
	existing, existingErr := scanTask(tx.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at
		FROM tasks
		WHERE agent_id=$1 AND action=$2 AND engine=$3
		  AND COALESCE(config_id,'')=$4 AND COALESCE(config_version,0)=$5 AND COALESCE(core_version,'')=$6
		  AND (CASE WHEN $2='install' AND $3='mihomo' AND $6='development' AND COALESCE($7,'') IN ('','official')
		            THEN 'official' ELSE COALESCE($7,'') END)
		    = (CASE WHEN action='install' AND engine='mihomo' AND core_version='development' AND COALESCE(core_source,'') IN ('','official')
		            THEN 'official' ELSE COALESCE(core_source,'') END)
		  AND status IN ('pending','running')
		ORDER BY created_at DESC LIMIT 1`,
		task.AgentID, task.Action, task.Engine, task.ConfigID, task.ConfigVersion, task.CoreVersion, task.CoreSource), false)
	if existingErr == nil {
		if err := tx.Commit(ctx); err != nil {
			return core.Task{}, err
		}
		existing.Reused = true
		return existing, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return core.Task{}, existingErr
	}
	task.ID, err = core.NewID("tsk")
	if err != nil {
		return core.Task{}, err
	}
	storedConfigContent, err := s.encryptContent(task.ConfigContent)
	if err != nil {
		return core.Task{}, err
	}
	_, err = tx.Exec(ctx, `
			INSERT INTO tasks (id,agent_id,action,engine,config_id,config_version,config_content,core_version,core_source,status,attempt,created_at)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,0),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),$10,0,$11)`,
		task.ID, task.AgentID, task.Action, task.Engine, task.ConfigID, task.ConfigVersion, storedConfigContent, task.CoreVersion, task.CoreSource, task.Status, task.CreatedAt)
	if err != nil {
		return core.Task{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Task{}, err
	}
	s.signalTaskReady(task.AgentID)
	return task, nil
}

// TaskReady returns a coalescing signal for newly created tasks assigned to an agent.
func (s *Store) TaskReady(agentID string) <-chan struct{} {
	return s.taskReadyChannel(agentID)
}

func (s *Store) taskReadyChannel(agentID string) chan struct{} {
	s.taskWakeMu.Lock()
	defer s.taskWakeMu.Unlock()
	if s.taskWakes == nil {
		s.taskWakes = make(map[string]chan struct{})
	}
	wake := s.taskWakes[agentID]
	if wake == nil {
		wake = make(chan struct{}, 1)
		s.taskWakes[agentID] = wake
	}
	return wake
}

func (s *Store) signalTaskReady(agentID string) {
	wake := s.taskReadyChannel(agentID)
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *Store) ListTasks(ctx context.Context, agentID string, limit int) ([]core.Task, error) {
	return s.ListTasksFiltered(ctx, agentID, "", "", limit)
}

func (s *Store) ListTasksFiltered(ctx context.Context, agentID string, status core.TaskStatus, action core.Action, limit int) ([]core.Task, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at
		FROM tasks
		WHERE ($1='' OR agent_id=$1) AND ($2='' OR status=$2) AND ($3='' OR action=$3)
		ORDER BY created_at DESC LIMIT $4`, agentID, status, action, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]core.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows, false)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, id string) (core.Task, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at
		FROM tasks WHERE id=$1`, id)
	task, err := scanTask(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, ErrNotFound
	}
	return task, err
}

func (s *Store) CancelTask(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status='canceled',error='canceled by administrator',finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return fmt.Errorf("%w: only pending tasks can be canceled", ErrConflict)
}

func (s *Store) RetryTask(ctx context.Context, id string) (core.Task, error) {
	previous, err := s.GetTask(ctx, id)
	if err != nil {
		return core.Task{}, err
	}
	if previous.Status != core.TaskFailed && previous.Status != core.TaskCanceled {
		return core.Task{}, fmt.Errorf("%w: only failed or canceled tasks can be retried", ErrConflict)
	}
	return s.CreateTask(ctx, core.TaskRequest{
		AgentID: previous.AgentID, Action: previous.Action, Engine: previous.Engine,
		ConfigID: previous.ConfigID, CoreVersion: previous.CoreVersion, CoreSource: previous.CoreSource,
	})
}

// RunningTask returns the task lease currently owned by an agent. A reconnecting
// Agent can resume result delivery without waiting for the stale-lease janitor.
// If the Agent no longer advertises the protocol required by the running task
// (for example a Mihomo development mirror install after the Agent was
// downgraded), the task is failed atomically instead of being delivered to an
// Agent that would silently fall back to the official repository.
func (s *Store) RunningTask(ctx context.Context, agentID string) (*core.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var featuresJSON []byte
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&featuresJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),
		       COALESCE(config_content,''),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,COALESCE(lease_id,''),
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at
		FROM tasks WHERE agent_id=$1 AND status='running'
		ORDER BY started_at DESC LIMIT 1`, agentID)
	task, err := scanTask(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, commitErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if isMihomoMirrorTask(task) && !containsFeature(features, core.AgentFeatureMihomoDevelopmentSource) {
		if _, updateErr := tx.Exec(ctx, `
			UPDATE tasks SET status='failed', error=$2, finished_at=now(), config_content=NULL, lease_id=NULL
			WHERE id=$1 AND status='running'`, task.ID,
			"Agent no longer advertises mihomo-development-source-v1; the mirror development task cannot be safely resumed and it is unknown whether the previous Agent executed it before the connection was lost"); updateErr != nil {
			return nil, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, commitErr
		}
		return nil, nil
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, commitErr
	}
	if task.ConfigContent != "" {
		task.ConfigContent, err = s.decryptContent(task.ConfigContent)
		if err != nil {
			return nil, err
		}
	}
	return &task, nil
}

func (s *Store) ClaimTask(ctx context.Context, agentID string) (*core.Task, error) {
	leaseID, err := core.NewToken()
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var featuresJSON []byte
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&featuresJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return nil, err
	}
	mirrorSupported := containsFeature(features, core.AgentFeatureMihomoDevelopmentSource)
	row := tx.QueryRow(ctx, `
		WITH next_task AS (
			SELECT t.id FROM tasks t
			WHERE t.agent_id=$1 AND t.status='pending'
			  AND NOT EXISTS (SELECT 1 FROM tasks running WHERE running.agent_id=$1 AND running.status='running')
			  AND ($3::boolean OR NOT (t.action='install' AND t.engine='mihomo' AND t.core_version='development' AND COALESCE(t.core_source,'')='mirror'))
			ORDER BY t.created_at ASC FOR UPDATE OF t SKIP LOCKED LIMIT 1
		)
		UPDATE tasks t SET status='running',started_at=now(),attempt=attempt+1,lease_id=$2
		FROM next_task n WHERE t.id=n.id
		RETURNING t.id,t.agent_id,t.action,t.engine,COALESCE(t.config_id,''),COALESCE(t.config_version,0),
		          COALESCE(t.config_content,''),COALESCE(t.core_version,''),COALESCE(t.core_source,''),t.status,t.attempt,COALESCE(t.lease_id,''),COALESCE(t.output,''),COALESCE(t.error,''),
		          t.created_at,t.started_at,t.finished_at`, agentID, leaseID, mirrorSupported)
	task, err := scanTask(row, true)
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, commitErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if task.ConfigContent != "" {
		task.ConfigContent, err = s.decryptContent(task.ConfigContent)
		if err != nil {
			return nil, err
		}
	}
	return &task, nil
}

// isMihomoMirrorTask reports whether a task is the explicit third-party
// vernesong/mihomo mirror install that requires mihomo-development-source-v1.
func isMihomoMirrorTask(task core.Task) bool {
	return task.Action == core.ActionInstall &&
		task.Engine == core.EngineMihomo &&
		task.CoreVersion == core.CoreVersionDevelopment &&
		task.CoreSource == string(core.CoreSourceMirror)
}

func (s *Store) CompleteTask(ctx context.Context, agentID, taskID string, result core.TaskResultRequest) error {
	if len(result.LeaseID) < 32 {
		return fmt.Errorf("%w: invalid task lease", ErrConflict)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var action core.Action
	var engine core.Engine
	if err := tx.QueryRow(ctx, `
		SELECT action,engine FROM tasks
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'
		FOR UPDATE`, taskID, agentID, result.LeaseID).Scan(&action, &engine); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND agent_id=$2)`, taskID, agentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return fmt.Errorf("%w: task is not running", ErrConflict)
	}
	status := core.TaskFailed
	if result.Success {
		status = core.TaskSucceeded
	}
	storedContent := ""
	storedOutput := truncate(result.Output, 64<<10)
	storedError := truncate(result.Error, 8<<10)
	if (action == core.ActionReadConfig || action == core.ActionReadManagedConfig) && result.Success {
		content := result.Output
		if !utf8.ValidString(content) {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned a current configuration that is not valid UTF-8"
		} else if len(content) > core.MaxConfigBytes {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned a current configuration larger than the supported limit"
		} else if validationErr := core.ValidateConfig(engine, content); validationErr != nil {
			status = core.TaskFailed
			storedOutput = ""
			storedError = "agent returned an invalid current configuration: " + validationErr.Error()
		} else {
			storedContent = content
			storedOutput = "current configuration read and validated"
			storedError = ""
		}
	}
	storedContent, err = s.encryptContent(storedContent)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status=$4,output=$5,error=$6,finished_at=now(),config_content=NULLIF($7,''),lease_id=NULL
		WHERE id=$1 AND agent_id=$2 AND lease_id=$3 AND status='running'`,
		taskID, agentID, result.LeaseID, status, storedOutput, storedError, storedContent)
	if err != nil {
		return err
	}
	if (action == core.ActionReadConfig || action == core.ActionReadManagedConfig) && status == core.TaskSucceeded {
		if _, err := tx.Exec(ctx, `
			UPDATE tasks SET config_content=NULL
			WHERE agent_id=$1 AND engine=$2 AND action=$3 AND id<>$4 AND config_content IS NOT NULL`,
			agentID, engine, action, taskID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ReadTaskConfigSnapshot(ctx context.Context, taskID, agentID string, engine core.Engine) (string, error) {
	var content string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(config_content,'') FROM tasks
		WHERE id=$1 AND agent_id=$2 AND engine=$3 AND action IN ($4,$5) AND status='succeeded'`,
		taskID, agentID, engine, core.ActionReadConfig, core.ActionReadManagedConfig).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && content == "") {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.decryptContent(content)
}

func (s *Store) RecentReadTask(ctx context.Context, agentID string, engine core.Engine, maxAge time.Duration) (core.Task, error) {
	if maxAge <= 0 {
		return core.Task{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at
		FROM tasks
		WHERE agent_id=$1 AND engine=$2 AND action=$3 AND status='succeeded'
		  AND config_content IS NOT NULL AND finished_at > now()-$4::interval
		ORDER BY finished_at DESC LIMIT 1`, agentID, engine, core.ActionReadConfig, intervalString(maxAge))
	task, err := scanTask(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, ErrNotFound
	}
	return task, err
}

func (s *Store) RequeueStaleTasks(ctx context.Context, age, installAge time.Duration, maxAttempts int) error {
	if installAge < age {
		installAge = age
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET
			status=CASE WHEN attempt >= $3 THEN 'failed' ELSE 'pending' END,
			error=CASE WHEN attempt >= $3 THEN 'agent did not report a result before the execution lease expired' ELSE error END,
			finished_at=CASE WHEN attempt >= $3 THEN now() ELSE NULL END,
			started_at=CASE WHEN attempt >= $3 THEN started_at ELSE NULL END,
			config_content=CASE WHEN attempt >= $3 THEN NULL ELSE config_content END,
			lease_id=NULL
		WHERE status='running' AND started_at < now() - CASE WHEN action='install' THEN $2::interval ELSE $1::interval END`,
		intervalString(age), intervalString(installAge), maxAttempts)
	return err
}

func (s *Store) Overview(ctx context.Context) (core.Overview, error) {
	var result core.Overview
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM agents WHERE revoked_at IS NULL),
			(SELECT count(*) FROM agents WHERE revoked_at IS NULL AND last_seen > now()-interval '45 seconds'),
			(SELECT count(*) FROM configs WHERE deleted_at IS NULL AND agent_id IS NULL),
			(SELECT count(*) FROM configs WHERE deleted_at IS NULL AND agent_id IS NOT NULL),
			(SELECT count(*) FROM tasks WHERE status IN ('pending','running')),
			(SELECT count(*) FROM tasks WHERE status='pending'),
			(SELECT count(*) FROM tasks WHERE status='running'),
			(SELECT count(*) FROM tasks WHERE status='failed')`).Scan(
		&result.Agents, &result.AgentsOnline, &result.Configs, &result.NodeConfigs,
		&result.TasksPending, &result.TasksQueued, &result.TasksRunning, &result.TasksFailed)
	return result, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner, includeContent bool) (core.Task, error) {
	var task core.Task
	var err error
	if includeContent {
		err = row.Scan(&task.ID, &task.AgentID, &task.Action, &task.Engine, &task.ConfigID, &task.ConfigVersion,
			&task.ConfigContent, &task.CoreVersion, &task.CoreSource, &task.Status, &task.Attempt, &task.LeaseID, &task.Output, &task.Error,
			&task.CreatedAt, &task.StartedAt, &task.FinishedAt)
	} else {
		err = row.Scan(&task.ID, &task.AgentID, &task.Action, &task.Engine, &task.ConfigID, &task.ConfigVersion,
			&task.CoreVersion, &task.CoreSource, &task.Status, &task.Attempt, &task.Output, &task.Error,
			&task.CreatedAt, &task.StartedAt, &task.FinishedAt)
	}
	return task, err
}

func cloneLabels(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func validateConfigMetadata(rawName, rawDescription string) (string, string, error) {
	name := strings.TrimSpace(rawName)
	description := strings.TrimSpace(rawDescription)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", "", fmt.Errorf("%w: configuration name is required and must not exceed 100 characters", ErrInvalid)
	}
	if utf8.RuneCountInString(description) > 300 {
		return "", "", fmt.Errorf("%w: configuration description exceeds 300 characters", ErrInvalid)
	}
	return name, description, nil
}

func containsEngine(values []core.Engine, expected core.Engine) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsFeature(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: duplicate value", ErrConflict)
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

func intervalString(duration time.Duration) string {
	seconds := int64(duration.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func truncate(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "\n… output truncated by QControlHub"
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
    id text PRIMARY KEY,
    name varchar(100) NOT NULL,
    version varchar(100) NOT NULL DEFAULT '',
    os varchar(50) NOT NULL,
    arch varchar(50) NOT NULL,
    capabilities jsonb NOT NULL,
	features jsonb NOT NULL DEFAULT '[]'::jsonb,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb,
	    runtime jsonb NOT NULL DEFAULT '{}'::jsonb,
	    metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
    last_seen timestamptz NOT NULL,
    enrolled_at timestamptz NOT NULL,
    revoked_at timestamptz
	);

	ALTER TABLE agents ADD COLUMN IF NOT EXISTS metrics jsonb NOT NULL DEFAULT '{}'::jsonb;
	ALTER TABLE agents ADD COLUMN IF NOT EXISTS features jsonb NOT NULL DEFAULT '[]'::jsonb;

CREATE TABLE IF NOT EXISTS core_log_batches (
    id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    received_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS core_logs (
    id bigserial PRIMARY KEY,
    batch_id text NOT NULL REFERENCES core_log_batches(id) ON DELETE CASCADE,
    entry_index smallint NOT NULL CHECK (entry_index >= 0),
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
    level varchar(10) NOT NULL CHECK (level IN ('debug','info','warning','error','critical')),
    message text NOT NULL CHECK (octet_length(message) BETWEEN 1 AND 4096),
    logged_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    UNIQUE (batch_id,entry_index)
);

CREATE INDEX IF NOT EXISTS core_logs_agent_recent_idx ON core_logs(agent_id,id DESC);
CREATE INDEX IF NOT EXISTS core_logs_engine_recent_idx ON core_logs(engine,id DESC);
CREATE INDEX IF NOT EXISTS core_logs_received_idx ON core_logs(received_at);
CREATE INDEX IF NOT EXISTS core_log_batches_received_idx ON core_log_batches(received_at);

CREATE TABLE IF NOT EXISTS configs (
    id text PRIMARY KEY,
    agent_id text REFERENCES agents(id),
    name varchar(100) NOT NULL,
    description varchar(300) NOT NULL DEFAULT '',
    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
    content text NOT NULL CHECK (octet_length(content) <= 4194304),
    version integer NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz
);
ALTER TABLE configs DROP CONSTRAINT IF EXISTS configs_content_check;
ALTER TABLE configs ADD CONSTRAINT configs_content_check CHECK (octet_length(content) <= 4194304);

	ALTER TABLE configs ADD COLUMN IF NOT EXISTS agent_id text REFERENCES agents(id);

	CREATE TABLE IF NOT EXISTS config_revisions (
	    config_id text NOT NULL REFERENCES configs(id),
	    version integer NOT NULL CHECK (version > 0),
	    agent_id text REFERENCES agents(id),
	    name varchar(100) NOT NULL,
	    description varchar(300) NOT NULL DEFAULT '',
	    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
	    content text NOT NULL CHECK (octet_length(content) <= 4194304),
	    created_at timestamptz NOT NULL,
	    PRIMARY KEY (config_id,version)
	);
	ALTER TABLE config_revisions DROP CONSTRAINT IF EXISTS config_revisions_content_check;
	ALTER TABLE config_revisions ADD CONSTRAINT config_revisions_content_check CHECK (octet_length(content) <= 4194304);

	INSERT INTO config_revisions (config_id,version,agent_id,name,description,engine,content,created_at)
	SELECT id,version,agent_id,name,description,engine,content,updated_at FROM configs
	WHERE deleted_at IS NULL
	ON CONFLICT (config_id,version) DO NOTHING;

CREATE TABLE IF NOT EXISTS tasks (
    id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES agents(id),
    action varchar(20) NOT NULL CHECK (action IN ('validate','deploy','import-existing','read-config','read-managed-config','start','stop','restart','status','install','upgrade-agent')),
	    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust') OR (action='upgrade-agent' AND engine='')),
    config_id text REFERENCES configs(id),
    config_version integer,
	    config_content text,
	    core_version varchar(64),
	    core_source varchar(32),
	    status varchar(20) NOT NULL CHECK (status IN ('pending','running','succeeded','failed','canceled')),
	    attempt integer NOT NULL DEFAULT 0,
    output text,
    error text,
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz
);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS lease_id text;
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS core_version varchar(64);
	ALTER TABLE tasks ADD COLUMN IF NOT EXISTS core_source varchar(32);
	DROP INDEX IF EXISTS tasks_latest_deployment_idx;
	ALTER TABLE tasks DROP COLUMN IF EXISTS simulated;
	ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_action_check;
	ALTER TABLE tasks ADD CONSTRAINT tasks_action_check CHECK (action IN ('validate','deploy','import-existing','read-config','read-managed-config','start','stop','restart','status','install','upgrade-agent'));
	ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
	ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('pending','running','succeeded','failed','canceled'));
	ALTER TABLE configs DROP CONSTRAINT IF EXISTS configs_engine_check;
	ALTER TABLE configs ADD CONSTRAINT configs_engine_check CHECK (engine IN ('mihomo','xray','sing-box','ss-rust'));
	ALTER TABLE config_revisions DROP CONSTRAINT IF EXISTS config_revisions_engine_check;
	ALTER TABLE config_revisions ADD CONSTRAINT config_revisions_engine_check CHECK (engine IN ('mihomo','xray','sing-box','ss-rust'));
	ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_engine_check;
	ALTER TABLE tasks ADD CONSTRAINT tasks_engine_check CHECK (engine IN ('mihomo','xray','sing-box','ss-rust') OR (action='upgrade-agent' AND engine=''));

CREATE TABLE IF NOT EXISTS enrollment_tokens (
    id text PRIMARY KEY,
    name varchar(100) NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
	    token_ciphertext text,
	    expires_at timestamptz,
	    max_uses integer NOT NULL CHECK (max_uses BETWEEN 0 AND 50),
	    used_count integer NOT NULL DEFAULT 0 CHECK (used_count >= 0),
	    reusable boolean NOT NULL DEFAULT false,
	    created_at timestamptz NOT NULL,
	    revoked_at timestamptz
);

ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS reusable boolean NOT NULL DEFAULT false;
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS token_ciphertext text;
ALTER TABLE enrollment_tokens ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE enrollment_tokens DROP CONSTRAINT IF EXISTS enrollment_tokens_max_uses_check;
ALTER TABLE enrollment_tokens ADD CONSTRAINT enrollment_tokens_max_uses_check CHECK (max_uses BETWEEN 0 AND 50);
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS agent_id text;
DROP INDEX IF EXISTS enrollment_tokens_reusable_name_unique_idx;

ALTER TABLE agents ADD COLUMN IF NOT EXISTS enrollment_id text;
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_enrollment_id_fkey;
ALTER TABLE agents ADD CONSTRAINT agents_enrollment_id_fkey FOREIGN KEY (enrollment_id) REFERENCES enrollment_tokens(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS agents_enrollment_id_unique_idx ON agents(enrollment_id) WHERE enrollment_id IS NOT NULL;

UPDATE enrollment_tokens AS token
SET agent_id=agent.id
FROM agents AS agent
WHERE agent.enrollment_id=token.id AND token.agent_id IS NULL;
ALTER TABLE enrollment_tokens DROP CONSTRAINT IF EXISTS enrollment_tokens_agent_id_fkey;
ALTER TABLE enrollment_tokens ADD CONSTRAINT enrollment_tokens_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS enrollment_tokens_agent_id_idx ON enrollment_tokens(agent_id,created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS enrollment_tokens_reusable_unbound_name_unique_idx ON enrollment_tokens(lower(name)) WHERE reusable AND agent_id IS NULL;

CREATE TABLE IF NOT EXISTS agent_nonces (
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    nonce varchar(100) NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (agent_id, nonce)
);

CREATE TABLE IF NOT EXISTS panel_settings (
    id smallint PRIMARY KEY CHECK (id = 1),
    panel_name varchar(40) NOT NULL,
    panel_description varchar(120) NOT NULL DEFAULT '',
    task_page_size integer NOT NULL CHECK (task_page_size IN (50,100,500)),
    task_poll_interval_ms integer NOT NULL CHECK (task_poll_interval_ms IN (600,1000,2000,5000)),
	core_log_minimum_level varchar(10) NOT NULL DEFAULT 'debug' CHECK (core_log_minimum_level IN ('debug','info','warning','error','critical','off')),
    webhook_url varchar(500) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL
);
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS webhook_url varchar(500) NOT NULL DEFAULT '';
ALTER TABLE panel_settings ADD COLUMN IF NOT EXISTS core_log_minimum_level varchar(10) NOT NULL DEFAULT 'debug';
ALTER TABLE panel_settings DROP CONSTRAINT IF EXISTS panel_settings_core_log_minimum_level_check;
ALTER TABLE panel_settings ADD CONSTRAINT panel_settings_core_log_minimum_level_check CHECK (core_log_minimum_level IN ('debug','info','warning','error','critical','off'));
ALTER TABLE panel_settings DROP COLUMN IF EXISTS enrollment_ttl_minutes;

CREATE TABLE IF NOT EXISTS panel_users (
    id text PRIMARY KEY,
    username varchar(64) NOT NULL,
    display_name varchar(100) NOT NULL DEFAULT '',
    role varchar(20) NOT NULL CHECK (role IN ('admin','user')),
    permissions jsonb NOT NULL DEFAULT '[]'::jsonb,
    password_hash varchar(100) NOT NULL,
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    last_login_at timestamptz
);
ALTER TABLE panel_users DROP CONSTRAINT IF EXISTS panel_users_role_check;
ALTER TABLE panel_users ADD COLUMN IF NOT EXISTS permissions jsonb NOT NULL DEFAULT '[]'::jsonb;
UPDATE panel_users SET permissions = CASE role
    WHEN 'admin' THEN '[]'::jsonb
    WHEN 'operator' THEN '["overview.read","agents.read","deployments.read","client-access.read","catalogs.read","agent-config.read","agent-config.write","configs.read","configs.write","tasks.read","tasks.execute","settings.read","audit.read","metrics.read","core-logs.read","templates.read","templates.write"]'::jsonb
    WHEN 'auditor' THEN '["overview.read","agents.read","deployments.read","tasks.read","audit.read","metrics.read","core-logs.read"]'::jsonb
    WHEN 'readonly' THEN '["overview.read","agents.read","deployments.read","client-access.read","catalogs.read","agent-config.read","configs.read","tasks.read","settings.read","audit.read","metrics.read","core-logs.read","templates.read"]'::jsonb
    ELSE permissions END
    WHERE role IN ('operator','auditor','readonly');
UPDATE panel_users SET role='user' WHERE role IN ('operator','auditor','readonly');
ALTER TABLE panel_users ADD CONSTRAINT panel_users_role_check CHECK (role IN ('admin','user'));
CREATE UNIQUE INDEX IF NOT EXISTS panel_users_username_unique_idx ON panel_users(lower(username));
CREATE INDEX IF NOT EXISTS panel_users_status_idx ON panel_users(disabled,username);

INSERT INTO panel_settings (
    id,panel_name,panel_description,task_page_size,task_poll_interval_ms,updated_at
) VALUES (1,'QControlHub','可信远程编排',100,600,now())
ON CONFLICT (id) DO NOTHING;

CREATE INDEX IF NOT EXISTS agents_active_seen_idx ON agents(last_seen DESC) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS agents_public_key_unique_idx ON agents(public_key);
	CREATE INDEX IF NOT EXISTS configs_active_updated_idx ON configs(updated_at DESC) WHERE deleted_at IS NULL;
	CREATE UNIQUE INDEX IF NOT EXISTS configs_agent_engine_unique_idx ON configs(agent_id,engine) WHERE agent_id IS NOT NULL AND deleted_at IS NULL;
	CREATE INDEX IF NOT EXISTS config_revisions_recent_idx ON config_revisions(config_id,version DESC);
CREATE INDEX IF NOT EXISTS tasks_agent_queue_idx ON tasks(agent_id, status, created_at);
CREATE INDEX IF NOT EXISTS tasks_created_idx ON tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS tasks_latest_deployment_idx ON tasks(agent_id,engine,finished_at DESC) WHERE action IN ('deploy','import-existing') AND status='succeeded';
CREATE UNIQUE INDEX IF NOT EXISTS tasks_one_running_per_agent_idx ON tasks(agent_id) WHERE status='running';
CREATE TABLE IF NOT EXISTS metric_samples (
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    sampled_at timestamptz NOT NULL,
    cpu_percent real NOT NULL,
    memory_percent real NOT NULL DEFAULT 0,
    rx_rate_bps bigint NOT NULL DEFAULT 0 CHECK (rx_rate_bps >= 0),
    tx_rate_bps bigint NOT NULL DEFAULT 0 CHECK (tx_rate_bps >= 0),
    PRIMARY KEY (agent_id, sampled_at)
);
CREATE INDEX IF NOT EXISTS metric_samples_recent_idx ON metric_samples(agent_id, sampled_at DESC);

CREATE TABLE IF NOT EXISTS port_traffic_policies (
    id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL,
    engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    protocol varchar(8) NOT NULL CHECK (protocol IN ('tcp','udp','both')),
    cycle varchar(8) NOT NULL CHECK (cycle IN ('monthly','yearly')),
    cycle_anchor date NOT NULL,
	limit_bytes bigint NOT NULL CHECK (limit_bytes > 0),
	auto_block boolean NOT NULL DEFAULT true,
	quota_enabled boolean NOT NULL DEFAULT true,
	monitoring_enabled boolean NOT NULL DEFAULT true,
	discovered boolean NOT NULL DEFAULT false,
	traffic_history_initialized boolean NOT NULL DEFAULT false,
    reset_generation bigint NOT NULL DEFAULT 1 CHECK (reset_generation > 0),
    received_bytes bigint NOT NULL DEFAULT 0 CHECK (received_bytes >= 0),
    sent_bytes bigint NOT NULL DEFAULT 0 CHECK (sent_bytes >= 0),
	reported_received_bytes bigint NOT NULL DEFAULT 0 CHECK (reported_received_bytes >= 0),
	reported_sent_bytes bigint NOT NULL DEFAULT 0 CHECK (reported_sent_bytes >= 0),
    used_bytes bigint NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    receive_bps bigint NOT NULL DEFAULT 0 CHECK (receive_bps >= 0),
    send_bps bigint NOT NULL DEFAULT 0 CHECK (send_bps >= 0),
    period_start timestamptz,
    period_end timestamptz,
    blocked boolean NOT NULL DEFAULT false,
    enforcement_available boolean NOT NULL DEFAULT false,
    enforcement_error varchar(500) NOT NULL DEFAULT '',
    last_reported_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (agent_id,port)
);
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS auto_block boolean NOT NULL DEFAULT true;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS quota_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS monitoring_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS discovered boolean NOT NULL DEFAULT false;
ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS traffic_history_initialized boolean NOT NULL DEFAULT false;
DO $traffic_baselines$
DECLARE
    add_reported_received boolean;
    add_reported_sent boolean;
BEGIN
    SELECT NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema=current_schema() AND table_name='port_traffic_policies' AND column_name='reported_received_bytes'
    ) INTO add_reported_received;
    SELECT NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema=current_schema() AND table_name='port_traffic_policies' AND column_name='reported_sent_bytes'
    ) INTO add_reported_sent;

    ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS reported_received_bytes bigint NOT NULL DEFAULT 0;
    ALTER TABLE port_traffic_policies ADD COLUMN IF NOT EXISTS reported_sent_bytes bigint NOT NULL DEFAULT 0;
    IF add_reported_received THEN
        UPDATE port_traffic_policies SET reported_received_bytes=received_bytes;
    END IF;
    IF add_reported_sent THEN
        UPDATE port_traffic_policies SET reported_sent_bytes=sent_bytes;
    END IF;
END
$traffic_baselines$;
ALTER TABLE port_traffic_policies DROP CONSTRAINT IF EXISTS port_traffic_policies_reported_received_bytes_check;
ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_reported_received_bytes_check CHECK (reported_received_bytes >= 0);
ALTER TABLE port_traffic_policies DROP CONSTRAINT IF EXISTS port_traffic_policies_reported_sent_bytes_check;
ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_reported_sent_bytes_check CHECK (reported_sent_bytes >= 0);
CREATE INDEX IF NOT EXISTS port_traffic_policies_agent_idx ON port_traffic_policies(agent_id,port);

CREATE TABLE IF NOT EXISTS port_traffic_daily_usage (
	policy_id text NOT NULL,
	reset_generation bigint NOT NULL CHECK (reset_generation > 0),
	usage_date date NOT NULL,
	agent_id text NOT NULL,
	name varchar(100) NOT NULL,
	engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
	port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
	protocol varchar(8) NOT NULL CHECK (protocol IN ('tcp','udp','both')),
	received_bytes bigint NOT NULL DEFAULT 0 CHECK (received_bytes >= 0),
	sent_bytes bigint NOT NULL DEFAULT 0 CHECK (sent_bytes >= 0),
	used_bytes bigint NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
	peak_receive_bps bigint NOT NULL DEFAULT 0 CHECK (peak_receive_bps >= 0),
	peak_send_bps bigint NOT NULL DEFAULT 0 CHECK (peak_send_bps >= 0),
	sample_count bigint NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
	first_reported_at timestamptz NOT NULL,
	last_reported_at timestamptz NOT NULL,
	PRIMARY KEY (policy_id,reset_generation,usage_date)
);
CREATE INDEX IF NOT EXISTS port_traffic_daily_agent_date_idx ON port_traffic_daily_usage(agent_id,usage_date,port);
CREATE INDEX IF NOT EXISTS port_traffic_daily_policy_date_idx ON port_traffic_daily_usage(policy_id,usage_date);

CREATE TABLE IF NOT EXISTS substore_sync_settings (
	id smallint PRIMARY KEY CHECK (id = 1),
	endpoint_ciphertext text NOT NULL,
	subscription_name varchar(100) NOT NULL,
	integration_id text NOT NULL,
	last_synced_at timestamptz,
	last_sync_status varchar(10) NOT NULL DEFAULT 'never' CHECK (last_sync_status IN ('never','success','failed')),
	last_sync_error varchar(500) NOT NULL DEFAULT '',
	updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS substore_sync_targets (
	id text PRIMARY KEY,
	display_name varchar(100) NOT NULL,
	subscription_name varchar(100) NOT NULL UNIQUE,
	integration_id text NOT NULL UNIQUE,
	last_synced_at timestamptz,
	last_sync_status varchar(10) NOT NULL DEFAULT 'never' CHECK (last_sync_status IN ('never','success','failed')),
	last_sync_error varchar(500) NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL
);
ALTER TABLE substore_sync_targets ADD COLUMN IF NOT EXISTS display_name varchar(100);
UPDATE substore_sync_targets SET display_name=subscription_name WHERE display_name IS NULL;
ALTER TABLE substore_sync_targets ALTER COLUMN display_name SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS substore_sync_targets_display_name_idx ON substore_sync_targets(display_name);
INSERT INTO substore_sync_targets (
	id,display_name,subscription_name,integration_id,last_synced_at,last_sync_status,last_sync_error,created_at,updated_at
)
SELECT
	'sst_default',subscription_name,subscription_name,integration_id,last_synced_at,last_sync_status,last_sync_error,updated_at,updated_at
FROM substore_sync_settings
WHERE NOT EXISTS (SELECT 1 FROM substore_sync_targets)
ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS substore_sync_items (
	target_id text NOT NULL REFERENCES substore_sync_targets(id) ON DELETE CASCADE,
	agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
	profile_tag text NOT NULL CHECK (octet_length(profile_tag) BETWEEN 1 AND 800),
	custom_name text NOT NULL CHECK (octet_length(custom_name) BETWEEN 1 AND 400),
	address_mode varchar(8) NOT NULL DEFAULT 'auto' CHECK (address_mode IN ('auto','ipv4','ipv6','both')),
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL,
	PRIMARY KEY (target_id,agent_id,engine,profile_tag)
);
ALTER TABLE substore_sync_items ADD COLUMN IF NOT EXISTS target_id text;
ALTER TABLE substore_sync_items ADD COLUMN IF NOT EXISTS address_mode varchar(8) NOT NULL DEFAULT 'auto';
ALTER TABLE substore_sync_items DROP CONSTRAINT IF EXISTS substore_sync_items_address_mode_check;
ALTER TABLE substore_sync_items ADD CONSTRAINT substore_sync_items_address_mode_check CHECK (address_mode IN ('auto','ipv4','ipv6','both'));
UPDATE substore_sync_items
SET target_id=(SELECT id FROM substore_sync_targets ORDER BY created_at,id LIMIT 1)
WHERE target_id IS NULL;
ALTER TABLE substore_sync_items ALTER COLUMN target_id SET NOT NULL;
ALTER TABLE substore_sync_items DROP CONSTRAINT IF EXISTS substore_sync_items_pkey;
ALTER TABLE substore_sync_items ADD CONSTRAINT substore_sync_items_pkey PRIMARY KEY (target_id,agent_id,engine,profile_tag);
DO $substore_target_fk$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='substore_sync_items'::regclass AND conname='substore_sync_items_target_id_fkey'
	) THEN
		ALTER TABLE substore_sync_items ADD CONSTRAINT substore_sync_items_target_id_fkey
			FOREIGN KEY (target_id) REFERENCES substore_sync_targets(id) ON DELETE CASCADE;
	END IF;
END
$substore_target_fk$;
DROP INDEX IF EXISTS substore_sync_items_created_idx;
CREATE INDEX substore_sync_items_created_idx ON substore_sync_items(target_id,created_at,agent_id);

-- Agent deletion is intentionally a soft revocation, so foreign-key cascades
-- do not run. Remove traffic rows left by versions that did not clean them in
-- DeleteAgent; otherwise revoked nodes survive as orphan cards indefinitely.
DELETE FROM port_traffic_daily_usage
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);
DELETE FROM port_traffic_policies
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);
DELETE FROM substore_sync_items
WHERE agent_id IN (SELECT id FROM agents WHERE revoked_at IS NOT NULL);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigserial PRIMARY KEY,
    acted_at timestamptz NOT NULL DEFAULT now(),
    actor varchar(40) NOT NULL DEFAULT 'admin',
    action varchar(40) NOT NULL,
    target text NOT NULL DEFAULT '',
    detail text NOT NULL DEFAULT '',
    remote_ip varchar(64) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_logs_recent_idx ON audit_logs(acted_at DESC);CREATE TABLE IF NOT EXISTS config_templates ( id text PRIMARY KEY, name varchar(100) NOT NULL, engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')), content text NOT NULL CHECK (octet_length(content) <= 4194304), created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL ); CREATE INDEX IF NOT EXISTS config_templates_recent_idx ON config_templates(updated_at DESC);
CREATE INDEX IF NOT EXISTS enrollment_tokens_active_idx ON enrollment_tokens(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS agent_nonces_expiry_idx ON agent_nonces(expires_at);
`
