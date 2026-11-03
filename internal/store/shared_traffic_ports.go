package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Reservations require an explicit allocation or unrestricted port authority.
// Drafts and validation never reserve ports or create firewall policies.
// The caller holds the Agent lock, serializing grants and deployments.
func (s *Store) setSharedPortsTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string, ports []int) error {
	var added []int
	if err := tx.QueryRow(ctx, `SELECT ARRAY(SELECT unnest($2::int[]) EXCEPT
		SELECT port FROM agent_share_ports WHERE share_id=$1)`, shareID, ports).Scan(&added); err != nil {
		return err
	}
	if err := s.checkSharedPortsAvailableTx(ctx, tx, shareID, userID, agentID, added); err != nil {
		return err
	}
	wanted := map[int]bool{}
	for _, port := range ports {
		wanted[port] = true
	}
	rows, err := tx.Query(ctx, `SELECT reserved.port,COALESCE(policy.id,''),COALESCE(policy.engine,'')
		FROM agent_share_ports reserved LEFT JOIN port_traffic_policies policy
		ON policy.agent_id=reserved.agent_id AND policy.port=reserved.port AND policy.share_id=reserved.share_id
		WHERE reserved.share_id=$1
		UNION ALL SELECT policy.port,policy.id,policy.engine FROM port_traffic_policies policy
		WHERE policy.share_id=$1 AND NOT EXISTS(SELECT 1 FROM agent_share_ports reserved
			WHERE reserved.share_id=$1 AND reserved.port=policy.port) ORDER BY 1`, shareID)
	if err != nil {
		return err
	}
	type priorPort struct {
		port             int
		policyID, engine string
	}
	var prior []priorPort
	for rows.Next() {
		var port priorPort
		if err := rows.Scan(&port.port, &port.policyID, &port.engine); err != nil {
			rows.Close()
			return err
		}
		prior = append(prior, port)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var unrestricted bool
	if err := tx.QueryRow(ctx, `SELECT ports_unrestricted FROM agent_shares WHERE id=$1`, shareID).Scan(&unrestricted); err != nil {
		return err
	}
	for _, port := range prior {
		if wanted[port.port] || unrestricted {
			continue
		}
		if port.policyID != "" {
			var stopped bool
			if err := tx.QueryRow(ctx, `SELECT (
				EXISTS(SELECT 1 FROM agent_shared_instance_ownership
					WHERE agent_id=$1 AND engine=$2 AND share_id=$3 AND NOT running AND NOT uncertain AND traffic_settled)
				OR (NOT EXISTS(SELECT 1 FROM agent_shared_instance_ownership
					WHERE agent_id=$1 AND engine=$2 AND share_id=$3)
					AND EXISTS(SELECT 1 FROM agent_engine_ownership
						WHERE agent_id=$1 AND engine=$2 AND NOT running AND NOT uncertain AND traffic_settled)))
				AND NOT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2 AND status IN ('pending','running'))`,
				agentID, port.engine, shareID).Scan(&stopped); err != nil {
				return err
			}
			if !stopped {
				return fmt.Errorf("%w: stop the %s core with successful traffic settlement before releasing reserved port %d", ErrConflict, port.engine, port.port)
			}
			// The allocation's total is deliberately retained. Old samples for
			// this policy cannot be accepted by a newly assigned policy ID.
			if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE id=$1`, port.policyID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_share_ports WHERE share_id=$1 AND port=$2`, shareID, port.port); err != nil {
			return err
		}
	}
	for _, port := range ports {
		var foreign bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports WHERE agent_id=$1 AND port=$2 AND share_id<>$3)`, agentID, port, shareID).Scan(&foreign); err != nil {
			return err
		}
		if foreign {
			return fmt.Errorf("%w: 端口 %d 已有授权分配，不能重复分配；具备不限端口权限的用户仍可申请使用空闲端口", ErrConflict, port)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_share_ports(agent_id,port,share_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`,
			agentID, port, shareID); err != nil {
			return mapError(err)
		}
	}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_share_ports WHERE agent_id=$1`, agentID).Scan(&total); err != nil {
		return err
	}
	if total > 256 {
		return fmt.Errorf("%w: an Agent supports at most 256 reserved shared ports", ErrConflict)
	}
	return nil
}

// Validation uses the same read-only conflict checks as reservation, without
// claiming a port or affecting another user's future deployment.
func (s *Store) checkSharedPortsAvailableTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string, ports []int, tasks ...*core.Task) error {
	if len(ports) == 0 {
		return nil
	}
	var engine core.Engine
	var candidate *core.Task
	if len(tasks) > 0 {
		candidate = tasks[0]
		engine = candidate.Engine
	}
	wanted := make(map[int]bool, len(ports))
	for _, port := range ports {
		wanted[port] = true
	}
	// Include queued deployment snapshots: a different core may be about
	// to start a foreign listener which has no shared traffic monitor yet.
	rows, err := tx.Query(ctx, `SELECT state.engine,COALESCE(revision.content,config.content,''),state.config_id,state.config_version,
			state.uncertain OR state.config_uncertain OR config.id IS NULL OR config.deleted_at IS NOT NULL
				OR (revision.config_id IS NULL AND config.version<>state.config_version),false,state.owner_id=$2
		FROM agent_engine_ownership state LEFT JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND (state.running OR state.uncertain) AND (state.owner_id<>$2 OR ($3<>'' AND state.engine<>$3))
		UNION ALL
		SELECT state.engine,COALESCE(revision.content,config.content,''),state.config_id,state.config_version,
			config.id IS NULL OR config.deleted_at IS NOT NULL
				OR (revision.config_id IS NULL AND config.version<>state.config_version),false,state.owner_id=$2
		FROM agent_shared_instance_ownership state LEFT JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND (state.running OR state.uncertain)
			AND state.config_id<>'' AND (state.owner_id<>$2 OR ($3<>'' AND state.engine<>$3))
		UNION ALL
		SELECT state.engine,COALESCE(revision.content,config.content,''),attempt.config_id,attempt.config_version,
			config.id IS NULL OR config.deleted_at IS NOT NULL
				OR (revision.config_id IS NULL AND config.version<>attempt.config_version),false,state.owner_id=$2
		FROM agent_shared_instance_ownership state LEFT JOIN tasks attempt ON attempt.id=state.last_task_id
		LEFT JOIN configs config ON config.id=attempt.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=attempt.config_id AND revision.version=attempt.config_version
		WHERE state.agent_id=$1 AND (state.running OR state.uncertain) AND (state.uncertain OR state.config_uncertain) AND attempt.action='deploy'
			AND (state.owner_id<>$2 OR ($3<>'' AND state.engine<>$3))
		UNION ALL
		SELECT state.engine,'','',0,true,false,state.owner_id=$2
		FROM agent_shared_instance_ownership state
		WHERE state.agent_id=$1 AND (state.running OR state.uncertain) AND (state.uncertain OR state.config_uncertain)
			AND (state.config_id='' OR state.config_uncertain)
			AND NOT EXISTS(SELECT 1 FROM tasks attempt WHERE attempt.id=state.last_task_id AND attempt.action='deploy')
			AND (state.owner_id<>$2 OR ($3<>'' AND state.engine<>$3))
		UNION ALL
		SELECT task.engine,COALESCE(task.config_content,''),COALESCE(task.config_id,''),COALESCE(task.config_version,0),COALESCE(task.config_content,'')='',true,COALESCE(config.owner_id,task.owner_id)=$2 FROM tasks task LEFT JOIN configs config ON config.id=task.config_id
		WHERE task.agent_id=$1 AND task.status IN ('pending','running') AND task.action IN ('deploy','import-existing','start','restart')
			AND (COALESCE(config.owner_id,task.owner_id)<>$2 OR ($3<>'' AND task.engine<>$3))`, agentID, userID, engine)
	if err != nil {
		return err
	}
	type deployment struct {
		engine      core.Engine
		content, id string
		version     int
		uncertain   bool
		pending     bool
		sameOwner   bool
	}
	var deployments []deployment
	for rows.Next() {
		var d deployment
		if err := rows.Scan(&d.engine, &d.content, &d.id, &d.version, &d.uncertain, &d.pending, &d.sameOwner); err != nil {
			rows.Close()
			return err
		}
		deployments = append(deployments, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, d := range deployments {
		if d.uncertain {
			return portConflict(ports[0], fmt.Errorf("%w: 无法确认实例的端口状态，请刷新节点状态后重试", ErrConflict))
		}
		content, err := s.decryptContent(d.content)
		if err != nil {
			return err
		}
		content, frontends, err := core.DecodeExecutionSnapshot(content)
		if err != nil {
			return err
		}
		if content == "" {
			return portConflict(ports[0], fmt.Errorf("%w: 无法确认待执行任务的监听端口，请等待任务结束并刷新状态", ErrConflict))
		}
		if len(frontends) == 0 {
			frontends, err = s.managedTLSFrontendsTx(ctx, tx, d.id, d.version, d.engine, content)
			if err != nil {
				return err
			}
		}
		conflict := func(port int) error {
			if d.pending {
				return portConflict(port, fmt.Errorf("%w: 端口 %d 有部署任务处理中，请等待任务结束后重试", ErrConflict, port))
			}
			return portConflict(port, fmt.Errorf("%w: 端口 %d 已被有效部署占用，请刷新状态或调整端口后重试。", ErrConflict, port))
		}
		for _, f := range frontends {
			ports := []int{f.PublicPort}
			if f.LocalOnly {
				ports = append(ports, f.BackendPort)
			}
			for _, port := range ports {
				if wanted[port] {
					compatible := false
					if d.sameOwner && candidate != nil && port == f.PublicPort && !f.LocalOnly {
						for _, next := range candidate.TLSFrontends {
							compatible = compatible || (!next.LocalOnly && next.PublicPort == port && next.Mode == f.Mode && next.Path != f.Path)
						}
						for _, endpoint := range serverconfig.DiscoverTrafficPorts(candidate.Engine, candidate.ConfigContent) {
							if endpoint.Port == port {
								compatible = false
							}
						}
					}
					if !compatible {
						return conflict(port)
					}
				}
			}
		}
		for _, endpoint := range serverconfig.DiscoverTrafficPorts(d.engine, content) {
			if wanted[endpoint.Port] {
				// Ordinary cores owned by the same principal may use separate
				// TCP/UDP sockets. Shared metering still protects both transports.
				if d.sameOwner && candidate != nil && candidate.SharedTrafficID == "" && !taskPortProtocolsOverlap(candidate, endpoint) {
					continue
				}
				return conflict(endpoint.Port)
			}
		}
	}
	return nil
}

// Unrestricted means no port allowlist, not unmetered execution. Reserve only
// the real fixed listeners as part of the deployment transaction, retaining
// old bindings until an explicit, settled release by the owner/administrator.
func (s *Store) reserveSharedDeploymentPortsTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string, endpoints []core.PortTrafficEndpoint) error {
	var unrestricted bool
	var ports []int
	if err := tx.QueryRow(ctx, `SELECT ports_unrestricted,
		ARRAY(SELECT port FROM agent_share_ports WHERE share_id=$1 ORDER BY port)
		FROM agent_shares WHERE id=$1`, shareID).Scan(&unrestricted, &ports); err != nil {
		return err
	}
	if !unrestricted {
		return nil
	}
	var requested []int
	for _, endpoint := range endpoints {
		requested = append(requested, endpoint.Port)
		var allocated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports WHERE agent_id=$1 AND port=$2)`, agentID, endpoint.Port).Scan(&allocated); err != nil {
			return err
		}
		if !allocated && !slices.Contains(ports, endpoint.Port) {
			ports = append(ports, endpoint.Port)
		}
	}
	if err := s.checkSharedPortsAvailableTx(ctx, tx, shareID, userID, agentID, requested); err != nil {
		return err
	}
	slices.Sort(ports)
	return s.setSharedPortsTx(ctx, tx, shareID, userID, agentID, ports)
}

// Adopt the exact running version, not a newer saved draft. Enabling a quota
// on an existing service must cover that service immediately after refresh.
func (s *Store) bindCurrentSharedDeploymentTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string) error {
	var uncertain bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership
		WHERE agent_id=$1 AND owner_id=$2 AND engine=ANY((SELECT engines FROM agent_shares WHERE id=$3)::text[])
			AND (uncertain OR (running AND config_uncertain)))`, agentID, userID, shareID).Scan(&uncertain); err != nil {
		return err
	}
	if uncertain {
		return fmt.Errorf("%w: stop the user's core before sharing an uncertain deployment", ErrConflict)
	}
	rows, err := tx.Query(ctx, `SELECT state.engine,COALESCE(revision.content,config.content),state.config_id,state.config_version
		FROM agent_engine_ownership state JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND state.owner_id=$2 AND state.running
			AND state.engine=ANY((SELECT engines FROM agent_shares WHERE id=$3)::text[])`, agentID, userID, shareID)
	if err != nil {
		return err
	}
	var endpoints []core.PortTrafficEndpoint
	type runningDeployment struct {
		engine      core.Engine
		content, id string
		version     int
	}
	var deployments []runningDeployment
	for rows.Next() {
		var deployment runningDeployment
		if err := rows.Scan(&deployment.engine, &deployment.content, &deployment.id, &deployment.version); err != nil {
			rows.Close()
			return err
		}
		deployments = append(deployments, deployment)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var frontends []core.TLSFrontend
	for _, deployment := range deployments {
		content, err := s.decryptContent(deployment.content)
		if err != nil {
			return err
		}
		ports, err := serverconfig.SharedTrafficEndpoints(deployment.engine, content)
		if err != nil {
			return fmt.Errorf("%w: stop or replace the current %s configuration before sharing: %v", ErrConflict, deployment.engine, err)
		}
		endpoints = append(endpoints, ports...)
		managed, err := s.managedTLSFrontendsTx(ctx, tx, deployment.id, deployment.version, deployment.engine, content)
		if err != nil {
			return err
		}
		frontends = append(frontends, managed...)
	}
	if err := s.reserveSharedDeploymentPortsTx(ctx, tx, shareID, userID, agentID, endpoints); err != nil {
		return err
	}
	if err := s.prepareTLSFrontendPortsTx(ctx, tx, &core.Task{AgentID: agentID, SharedTrafficID: shareID, Action: core.ActionDeploy, TLSFrontends: frontends}, userID); err != nil {
		return err
	}
	return bindSharedTrafficPortsTx(ctx, tx, shareID, agentID, endpoints)
}

func bindSharedTrafficPortsTx(ctx context.Context, tx pgx.Tx, shareID, agentID string, endpoints []core.PortTrafficEndpoint) error {
	return prepareSharedTrafficPortsTx(ctx, tx, shareID, agentID, endpoints, true)
}

func prepareSharedTrafficPortsTx(ctx context.Context, tx pgx.Tx, shareID, agentID string, endpoints []core.PortTrafficEndpoint, activate bool) error {
	for _, endpoint := range endpoints {
		var reserved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares s
			WHERE s.agent_id=$1 AND s.id=$3 AND $4=ANY(s.engines) AND
			(s.ports_unrestricted OR EXISTS(SELECT 1 FROM agent_share_ports p WHERE p.share_id=s.id AND p.port=$2)))`,
			agentID, endpoint.Port, shareID, endpoint.Engine).Scan(&reserved); err != nil {
			return err
		}
		if !reserved {
			return fmt.Errorf("%w: ask an administrator to reserve port %d for this user before deploying", ErrConflict, endpoint.Port)
		}
		var boundID string
		var engine core.Engine
		var quota bool
		err := tx.QueryRow(ctx, `SELECT COALESCE(share_id,''),engine,quota_enabled FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 FOR UPDATE`,
			agentID, endpoint.Port).Scan(&boundID, &engine, &quota)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if boundID != "" && (boundID != shareID || engine != endpoint.Engine) {
			if err := retireUnusedSharedPolicyTx(ctx, tx, agentID, endpoint.Port, boundID, engine); err != nil {
				return err
			}
			boundID = ""
		}
		if quota {
			return fmt.Errorf("%w: remove the existing per-port quota on port %d before using a shared allowance", ErrConflict, endpoint.Port)
		}
		if boundID != "" {
			// A previously retired port may return in a later deployment. Keep
			// its identity/history; the Agent reconciles the next source epoch.
			if _, err := tx.Exec(ctx, `UPDATE port_traffic_policies SET name=CASE WHEN config_id='' THEN $3 ELSE name END,monitoring_enabled=monitoring_enabled OR $4,updated_at=now()
				WHERE agent_id=$1 AND port=$2`, agentID, endpoint.Port, endpoint.Name, activate); err != nil {
				return err
			}
			continue
		}
		id, err := core.NewID("trf")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO port_traffic_policies
			(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,discovered,
			 monitoring_enabled,metadata_managed,traffic_history_initialized,share_id,share_generation,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,'both','monthly',CURRENT_DATE,0,false,false,false,true,true,true,$6,1,now(),now())
			ON CONFLICT(agent_id,port) DO UPDATE SET
				name=EXCLUDED.name,engine=EXCLUDED.engine,protocol='both',monitoring_enabled=true,discovered=false,metadata_managed=true,
				limit_bytes=0,auto_block=false,quota_enabled=false,share_id=$6,share_generation=port_traffic_policies.reset_generation+1,
				reset_generation=port_traffic_policies.reset_generation+1,share_used_bytes=0,
				received_bytes=0,sent_bytes=0,used_bytes=0,reported_received_bytes=0,reported_sent_bytes=0,receive_bps=0,send_bps=0,
				reported_lifetime_received_bytes=0,reported_lifetime_sent_bytes=0,counter_epoch='',accounting=NULL,
				last_reported_at=NULL,last_collected_at=NULL,period_start=NULL,period_end=NULL,blocked=false,
				enforcement_available=false,enforcement_error='',traffic_history_initialized=true,updated_at=now()`,
			id, agentID, endpoint.Name, endpoint.Engine, endpoint.Port, shareID); err != nil {
			return mapError(err)
		}
	}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM port_traffic_policies WHERE agent_id=$1 AND monitoring_enabled`, agentID).Scan(&total); err != nil {
		return err
	}
	if total > 256 {
		return fmt.Errorf("%w: an Agent supports at most 256 monitored ports", ErrConflict)
	}
	return nil
}

// A policy is a metering identity, never a port allocation. Retire it only
// after settlement (or when no task ever started). A fresh ID rejects delayed
// samples for the previous user while their cumulative share ledger survives.
func retireUnusedSharedPolicyTx(ctx context.Context, tx pgx.Tx, agentID string, port int, shareID string, engine core.Engine, readOnly ...bool) error {
	var safe bool
	if err := tx.QueryRow(ctx, `SELECT
		NOT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2
			AND shared_traffic_id=$3 AND status IN ('pending','running') AND action NOT IN ('validate','status'))
		AND NOT EXISTS(SELECT 1 FROM agent_shared_instance_ownership
			WHERE agent_id=$1 AND engine=$2 AND share_id=$3 AND (uncertain OR running AND config_uncertain OR
				(running OR NOT traffic_settled) AND EXISTS(SELECT 1 FROM port_traffic_policies
					WHERE agent_id=$1 AND port=$4 AND monitoring_enabled)))
		AND NOT EXISTS(SELECT 1 FROM agent_engine_ownership state JOIN agent_shares sh ON sh.user_id=state.owner_id
			WHERE state.agent_id=$1 AND state.engine=$2 AND sh.id=$3 AND (state.uncertain OR state.running AND state.config_uncertain OR
				(state.running OR NOT state.traffic_settled) AND EXISTS(SELECT 1 FROM port_traffic_policies
					WHERE agent_id=$1 AND port=$4 AND monitoring_enabled)))`,
		agentID, engine, shareID, port).Scan(&safe); err != nil {
		return err
	}
	if !safe {
		return portConflict(port, fmt.Errorf("%w: 端口 %d 的使用状态或流量结算尚未确认，请刷新节点状态后重试", ErrConflict, port))
	}
	if len(readOnly) > 0 && readOnly[0] {
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 AND share_id=$3`, agentID, port, shareID)
	return err
}

func taskPortProtocolsOverlap(task *core.Task, existing core.PortTrafficEndpoint) bool {
	for _, frontend := range task.TLSFrontends {
		if frontend.PublicPort == existing.Port || frontend.LocalOnly && frontend.BackendPort == existing.Port {
			return true
		}
	}
	matched := false
	for _, next := range serverconfig.DiscoverTrafficPorts(task.Engine, task.ConfigContent) {
		if next.Port != existing.Port {
			continue
		}
		matched = true
		if next.Protocol != core.TrafficProtocolTCP && next.Protocol != core.TrafficProtocolUDP ||
			existing.Protocol != core.TrafficProtocolTCP && existing.Protocol != core.TrafficProtocolUDP || next.Protocol == existing.Protocol {
			return true
		}
	}
	return !matched
}
