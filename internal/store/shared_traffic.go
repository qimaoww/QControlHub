package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func trafficAccessClause(ctx context.Context, agentColumn, policyColumn string, args *[]any) string {
	clause := agentAccessClause(ctx, agentColumn, args)
	if scopeForConfig(ctx).Admin {
		return clause
	}
	*args = append(*args, scopeForConfig(ctx).OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	return clause + ` AND (NOT EXISTS(SELECT 1 FROM panel_users traffic_user WHERE traffic_user.id=` + owner + `)
		OR EXISTS(SELECT 1 FROM agents owned_agent WHERE owned_agent.id=` + agentColumn + ` AND owned_agent.owner_id=` + owner + `)
		OR EXISTS(SELECT 1 FROM port_traffic_policies owned_policy JOIN agent_shares owned_share ON owned_share.id=owned_policy.share_id
			WHERE owned_policy.id=` + policyColumn + ` AND owned_share.user_id=` + owner + `))`
}

func lockTrafficMutationPolicy(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	if err := lockAgentUser(ctx, tx); err != nil {
		return "", err
	}
	var agentID string
	if err := tx.QueryRow(ctx, `SELECT agent_id FROM port_traffic_policies WHERE id=$1`, id).Scan(&agentID); err != nil {
		return "", mapError(err)
	}
	if err := requireAgentAdministration(ctx, tx, agentID); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&agentID); err != nil {
		return "", mapError(err)
	}
	var shareID string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(share_id,'') FROM port_traffic_policies WHERE id=$1 FOR UPDATE`, id).Scan(&shareID); err != nil {
		return "", mapError(err)
	}
	if shareID != "" {
		return "", fmt.Errorf("%w: reserved shared-port accounting cannot be reset, disabled or edited; manage its user allocation instead", ErrConflict)
	}
	return agentID, nil
}

// A reservation is administrator-controlled, independent of saved drafts.
// It prevents an untrusted shared user from creating a firewall rule for an
// unrelated host service just by entering its port in a configuration.
func (s *Store) setSharedPortsTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string, ports []int) error {
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
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership
				WHERE agent_id=$1 AND engine=$2 AND NOT running AND NOT uncertain AND traffic_settled)
				AND NOT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2 AND status IN ('pending','running'))`,
				agentID, port.engine).Scan(&stopped); err != nil {
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
		var current string
		err := tx.QueryRow(ctx, `SELECT share_id FROM agent_share_ports WHERE agent_id=$1 AND port=$2`, agentID, port).Scan(&current)
		if err == nil && current != shareID {
			return fmt.Errorf("%w: port %d is already reserved for another user", ErrConflict, port)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
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
	// A new allocation must not appropriate an already deployed foreign
	// listener, even when that older deployment had no traffic monitor.
	rows, err = tx.Query(ctx, `SELECT state.engine,COALESCE(revision.content,config.content)
		FROM agent_engine_ownership state JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND state.running AND state.owner_id<>$2`, agentID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var engine core.Engine
		var content string
		if err := rows.Scan(&engine, &content); err != nil {
			return err
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

// Adopt the exact running version, not a newer saved draft. Enabling a quota
// on an existing service must cover that service immediately after refresh.
func (s *Store) bindCurrentSharedDeploymentTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string) error {
	var uncertain bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership
		WHERE agent_id=$1 AND owner_id=$2 AND (uncertain OR (running AND config_uncertain)))`, agentID, userID).Scan(&uncertain); err != nil {
		return err
	}
	if uncertain {
		return fmt.Errorf("%w: stop the user's core before sharing an uncertain deployment", ErrConflict)
	}
	rows, err := tx.Query(ctx, `SELECT state.engine,COALESCE(revision.content,config.content)
		FROM agent_engine_ownership state JOIN configs config ON config.id=state.config_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.agent_id=$1 AND state.owner_id=$2 AND state.running`, agentID, userID)
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
	return bindSharedTrafficPortsTx(ctx, tx, shareID, agentID, endpoints)
}

func bindSharedTrafficPortsTx(ctx context.Context, tx pgx.Tx, shareID, agentID string, endpoints []core.PortTrafficEndpoint) error {
	for _, endpoint := range endpoints {
		var reserved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports WHERE agent_id=$1 AND port=$2 AND share_id=$3)`,
			agentID, endpoint.Port, shareID).Scan(&reserved); err != nil {
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

// Starting a retained config does not prove which listeners are on disk after
// a failed deployment, nor restore billing after reserved ports were released.
// Shared cores must be restarted by deploying an authorized exact snapshot.
func requireSafeEngineStart(ctx context.Context, tx pgx.Tx, agentID string, engine core.Engine) error {
	var unsafe bool
	if err := tx.QueryRow(ctx, `SELECT `+unsafeEngineStartSQL("$1", "$2"), agentID, engine).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("%w: redeploy the user's configuration instead of starting a shared or uncertain core", ErrConflict)
	}
	return nil
}

func unsafeEngineStartSQL(agent, engine string) string {
	return `EXISTS(SELECT 1 FROM agent_engine_ownership state JOIN agents a ON a.id=state.agent_id
		LEFT JOIN panel_users u ON u.id=state.owner_id
		WHERE state.agent_id=` + agent + ` AND state.engine=` + engine + `
		AND (state.config_uncertain OR COALESCE(u.disabled OR (u.role='user' AND a.owner_id<>u.id),false)))
		OR EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.agent_id=` + agent + ` AND p.engine=` + engine + ` AND p.share_id IS NOT NULL)`
}

func (s *Store) prepareSharedTaskTx(ctx context.Context, tx pgx.Tx, task *core.Task, configOwner string, features []string, runtime core.RuntimeState) error {
	var isolated, disabled bool
	err := tx.QueryRow(ctx, `SELECT u.role='user' AND a.owner_id<>u.id,u.disabled
		FROM panel_users u JOIN agents a ON a.id=$2 WHERE u.id=$1`, configOwner, task.AgentID).Scan(&isolated, &disabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if disabled {
		return fmt.Errorf("%w: configuration owner is disabled", ErrConflict)
	}
	if isolated && task.Action == core.ActionImportExisting {
		return fmt.Errorf("%w: stop the existing service and deploy the isolated user's configuration instead of importing it", ErrConflict)
	}
	if task.Action == core.ActionDeploy {
		var owner string
		var running, uncertain, ownerIsolated bool
		err := tx.QueryRow(ctx, `SELECT state.owner_id,state.running,state.uncertain,COALESCE(u.role='user' AND a.owner_id<>u.id,false)
			FROM agent_engine_ownership state JOIN agents a ON a.id=state.agent_id LEFT JOIN panel_users u ON u.id=state.owner_id
			WHERE state.agent_id=$1 AND state.engine=$2`, task.AgentID, task.Engine).Scan(&owner, &running, &uncertain, &ownerIsolated)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		untrackedActive := strings.EqualFold(runtime.ServiceStatus, "active") || strings.EqualFold(runtime.ServiceStatus, "running") || runtime.ExistingConfigAvailable
		if (err == nil && (running || uncertain) && owner != configOwner && (isolated || ownerIsolated)) ||
			(isolated && errors.Is(err, pgx.ErrNoRows) && untrackedActive) {
			return fmt.Errorf("%w: this core belongs to another or untracked deployment; an administrator must stop it before reassignment", ErrConflict)
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t LEFT JOIN configs c ON c.id=t.config_id
			LEFT JOIN panel_users u ON u.id=COALESCE(c.owner_id,t.owner_id) JOIN agents a ON a.id=t.agent_id
			WHERE t.agent_id=$1 AND t.engine=$2 AND t.status IN ('pending','running')
			AND t.action NOT IN ('validate','status') AND COALESCE(c.owner_id,t.owner_id)<>$3
			AND ($4::boolean OR COALESCE(u.role='user' AND a.owner_id<>u.id,false)))`,
			task.AgentID, task.Engine, configOwner, isolated).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return fmt.Errorf("%w: this core has another user's pending or running task", ErrConflict)
		}
	}
	endpoints := serverconfig.DiscoverTrafficPorts(task.Engine, task.ConfigContent)
	if isolated {
		if !containsFeature(features, core.AgentFeatureSharedTraffic) {
			return fmt.Errorf("%w: upgrade the Agent before shared deployments", ErrConflict)
		}
		endpoints, err = serverconfig.SharedTrafficEndpoints(task.Engine, task.ConfigContent)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		var limit, used uint64
		if err := tx.QueryRow(ctx, `SELECT id,limit_bytes,used_bytes FROM agent_shares WHERE user_id=$1 AND agent_id=$2 AND enabled AND status='accepted'`,
			configOwner, task.AgentID).Scan(&task.SharedTrafficID, &limit, &used); err != nil {
			return mapError(err)
		}
		if limit > 0 && used >= limit {
			return fmt.Errorf("%w: the user's cumulative Agent traffic allowance is exhausted", ErrConflict)
		}
		if task.Action == core.ActionDeploy {
			return bindSharedTrafficPortsTx(ctx, tx, task.SharedTrafficID, task.AgentID, endpoints)
		}
	}
	// An unrestricted operator must not accidentally deploy over another
	// user's reserved ports. Administrators can explicitly release/reassign.
	for _, endpoint := range endpoints {
		var foreign bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports reserved JOIN agent_shares share ON share.id=reserved.share_id
			WHERE reserved.agent_id=$1 AND reserved.port=$2 AND share.user_id<>$3)`, task.AgentID, endpoint.Port, configOwner).Scan(&foreign); err != nil {
			return err
		}
		if foreign {
			return fmt.Errorf("%w: port %d is reserved for another user", ErrConflict, endpoint.Port)
		}
	}
	return nil
}

// Claiming a lifecycle task makes its outcome uncertain until acknowledgement.
// A failed/lost result must not authorize another user to take over a core.
func markEngineExecutionTx(ctx context.Context, tx pgx.Tx, task core.Task) error {
	switch task.Action {
	case core.ActionDeploy, core.ActionImportExisting, core.ActionStart, core.ActionRestart, core.ActionStop:
		_, err := tx.Exec(ctx, `INSERT INTO agent_engine_ownership
			(agent_id,engine,owner_id,config_id,config_version,running,uncertain,config_uncertain,traffic_settled,updated_at)
			SELECT t.agent_id,t.engine,COALESCE(c.owner_id,t.owner_id),COALESCE(t.config_id,''),COALESCE(t.config_version,0),
				true,true,t.action IN ('deploy','import-existing'),false,now()
			FROM tasks t LEFT JOIN configs c ON c.id=t.config_id WHERE t.id=$1
			ON CONFLICT(agent_id,engine) DO UPDATE SET uncertain=true,
				config_uncertain=agent_engine_ownership.config_uncertain OR EXCLUDED.config_uncertain,
				traffic_settled=false,updated_at=now()`, task.ID)
		return err
	}
	return nil
}

func recordEngineOwnershipTx(ctx context.Context, tx pgx.Tx, taskID string, action core.Action, settled bool) error {
	switch action {
	case core.ActionDeploy, core.ActionImportExisting:
		_, err := tx.Exec(ctx, `INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,running,updated_at)
			SELECT t.agent_id,t.engine,COALESCE(c.owner_id,t.owner_id),t.config_id,t.config_version,true,now()
			FROM tasks t LEFT JOIN configs c ON c.id=t.config_id WHERE t.id=$1
			ON CONFLICT(agent_id,engine) DO UPDATE SET owner_id=EXCLUDED.owner_id,config_id=EXCLUDED.config_id,
				config_version=EXCLUDED.config_version,running=true,uncertain=false,config_uncertain=false,traffic_settled=false,updated_at=now()`, taskID)
		return err
	case core.ActionStop, core.ActionStart, core.ActionRestart:
		_, err := tx.Exec(ctx, `INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,running,updated_at)
			SELECT agent_id,engine,'','',0,$2,now() FROM tasks WHERE id=$1
			ON CONFLICT(agent_id,engine) DO UPDATE SET running=$2,uncertain=false,traffic_settled=$3,updated_at=now()`, taskID, action != core.ActionStop, action == core.ActionStop && settled)
		return err
	}
	return nil
}

func applySharedTrafficUsageTx(ctx context.Context, tx pgx.Tx, agentID string, usages []core.PortTrafficUsage) error {
	var shared []core.PortTrafficUsage
	for _, usage := range usages {
		if usage.ShareID != "" {
			shared = append(shared, usage)
		}
	}
	if len(shared) == 0 {
		return nil
	}
	payload, err := json.Marshal(shared)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH input AS (
		SELECT * FROM jsonb_to_recordset($2::jsonb) AS i(policy_id text,share_id text,share_used_bytes bigint)
	), deltas AS MATERIALIZED (
		SELECT p.id,p.share_id,i.share_used_bytes-p.share_used_bytes AS delta
		FROM port_traffic_policies p JOIN input i ON i.policy_id=p.id AND i.share_id=p.share_id
		WHERE p.agent_id=$1 AND p.monitoring_enabled AND i.share_used_bytes>p.share_used_bytes
	), written AS (
		UPDATE port_traffic_policies p SET share_used_bytes=p.share_used_bytes+d.delta
		FROM deltas d WHERE p.id=d.id RETURNING p.share_id,d.delta
	), totals AS (SELECT share_id,SUM(delta) AS delta FROM written GROUP BY share_id)
	UPDATE agent_shares share SET used_bytes=LEAST(9223372036854775807::numeric,share.used_bytes::numeric+totals.delta)::bigint,
		updated_at=now() FROM totals WHERE share.id=totals.share_id`, agentID, payload)
	return err
}

// Called while the Agent row is locked. A revoked/disabled principal and
// stale tasks created before isolation must never be dispatched on reconnect.
// Invalidating running leases also prevents later reacceptance from reviving
// pre-revocation work. Execution already in flight may remain uncertain.
func cancelUnauthorizedAgentTasksTx(ctx context.Context, tx pgx.Tx, agentID string, features []string) error {
	_, err := tx.Exec(ctx, `UPDATE tasks t SET status=CASE WHEN t.status='running' THEN 'failed' ELSE 'canceled' END,
		error=CASE WHEN t.status='running' THEN 'Agent sharing authorization changed; previous execution is unknown'
			ELSE 'Agent sharing authorization changed; submit a new task' END,
		finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE t.agent_id=$1 AND t.status IN ('pending','running') AND (
			(t.status='pending' AND t.action IN ('start','restart') AND (`+unsafeEngineStartSQL("t.agent_id", "t.engine")+`))
			OR (`+unauthorizedTaskPrincipalSQL+`)
			OR (t.shared_traffic_id<>'' AND (
				NOT $2::boolean OR NOT EXISTS(
					SELECT 1 FROM agent_shares s JOIN panel_users u ON u.id=s.user_id
					WHERE s.id=t.shared_traffic_id AND NOT u.disabled AND s.enabled AND s.status='accepted'
						AND (s.limit_bytes=0 OR s.used_bytes<s.limit_bytes)
				)
			))
		)`,
		agentID, containsFeature(features, core.AgentFeatureSharedTraffic))
	return err
}

const unauthorizedTaskPrincipalSQL = `EXISTS(SELECT 1 FROM panel_users u JOIN agents a ON a.id=t.agent_id
	WHERE u.id=t.owner_id AND (u.disabled OR (u.role='user' AND a.owner_id<>u.id AND
		(t.action NOT IN ('deploy','validate','status') OR (t.action IN ('deploy','validate') AND t.shared_traffic_id='')
		 OR NOT EXISTS(SELECT 1 FROM agent_shares s WHERE s.user_id=u.id AND s.agent_id=t.agent_id AND s.enabled AND s.status='accepted')))))
	OR EXISTS(SELECT 1 FROM configs c JOIN panel_users u ON u.id=c.owner_id JOIN agents a ON a.id=t.agent_id
		WHERE c.id=t.config_id AND (u.disabled OR (u.role='user' AND a.owner_id<>u.id AND t.shared_traffic_id='')))`
