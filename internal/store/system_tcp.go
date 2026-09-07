package store

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// LatestSystemTCPTasks reads at most one task per active node. A global task
// history limit can hide a busy node behind another node's recent history.
func (s *Store) LatestSystemTCPTasks(ctx context.Context, agentID string) ([]core.Task, error) {
	where := "agent.revoked_at IS NULL"
	var args []any
	if agentID != "" {
		where += " AND agent.id=$1"
		args = append(args, agentID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id,t.agent_id,t.action,t.engine,COALESCE(t.config_id,''),COALESCE(t.config_version,0),
		       COALESCE(t.core_version,''),COALESCE(t.core_source,''),t.status,t.attempt,
		       '',COALESCE(t.error,''),t.created_at,t.started_at,t.finished_at,t.tcp_settings
		FROM agents agent CROSS JOIN LATERAL (
			SELECT * FROM tasks WHERE agent_id=agent.id
			AND action IN ('enable-bbr','disable-bbr','configure-tcp')
			ORDER BY created_at DESC,id DESC LIMIT 1
		) t WHERE `+where+` ORDER BY agent.id`, args...)
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
