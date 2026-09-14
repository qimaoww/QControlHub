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
