package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) SubStoreSyncSettings(ctx context.Context) (core.SubStoreSyncSettings, error) {
	var settings core.SubStoreSyncSettings
	var endpoint string
	err := s.pool.QueryRow(ctx, `
		SELECT endpoint_ciphertext,updated_at
		FROM substore_sync_settings WHERE id=1`).Scan(
		&endpoint, &settings.UpdatedAt,
	)
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO substore_sync_settings
			(id,endpoint_ciphertext,subscription_name,integration_id,last_sync_status,last_sync_error,updated_at)
		VALUES (1,$1,'QControlHub',$2,'never','',$3)
		ON CONFLICT (id) DO UPDATE SET
			endpoint_ciphertext=EXCLUDED.endpoint_ciphertext,
			updated_at=EXCLUDED.updated_at`, sealed, integrationID, now); err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("save Sub-Store sync settings: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE substore_sync_targets SET
			last_synced_at=NULL,last_sync_status='never',last_sync_error='',updated_at=$1`, now); err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("reset Sub-Store sync targets: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SubStoreSyncSettings{}, err
	}
	return s.SubStoreSyncSettings(ctx)
}

func validateSubStoreTargetName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 || name == "." || name == ".." || strings.ContainsAny(name, "/\\?#\r\n") {
		return "", fmt.Errorf("%w: Sub-Store subscription name is required, must not exceed 100 characters, and cannot contain path characters", ErrInvalid)
	}
	return name, nil
}

func (s *Store) ListSubStoreSyncTargets(ctx context.Context) ([]core.SubStoreSyncTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT target.id,target.display_name,target.subscription_name,target.integration_id,target.last_synced_at,
		       target.last_sync_status,target.last_sync_error,
		       (SELECT count(*) FROM substore_sync_items item WHERE item.target_id=target.id),
		       target.created_at,target.updated_at
		FROM substore_sync_targets target ORDER BY target.created_at,target.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]core.SubStoreSyncTarget, 0)
	for rows.Next() {
		var target core.SubStoreSyncTarget
		if err := rows.Scan(
			&target.ID, &target.DisplayName, &target.SubscriptionName, &target.IntegrationID, &target.LastSyncedAt,
			&target.LastSyncStatus, &target.LastSyncError, &target.SelectionCount, &target.CreatedAt, &target.UpdatedAt,
		); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) SubStoreSyncTarget(ctx context.Context, id string) (core.SubStoreSyncTarget, error) {
	var target core.SubStoreSyncTarget
	err := s.pool.QueryRow(ctx, `
		SELECT target.id,target.display_name,target.subscription_name,target.integration_id,target.last_synced_at,
		       target.last_sync_status,target.last_sync_error,
		       (SELECT count(*) FROM substore_sync_items item WHERE item.target_id=target.id),
		       target.created_at,target.updated_at
		FROM substore_sync_targets target WHERE target.id=$1`, strings.TrimSpace(id)).Scan(
		&target.ID, &target.DisplayName, &target.SubscriptionName, &target.IntegrationID, &target.LastSyncedAt,
		&target.LastSyncStatus, &target.LastSyncError, &target.SelectionCount, &target.CreatedAt, &target.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SubStoreSyncTarget{}, ErrNotFound
	}
	return target, err
}

func (s *Store) CreateSubStoreSyncTarget(ctx context.Context, name string) (core.SubStoreSyncTarget, error) {
	name, err := validateSubStoreTargetName(name)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	integrationID, err := core.NewID("ssi")
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	return s.createSubStoreSyncTarget(ctx, name, integrationID)
}

func (s *Store) ImportSubStoreSyncTarget(ctx context.Context, name, integrationID string) (core.SubStoreSyncTarget, error) {
	name, err := validateSubStoreTargetName(name)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	integrationID = strings.TrimSpace(integrationID)
	if !strings.HasPrefix(integrationID, "ssi_") || utf8.RuneCountInString(integrationID) > 100 {
		return core.SubStoreSyncTarget{}, fmt.Errorf("%w: Sub-Store integration identity is invalid", ErrInvalid)
	}
	return s.createSubStoreSyncTarget(ctx, name, integrationID)
}

func (s *Store) createSubStoreSyncTarget(ctx context.Context, name, integrationID string) (core.SubStoreSyncTarget, error) {
	id, err := core.NewID("sst")
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	now := time.Now().UTC()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO substore_sync_targets
			(id,display_name,subscription_name,integration_id,last_sync_status,last_sync_error,created_at,updated_at)
		VALUES ($1,$2,$2,$3,'never','',$4,$4)`, id, name, integrationID, now); err != nil {
		return core.SubStoreSyncTarget{}, mapError(err)
	}
	return s.SubStoreSyncTarget(ctx, id)
}

func (s *Store) UpdateSubStoreSyncTarget(ctx context.Context, id, displayName, subscriptionName string) (core.SubStoreSyncTarget, error) {
	displayName, err := validateSubStoreTargetName(displayName)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	subscriptionName, err = validateSubStoreTargetName(subscriptionName)
	if err != nil {
		return core.SubStoreSyncTarget{}, err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE substore_sync_targets SET
			display_name=$2,subscription_name=$3,updated_at=now()
		WHERE id=$1`, strings.TrimSpace(id), displayName, subscriptionName)
	if err != nil {
		return core.SubStoreSyncTarget{}, mapError(err)
	}
	if command.RowsAffected() == 0 {
		return core.SubStoreSyncTarget{}, ErrNotFound
	}
	return s.SubStoreSyncTarget(ctx, id)
}

func (s *Store) DeleteSubStoreSyncTarget(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM substore_sync_targets WHERE id=$1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListSubStoreSyncSelections(ctx context.Context, targetID string) ([]core.SubStoreSyncSelection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT target_id,agent_id,engine,profile_tag,custom_name,address_mode,created_at,updated_at
		FROM substore_sync_items WHERE target_id=$1 ORDER BY created_at,agent_id,engine,profile_tag`, strings.TrimSpace(targetID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SubStoreSyncSelection, 0)
	for rows.Next() {
		var item core.SubStoreSyncSelection
		if err := rows.Scan(&item.TargetID, &item.AgentID, &item.Engine, &item.ProfileTag, &item.CustomName, &item.AddressMode, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
		if _, err := tx.Exec(ctx, `
			INSERT INTO substore_sync_items (target_id,agent_id,engine,profile_tag,custom_name,address_mode,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$7)`, targetID, item.AgentID, item.Engine, item.ProfileTag, item.CustomName, item.AddressMode, now); err != nil {
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
	command, err := s.pool.Exec(ctx, `
		UPDATE substore_sync_targets SET
			last_synced_at=now(),last_sync_status=$1,last_sync_error=$2,updated_at=now()
		WHERE id=$3`, status, message, strings.TrimSpace(targetID))
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
