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
		SELECT endpoint_ciphertext,subscription_name,integration_id,last_synced_at,last_sync_status,last_sync_error,updated_at
		FROM substore_sync_settings WHERE id=1`).Scan(
		&endpoint, &settings.SubscriptionName, &settings.IntegrationID, &settings.LastSyncedAt,
		&settings.LastSyncStatus, &settings.LastSyncError, &settings.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SubStoreSyncSettings{LastSyncStatus: "never"}, nil
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

func (s *Store) SaveSubStoreSyncSettings(ctx context.Context, endpointURL, subscriptionName string) (core.SubStoreSyncSettings, error) {
	endpointURL = strings.TrimSpace(endpointURL)
	subscriptionName = strings.TrimSpace(subscriptionName)
	if endpointURL == "" || utf8.RuneCountInString(endpointURL) > 1000 {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: Sub-Store endpoint is required and must not exceed 1000 characters", ErrInvalid)
	}
	if subscriptionName == "" || utf8.RuneCountInString(subscriptionName) > 100 || subscriptionName == "." || subscriptionName == ".." || strings.ContainsAny(subscriptionName, "/\\?#\r\n") {
		return core.SubStoreSyncSettings{}, fmt.Errorf("%w: Sub-Store subscription name is required, must not exceed 100 characters, and cannot contain path characters", ErrInvalid)
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
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO substore_sync_settings
			(id,endpoint_ciphertext,subscription_name,integration_id,last_sync_status,last_sync_error,updated_at)
		VALUES (1,$1,$2,$3,'never','',$4)
		ON CONFLICT (id) DO UPDATE SET
			endpoint_ciphertext=EXCLUDED.endpoint_ciphertext,
			subscription_name=EXCLUDED.subscription_name,
			updated_at=EXCLUDED.updated_at`, sealed, subscriptionName, integrationID, now); err != nil {
		return core.SubStoreSyncSettings{}, fmt.Errorf("save Sub-Store sync settings: %w", err)
	}
	return s.SubStoreSyncSettings(ctx)
}

func (s *Store) ListSubStoreSyncSelections(ctx context.Context) ([]core.SubStoreSyncSelection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT agent_id,engine,profile_tag,custom_name,created_at,updated_at
		FROM substore_sync_items ORDER BY created_at,agent_id,engine,profile_tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.SubStoreSyncSelection, 0)
	for rows.Next() {
		var item core.SubStoreSyncSelection
		if err := rows.Scan(&item.AgentID, &item.Engine, &item.ProfileTag, &item.CustomName, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ReplaceSubStoreSyncSelections(ctx context.Context, selections []core.SubStoreSyncSelection) ([]core.SubStoreSyncSelection, error) {
	if len(selections) > 512 {
		return nil, fmt.Errorf("%w: no more than 512 Sub-Store nodes can be selected", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(selections))
	for index := range selections {
		selections[index].AgentID = strings.TrimSpace(selections[index].AgentID)
		selections[index].ProfileTag = strings.TrimSpace(selections[index].ProfileTag)
		selections[index].CustomName = strings.TrimSpace(selections[index].CustomName)
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
	if _, err := tx.Exec(ctx, `DELETE FROM substore_sync_items`); err != nil {
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
			INSERT INTO substore_sync_items (agent_id,engine,profile_tag,custom_name,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$5)`, item.AgentID, item.Engine, item.ProfileTag, item.CustomName, now); err != nil {
			return nil, mapError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListSubStoreSyncSelections(ctx)
}

func (s *Store) RecordSubStoreSyncResult(ctx context.Context, syncErr error) error {
	status := "success"
	message := ""
	if syncErr != nil {
		status = "failed"
		message = truncate(strings.TrimSpace(syncErr.Error()), 500)
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE substore_sync_settings SET
			last_synced_at=now(),last_sync_status=$1,last_sync_error=$2,updated_at=now()
		WHERE id=1`, status, message)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
