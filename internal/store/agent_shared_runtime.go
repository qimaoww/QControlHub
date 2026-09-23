package store

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A recipient's status describes their private process. The base service may
// still be running for the node owner or another recipient.
func (s *Store) overlaySharedInstanceRuntime(ctx context.Context, agents []core.Agent) error {
	owner := scopeForConfig(ctx).OwnerID
	var ids []string
	for index := range agents {
		agent := &agents[index]
		if agent.CanManage || !containsFeature(agent.Features, core.AgentFeatureSharedCoreInstances) {
			continue
		}
		ids = append(ids, agent.ID)
		for engine, runtime := range agent.Runtime {
			runtime.ServiceStatus = "inactive"
			agent.Runtime[engine] = runtime
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT agent_id,engine,running,uncertain FROM (
		SELECT agent_id,engine,running,uncertain,1 AS priority FROM agent_engine_ownership
		WHERE owner_id=$1 AND agent_id=ANY($2::text[])
		UNION ALL
		SELECT agent_id,engine,running,uncertain,2 AS priority FROM agent_shared_instance_ownership
		WHERE owner_id=$1 AND agent_id=ANY($2::text[])
	) owned ORDER BY priority`, owner, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*core.Agent, len(agents))
	for index := range agents {
		byID[agents[index].ID] = &agents[index]
	}
	for rows.Next() {
		var id string
		var engine core.Engine
		var running, uncertain bool
		if err := rows.Scan(&id, &engine, &running, &uncertain); err != nil {
			rows.Close()
			return err
		}
		agent := byID[id]
		if agent == nil {
			continue
		}
		runtime, ok := agent.Runtime[engine]
		if !ok {
			continue
		}
		switch {
		case uncertain:
			runtime.ServiceStatus = "unknown"
		case running:
			runtime.ServiceStatus = "active"
		default:
			runtime.ServiceStatus = "inactive"
		}
		agent.Runtime[engine] = runtime
	}
	rows.Close()
	return rows.Err()
}
