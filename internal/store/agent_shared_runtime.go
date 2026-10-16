package store

import (
	"context"
	"encoding/json"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A recipient's status describes their private process. The base service may
// still be running for the node owner or another recipient. For node owners
// and administrators, per-recipient shared core statuses are attached under
// Runtime[engine].SharedInstances so host and multi-user kernel states stay
// explicitly separated.
func (s *Store) overlaySharedInstanceRuntime(ctx context.Context, agents []core.Agent) error {
	owner := scopeForConfig(ctx).OwnerID
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
		instanceScope := "shared-engine"
		if containsFeature(agent.Features, core.AgentFeatureSharedCoreInstances) {
			instanceScope = "shared-instance"
		}
		for engine, runtime := range agent.Runtime {
			runtime.ServiceStatus = "inactive"
			runtime.InstanceScope = instanceScope
			runtime.Deployed = false
			runtime.ConfigVersion = 0
			runtime.SharedInstances = nil
			agent.Runtime[engine] = runtime
		}
	}
	if len(recipientIDs) > 0 {
		rows, err := s.pool.Query(ctx, `SELECT agent_id,engine,config_id,config_version,running,uncertain FROM (
			SELECT agent_id,engine,config_id,config_version,running,uncertain,1 AS priority FROM agent_engine_ownership
			WHERE owner_id=$1 AND agent_id=ANY($2::text[])
			UNION ALL
			SELECT agent_id,engine,config_id,config_version,running,uncertain,2 AS priority FROM agent_shared_instance_ownership
			WHERE owner_id=$1 AND agent_id=ANY($2::text[])
		) owned ORDER BY priority`, owner, recipientIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, configID string
			var engine core.Engine
			var configVersion int
			var running, uncertain bool
			if err := rows.Scan(&id, &engine, &configID, &configVersion, &running, &uncertain); err != nil {
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
			runtime.Deployed = configID != ""
			runtime.ConfigVersion = configVersion
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
		if err := rows.Err(); err != nil {
			return err
		}
	}
	if len(managedIDs) > 0 {
		rows, err := s.pool.Query(ctx, `
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
			WHERE s.agent_id = ANY($1::text[])
			ORDER BY s.agent_id, engine_name, u.username, s.id`, managedIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var agentID, shareID, userID, username, displayName, configID string
			var engine core.Engine
			var configVersion int
			var running, uncertain, trafficSettled bool
			if err := rows.Scan(&agentID, &engine, &shareID, &userID, &username, &displayName,
				&configID, &configVersion, &running, &uncertain, &trafficSettled); err != nil {
				rows.Close()
				return err
			}
			agent := byID[agentID]
			if agent == nil {
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
		rows.Close()
		if err := rows.Err(); err != nil {
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
