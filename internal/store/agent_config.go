package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// AgentConfig returns this user's workspace for an agent/core pair.
// Node-owned configurations cannot accidentally be deployed elsewhere.
func (s *Store) AgentConfig(ctx context.Context, agentID string, engine core.Engine) (core.Config, error) {
	if err := requireAgentEngineAccess(ctx, s.pool, agentID, engine); err != nil {
		return core.Config{}, err
	}
	var config core.Config
	err := s.pool.QueryRow(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL`, agentID, engine, scopeForConfig(ctx).OwnerID).Scan(
		&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content,
		&config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, ErrNotFound
	}
	if err != nil {
		return core.Config{}, err
	}
	config.Content, err = s.decryptContent(config.Content)
	if err != nil {
		return core.Config{}, err
	}
	return config, nil
}

// ListAgentConfigs returns every active node-owned configuration. The control
// plane uses this for fleet-level deployment drift and listener summaries;
// general configuration workspaces remain isolated through ListConfigs.
func (s *Store) ListAgentConfigs(ctx context.Context) ([]core.Config, error) {
	return s.listAgentConfigs(ctx, "", false)
}

// AgentConfigsForMonitoring includes every user's workspace when reconciling
// shared host counters. A user's save must not prune another user's ports.
func (s *Store) AgentConfigsForMonitoring(ctx context.Context, agentID string) ([]core.Config, error) {
	if !scopeForConfig(ctx).Admin {
		return nil, ErrForbidden
	}
	return s.listAgentConfigs(ctx, agentID, true)
}

// AgentConfigs filters before fetching or decrypting configuration bodies.
func (s *Store) AgentConfigs(ctx context.Context, agentID string) ([]core.Config, error) {
	if agentID == "" {
		return nil, ErrInvalid
	}
	if err := requireAgentAccess(ctx, s.pool, agentID); err != nil {
		return nil, err
	}
	configs, err := s.listAgentConfigs(ctx, agentID, false)
	if err != nil || len(configs) > 0 {
		return configs, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND revoked_at IS NULL)`, agentID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return configs, nil
}

func (s *Store) listAgentConfigs(ctx context.Context, agentID string, allOwners bool) ([]core.Config, error) {
	where := "agent_id IS NOT NULL"
	var args []any
	if agentID != "" {
		where = "agent_id=$1"
		args = []any{agentID}
	}
	if !allOwners {
		where += workspaceOwnerClause(ctx, "owner_id", &args)
		where += agentEngineAccessClause(ctx, "configs.agent_id", "configs.engine", &args)
	} else {
		// Isolated drafts are not host monitors. Only deployment may bind
		// their administrator-reserved listeners to cumulative accounting.
		where += ` AND NOT EXISTS(SELECT 1 FROM panel_users u JOIN agents a ON a.id=configs.agent_id
			WHERE u.id=configs.owner_id AND u.role='user' AND a.owner_id<>u.id)`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE `+where+` AND deleted_at IS NULL
		  AND agent_id IN (SELECT id FROM agents WHERE revoked_at IS NULL)
		ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]core.Config, 0)
	for rows.Next() {
		var config core.Config
		if err := rows.Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content,
			&config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID); err != nil {
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

func (s *Store) LatestDeployments(ctx context.Context) ([]core.Deployment, error) {
	args := []any{}
	ownerWhere := ownerClause(ctx, "c.owner_id", &args)
	ownerWhere += agentEngineAccessClause(ctx, "latest.agent_id", "latest.engine", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT latest.agent_id,latest.engine,COALESCE(latest.config_id,''),COALESCE(latest.config_version,0),latest.finished_at
		FROM (`+latestDeploymentsSQL+`) latest
		LEFT JOIN configs c ON c.id=latest.config_id
		WHERE true`+ownerWhere+` ORDER BY latest.agent_id,latest.engine`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.Deployment, 0)
	for rows.Next() {
		var deployment core.Deployment
		if err := rows.Scan(&deployment.AgentID, &deployment.Engine, &deployment.ConfigID, &deployment.ConfigVersion, &deployment.DeployedAt); err != nil {
			return nil, err
		}
		result = append(result, deployment)
	}
	return result, rows.Err()
}

// Probe the existing (agent_id,engine,finished_at) partial index once per
// possible service instead of scanning every successful deployment retained
// in tasks. Do not restrict this to current capabilities: historical deployed
// services (and revoked-node history) retain the same listing semantics.
const latestDeploymentsSQL = `
	SELECT agent.id AS agent_id,latest.engine,latest.config_id,latest.config_version,latest.finished_at
	FROM agents agent CROSS JOIN unnest(ARRAY['mihomo','xray','sing-box','ss-rust']::text[]) selected(engine)
	CROSS JOIN LATERAL (
		SELECT task.engine,task.config_id,task.config_version,task.finished_at
		FROM tasks task
		WHERE task.agent_id=agent.id AND task.engine=selected.engine
		  AND task.action IN ('deploy','import-existing') AND task.status='succeeded' AND task.finished_at IS NOT NULL
		ORDER BY task.finished_at DESC LIMIT 1
	) latest`
