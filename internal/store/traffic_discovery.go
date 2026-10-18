package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Only confirmed applied revisions supply running listeners. The saved workspace
// is included separately so a scan can explain a change awaiting deployment.
type trafficConfiguration struct {
	Config         core.Config
	AppliedVersion int
	ShareID        string
	Uncertain      bool
	AppliedContent string
	Ownership      core.TrafficOwnership
	ServiceStatus  string
}

type TrafficSyncPreview struct {
	Snapshot   string                      `json:"snapshot"`
	Candidates []core.TrafficSyncCandidate `json:"candidates"`
	Endpoints  []core.PortTrafficEndpoint  `json:"endpoints"`
}

func (s *Store) trafficConfigurations(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, allOwners bool) ([]trafficConfiguration, error) {
	args := []any{}
	where := agentEngineAccessClause(ctx, "a.id", "c.engine", &args)
	if !allOwners {
		if scope, scoped := requestConfigScope(ctx); scoped && !scope.Admin {
			args = append(args, scope.OwnerID)
			where += fmt.Sprintf(" AND (c.owner_id=$%[1]d OR a.owner_id=$%[1]d)", len(args))
		}
	}
	rows, err := q.Query(ctx, `SELECT c.id,a.id,c.name,c.engine,c.content,c.version,c.updated_at,c.owner_id,
  COALESCE(state.config_version,0),COALESCE(state.share_id,''),COALESCE(state.uncertain,false),COALESCE(state.service_status,'unknown'),
  COALESCE(r.content,CASE WHEN c.version=state.config_version THEN c.content ELSE '' END),
 COALESCE(NULLIF(u.display_name,''),NULLIF(u.username,''),CASE WHEN c.owner_id='' THEN '管理员' WHEN starts_with(c.owner_id,'token_') THEN '令牌用户' ELSE '已删除用户' END)
 FROM configs c
 JOIN LATERAL (
  SELECT c.agent_id AS agent_id WHERE c.agent_id<>''
  UNION SELECT base.agent_id FROM agent_engine_ownership base WHERE base.config_id=c.id AND base.engine=c.engine AND base.owner_id=c.owner_id
  UNION SELECT sh.agent_id FROM agent_shared_instance_ownership sh WHERE sh.config_id=c.id AND sh.engine=c.engine AND sh.owner_id=c.owner_id
 ) target ON true
 JOIN agents a ON a.id=target.agent_id LEFT JOIN panel_users u ON u.id=c.owner_id
 LEFT JOIN LATERAL (
  SELECT CASE WHEN sh.traffic_config_confirmed THEN sh.config_version ELSE 0 END AS config_version,sh.share_id,sh.uncertain OR sh.config_uncertain AS uncertain,sh.service_status
  FROM agent_shared_instance_ownership sh JOIN agent_shares grant_ ON grant_.id=sh.share_id
  WHERE sh.agent_id=a.id AND sh.engine=c.engine AND sh.owner_id=c.owner_id AND sh.config_id=c.id
    AND grant_.user_id=c.owner_id AND grant_.enabled AND grant_.status='accepted' AND sh.engine=ANY(grant_.engines)
  UNION ALL
  SELECT CASE WHEN base.traffic_config_confirmed THEN base.config_version ELSE 0 END AS config_version,'' AS share_id,base.uncertain OR base.config_uncertain AS uncertain,CASE WHEN base.uncertain THEN 'unknown' WHEN base.running THEN 'active' ELSE 'inactive' END AS service_status
  FROM agent_engine_ownership base WHERE base.agent_id=a.id AND base.engine=c.engine
    AND base.owner_id=c.owner_id AND base.config_id=c.id
 ) state ON true
 LEFT JOIN config_revisions r ON r.config_id=c.id AND r.version=state.config_version
 WHERE c.deleted_at IS NULL AND a.revoked_at IS NULL`+where+` ORDER BY a.id,c.engine,c.owner_id,state.share_id`, args...)
	if err != nil {
		return nil, err
	}
	var result []trafficConfiguration
	for rows.Next() {
		var item trafficConfiguration
		if err = rows.Scan(&item.Config.ID, &item.Config.AgentID, &item.Config.Name, &item.Config.Engine, &item.Config.Content, &item.Config.Version, &item.Config.UpdatedAt, &item.Config.OwnerID, &item.AppliedVersion, &item.ShareID, &item.Uncertain, &item.ServiceStatus, &item.AppliedContent, &item.Ownership.OwnerName); err != nil {
			rows.Close()
			return nil, err
		}
		item.Ownership.OwnerID = item.Config.OwnerID
		item.Ownership.IsCurrentUser = item.Config.OwnerID == scopeForConfig(ctx).OwnerID
		result = append(result, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range result {
		item := &result[i]
		if item.Config.Content, err = s.decryptContent(item.Config.Content); err != nil {
			return nil, err
		}
		if item.AppliedContent != "" {
			if item.AppliedContent, err = s.decryptContent(item.AppliedContent); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func configurationTrafficEndpoints(config core.Config, content string, version int, shareID string) []core.PortTrafficEndpoint {
	endpoints := serverconfig.DiscoverTrafficPorts(config.Engine, content)
	for i := range endpoints {
		endpoints[i].AgentID = config.AgentID
		endpoints[i].ConfigID = config.ID
		endpoints[i].ConfigVersion = version
		endpoints[i].ConfigUpdatedAt = config.UpdatedAt
		endpoints[i].ShareID = shareID
		endpoints[i].OwnerID = config.OwnerID
	}
	return endpoints
}

func (s *Store) AppliedTrafficEndpoints(ctx context.Context) ([]core.PortTrafficEndpoint, error) {
	configurations, err := s.trafficConfigurations(ctx, s.pool, false)
	if err != nil {
		return nil, err
	}
	result := make([]core.PortTrafficEndpoint, 0)
	for _, c := range configurations {
		if c.AppliedVersion > 0 && c.AppliedContent != "" {
			result = append(result, c.endpoints(c.AppliedContent, c.AppliedVersion)...)
		}
	}
	return result, nil
}

func (s *Store) PreviewPortTrafficSync(ctx context.Context) (TrafficSyncPreview, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TrafficSyncPreview{}, err
	}
	defer tx.Rollback(ctx)
	preview, err := s.trafficSyncPreviewTx(ctx, tx)
	if err != nil {
		return preview, err
	}
	return preview, tx.Commit(ctx)
}

func (s *Store) trafficSyncPreviewTx(ctx context.Context, tx pgx.Tx) (TrafficSyncPreview, error) {
	result := TrafficSyncPreview{Candidates: []core.TrafficSyncCandidate{}, Endpoints: []core.PortTrafficEndpoint{}}
	configurations, err := s.trafficConfigurations(ctx, tx, false)
	if err != nil {
		return result, err
	}
	args := []any{}
	where := trafficAccessClause(ctx, "port_traffic_policies.agent_id", "port_traffic_policies.id", &args)
	rows, err := tx.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE true`+where+` ORDER BY agent_id,port`, args...)
	if err != nil {
		return result, err
	}
	policies := map[string]core.PortTrafficPolicy{}
	for rows.Next() {
		p, e := scanTrafficPolicy(rows)
		if e != nil {
			rows.Close()
			return result, e
		}
		policies[trafficPortKey(p.AgentID, p.Port)] = p
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	// Configuration identities are versioned independently of byte counters and
	// policy visibility, making retries idempotent while rejecting a stale draft.
	identities := make([]any, 0, len(configurations))
	covered := map[string]bool{}
	for _, c := range configurations {
		identities = append(identities, []any{c.Config.ID, c.Config.Version, c.Config.UpdatedAt, c.AppliedVersion, c.ShareID, c.Uncertain})
		active := c.endpoints(c.AppliedContent, c.AppliedVersion)
		draft := c.endpoints(c.Config.Content, c.Config.Version)
		if c.AppliedVersion == 0 {
			active = nil
		}
		for _, ep := range active {
			key := trafficPortKey(ep.AgentID, ep.Port)
			covered[key] = true
			candidate := core.TrafficSyncCandidate{PortTrafficEndpoint: ep, Kind: "new", DeploymentStatus: "applied"}
			if c.ShareID != "" && c.Config.OwnerID != scopeForConfig(ctx).OwnerID {
				candidate.BlockedReason = "共享实例由所属用户部署并同步；节点所有者可查看分配情况"
			}
			if p, ok := policies[key]; ok {
				if p.MonitoringEnabled {
					candidate.Kind = "unchanged"
				} else {
					candidate.Kind = "deleted"
				}
				if p.ShareID != ep.ShareID || p.Engine != ep.Engine {
					candidate.BlockedReason = "端口已绑定其他实例，请先处理端口冲突"
				}
			}
			var previousUpdated time.Time
			for _, p := range policies {
				if p.UpdatedAt.After(previousUpdated) && p.AgentID == ep.AgentID && p.Engine == ep.Engine && p.ShareID == ep.ShareID && p.Name == ep.Name && p.Port != ep.Port {
					candidate.Kind = "changed"
					candidate.PreviousPort = p.Port
					previousUpdated = p.UpdatedAt
				}
			}
			if c.AppliedContent != "" {
				result.Endpoints = append(result.Endpoints, ep)
			}
			if c.Uncertain || c.AppliedContent == "" {
				candidate.DeploymentStatus = "uncertain"
				candidate.BlockedReason = "部署状态尚未确认，请等待 Agent 确认后重新扫描"
			}
			result.Candidates = append(result.Candidates, candidate)
		}
		if c.Config.Version != c.AppliedVersion || c.Uncertain {
			for _, ep := range draft {
				unchanged := false
				for _, old := range active {
					if old.Port == ep.Port && old.Name == ep.Name && old.Protocol == ep.Protocol {
						unchanged = true
						break
					}
				}
				if unchanged {
					continue
				}
				candidate := core.TrafficSyncCandidate{PortTrafficEndpoint: ep, Kind: "new", DeploymentStatus: "pending", BlockedReason: "已保存但尚未确认部署，继续统计已生效端口"}
				for _, old := range active {
					if ep.Name == old.Name && ep.Port != old.Port {
						candidate.Kind = "changed"
						candidate.PreviousPort = old.Port
					}
				}
				result.Candidates = append(result.Candidates, candidate)
			}
		}
	}
	for key, p := range policies {
		if covered[key] {
			continue
		}
		for _, c := range configurations {
			if c.Config.ID == p.ConfigID || p.ShareID != "" && c.ShareID == p.ShareID && c.Config.Engine == p.Engine {
				p.TrafficOwnership = c.Ownership
				break
			}
		}
		candidate := core.TrafficSyncCandidate{PortTrafficEndpoint: core.PortTrafficEndpoint{TrafficOwnership: p.TrafficOwnership, AgentID: p.AgentID, Name: p.Name, Engine: p.Engine, Port: p.Port, Protocol: p.Protocol, ShareID: p.ShareID, ConfigID: p.ConfigID, ConfigVersion: p.ConfigVersion}, Kind: "removed", DeploymentStatus: "retained", BlockedReason: "已不在确认生效配置中；保留历史，端口预留由节点所有者管理"}
		result.Candidates = append(result.Candidates, candidate)
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		a, b := result.Candidates[i], result.Candidates[j]
		if a.AgentID != b.AgentID {
			return a.AgentID < b.AgentID
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.DeploymentStatus < b.DeploymentStatus
	})
	data, _ := json.Marshal(identities)
	sum := sha256.Sum256(data)
	result.Snapshot = hex.EncodeToString(sum[:])
	return result, nil
}

// The node locks serialize preview validation and writes with saves, task
// completion and allocation changes. Client supplied labels/instance IDs never
// authorize a policy; candidates are rebuilt inside this transaction.
func (s *Store) SyncPortTrafficSnapshot(ctx context.Context, snapshot string, selections []core.TrafficSyncSelection) ([]string, error) {
	if len(snapshot) != 64 || len(selections) == 0 || len(selections) > 4096 {
		return nil, fmt.Errorf("%w: scan and select ports before syncing", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:traffic-endpoints'))`); err != nil {
		return nil, err
	}
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	rows, err := tx.Query(ctx, `SELECT id FROM agents WHERE revoked_at IS NULL`+where+` ORDER BY id FOR UPDATE`, args...)
	if err != nil {
		return nil, err
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	preview, err := s.trafficSyncPreviewTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if snapshot != preview.Snapshot {
		return nil, fmt.Errorf("%w: 配置或部署版本已变化，请重新扫描", ErrConflict)
	}
	available := map[string]core.TrafficSyncCandidate{}
	for _, c := range preview.Candidates {
		if c.BlockedReason == "" {
			available[trafficPortKey(c.AgentID, c.Port)] = c
		}
	}
	seen := map[string]bool{}
	changed := map[string]bool{}
	for _, selection := range selections {
		key := trafficPortKey(selection.AgentID, selection.Port)
		c, ok := available[key]
		if !ok || seen[key] {
			return nil, fmt.Errorf("%w: 端口已失效、未部署或无权同步，请重新扫描", ErrConflict)
		}
		seen[key] = true
		if c.ShareID != "" {
			var reserved bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports p JOIN agent_shares sh ON sh.id=p.share_id
    WHERE p.agent_id=$1 AND p.port=$2 AND sh.id=$3 AND sh.enabled AND sh.status='accepted' AND $4=ANY(sh.engines)
      AND sh.user_id=$5)`, c.AgentID, c.Port, c.ShareID, c.Engine, scopeForConfig(ctx).OwnerID).Scan(&reserved); err != nil {
				return nil, err
			}
			if !reserved {
				return nil, fmt.Errorf("%w: 端口不在当前用户授权范围内", ErrConflict)
			}
			// Deployment installs shared protection before starting the new listener.
			// A scan must never create an unreserved personal or host policy.
			var active bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 AND share_id=$3 AND monitoring_enabled)`, c.AgentID, c.Port, c.ShareID).Scan(&active); err != nil {
				return nil, err
			}
			if active {
				continue
			}
			if err = bindSharedTrafficPortsTx(ctx, tx, c.ShareID, c.AgentID, []core.PortTrafficEndpoint{c.PortTrafficEndpoint}); err != nil {
				return nil, err
			}
			changed[c.AgentID] = true
		} else {
			if err = requireAgentAdministration(ctx, tx, c.AgentID); err != nil {
				return nil, err
			}
			if c.Kind == "unchanged" || c.Kind == "changed" {
				var active bool
				if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 AND monitoring_enabled)`, c.AgentID, c.Port).Scan(&active); err != nil {
					return nil, err
				}
				if active {
					continue
				}
			}
			id, e := core.NewID("trf")
			if e != nil {
				return nil, e
			}
			tag, e := tx.Exec(ctx, `INSERT INTO port_traffic_policies(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,discovered,traffic_history_initialized,created_at,updated_at)
    VALUES($1,$2,$3,$4,$5,$6,'monthly',date_trunc('month',now()),9223372036854775807,false,false,true,true,now(),now())
    ON CONFLICT(agent_id,port) DO UPDATE SET monitoring_enabled=true,quota_enabled=false,auto_block=false,blocked=false,updated_at=now()
    WHERE NOT port_traffic_policies.monitoring_enabled AND port_traffic_policies.share_id IS NULL`, id, c.AgentID, c.Name, c.Engine, c.Port, c.Protocol)
			if e != nil {
				return nil, mapError(e)
			}
			if tag.RowsAffected() > 0 {
				changed[c.AgentID] = true
			}
		}
	}
	var over bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM port_traffic_policies WHERE monitoring_enabled GROUP BY agent_id HAVING count(*)>256)`).Scan(&over); err != nil {
		return nil, err
	}
	if over {
		return nil, fmt.Errorf("%w: at most 256 monitored ports per Agent", ErrConflict)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	result := []string{}
	for id := range changed {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

// Retiring a listener keeps its policy and daily records, so personal history
// remains authorized through the original allocation. Reservations are retained
// until an explicit settled release; no automatic sync reallocates a port.
func (s *Store) finishDeployedTrafficTx(ctx context.Context, tx pgx.Tx, agentID string, engine core.Engine, shareID, configID string, version int, settled bool) error {
	if _, err := tx.Exec(ctx, `UPDATE agent_engine_ownership SET traffic_config_confirmed=true
 WHERE agent_id=$1 AND engine=$2 AND $3='' AND config_id=$4 AND config_version=$5 AND NOT config_uncertain;
 `, agentID, engine, shareID, configID, version); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_shared_instance_ownership SET traffic_config_confirmed=true
 WHERE agent_id=$1 AND engine=$2 AND share_id=$3 AND config_id=$4 AND config_version=$5 AND NOT config_uncertain`, agentID, engine, shareID, configID, version); err != nil {
		return err
	}
	var content string
	if err := tx.QueryRow(ctx, `SELECT content FROM config_revisions WHERE config_id=$1 AND version=$2`, configID, version).Scan(&content); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Archives can be deleted while an already leased deployment runs.
			// Still acknowledge its result, but retain all existing accounting
			// policies when the exact revision is no longer available.
			return nil
		}
		return err
	}
	content, err := s.decryptContent(content)
	if err != nil {
		return err
	}
	endpoints := serverconfig.DiscoverTrafficPorts(engine, content)
	ports := []int{}
	for _, ep := range endpoints {
		ports = append(ports, ep.Port)
	}
	_, err = s.applyDeployedTrafficTx(ctx, tx, agentID, engine, shareID, configID, version, endpoints, ports, true)
	return err
}

// The Agent settles old counters before acknowledging deployment. Settlement
// completeness does not determine whether the removed listener is still live.
func (s *Store) applyDeployedTrafficTx(ctx context.Context, tx pgx.Tx, agentID string, engine core.Engine, shareID, configID string, version int, endpoints []core.PortTrafficEndpoint, ports []int, activate bool) (bool, error) {
	var confirmed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2 AND $3='' AND config_id=$4 AND config_version=$5 AND NOT uncertain AND NOT config_uncertain
 UNION ALL SELECT 1 FROM agent_shared_instance_ownership WHERE agent_id=$1 AND engine=$2 AND share_id=$3 AND config_id=$4 AND config_version=$5 AND NOT uncertain AND NOT config_uncertain)`, agentID, engine, shareID, configID, version).Scan(&confirmed); err != nil {
		return false, err
	}
	if !confirmed {
		return false, nil
	}
	// Older rows have no explicit config binding. Prove membership using only
	// revisions of this configuration up to the confirmed applied revision.
	historicalPorts := []int{}
	var needsLegacyEvidence bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM port_traffic_policies WHERE agent_id=$1 AND engine=$2 AND COALESCE(share_id,'')=$3 AND monitoring_enabled AND config_id='' AND NOT(port=ANY($4::int[])))`, agentID, engine, shareID, ports).Scan(&needsLegacyEvidence); err != nil {
		return false, err
	}
	if needsLegacyEvidence {
		rows, err := tx.Query(ctx, `SELECT content FROM config_revisions WHERE config_id=$1 AND version<=$2`, configID, version)
		if err != nil {
			return false, err
		}
		var revisions []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return false, err
			}
			revisions = append(revisions, value)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
		for _, value := range revisions {
			content, err := s.decryptContent(value)
			if err != nil {
				return false, err
			}
			for _, ep := range serverconfig.DiscoverTrafficPorts(engine, content) {
				historicalPorts = append(historicalPorts, ep.Port)
			}
		}
	}
	changed := false
	result, err := tx.Exec(ctx, `UPDATE port_traffic_policies SET monitoring_enabled=false,receive_bps=0,send_bps=0,blocked=false,
 reset_generation=reset_generation+1,updated_at=now()
 WHERE agent_id=$1 AND engine=$2 AND COALESCE(share_id,'')=$3 AND monitoring_enabled AND NOT(port=ANY($4::int[]))
 AND (config_id=$5 OR config_id='' AND port=ANY($6::int[]))`, agentID, engine, shareID, ports, configID, historicalPorts)
	if err != nil {
		return false, err
	}
	changed = result.RowsAffected() > 0
	for _, ep := range endpoints {
		// Shared reservations meter both transports even when a listener currently
		// exposes only TCP; retain the allocation's existing accounting contract.
		if shareID != "" {
			ep.Protocol = core.TrafficProtocolBoth
		}
		if activate && shareID == "" {
			id, err := core.NewID("trf")
			if err != nil {
				return false, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO port_traffic_policies(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,discovered,traffic_history_initialized,created_at,updated_at)
   VALUES($1,$2,$3,$4,$5,$6,'monthly',date_trunc('month',now()),9223372036854775807,false,false,true,true,now(),now()) ON CONFLICT(agent_id,port) DO NOTHING`, id, agentID, ep.Name, engine, ep.Port, ep.Protocol); err != nil {
				return false, mapError(err)
			}
		}
		// A changed identity starts a new reporting generation. Retain accepted
		// bytes, daily history, quotas and allocation totals; clear raw baselines.
		result, err = tx.Exec(ctx, `WITH desired AS (
 SELECT id,(config_id<>'' AND (config_id<>$4 OR inbound_tag<>$6 OR protocol<>$7) OR NOT monitoring_enabled AND $8) AS changed
 FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 AND engine=$9 AND COALESCE(share_id,'')=$3
 AND (config_id<>$4 OR config_version<>$5 OR inbound_tag<>$6 OR protocol<>$7 OR NOT monitoring_enabled AND $8) FOR UPDATE)
 UPDATE port_traffic_policies p SET
 reset_generation=p.reset_generation+CASE WHEN d.changed THEN 1 ELSE 0 END,
 reported_received_bytes=CASE WHEN d.changed THEN 0 ELSE p.reported_received_bytes END,
 reported_sent_bytes=CASE WHEN d.changed THEN 0 ELSE p.reported_sent_bytes END,
 reported_lifetime_received_bytes=CASE WHEN d.changed THEN 0 ELSE p.reported_lifetime_received_bytes END,
 reported_lifetime_sent_bytes=CASE WHEN d.changed THEN 0 ELSE p.reported_lifetime_sent_bytes END,
 counter_epoch=CASE WHEN d.changed THEN '' ELSE p.counter_epoch END,
 accounting=CASE WHEN d.changed THEN NULL ELSE p.accounting END,
 last_collected_at=CASE WHEN d.changed THEN NULL ELSE p.last_collected_at END,
 last_reported_at=CASE WHEN d.changed THEN NULL ELSE p.last_reported_at END,
 receive_bps=CASE WHEN d.changed THEN 0 ELSE p.receive_bps END,
 send_bps=CASE WHEN d.changed THEN 0 ELSE p.send_bps END,
 monitoring_enabled=p.monitoring_enabled OR $8,config_id=$4,config_version=$5,inbound_tag=$6,
 name=CASE WHEN NOT p.metadata_managed AND NOT p.quota_enabled THEN $6 ELSE p.name END,protocol=$7,updated_at=now()
 FROM desired d WHERE p.id=d.id`, agentID, ep.Port, shareID, configID, version, ep.Name, ep.Protocol, activate, engine)
		if err != nil {
			return false, err
		}
		changed = changed || result.RowsAffected() > 0
	}
	return changed, nil
}

// RepairAppliedTrafficPolicies corrects old residue after upgrade/reconnect.
// Locks prevent a previously read discovery snapshot from undoing a deployment.
// Current monitors require a confirmed listener. Retirement only disables
// collection; retained usage, history, quota and shared protection stay intact.
func (s *Store) RepairAppliedTrafficPolicies(ctx context.Context, agentID string) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	args := []any{agentID}
	where := agentAccessClause(ctx, "agents.id", &args)
	rows, err := tx.Query(ctx, `SELECT id FROM agents WHERE revoked_at IS NULL AND ($1='' OR id=$1)`+where+` ORDER BY id FOR UPDATE`, args...)
	if err != nil {
		return nil, err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	configs, err := s.trafficConfigurations(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	changedAgents := map[string]bool{}
	active := map[string][]core.PortTrafficEndpoint{}
	for _, c := range configs {
		if c.AppliedVersion > 0 && c.AppliedContent != "" {
			for _, ep := range c.endpoints(c.AppliedContent, c.AppliedVersion) {
				key := trafficPortKey(ep.AgentID, ep.Port)
				active[key] = append(active[key], ep)
			}
		}
	}
	policyArgs := []any{agentID}
	policyWhere := trafficAccessClause(ctx, "port_traffic_policies.agent_id", "port_traffic_policies.id", &policyArgs)
	residue, err := tx.Query(ctx, `SELECT id,agent_id,port,engine,config_id FROM port_traffic_policies WHERE monitoring_enabled AND share_id IS NULL AND ($1='' OR agent_id=$1)`+policyWhere+` ORDER BY id FOR UPDATE`, policyArgs...)
	if err != nil {
		return nil, err
	}
	type stalePolicy struct {
		id, agent, config string
		port              int
		engine            core.Engine
	}
	var stale []stalePolicy
	for residue.Next() {
		var p stalePolicy
		if err := residue.Scan(&p.id, &p.agent, &p.port, &p.engine, &p.config); err != nil {
			residue.Close()
			return nil, err
		}
		confirmed := false
		for _, ep := range active[trafficPortKey(p.agent, p.port)] {
			// A previous revision of this configuration can still be rebound by
			// the repair below. Another engine or instance is never evidence.
			confirmed = confirmed || p.engine == ep.Engine && ep.ShareID == "" && (p.config == "" || p.config == ep.ConfigID)
		}
		if !confirmed {
			stale = append(stale, p)
		}
	}
	residue.Close()
	if err := residue.Err(); err != nil {
		return nil, err
	}
	for _, p := range stale {
		if _, err := tx.Exec(ctx, `UPDATE port_traffic_policies SET monitoring_enabled=false,receive_bps=0,send_bps=0,blocked=false,reset_generation=reset_generation+1,updated_at=now() WHERE id=$1`, p.id); err != nil {
			return nil, err
		}
		changedAgents[p.agent] = true
	}
	for _, c := range configs {
		if agentID != "" && c.Config.AgentID != agentID || c.Uncertain || c.AppliedVersion == 0 || c.AppliedContent == "" {
			continue
		}
		endpoints := c.endpoints(c.AppliedContent, c.AppliedVersion)
		ports := []int{}
		for _, ep := range endpoints {
			ports = append(ports, ep.Port)
		}
		changed, err := s.applyDeployedTrafficTx(ctx, tx, c.Config.AgentID, c.Config.Engine, c.ShareID, c.Config.ID, c.AppliedVersion, endpoints, ports, false)
		if err != nil {
			return nil, err
		}
		if changed {
			changedAgents[c.Config.AgentID] = true
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(changedAgents))
	for id := range changedAgents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
