package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// TryLockSubStoreOperation serializes remote ownership/content changes with
// local target mutations across control-plane processes. A busy operation
// fails immediately instead of occupying pool connections waiting for a lock
// while the holder needs those connections to finish its API workflow.
func (s *Store) TryLockSubStoreOperation(ctx context.Context, newEndpoints ...string) (func(), error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	discard := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = connection.Hijack().Close(cleanup)
	}
	keys := []string{}
	lock := func(key string) error {
		var locked bool
		if err := connection.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema() || ':qcontrolhub:substore:' || $1,0))`, key).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return fmt.Errorf("%w: 另有 Sub-Store 操作正在执行，请稍后重试", ErrConflict)
		}
		keys = append(keys, key)
		return nil
	}
	// A session lock leaves no MVCC transaction open during remote HTTP
	// requests, so frequent traffic updates can still prune their HOT chains.
	if err := lock("owner:" + scopeForConfig(ctx).OwnerID); err != nil {
		discard() // A canceled reply may have acquired the server-side lock.
		return nil, err
	}
	settings, err := s.subStoreSyncSettings(ctx, connection)
	if err != nil {
		discard()
		return nil, err
	}
	backends := map[string]bool{}
	if settings.BackendKey != "" {
		backends[settings.BackendKey] = true
	}
	for _, endpoint := range newEndpoints {
		if key := subStoreBackendKey(endpoint); key != "" {
			backends[key] = true
		}
	}
	for key := range backends {
		if err := lock("backend:" + key); err != nil {
			discard() // Closing also releases the owner lock.
			return nil, err
		}
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := len(keys)-1; i >= 0; i-- {
				var unlocked bool
				if err := connection.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended(current_schema() || ':qcontrolhub:substore:' || $1,0))`, keys[i]).Scan(&unlocked); err != nil || !unlocked {
					discard() // Never return a possibly locked connection to the pool.
					return
				}
			}
			connection.Release()
		})
	}
	return release, nil
}

func (s *Store) SubStoreSyncSettings(ctx context.Context) (core.SubStoreSyncSettings, error) {
	return s.subStoreSyncSettings(ctx, s.pool)
}

func (s *Store) subStoreSyncSettings(ctx context.Context, executor storeExecutor) (core.SubStoreSyncSettings, error) {
	var settings core.SubStoreSyncSettings
	var endpoint string
	var err error
	if ownerID := scopeForConfig(ctx).OwnerID; ownerID != "" {
		err = executor.QueryRow(ctx, `SELECT endpoint_ciphertext,updated_at,backend_key FROM user_substore_settings WHERE owner_id=$1`,
			ownerID).Scan(&endpoint, &settings.UpdatedAt, &settings.BackendKey)
	} else {
		err = executor.QueryRow(ctx, `SELECT endpoint_ciphertext,updated_at,backend_key
			FROM substore_sync_settings WHERE id=1`).Scan(&endpoint, &settings.UpdatedAt, &settings.BackendKey)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SubStoreSyncSettings{}, nil
	}
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("read Sub-Store sync settings: %w", err)
	}
	settings.EndpointURL, err = s.decryptContent(endpoint)
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("decrypt Sub-Store endpoint: %w", err)
	}
	settings.Configured = strings.TrimSpace(settings.EndpointURL) != ""
	return settings, nil
}

func (s *Store) SaveSubStoreSyncSettings(ctx context.Context, endpointURL string) (core.SubStoreSyncSettings, error) {
	ownerID := scopeForConfig(ctx).OwnerID
	endpointURL = strings.TrimSpace(endpointURL)
	if endpointURL == "" || utf8.RuneCountInString(endpointURL) > 1000 {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: Sub-Store endpoint is required and must not exceed 1000 characters", ErrInvalid)
	}
	if s.cryptor == nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required for Sub-Store credentials", ErrSecretUnavailable)
	}
	sealed, err := s.encryptContent(endpointURL)
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: encrypt Sub-Store endpoint: %v", ErrSecretUnavailable, err)
	}
	integrationID, err := core.NewID("ssi")
	if err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	now := time.Now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	defer tx.Rollback(ctx)
	backendKey := subStoreBackendKey(endpointURL)
	if ownerID != "" {
		_, err = tx.Exec(ctx, `INSERT INTO user_substore_settings(owner_id,endpoint_ciphertext,backend_key,updated_at)
			VALUES($1,$2,$3,$4) ON CONFLICT(owner_id) DO UPDATE SET endpoint_ciphertext=EXCLUDED.endpoint_ciphertext,
				backend_key=EXCLUDED.backend_key,updated_at=EXCLUDED.updated_at`, ownerID, sealed, backendKey, now)
	} else {
		_, err = tx.Exec(ctx, `
		INSERT INTO substore_sync_settings
			(id,endpoint_ciphertext,subscription_name,integration_id,last_sync_status,last_sync_error,updated_at,backend_key)
		VALUES (1,$1,'QControlHub',$2,'never','',$3,$4)
		ON CONFLICT (id) DO UPDATE SET
			endpoint_ciphertext=EXCLUDED.endpoint_ciphertext,
			backend_key=EXCLUDED.backend_key,updated_at=EXCLUDED.updated_at`, sealed, integrationID, now, backendKey)
	}
	if err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("save Sub-Store sync settings: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE substore_sync_targets SET
			last_synced_at=NULL,last_sync_status='never',last_sync_error='',updated_at=$1,backend_key=$3
			WHERE owner_id=$2`, now, ownerID, backendKey); err != nil {
		return core.SubStoreSyncSettings{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	return s.SubStoreSyncSettings(ctx)
}

func subStoreBackendKey(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, err := url.Parse(endpoint); err == nil {
		parsed.Scheme, parsed.Host = strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Host)
		parsed.Path, parsed.RawPath = strings.TrimRight(parsed.Path, "/"), strings.TrimRight(parsed.RawPath, "/")
		endpoint = parsed.String()
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint)))
}

func (s *Store) migrateSubStoreBackends(ctx context.Context, tx pgx.Tx) error {
	var endpoint string
	err := tx.QueryRow(ctx, `SELECT endpoint_ciphertext FROM substore_sync_settings WHERE id=1`).Scan(&endpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	endpoint, err = s.decryptContent(endpoint)
	if err != nil {
		return fmt.Errorf("read legacy Sub-Store backend: %w", err)
	}
	key := subStoreBackendKey(endpoint)
	if _, err := tx.Exec(ctx, `UPDATE substore_sync_settings SET backend_key=$1 WHERE id=1`, key); err != nil {
		return err
	}
	// Preserve remote claims, but never copy the administrator's credential
	// into an ordinary account. That account must configure its own backend.
	_, err = tx.Exec(ctx, `UPDATE substore_sync_targets SET backend_key=$1 WHERE backend_key=''`, key)
	return err
}

func validateSubStoreTargetName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 || name == "." || name == ".." || strings.ContainsAny(name, "/\\?#\r\n") {
		return "", fmt.Errorf("%w: Sub-Store subscription name is required, must not exceed 100 characters, and cannot contain path characters", ErrInvalid)
	}
	return name, nil
}

// SubStoreTargetClaims returns only the identities needed to reserve groups
// on the shared backend. Callers must not expose another owner's claims.
func (s *Store) SubStoreTargetClaims(ctx context.Context) ([]core.SubStoreSyncTarget, error) {
	settings, err := s.SubStoreSyncSettings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,owner_id,subscription_name,integration_id FROM substore_sync_targets
		WHERE backend_key=$1 AND ($1<>'' OR owner_id=$2)`, settings.BackendKey, scopeForConfig(ctx).OwnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SubStoreSyncTarget, 0)
	for rows.Next() {
		var target core.SubStoreSyncTarget
		if err := rows.Scan(&target.ID, &target.OwnerID, &target.SubscriptionName, &target.IntegrationID); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) ListSubStoreSyncTargets(ctx context.Context) ([]core.SubStoreSyncTarget, error) {
	args := []any{}
	ownerWhere := workspaceOwnerClause(ctx, "target.owner_id", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT target.id,target.display_name,target.subscription_name,target.integration_id,target.sync_mode,target.last_synced_at,
		       target.last_sync_status,target.last_sync_error,
		       (SELECT count(*) FROM substore_sync_items item WHERE item.target_id=target.id),
		       target.created_at,target.updated_at,target.owner_id,target.sync_format
		FROM substore_sync_targets target WHERE true`+ownerWhere+` ORDER BY target.created_at,target.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]core.SubStoreSyncTarget, 0)
	for rows.Next() {
		var target core.SubStoreSyncTarget
		if err := rows.Scan(
			&target.ID, &target.DisplayName, &target.SubscriptionName, &target.IntegrationID, &target.SyncMode, &target.LastSyncedAt,
			&target.LastSyncStatus, &target.LastSyncError, &target.SelectionCount, &target.CreatedAt, &target.UpdatedAt, &target.OwnerID, &target.SyncFormat,
		); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) SubStoreSyncTarget(ctx context.Context, id string) (core.SubStoreSyncTarget, error) {
	var target core.SubStoreSyncTarget
	args := []any{strings.TrimSpace(id)}
	ownerWhere := workspaceOwnerClause(ctx, "target.owner_id", &args)
	err := s.pool.QueryRow(ctx, `
		SELECT target.id,target.display_name,target.subscription_name,target.integration_id,target.sync_mode,target.last_synced_at,
		       target.last_sync_status,target.last_sync_error,
		       (SELECT count(*) FROM substore_sync_items item WHERE item.target_id=target.id),
		       target.created_at,target.updated_at,target.owner_id,target.sync_format
		FROM substore_sync_targets target WHERE target.id=$1`+ownerWhere, args...).Scan(
		&target.ID, &target.DisplayName, &target.SubscriptionName, &target.IntegrationID, &target.SyncMode, &target.LastSyncedAt,
		&target.LastSyncStatus, &target.LastSyncError, &target.SelectionCount, &target.CreatedAt, &target.UpdatedAt, &target.OwnerID, &target.SyncFormat,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SubStoreSyncTarget{}, ErrNotFound
	}
	return target, err
}

func (s *Store) CreateSubStoreSyncTarget(ctx context.Context, name string, options ...string) (core.SubStoreSyncTarget, error) {
	name, err := validateSubStoreTargetName(name)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	integrationID, err := core.NewID("ssi")
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	mode := core.SubStoreSyncModeIncremental
	format := core.SubStoreSyncFormatURL
	if len(options) > 0 {
		mode = options[0]
	}
	if len(options) > 1 {
		format = options[1]
	}
	return s.createSubStoreSyncTarget(ctx, name, integrationID, mode, format)
}

func (s *Store) ImportSubStoreSyncTarget(ctx context.Context, name, integrationID string, formats ...string) (core.SubStoreSyncTarget, error) {
	name, err := validateSubStoreTargetName(name)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	integrationID = strings.TrimSpace(integrationID)
	if !strings.HasPrefix(integrationID, "ssi_") || utf8.RuneCountInString(integrationID) > 100 {
		return core.SubStoreSyncTarget{}, fmt.Errorf("%w: Sub-Store integration identity is invalid", ErrInvalid)
	}
	format := core.SubStoreSyncFormatURL
	if len(formats) > 0 {
		format = formats[0]
	}
	return s.createSubStoreSyncTarget(ctx, name, integrationID, core.SubStoreSyncModeIncremental, format)
}

func (s *Store) createSubStoreSyncTarget(ctx context.Context, name, integrationID, mode, format string) (core.SubStoreSyncTarget, error) {
	mode, valid := core.NormalizeSubStoreSyncMode(strings.TrimSpace(mode))
	if !valid {
		return core.SubStoreSyncTarget{}, fmt.Errorf("%w: Sub-Store sync mode is invalid", ErrInvalid)
	}
	format, valid = core.NormalizeSubStoreSyncFormat(strings.TrimSpace(format))
	if !valid {
		return core.SubStoreSyncTarget{}, fmt.Errorf("%w: Sub-Store sync format is invalid", ErrInvalid)
	}
	id, err := core.NewID("sst")
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	now := time.Now().UTC()
	settings, err := s.SubStoreSyncSettings(ctx)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO substore_sync_targets
			(id,display_name,subscription_name,integration_id,sync_mode,last_sync_status,last_sync_error,created_at,updated_at,owner_id,sync_format,backend_key)
		VALUES ($1,$2,$2,$3,$4,'never','',$5,$5,$6,$7,$8)`, id, name, integrationID, mode, now, scopeForConfig(ctx).OwnerID, format, settings.BackendKey); err != nil {
		return core.SubStoreSyncTarget{}, mapError(err)
	}
	return s.SubStoreSyncTarget(ctx, id)
}

func (s *Store) UpdateSubStoreSyncTarget(ctx context.Context, id, displayName, subscriptionName, mode string, formats ...string) (core.SubStoreSyncTarget, error) {
	displayName, err := validateSubStoreTargetName(displayName)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	subscriptionName, err = validateSubStoreTargetName(subscriptionName)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	mode, valid := core.NormalizeSubStoreSyncMode(strings.TrimSpace(mode))
	if !valid {
		return core.SubStoreSyncTarget{}, fmt.Errorf("%w: Sub-Store sync mode is invalid", ErrInvalid)
	}
	format := ""
	if len(formats) > 0 {
		format, valid = core.NormalizeSubStoreSyncFormat(strings.TrimSpace(formats[0]))
		if !valid {
			return core.SubStoreSyncTarget{}, fmt.Errorf("%w: Sub-Store sync format is invalid", ErrInvalid)
		}
	}
	args := []any{strings.TrimSpace(id), displayName, subscriptionName, mode, format}
	ownerWhere := workspaceOwnerClause(ctx, "owner_id", &args)
	command, err := s.pool.Exec(ctx, `
		UPDATE substore_sync_targets SET
			display_name=$2,subscription_name=$3,sync_mode=$4,sync_format=COALESCE(NULLIF($5,''),sync_format),updated_at=now()
		WHERE id=$1`+ownerWhere, args...)
	if err != nil {
		return core.SubStoreSyncTarget{}, mapError(err)
	}
	if command.RowsAffected() == 0 {
		return core.SubStoreSyncTarget{}, ErrNotFound
	}
	return s.SubStoreSyncTarget(ctx, id)
}

func (s *Store) DeleteSubStoreSyncTarget(ctx context.Context, id string) error {
	args := []any{strings.TrimSpace(id)}
	ownerWhere := workspaceOwnerClause(ctx, "owner_id", &args)
	command, err := s.pool.Exec(ctx, `DELETE FROM substore_sync_targets WHERE id=$1`+ownerWhere, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListSubStoreSyncSelections(ctx context.Context, targetID string) ([]core.SubStoreSyncSelection, error) {
	args := []any{strings.TrimSpace(targetID)}
	ownerWhere := workspaceOwnerClause(ctx, "target.owner_id", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT item.target_id,item.agent_id,item.engine,item.profile_tag,item.custom_name,item.address_mode,item.created_at,item.updated_at,item.config_id
		FROM substore_sync_items item JOIN substore_sync_targets target ON target.id=item.target_id
		WHERE item.target_id=$1`+ownerWhere+` ORDER BY item.created_at,item.agent_id,item.engine,item.profile_tag`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SubStoreSyncSelection, 0)
	for rows.Next() {
		var item core.SubStoreSyncSelection
		if err := rows.Scan(&item.TargetID, &item.AgentID, &item.Engine, &item.ProfileTag, &item.CustomName, &item.AddressMode, &item.CreatedAt, &item.UpdatedAt, &item.ConfigID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ReplaceSubStoreSyncSelections(ctx context.Context, targetID string, selections []core.SubStoreSyncSelection) ([]core.SubStoreSyncSelection, error) {
	targetID = strings.TrimSpace(targetID)
	if _, err := s.SubStoreSyncTarget(ctx, targetID); err != nil {
		return nil, err
	}
	if len(selections) > 512 {
		return nil, fmt.Errorf("%w: no more than 512 Sub-Store nodes can be selected", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(selections))
	for index := range selections {
		selections[index].TargetID = targetID
		selections[index].ConfigID = strings.TrimSpace(selections[index].ConfigID)
		selections[index].AgentID = strings.TrimSpace(selections[index].AgentID)
		selections[index].ProfileTag = strings.TrimSpace(selections[index].ProfileTag)
		selections[index].CustomName = strings.TrimSpace(selections[index].CustomName)
		addressMode, valid := core.NormalizeSubStoreAddressMode(strings.TrimSpace(selections[index].AddressMode))
		if !valid {
			return nil, fmt.Errorf("%w: selected Sub-Store address mode is invalid", ErrInvalid)
		}
		selections[index].AddressMode = addressMode
		if selections[index].AgentID == "" || selections[index].ProfileTag == "" || utf8.RuneCountInString(selections[index].ProfileTag) > 200 {
			return nil, fmt.Errorf("%w: selected Sub-Store node identity is invalid", ErrInvalid)
		}
		if _, err := core.ParseEngine(string(selections[index].Engine)); err != nil {
			return nil, fmt.Errorf("%w: selected Sub-Store engine is invalid", ErrInvalid)
		}
		if selections[index].CustomName == "" || utf8.RuneCountInString(selections[index].CustomName) > 100 || strings.ContainsAny(selections[index].CustomName, "\r\n") {
			return nil, fmt.Errorf("%w: each Sub-Store node name is required and must not exceed 100 characters", ErrInvalid)
		}
		if _, exists := seen[selections[index].Key()]; exists {
			return nil, fmt.Errorf("%w: duplicate Sub-Store node selection", ErrInvalid)
		}
		seen[selections[index].Key()] = struct{}{}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM substore_sync_items WHERE target_id=$1`, targetID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, item := range selections {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT revoked_at IS NULL FROM agents WHERE id=$1`, item.AgentID).Scan(&active); errors.Is(err, pgx.ErrNoRows) || !active {
			return nil, fmt.Errorf("%w: selected node is no longer available", ErrInvalid)
		} else if err != nil {
			return nil, err
		}
		if item.ConfigID != "" || !scopeForConfig(ctx).Admin {
			args := []any{item.ConfigID, item.AgentID, item.Engine}
			ownerWhere := ownerClause(ctx, "owner_id", &args)
			var visible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM configs
				WHERE id=$1 AND (agent_id IS NULL OR agent_id=$2) AND engine=$3 AND deleted_at IS NULL`+ownerWhere+`)`, args...).Scan(&visible); err != nil {
				return nil, err
			}
			if !visible {
				return nil, ErrNotFound
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO substore_sync_items (target_id,agent_id,engine,profile_tag,custom_name,address_mode,created_at,updated_at,config_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8)`, targetID, item.AgentID, item.Engine, item.ProfileTag, item.CustomName, item.AddressMode, now, item.ConfigID); err != nil {
			return nil, mapError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListSubStoreSyncSelections(ctx, targetID)
}

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
