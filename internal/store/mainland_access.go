package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ReplaceMainlandAccessPolicies atomically replaces the complete desired
// Shadowsocks Rust policy set for one exact configuration version.
func (s *Store) ReplaceMainlandAccessPolicies(ctx context.Context, agentID string, expectedVersion int, policies []core.MainlandAccessPolicy) error {
	if strings.TrimSpace(agentID) == "" || expectedVersion < 1 {
		return errors.New("agent ID and configuration version are required")
	}
	for _, policy := range policies {
		if policy.AgentID != agentID || policy.Engine != core.EngineShadowsocksRust || strings.TrimSpace(policy.Tag) == "" || policy.Port < 1 || policy.Port > 65535 ||
			(!policy.BlockMainlandDestination && !policy.BlockMainlandSource) {
			return errors.New("invalid Shadowsocks Rust mainland access policy")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var version int
	if err := tx.QueryRow(ctx, `SELECT version FROM configs WHERE agent_id=$1 AND engine=$2 AND deleted_at IS NULL FOR UPDATE`, agentID, core.EngineShadowsocksRust).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if version != expectedVersion {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mainland_access_policies WHERE agent_id=$1 AND engine=$2`, agentID, core.EngineShadowsocksRust); err != nil {
		return err
	}
	for _, policy := range policies {
		if _, err := tx.Exec(ctx, `INSERT INTO mainland_access_policies
			(agent_id,engine,tag,kind,port,config_version,block_mainland_destination,block_mainland_source,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())`, agentID, core.EngineShadowsocksRust, strings.TrimSpace(policy.Tag),
			strings.TrimSpace(policy.Kind), policy.Port, expectedVersion, policy.BlockMainlandDestination, policy.BlockMainlandSource); err != nil {
			return mapError(err)
		}
	}
	return tx.Commit(ctx)
}

// ListMainlandAccessPolicies returns Agent-applied Shadowsocks Rust policies.
// Other core-native policies remain embedded in their configurations.
func (s *Store) ListMainlandAccessPolicies(ctx context.Context, agentID string) ([]core.MainlandAccessPolicy, error) {
	query := `SELECT p.agent_id,p.engine,p.tag,p.kind,p.port,p.config_version,p.block_mainland_destination,p.block_mainland_source
	          FROM mainland_access_policies p
	          JOIN configs c ON c.agent_id=p.agent_id AND c.engine=p.engine AND c.version=p.config_version AND c.deleted_at IS NULL`
	args := []any{}
	if agentID != "" {
		query += " WHERE p.agent_id=$1"
		args = append(args, agentID)
	}
	query += " ORDER BY p.agent_id,p.port,p.tag"
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.MainlandAccessPolicy, 0)
	for rows.Next() {
		var policy core.MainlandAccessPolicy
		if err := rows.Scan(&policy.AgentID, &policy.Engine, &policy.Tag, &policy.Kind, &policy.Port, &policy.ConfigVersion,
			&policy.BlockMainlandDestination, &policy.BlockMainlandSource); err != nil {
			return nil, err
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

// SaveMainlandAccessPolicy updates one Shadowsocks Rust policy while checking
// the configuration version selected by the operator. The JSON config itself
// remains untouched; this is deliberately separate durable state.
func (s *Store) SaveMainlandAccessPolicy(ctx context.Context, policy core.MainlandAccessPolicy, expectedVersion int) error {
	if policy.Engine != core.EngineShadowsocksRust {
		return errors.New("Agent-applied mainland policies only support ss-rust")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var version int
	if err := tx.QueryRow(ctx, `SELECT version FROM configs WHERE agent_id=$1 AND engine=$2 AND deleted_at IS NULL FOR UPDATE`, policy.AgentID, policy.Engine).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if expectedVersion > 0 && expectedVersion != version {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mainland_access_policies
			(agent_id,engine,tag,kind,port,config_version,block_mainland_destination,block_mainland_source,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
		ON CONFLICT (agent_id,engine,tag,port) DO UPDATE SET
			kind=EXCLUDED.kind,
			config_version=EXCLUDED.config_version,
			block_mainland_destination=EXCLUDED.block_mainland_destination,
			block_mainland_source=EXCLUDED.block_mainland_source,
			updated_at=now()`,
		policy.AgentID, policy.Engine, policy.Tag, policy.Kind, policy.Port, version,
		policy.BlockMainlandDestination, policy.BlockMainlandSource)
	if err != nil {
		return mapError(err)
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteMainlandAccessPolicy(ctx context.Context, agentID string, engine core.Engine, tag string, expectedVersion int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var version int
	if err := tx.QueryRow(ctx, `SELECT version FROM configs WHERE agent_id=$1 AND engine=$2 AND deleted_at IS NULL FOR UPDATE`, agentID, engine).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if expectedVersion > 0 && expectedVersion != version {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mainland_access_policies WHERE agent_id=$1 AND engine=$2 AND tag=$3`, agentID, engine, tag); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
