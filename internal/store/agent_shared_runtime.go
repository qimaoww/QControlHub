package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Status polling reads revision metadata, never configuration bodies. An
// absent recipient instance must never fall back to the host base service.
func (s *Store) overlaySharedInstanceRuntime(ctx context.Context, agents []core.Agent) error {
	var ids []string
	byID := make(map[string]*core.Agent, len(agents))
	for i := range agents {
		agent := &agents[i]
		if agent.CanManage {
			continue
		}
		ids = append(ids, agent.ID)
		byID[agent.ID] = agent
		if agent.Runtime == nil {
			agent.Runtime = make(map[core.Engine]core.RuntimeState)
		}
		for _, engine := range agent.Capabilities {
			runtime := agent.Runtime[engine]
			runtime.ServiceStatus = "unknown"
			agent.Runtime[engine] = runtime
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT sh.agent_id,sh.id,sh.user_id,sh.enabled,sh.status,sh.invitation_revision,
		sh.engines,CASE WHEN sh.ports_unrestricted THEN ARRAY[0] ELSE ARRAY(SELECT port FROM agent_share_ports WHERE share_id=sh.id ORDER BY port) END,
		sh.limit_bytes,sh.used_bytes,sh.created_at,sh.updated_at,selected.engine,
		COALESCE(c.id,''),COALESCE(c.version,0),COALESCE(state.config_id,''),COALESCE(state.config_version,0),
		state.deployed_at,state.observed_at,COALESCE(state.config_uncertain,false),
		COALESCE(state.service_status,'unknown'),COALESCE(state.status_reason,''),
		(state.observed_at IS NOT NULL),COALESCE(operation.body,'null'::jsonb)
		FROM agent_shares sh CROSS JOIN LATERAL unnest(sh.engines) selected(engine)
		LEFT JOIN configs c ON c.agent_id=sh.agent_id AND c.engine=selected.engine AND c.owner_id=sh.user_id AND c.deleted_at IS NULL
		LEFT JOIN agent_shared_instance_ownership state ON state.agent_id=sh.agent_id AND state.engine=selected.engine
			AND state.share_id=sh.id AND state.owner_id=sh.user_id
		LEFT JOIN LATERAL (SELECT jsonb_build_object('id',t.id,'agent_id',t.agent_id,'engine',t.engine,'action',t.action,
			'status',t.status,'config_id',t.config_id,'config_version',t.config_version,'error',t.error,
			'created_at',t.created_at,'started_at',t.started_at,'finished_at',t.finished_at) AS body
			FROM tasks t WHERE t.agent_id=sh.agent_id AND t.engine=selected.engine AND t.shared_instance
			AND t.shared_traffic_id=sh.id AND t.owner_id=sh.user_id AND t.action IN ('deploy','start','stop','restart','install')
			ORDER BY t.created_at DESC,t.id DESC LIMIT 1) operation ON true
		WHERE sh.user_id=$1 AND sh.agent_id=ANY($2::text[]) AND sh.enabled AND sh.status='accepted'`, scopeForConfig(ctx).OwnerID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var allocation core.AgentShare
		var engine core.Engine
		var instance core.SharedInstanceRuntime
		var service string
		var operation []byte
		if err := rows.Scan(&allocation.AgentID, &allocation.ID, &allocation.UserID, &allocation.Enabled, &allocation.Status,
			&allocation.InvitationRevision, &allocation.Engines, &allocation.Ports, &allocation.LimitBytes, &allocation.UsedBytes,
			&allocation.CreatedAt, &allocation.UpdatedAt, &engine, &instance.SavedConfigID, &instance.SavedVersion,
			&instance.DeployedConfigID, &instance.DeployedVersion, &instance.DeployedAt, &instance.ObservedAt,
			&instance.ConfigUncertain, &service, &instance.Reason, &instance.Created, &operation); err != nil {
			return err
		}
		agent := byID[allocation.AgentID]
		if agent == nil {
			continue
		}
		allocation.AgentName = agent.Name
		agent.SharedAllocation = &allocation
		instance.ID = allocation.ID
		instance.UpgradeRequired = !containsFeature(agent.Features, core.AgentFeatureSharedCoreLifecycle)
		if allocation.LimitBytes > 0 && allocation.UsedBytes >= allocation.LimitBytes {
			instance.Restriction = "共享额度已耗尽，请联系节点所有者调整额度"
		} else if len(allocation.Ports) == 0 {
			instance.Restriction = "尚未分配端口，请联系节点所有者"
		}
		if err := json.Unmarshal(operation, &instance.Operation); err != nil {
			return err
		}
		if agent.Status != "online" || instance.ObservedAt == nil || now.Sub(*instance.ObservedAt) > 90*time.Second {
			service = "unknown"
		}
		runtime := agent.Runtime[engine]
		runtime.ServiceStatus, runtime.Instance = service, &instance
		agent.Runtime[engine] = runtime
	}
	return rows.Err()
}
