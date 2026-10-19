package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Status polling reads revision metadata, never configuration bodies. An
// absent recipient instance must never fall back to the host base service.
// For node owners and administrators, per-recipient shared core statuses are
// attached under Runtime[engine].SharedInstances so host and multi-user kernel
// states stay explicitly separated.
func prepareSharedInstanceRuntimeOverlay(agents []core.Agent) (map[string]*core.Agent, []string, []string) {
	var recipientIDs, managedIDs []string
	byID := make(map[string]*core.Agent, len(agents))
	for index := range agents {
		agent := &agents[index]
		byID[agent.ID] = agent
		if agent.CanManage {
			managedIDs = append(managedIDs, agent.ID)
			continue
		}
		recipientIDs = append(recipientIDs, agent.ID)
		if agent.Runtime == nil {
			agent.Runtime = make(map[core.Engine]core.RuntimeState)
		}
		instanceScope := "shared-engine"
		if containsFeature(agent.Features, core.AgentFeatureSharedCoreInstances) {
			instanceScope = "shared-instance"
		}
		for engine, runtime := range agent.Runtime {
			runtime.ServiceStatus = "unknown"
			runtime.InstanceScope = instanceScope
			runtime.Deployed = false
			runtime.ConfigVersion = 0
			runtime.SharedInstances = nil
			agent.Runtime[engine] = runtime
		}
		for _, engine := range agent.Capabilities {
			runtime := agent.Runtime[engine]
			runtime.ServiceStatus = "unknown"
			runtime.InstanceScope = instanceScope
			runtime.Deployed = false
			runtime.ConfigVersion = 0
			runtime.SharedInstances = nil
			agent.Runtime[engine] = runtime
		}
	}
	return byID, recipientIDs, managedIDs
}

func recipientSharedInstanceRuntimeQuery(ctx context.Context, recipientIDs []string) (string, []any) {
	return `SELECT sh.agent_id,sh.id,sh.user_id,sh.enabled,sh.status,sh.invitation_revision,
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
		WHERE sh.user_id=$1 AND ($2::text[] IS NULL OR sh.agent_id=ANY($2::text[])) AND sh.enabled AND sh.status='accepted'`, []any{scopeForConfig(ctx).OwnerID, recipientIDs}
}

func scanRecipientSharedInstanceRuntime(rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}, byID map[string]*core.Agent) error {
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
		if agent == nil || agent.CanManage {
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
		runtime.Deployed = instance.DeployedConfigID != ""
		runtime.ConfigVersion = instance.DeployedVersion
		agent.Runtime[engine] = runtime
	}
	return rows.Err()
}

func managedSharedInstanceRuntimeQuery(ctx context.Context, managedIDs []string) (string, []any) {
	args := []any{managedIDs}
	where := ownerClause(ctx, "a.owner_id", &args)
	return `
		SELECT s.agent_id, engine_name::text, s.id, s.user_id, u.username, COALESCE(u.display_name,''),
			COALESCE(inst.config_id, legacy.config_id, ''),
			COALESCE(inst.config_version, legacy.config_version, 0),
			COALESCE(inst.running, legacy.running, false),
			COALESCE(inst.uncertain, legacy.uncertain, false),
			COALESCE(inst.traffic_settled, legacy.traffic_settled, true)
		FROM agent_shares s
		JOIN panel_users u ON u.id = s.user_id
		JOIN agents a ON a.id = s.agent_id
		CROSS JOIN LATERAL unnest(
			CASE WHEN s.enabled AND s.status = 'accepted' THEN s.engines
			     ELSE ARRAY(
			         SELECT state.engine FROM agent_shared_instance_ownership state
			         WHERE state.agent_id = s.agent_id AND state.share_id = s.id AND (state.running OR state.uncertain OR NOT state.traffic_settled)
			     )
			END
		) AS engine_name
		LEFT JOIN agent_shared_instance_ownership inst
			ON inst.agent_id = s.agent_id AND inst.engine = engine_name AND inst.share_id = s.id
		LEFT JOIN agent_engine_ownership legacy
			ON legacy.agent_id = s.agent_id AND legacy.engine = engine_name AND legacy.owner_id = s.user_id AND a.owner_id <> s.user_id
			   AND inst.share_id IS NULL
		WHERE ($1::text[] IS NULL OR s.agent_id = ANY($1::text[]))
		  AND a.revoked_at IS NULL` + where + `
		ORDER BY s.agent_id, engine_name, u.username, s.id`, args
}

func scanManagedSharedInstanceRuntime(rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}, byID map[string]*core.Agent) error {
	defer rows.Close()
	for rows.Next() {
		var agentID, shareID, userID, username, displayName, configID string
		var engine core.Engine
		var configVersion int
		var running, uncertain, trafficSettled bool
		if err := rows.Scan(&agentID, &engine, &shareID, &userID, &username, &displayName,
			&configID, &configVersion, &running, &uncertain, &trafficSettled); err != nil {
			return err
		}
		agent := byID[agentID]
		if agent == nil || !agent.CanManage {
			continue
		}
		if agent.Runtime == nil {
			agent.Runtime = make(map[core.Engine]core.RuntimeState)
		}
		runtime := agent.Runtime[engine]
		status := "inactive"
		switch {
		case !runtime.Installed:
			status = "uninstalled"
		case uncertain:
			status = "unknown"
		case running:
			status = "active"
		}
		runtime.SharedInstances = append(runtime.SharedInstances, core.SharedCoreRuntime{
			ShareID:        shareID,
			UserID:         userID,
			Username:       username,
			DisplayName:    displayName,
			Installed:      runtime.Installed,
			Version:        runtime.Version,
			ServiceStatus:  status,
			Deployed:       configID != "",
			ConfigVersion:  configVersion,
			Running:        running,
			Uncertain:      uncertain,
			TrafficSettled: trafficSettled,
		})
		agent.Runtime[engine] = runtime
	}
	return rows.Err()
}

func (s *Store) overlaySharedInstanceRuntime(ctx context.Context, agents []core.Agent) error {
	byID, recipientIDs, managedIDs := prepareSharedInstanceRuntimeOverlay(agents)
	if len(recipientIDs) > 0 {
		query, args := recipientSharedInstanceRuntimeQuery(ctx, recipientIDs)
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		if err := scanRecipientSharedInstanceRuntime(rows, byID); err != nil {
			return err
		}
	}
	if len(managedIDs) > 0 {
		query, args := managedSharedInstanceRuntimeQuery(ctx, managedIDs)
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		if err := scanManagedSharedInstanceRuntime(rows, byID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) populateShareEngineRuntimes(ctx context.Context, shares []core.AgentShare) error {
	if len(shares) == 0 {
		return nil
	}
	shareIDs := make([]string, 0, len(shares))
	agentIDSet := make(map[string]bool, len(shares))
	agentIDs := make([]string, 0, len(shares))
	for _, share := range shares {
		if share.ID != "" {
			shareIDs = append(shareIDs, share.ID)
		}
		if share.AgentID != "" && !agentIDSet[share.AgentID] {
			agentIDSet[share.AgentID] = true
			agentIDs = append(agentIDs, share.AgentID)
		}
	}
	hostRuntimes := make(map[string]map[core.Engine]core.RuntimeState, len(agentIDs))
	if len(agentIDs) > 0 {
		rows, err := s.pool.Query(ctx, `SELECT id, runtime FROM agents WHERE id = ANY($1::text[])`, agentIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var rt map[core.Engine]core.RuntimeState
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &rt); err != nil {
					rows.Close()
					return err
				}
			}
			hostRuntimes[id] = rt
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	type shareEngineState struct {
		configID       string
		configVersion  int
		running        bool
		uncertain      bool
		trafficSettled bool
	}
	states := make(map[string]map[core.Engine]shareEngineState, len(shareIDs))
	if len(shareIDs) > 0 {
		rows, err := s.pool.Query(ctx, `
			SELECT s.id, engine_name::text,
				COALESCE(inst.config_id, legacy.config_id, ''),
				COALESCE(inst.config_version, legacy.config_version, 0),
				COALESCE(inst.running, legacy.running, false),
				COALESCE(inst.uncertain, legacy.uncertain, false),
				COALESCE(inst.traffic_settled, legacy.traffic_settled, true)
			FROM agent_shares s
			JOIN agents a ON a.id = s.agent_id
			CROSS JOIN LATERAL unnest(s.engines) AS engine_name
			LEFT JOIN agent_shared_instance_ownership inst
				ON inst.agent_id = s.agent_id AND inst.engine = engine_name AND inst.share_id = s.id
			LEFT JOIN agent_engine_ownership legacy
				ON legacy.agent_id = s.agent_id AND legacy.engine = engine_name AND legacy.owner_id = s.user_id AND a.owner_id <> s.user_id
				   AND inst.share_id IS NULL
			WHERE s.id = ANY($1::text[])`, shareIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var shareID string
			var engine core.Engine
			var item shareEngineState
			if err := rows.Scan(&shareID, &engine, &item.configID, &item.configVersion, &item.running, &item.uncertain, &item.trafficSettled); err != nil {
				rows.Close()
				return err
			}
			if states[shareID] == nil {
				states[shareID] = make(map[core.Engine]shareEngineState)
			}
			states[shareID][engine] = item
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for index := range shares {
		share := &shares[index]
		if len(share.Engines) == 0 {
			continue
		}
		share.EngineRuntime = make(map[core.Engine]core.SharedCoreRuntime, len(share.Engines))
		for _, engine := range share.Engines {
			host := hostRuntimes[share.AgentID][engine]
			owned, hasOwned := states[share.ID][engine]
			if !hasOwned {
				owned.trafficSettled = true
			}
			status := "inactive"
			switch {
			case !host.Installed:
				status = "uninstalled"
			case owned.uncertain:
				status = "unknown"
			case owned.running:
				status = "active"
			}
			share.EngineRuntime[engine] = core.SharedCoreRuntime{
				ShareID:        share.ID,
				UserID:         share.UserID,
				Username:       share.Username,
				DisplayName:    share.DisplayName,
				Installed:      host.Installed,
				Version:        host.Version,
				ServiceStatus:  status,
				Deployed:       owned.configID != "",
				ConfigVersion:  owned.configVersion,
				Running:        owned.running,
				Uncertain:      owned.uncertain,
				TrafficSettled: owned.trafficSettled,
			}
		}
	}
	return nil
}
