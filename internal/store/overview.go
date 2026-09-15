package store

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) Overview(ctx context.Context) (core.Overview, error) {
	var result core.Overview
	args := []any{}
	configWhere := ownerClause(ctx, "owner_id", &args)
	configWhere += configAgentAccessClause(ctx, "configs.agent_id", "configs.engine", &args)
	taskWhere := ownerClause(ctx, "owner_id", &args)
	taskWhere += agentEngineAccessClause(ctx, "tasks.agent_id", "tasks.engine", &args)
	agentWhere := agentAccessClause(ctx, "agents.id", &args)
	err := s.pool.QueryRow(ctx, `
		SELECT agents.total,agents.online,configs.archived,configs.node,
		       tasks.queued+tasks.running,tasks.queued,tasks.running,tasks.failed
		FROM (
			SELECT count(*) AS total,
			       count(*) FILTER (WHERE last_seen > now() - make_interval(secs => `+agentOfflineThresholdSQL+`)) AS online
			FROM agents WHERE revoked_at IS NULL`+agentWhere+`
		) agents CROSS JOIN (
			SELECT count(*) FILTER (WHERE agent_id IS NULL) AS archived,
			       count(*) FILTER (WHERE agent_id IS NOT NULL) AS node
			FROM configs WHERE deleted_at IS NULL`+configWhere+`
		) configs CROSS JOIN (
			SELECT count(*) FILTER (WHERE status='pending') AS queued,
			       count(*) FILTER (WHERE status='running') AS running,
			       count(*) FILTER (WHERE status='failed') AS failed
			FROM tasks WHERE status IN ('pending','running','failed')`+taskWhere+`
		) tasks`, args...).Scan(
		&result.Agents, &result.AgentsOnline, &result.Configs, &result.NodeConfigs,
		&result.TasksPending, &result.TasksQueued, &result.TasksRunning, &result.TasksFailed)
	return result, err
}
