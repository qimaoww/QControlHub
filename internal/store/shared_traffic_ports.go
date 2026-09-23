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
	if err := s.checkSharedPortsAvailableTx(ctx, tx, shareID, userID, agentID, ports); err != nil {
		return err
	}
	wanted := map[int]bool{}
	for _, port := range ports {
		wanted[port] = true
	}
	rows, err := tx.Query(ctx, `SELECT reserved.port,COALESCE(policy.id,''),COALESCE(policy.engine,'')
		FROM agent_share_ports reserved LEFT JOIN port_traffic_policies policy
		ON policy.agent_id=reserved.agent_id AND policy.port=reserved.port AND policy.share_id=reserved.share_id
		WHERE reserved.share_id=$1 ORDER BY reserved.port`, shareID)
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
	for _, port := range prior {
		if wanted[port.port] {
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
func (s *Store) checkSharedPortsAvailableTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string, ports []int) error {
	if len(ports) == 0 {
		return nil
	}
	wanted := make(map[int]bool, len(ports))
	for _, port := range ports {
		wanted[port] = true
	}
	rows, err := tx.Query(ctx, `SELECT port,share_id FROM agent_share_ports
		WHERE agent_id=$1 AND port=ANY($2::int[]) ORDER BY port`, agentID, ports)
	if err != nil {
		return err
	}
	for rows.Next() {
		var port int
		var reservedFor string
		if err := rows.Scan(&port, &reservedFor); err != nil {
			rows.Close()
			return err
		}
		if reservedFor != shareID {
			rows.Close()
			return fmt.Errorf("%w: port %d is already reserved for another user", ErrConflict, port)
		}
		delete(wanted, port)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Existing reservations already authorize these ports. A foreign
	// uncertain service must not prevent revocation, quota edits or release.
	if len(wanted) == 0 {
		return nil
	}
	// Include queued deployment snapshots: a different core may be about
	// to start a foreign listener which has no shared traffic monitor yet.
	rows, err = tx.Query(ctx, `SELECT state.engine,COALESCE(revision.content,config.content,''),
			state.uncertain OR state.config_uncertain OR config.id IS NULL OR config.deleted_at IS NOT NULL
				OR (revision.config_id IS NULL AND config.version<>state.config_version)
		FROM agent_engine_ownership state LEFT JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND (state.running OR state.uncertain) AND state.owner_id<>$2
		UNION ALL
		SELECT state.engine,COALESCE(revision.content,config.content,''),
			state.uncertain OR state.config_uncertain OR config.id IS NULL OR config.deleted_at IS NOT NULL
				OR (revision.config_id IS NULL AND config.version<>state.config_version)
		FROM agent_shared_instance_ownership state LEFT JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND (state.running OR state.uncertain) AND state.owner_id<>$2
		UNION ALL
		SELECT task.engine,task.config_content,false FROM tasks task LEFT JOIN configs config ON config.id=task.config_id
		WHERE task.agent_id=$1 AND task.status IN ('pending','running') AND task.action IN ('deploy','import-existing')
			AND COALESCE(config.owner_id,task.owner_id)<>$2 AND task.config_content IS NOT NULL`, agentID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var engine core.Engine
		var content string
		var uncertain bool
		if err := rows.Scan(&engine, &content, &uncertain); err != nil {
			return err
		}
		if uncertain {
			return fmt.Errorf("%w: stop the uncertain %s core before reserving shared ports", ErrConflict, engine)
		}
		content, err = s.decryptContent(content)
		if err != nil {
			return err
		}
		for _, endpoint := range serverconfig.DiscoverTrafficPorts(engine, content) {
			if wanted[endpoint.Port] {
				return fmt.Errorf("%w: port %d belongs to another user's deployed configuration", ErrConflict, endpoint.Port)
			}
		}
	}
	return rows.Err()
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
	for _, endpoint := range endpoints {
		if !slices.Contains(ports, endpoint.Port) {
			ports = append(ports, endpoint.Port)
		}
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
	rows, err := tx.Query(ctx, `SELECT state.engine,COALESCE(revision.content,config.content)
		FROM agent_engine_ownership state JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND state.owner_id=$2 AND state.running
			AND state.engine=ANY((SELECT engines FROM agent_shares WHERE id=$3)::text[])`, agentID, userID, shareID)
	if err != nil {
		return err
	}
	var endpoints []core.PortTrafficEndpoint
	for rows.Next() {
		var engine core.Engine
		var content string
		if err := rows.Scan(&engine, &content); err != nil {
			rows.Close()
			return err
		}
		content, err = s.decryptContent(content)
		if err != nil {
			rows.Close()
			return err
		}
		ports, err := serverconfig.SharedTrafficEndpoints(engine, content)
		if err != nil {
			rows.Close()
			return fmt.Errorf("%w: stop or replace the current %s configuration before sharing: %v", ErrConflict, engine, err)
		}
		endpoints = append(endpoints, ports...)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := s.reserveSharedDeploymentPortsTx(ctx, tx, shareID, userID, agentID, endpoints); err != nil {
		return err
	}
	return bindSharedTrafficPortsTx(ctx, tx, shareID, agentID, endpoints)
}

func bindSharedTrafficPortsTx(ctx context.Context, tx pgx.Tx, shareID, agentID string, endpoints []core.PortTrafficEndpoint) error {
	for _, endpoint := range endpoints {
		var reserved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports p JOIN agent_shares s ON s.id=p.share_id
			WHERE p.agent_id=$1 AND p.port=$2 AND p.share_id=$3 AND $4=ANY(s.engines))`,
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
			return fmt.Errorf("%w: shared port %d is bound to a different allocation or core; stop and release it first", ErrConflict, endpoint.Port)
		}
		if quota {
			return fmt.Errorf("%w: remove the existing per-port quota on port %d before using a shared allowance", ErrConflict, endpoint.Port)
		}
		if boundID != "" {
			continue // Changes of config/name must not reset the shared counters.
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
