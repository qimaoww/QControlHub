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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.replaceMainlandAccessPoliciesTx(ctx, tx, agentID, expectedVersion, policies); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) replaceMainlandAccessPoliciesTx(ctx context.Context, tx pgx.Tx, agentID string, expectedVersion int, policies []core.MainlandAccessPolicy) error {
	if strings.TrimSpace(agentID) == "" || expectedVersion < 1 {
		return errors.New("agent ID and configuration version are required")
	}
	for _, policy := range policies {
		if policy.AgentID != agentID || policy.Engine != core.EngineShadowsocksRust || strings.TrimSpace(policy.Tag) == "" || policy.Port < 1 || policy.Port > 65535 ||
			(!policy.BlockMainlandDestination && !policy.BlockMainlandSource) {
			return errors.New("invalid Shadowsocks Rust mainland access policy")
		}
	}
	configID, version, err := lockMainlandConfig(ctx, tx, agentID, core.EngineShadowsocksRust)
	if err != nil {
		return err
	}
	if version != expectedVersion {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mainland_access_policies WHERE config_id=$1`, configID); err != nil {
		return err
	}
	for _, policy := range policies {
		if _, err := tx.Exec(ctx, `INSERT INTO mainland_access_policies
			(agent_id,engine,tag,kind,port,config_version,block_mainland_destination,block_mainland_source,updated_at,config_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now(),$9)`, agentID, core.EngineShadowsocksRust, strings.TrimSpace(policy.Tag),
			strings.TrimSpace(policy.Kind), policy.Port, expectedVersion, policy.BlockMainlandDestination, policy.BlockMainlandSource, configID); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// ListMainlandAccessPolicies returns Agent-applied Shadowsocks Rust policies.
// Other core-native policies remain embedded in their configurations.
func (s *Store) ListMainlandAccessPolicies(ctx context.Context, agentID string) ([]core.MainlandAccessPolicy, error) {
	query := `SELECT p.agent_id,p.engine,p.tag,p.kind,p.port,p.config_version,p.block_mainland_destination,p.block_mainland_source
	          FROM mainland_access_policies p
	          JOIN configs c ON c.id=p.config_id AND c.version=p.config_version AND c.deleted_at IS NULL
	          WHERE true`
	args := []any{}
	if agentID != "" {
		query += " AND p.agent_id=$1"
		args = append(args, agentID)
	}
	query += workspaceOwnerClause(ctx, "c.owner_id", &args)
	query += agentEngineAccessClause(ctx, "p.agent_id", "p.engine", &args)
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
	configID, version, err := lockMainlandConfig(ctx, tx, policy.AgentID, policy.Engine)
	if err != nil {
		return err
	}
	if expectedVersion > 0 && expectedVersion != version {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mainland_access_policies
		(agent_id,engine,tag,kind,port,config_version,block_mainland_destination,block_mainland_source,updated_at,config_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now(),$9)
		ON CONFLICT (config_id,tag,port) DO UPDATE SET
			kind=EXCLUDED.kind,
			config_version=EXCLUDED.config_version,
			block_mainland_destination=EXCLUDED.block_mainland_destination,
			block_mainland_source=EXCLUDED.block_mainland_source,
			updated_at=now()`,
		policy.AgentID, policy.Engine, policy.Tag, policy.Kind, policy.Port, version,
		policy.BlockMainlandDestination, policy.BlockMainlandSource, configID)
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
	configID, version, err := lockMainlandConfig(ctx, tx, agentID, engine)
	if err != nil {
		return err
	}
	if expectedVersion > 0 && expectedVersion != version {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mainland_access_policies WHERE config_id=$1 AND tag=$2`, configID, tag); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockMainlandConfig(ctx context.Context, tx pgx.Tx, agentID string, engine core.Engine) (string, int, error) {
	if err := lockAgentUser(ctx, tx); err != nil {
		return "", 0, err
	}
	if err := requireAgentEngineAccess(ctx, tx, agentID, engine); err != nil {
		return "", 0, err
	}
	var id string
	var version int
	err := tx.QueryRow(ctx, `SELECT id,version FROM configs
		WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL FOR UPDATE`,
		agentID, engine, scopeForConfig(ctx).OwnerID).Scan(&id, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return id, version, err
}
