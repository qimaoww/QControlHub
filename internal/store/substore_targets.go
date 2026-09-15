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
