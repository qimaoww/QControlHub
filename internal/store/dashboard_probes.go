package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const dashboardProbeMaxRetainedSamples = 30

func dashboardProbeDeadline(now time.Time, params core.DiagnosticParameters, hopCount int) time.Time {
	count := max(1, params.Count)
	perProbeMS := max(100, params.IntervalMS) + max(100, params.TimeoutMS)
	rounds := max(1, min(hopCount, 4))
	span := time.Duration(count*perProbeMS*rounds)*time.Millisecond + 30*time.Second
	if span < 60*time.Second {
		span = 60 * time.Second
	}
	return now.Add(span)
}

func pairEndpoints(pair core.DashboardProbePair) []core.DiagnosticEndpoint {
	if len(pair.Nodes) >= 2 {
		out := make([]core.DiagnosticEndpoint, len(pair.Nodes))
		copy(out, pair.Nodes)
		return out
	}
	return []core.DiagnosticEndpoint{pair.Node1, pair.Node2}
}

func endpointsEqual(a, b []core.DiagnosticEndpoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].AgentID != b[i].AgentID || a[i].InstanceID != b[i].InstanceID || a[i].Address != b[i].Address {
			return false
		}
	}
	return true
}

type probeEndpointMeta struct {
	exists      bool
	name        string
	region      string
	ownerID     string
	online      bool
	granted     bool
	hasDiagFeat bool
	hasPing     bool
	pingError   string
	addresses   []string
}

func loadProbeEndpointMeta(ctx context.Context, ex storeExecutor, agentID, instanceID string, now time.Time) (probeEndpointMeta, error) {
	if agentID == "" {
		return probeEndpointMeta{}, nil
	}
	var meta probeEndpointMeta
	var labelsJSON, featuresJSON, capsJSON []byte
	var lastSeen time.Time
	var offlineThreshold int
	err := ex.QueryRow(ctx, `
		SELECT a.name, a.owner_id, a.labels, a.features, a.last_seen, `+strings.ReplaceAll(agentOfflineThresholdSQL, "agents.", "a.")+`, COALESCE(c.capabilities, '{}'::jsonb)
		FROM agents a
		LEFT JOIN network_diagnostic_capabilities c ON c.agent_id = a.id
		WHERE a.id = $1 AND a.revoked_at IS NULL`, agentID).Scan(
		&meta.name, &meta.ownerID, &labelsJSON, &featuresJSON, &lastSeen, &offlineThreshold, &capsJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return meta, nil
	}
	if err != nil {
		return meta, err
	}
	meta.exists = true
	meta.online = lastSeen.After(now.Add(-time.Duration(offlineThreshold) * time.Second))
	var labels map[string]string
	if len(labelsJSON) > 0 {
		_ = json.Unmarshal(labelsJSON, &labels)
	}
	meta.region = strings.ToUpper(strings.TrimSpace(labels["region_code"]))
	var features []string
	if len(featuresJSON) > 0 {
		_ = json.Unmarshal(featuresJSON, &features)
	}
	meta.hasDiagFeat = slices.Contains(features, core.AgentFeatureNetworkDiagnostics)
	var caps core.DiagnosticCapabilities
	if len(capsJSON) > 0 {
		_ = json.Unmarshal(capsJSON, &caps)
	}
	meta.addresses = caps.Addresses
	if pingTool, ok := caps.Tools["ping"]; ok {
		if pingTool.Error == "" && (pingTool.Version != "" || pingTool.AutoInstall) {
			meta.hasPing = true
		} else {
			meta.pingError = pingTool.Error
		}
	}
	if accessErr := diagnosticEndpointAccess(ctx, ex, core.DiagnosticEndpoint{AgentID: agentID, InstanceID: instanceID}); accessErr == nil {
		meta.granted = true
	} else if !errors.Is(accessErr, ErrForbidden) && !errors.Is(accessErr, ErrNotFound) {
		return meta, accessErr
	}
	return meta, nil
}

func evaluateDashboardProbeAvailability(pair *core.DashboardProbePair, metas []probeEndpointMeta) {
	avail := core.DashboardProbeAvailability{
		Running: pair.ActiveState != "" && slices.Contains([]string{"preparing", "running", "stopping", "cleanup_pending"}, pair.ActiveState),
	}
	eps := pairEndpoints(*pair)
	chain := make([]core.DashboardProbeNodeStatus, len(eps))
	isMultiHop := len(eps) > 2

	var reason string
	for i := range eps {
		ep := &eps[i]
		if pair.TargetMode == "ip" && i == len(eps)-1 {
			ep.Name = ep.Address
			chain[i] = core.DashboardProbeNodeStatus{
				Name:    ep.Address,
				Address: ep.Address,
				Online:  true,
				Granted: true,
				Ping:    true,
			}
			continue
		}
		var m probeEndpointMeta
		if i < len(metas) {
			m = metas[i]
		}
		if m.exists && m.name != "" {
			ep.Name = m.name
		}
		pingReady := m.hasDiagFeat && m.hasPing
		chain[i] = core.DashboardProbeNodeStatus{
			AgentID:    ep.AgentID,
			InstanceID: ep.InstanceID,
			Name:       ep.Name,
			Region:     m.region,
			Address:    ep.Address,
			Online:     m.online,
			Granted:    m.granted,
			Ping:       pingReady,
		}
		if reason != "" {
			continue
		}
		hopLabel := fmt.Sprintf("第 %d 跳节点", i+1)
		if !isMultiHop {
			if i == 0 {
				hopLabel = "源节点"
			} else {
				hopLabel = "目标节点"
			}
		} else if ep.Name != "" {
			hopLabel = fmt.Sprintf("第 %d 跳 (%s)", i+1, ep.Name)
		}
		switch {
		case !m.exists:
			reason = hopLabel + "已删除或不可访问"
		case !m.granted:
			if ep.InstanceID != "" {
				reason = hopLabel + "共享实例未获网络诊断授权"
			} else {
				reason = "当前账号无权探测" + hopLabel
			}
		case !m.hasDiagFeat:
			reason = hopLabel + " Agent 需升级后支持网络诊断"
		case !m.online:
			reason = hopLabel + "当前离线"
		case !m.hasPing:
			if m.pingError != "" {
				reason = core.RedactDiagnostic(m.pingError)
			} else {
				reason = hopLabel + "缺少 ping 检测能力"
			}
		case !slices.Contains(m.addresses, ep.Address):
			reason = hopLabel + "探测地址已变更或未分配"
		}
	}

	pair.Nodes = eps
	pair.Chain = chain
	if len(eps) > 0 {
		pair.Node1 = eps[0]
		if len(metas) > 0 && metas[0].exists {
			pair.Node1Name = metas[0].name
			pair.Node1Region = metas[0].region
			avail.Node1Online = metas[0].online
			avail.Node1Granted = metas[0].granted
			avail.Node1Ping = metas[0].hasDiagFeat && metas[0].hasPing
		}
	}
	if len(eps) > 1 {
		lastIdx := len(eps) - 1
		pair.Node2 = eps[lastIdx]
		if pair.TargetMode == "ip" {
			pair.Node2Name = eps[lastIdx].Address
			pair.Node2Region = ""
		} else if lastIdx < len(metas) && metas[lastIdx].exists {
			mLast := metas[lastIdx]
			pair.Node2Name = mLast.name
			pair.Node2Region = mLast.region
			avail.Node2Online = mLast.online
			avail.Node2Granted = mLast.granted
			avail.Node2Ping = mLast.hasDiagFeat && mLast.hasPing
		}
	}

	avail.Reason = reason
	avail.Ready = reason == ""
	pair.Availability = avail
}

func validateDashboardProbeEndpointWritableTx(ctx context.Context, ex storeExecutor, ep core.DiagnosticEndpoint, checkAddress bool) error {
	if err := diagnosticEndpointAccess(ctx, ex, ep); err != nil {
		return err
	}
	var featuresJSON, capsJSON []byte
	err := ex.QueryRow(ctx, `
		SELECT a.features, COALESCE(c.capabilities, '{}'::jsonb)
		FROM agents a
		LEFT JOIN network_diagnostic_capabilities c ON c.agent_id = a.id
		WHERE a.id = $1 AND a.revoked_at IS NULL`, ep.AgentID).Scan(&featuresJSON, &capsJSON)
	if err != nil {
		return mapError(err)
	}
	var features []string
	if len(featuresJSON) > 0 {
		_ = json.Unmarshal(featuresJSON, &features)
	}
	if !slices.Contains(features, core.AgentFeatureNetworkDiagnostics) {
		return fmt.Errorf("%w: 所选节点 Agent 需升级后支持网络诊断", ErrConflict)
	}
	if checkAddress {
		var caps core.DiagnosticCapabilities
		if len(capsJSON) > 0 {
			_ = json.Unmarshal(capsJSON, &caps)
		}
		if !slices.Contains(caps.Addresses, ep.Address) {
			return fmt.Errorf("%w: 所选源/目标地址未分配给该节点", ErrInvalid)
		}
	}
	return nil
}

// CreateDashboardProbePair creates a persistent dashboard A/B probe pair for the caller.
func (s *Store) CreateDashboardProbePair(ctx context.Context, req core.DashboardProbePairRequest) (core.DashboardProbePair, error) {
	if err := req.NormalizeAndValidate(); err != nil {
		return core.DashboardProbePair{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.DashboardProbePair{}, err
	}
	if err := requireTaskPermission(ctx, tx, core.ActionStatus, false); err != nil {
		return core.DashboardProbePair{}, err
	}
	ownerID := scopeForConfig(ctx).OwnerID
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_probe_pairs WHERE owner_id=$1`, ownerID).Scan(&count); err != nil {
		return core.DashboardProbePair{}, err
	}
	if count >= core.DashboardProbeMaxPairsPerOwner {
		return core.DashboardProbePair{}, fmt.Errorf("%w: 每个账号最多配置 %d 个监测链路", ErrConflict, core.DashboardProbeMaxPairsPerOwner)
	}
	for i, ep := range req.Nodes {
		if req.TargetMode == "ip" && i == len(req.Nodes)-1 {
			continue
		}
		if err := validateDashboardProbeEndpointWritableTx(ctx, tx, ep, true); err != nil {
			return core.DashboardProbePair{}, err
		}
	}
	id, err := core.NewID("dpb")
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	paramsRaw, _ := json.Marshal(req.Parameters)
	nodesRaw, _ := json.Marshal(req.Nodes)
	now := time.Now().UTC()
	var nextRunAt *time.Time
	if req.AutoEnabled {
		next := now
		nextRunAt = &next
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO dashboard_probe_pairs(
			id, owner_id, name, group_name, target_mode,
			node1_agent_id, node1_instance_id, node1_address,
			node2_agent_id, node2_instance_id, node2_address,
			nodes, bidirectional, parameters, auto_enabled, auto_interval_seconds,
			last_session_id, next_run_at, revision, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'',$17,1,$18,$18)`,
		id, ownerID, req.Name, req.GroupName, req.TargetMode,
		req.Node1.AgentID, req.Node1.InstanceID, req.Node1.Address,
		req.Node2.AgentID, req.Node2.InstanceID, req.Node2.Address,
		nodesRaw, req.Bidirectional, paramsRaw, req.AutoEnabled, req.AutoIntervalSeconds,
		nextRunAt, now,
	); err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if err := recordAPIKeyWork(ctx, tx, "dashboard-probe", id); err != nil {
		return core.DashboardProbePair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.DashboardProbePair{}, err
	}
	return s.GetDashboardProbePair(ctx, id)
}

// UpdateDashboardProbePair updates an existing probe pair/chain with optimistic concurrency.
func (s *Store) UpdateDashboardProbePair(ctx context.Context, id string, req core.DashboardProbePairRequest) (core.DashboardProbePair, error) {
	if err := req.NormalizeAndValidate(); err != nil {
		return core.DashboardProbePair{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.DashboardProbePair{}, err
	}
	if err := requireTaskPermission(ctx, tx, core.ActionStatus, false); err != nil {
		return core.DashboardProbePair{}, err
	}
	ownerID := scopeForConfig(ctx).OwnerID
	var curRevision int64
	var curTargetMode, curN1ID, curN1Inst, curN1Addr, curN2ID, curN2Inst, curN2Addr string
	var curNodesJSON []byte
	var curAutoEnabled bool
	var curLastRunAt, curNextRunAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT revision, target_mode, node1_agent_id, node1_instance_id, node1_address,
		       node2_agent_id, node2_instance_id, node2_address, nodes, auto_enabled, last_run_at, next_run_at
		FROM dashboard_probe_pairs
		WHERE id=$1 AND owner_id=$2
		FOR UPDATE`, id, ownerID).Scan(
		&curRevision, &curTargetMode, &curN1ID, &curN1Inst, &curN1Addr,
		&curN2ID, &curN2Inst, &curN2Addr, &curNodesJSON, &curAutoEnabled, &curLastRunAt, &curNextRunAt,
	)
	if err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if req.Revision > 0 && req.Revision != curRevision {
		return core.DashboardProbePair{}, fmt.Errorf("%w: 探测配置已更新，请刷新后重试", ErrConflict)
	}
	var curNodes []core.DiagnosticEndpoint
	if len(curNodesJSON) > 0 {
		_ = json.Unmarshal(curNodesJSON, &curNodes)
	}
	if len(curNodes) < 2 {
		curNodes = []core.DiagnosticEndpoint{
			{AgentID: curN1ID, InstanceID: curN1Inst, Address: curN1Addr},
			{AgentID: curN2ID, InstanceID: curN2Inst, Address: curN2Addr},
		}
	}
	curByKey := map[string]core.DiagnosticEndpoint{}
	for _, ep := range curNodes {
		if ep.AgentID != "" {
			curByKey[ep.AgentID+"\x00"+ep.InstanceID] = ep
		}
	}
	nodesChanged := req.TargetMode != curTargetMode || !endpointsEqual(curNodes, req.Nodes)
	for i, ep := range req.Nodes {
		if req.TargetMode == "ip" && i == len(req.Nodes)-1 {
			continue
		}
		prev, existed := curByKey[ep.AgentID+"\x00"+ep.InstanceID]
		epChanged := !existed || prev.Address != ep.Address
		if req.AutoEnabled || epChanged {
			if err := validateDashboardProbeEndpointWritableTx(ctx, tx, ep, epChanged || req.AutoEnabled); err != nil {
				return core.DashboardProbePair{}, err
			}
		} else if err := diagnosticEndpointAccess(ctx, tx, ep); err != nil {
			return core.DashboardProbePair{}, err
		}
	}

	now := time.Now().UTC()
	nextRunAt := curNextRunAt
	if !req.AutoEnabled {
		nextRunAt = nil
	} else if !curAutoEnabled || nodesChanged {
		next := now
		nextRunAt = &next
	} else if curLastRunAt != nil {
		next := curLastRunAt.Add(time.Duration(req.AutoIntervalSeconds) * time.Second)
		if next.Before(now) {
			next = now
		}
		nextRunAt = &next
	} else if nextRunAt == nil {
		next := now
		nextRunAt = &next
	}
	paramsRaw, _ := json.Marshal(req.Parameters)
	nodesRaw, _ := json.Marshal(req.Nodes)
	if _, err := tx.Exec(ctx, `
		UPDATE dashboard_probe_pairs
		SET name=$3, group_name=$4, target_mode=$5,
		    node1_agent_id=$6, node1_instance_id=$7, node1_address=$8,
		    node2_agent_id=$9, node2_instance_id=$10, node2_address=$11,
		    nodes=$12, bidirectional=$13, parameters=$14, auto_enabled=$15, auto_interval_seconds=$16,
		    next_run_at=$17, revision=revision+1, updated_at=$18
		WHERE id=$1 AND owner_id=$2`,
		id, ownerID, req.Name, req.GroupName, req.TargetMode,
		req.Node1.AgentID, req.Node1.InstanceID, req.Node1.Address,
		req.Node2.AgentID, req.Node2.InstanceID, req.Node2.Address,
		nodesRaw, req.Bidirectional, paramsRaw, req.AutoEnabled, req.AutoIntervalSeconds,
		nextRunAt, now,
	); err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if err := recordAPIKeyWork(ctx, tx, "dashboard-probe", id); err != nil {
		return core.DashboardProbePair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.DashboardProbePair{}, err
	}
	return s.GetDashboardProbePair(ctx, id)
}

// DeleteDashboardProbePair deletes an owner's probe pair and requests stop if active.
func (s *Store) DeleteDashboardProbePair(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return err
	}
	if err := requireTaskPermission(ctx, tx, core.ActionStatus, false); err != nil {
		return err
	}
	ownerID := scopeForConfig(ctx).OwnerID
	var lastSessionID string
	if err := tx.QueryRow(ctx, `SELECT last_session_id FROM dashboard_probe_pairs WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, ownerID).Scan(&lastSessionID); err != nil {
		return mapError(err)
	}
	if lastSessionID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE network_diagnostic_sessions
			SET state='stopping', document=jsonb_set(document,'{state}','"stopping"')
			WHERE id=$1 AND owner_id=$2 AND state IN ('preparing','running')`,
			lastSessionID, ownerID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM api_key_work WHERE kind='dashboard-probe' AND work_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_probe_pairs WHERE id=$1 AND owner_id=$2`, id, ownerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RunDashboardProbePair starts one manual probe run for the configured A/B pair.
func (s *Store) RunDashboardProbePair(ctx context.Context, id string) (core.DashboardProbePair, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.DashboardProbePair{}, err
	}
	if err := requireTaskPermission(ctx, tx, core.ActionStatus, false); err != nil {
		return core.DashboardProbePair{}, err
	}
	pair, err := scanDashboardProbePairTx(ctx, tx, id, scopeForConfig(ctx).OwnerID, true)
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	if pair.LastSessionID != "" {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM network_diagnostic_sessions WHERE id=$1 AND state IN ('preparing','running','stopping','cleanup_pending'))`, pair.LastSessionID).Scan(&active); err != nil {
			return core.DashboardProbePair{}, err
		}
		if active {
			return core.DashboardProbePair{}, fmt.Errorf("%w: 该探测对已有正在进行的测量", ErrConflict)
		}
	}
	requestID, err := core.NewID("dpr")
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	now := time.Now().UTC()
	eps := pairEndpoints(pair)
	diagReq := core.DiagnosticRequest{
		RequestID:     requestID,
		TargetMode:    pair.TargetMode,
		Node1:         eps[0],
		Node2:         eps[len(eps)-1],
		Nodes:         eps,
		Bidirectional: pair.Bidirectional,
		Parameters:    pair.Parameters,
	}
	session, err := createDiagnosticTx(ctx, tx, diagReq, now, dashboardProbeDeadline(now, pair.Parameters, max(1, len(eps)-1)))
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	var nextRunAt *time.Time
	if pair.AutoEnabled {
		next := now.Add(time.Duration(pair.AutoIntervalSeconds) * time.Second)
		nextRunAt = &next
	}
	if _, err := tx.Exec(ctx, `
		UPDATE dashboard_probe_pairs
		SET last_session_id=$2, last_run_at=$3, next_run_at=COALESCE($4, next_run_at), updated_at=$3
		WHERE id=$1`,
		pair.ID, session.ID, now, nextRunAt,
	); err != nil {
		return core.DashboardProbePair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.DashboardProbePair{}, err
	}
	return s.GetDashboardProbePair(ctx, id)
}

// StopDashboardProbePair cancels the in-flight probe session belonging to a pair.
func (s *Store) StopDashboardProbePair(ctx context.Context, id string) (core.DashboardProbePair, error) {
	ownerID := scopeForConfig(ctx).OwnerID
	var lastSessionID string
	if err := s.pool.QueryRow(ctx, `SELECT last_session_id FROM dashboard_probe_pairs WHERE id=$1 AND owner_id=$2`, id, ownerID).Scan(&lastSessionID); err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if lastSessionID != "" {
		if err := s.StopDiagnostic(ctx, lastSessionID); err != nil && !errors.Is(err, ErrNotFound) {
			return core.DashboardProbePair{}, err
		}
	}
	return s.GetDashboardProbePair(ctx, id)
}

// GetDashboardProbePair returns a single caller-owned probe pair with samples and readiness.
func (s *Store) GetDashboardProbePair(ctx context.Context, id string) (core.DashboardProbePair, error) {
	pairs, err := s.listDashboardProbePairs(ctx, id)
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	if len(pairs) == 0 {
		return core.DashboardProbePair{}, ErrNotFound
	}
	return pairs[0], nil
}

// ListDashboardProbePairs lists all caller-owned A/B probe pairs with recent samples and availability.
func (s *Store) ListDashboardProbePairs(ctx context.Context) ([]core.DashboardProbePair, error) {
	return s.listDashboardProbePairs(ctx, "")
}

func scanDashboardProbePairTx(ctx context.Context, q storeExecutor, id, ownerID string, forUpdate bool) (core.DashboardProbePair, error) {
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE"
	}
	var pair core.DashboardProbePair
	var paramsJSON, nodesJSON []byte
	err := q.QueryRow(ctx, `
		SELECT id, name, group_name, target_mode,
		       node1_agent_id, node1_instance_id, node1_address,
		       node2_agent_id, node2_instance_id, node2_address,
		       nodes, bidirectional, parameters, auto_enabled, auto_interval_seconds,
		       last_session_id, last_run_at, next_run_at, revision, created_at, updated_at
		FROM dashboard_probe_pairs
		WHERE id=$1 AND owner_id=$2`+suffix, id, ownerID).Scan(
		&pair.ID, &pair.Name, &pair.GroupName, &pair.TargetMode,
		&pair.Node1.AgentID, &pair.Node1.InstanceID, &pair.Node1.Address,
		&pair.Node2.AgentID, &pair.Node2.InstanceID, &pair.Node2.Address,
		&nodesJSON, &pair.Bidirectional, &paramsJSON, &pair.AutoEnabled, &pair.AutoIntervalSeconds,
		&pair.LastSessionID, &pair.LastRunAt, &pair.NextRunAt, &pair.Revision, &pair.CreatedAt, &pair.UpdatedAt,
	)
	if err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if len(paramsJSON) > 0 {
		_ = json.Unmarshal(paramsJSON, &pair.Parameters)
	}
	if len(nodesJSON) > 0 {
		_ = json.Unmarshal(nodesJSON, &pair.Nodes)
	}
	pair.Nodes = pairEndpoints(pair)
	return pair, nil
}

func (s *Store) listDashboardProbePairs(ctx context.Context, filterID string) ([]core.DashboardProbePair, error) {
	ownerID := scopeForConfig(ctx).OwnerID
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.name, p.group_name, p.target_mode,
		       p.node1_agent_id, p.node1_instance_id, p.node1_address,
		       p.node2_agent_id, p.node2_instance_id, p.node2_address,
		       p.nodes, p.bidirectional, p.parameters, p.auto_enabled, p.auto_interval_seconds,
		       p.last_session_id, COALESCE(ds.state, ''), p.last_run_at, p.next_run_at,
		       p.revision, p.created_at, p.updated_at
		FROM dashboard_probe_pairs p
		LEFT JOIN network_diagnostic_sessions ds ON ds.id = p.last_session_id
		WHERE p.owner_id = $1 AND ($2 = '' OR p.id = $2)
		ORDER BY p.created_at ASC, p.id ASC`, ownerID, filterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pairs := []core.DashboardProbePair{}
	pairIDs := []string{}
	for rows.Next() {
		var pair core.DashboardProbePair
		var paramsJSON, nodesJSON []byte
		if err := rows.Scan(
			&pair.ID, &pair.Name, &pair.GroupName, &pair.TargetMode,
			&pair.Node1.AgentID, &pair.Node1.InstanceID, &pair.Node1.Address,
			&pair.Node2.AgentID, &pair.Node2.InstanceID, &pair.Node2.Address,
			&nodesJSON, &pair.Bidirectional, &paramsJSON, &pair.AutoEnabled, &pair.AutoIntervalSeconds,
			&pair.LastSessionID, &pair.ActiveState, &pair.LastRunAt, &pair.NextRunAt,
			&pair.Revision, &pair.CreatedAt, &pair.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(paramsJSON) > 0 {
			_ = json.Unmarshal(paramsJSON, &pair.Parameters)
		}
		if len(nodesJSON) > 0 {
			_ = json.Unmarshal(nodesJSON, &pair.Nodes)
		}
		pair.Nodes = pairEndpoints(pair)
		pairs = append(pairs, pair)
		pairIDs = append(pairIDs, pair.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return pairs, nil
	}

	sampleRows, err := s.pool.Query(ctx, `
		SELECT pair_id, id, session_id, state, forward, reverse, hops, error, sampled_at
		FROM (
			SELECT pair_id, id, session_id, state, forward, reverse, hops, error, sampled_at,
			       row_number() OVER (PARTITION BY pair_id ORDER BY sampled_at DESC, id DESC) AS rn
			FROM dashboard_probe_samples
			WHERE pair_id = ANY($1::text[])
		) ranked
		WHERE rn <= $2
		ORDER BY sampled_at ASC, id ASC`, pairIDs, core.DashboardProbeHistoryLimit)
	if err != nil {
		return nil, err
	}
	defer sampleRows.Close()

	samplesByPair := map[string][]core.DashboardProbeSample{}
	for sampleRows.Next() {
		var pairID string
		var sample core.DashboardProbeSample
		var fwdRaw, revRaw, hopsRaw []byte
		if err := sampleRows.Scan(&pairID, &sample.ID, &sample.SessionID, &sample.State, &fwdRaw, &revRaw, &hopsRaw, &sample.Error, &sample.SampledAt); err != nil {
			return nil, err
		}
		if len(fwdRaw) > 0 && string(fwdRaw) != "null" {
			var dir core.DashboardProbeDirectionMetrics
			if json.Unmarshal(fwdRaw, &dir) == nil {
				sample.Forward = &dir
			}
		}
		if len(revRaw) > 0 && string(revRaw) != "null" {
			var dir core.DashboardProbeDirectionMetrics
			if json.Unmarshal(revRaw, &dir) == nil {
				sample.Reverse = &dir
			}
		}
		if len(hopsRaw) > 0 && string(hopsRaw) != "null" {
			var hops []core.DashboardProbeHopMetrics
			if json.Unmarshal(hopsRaw, &hops) == nil {
				sample.Hops = hops
			}
		}
		samplesByPair[pairID] = append(samplesByPair[pairID], sample)
	}
	if err := sampleRows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	metaCache := map[string]probeEndpointMeta{}
	getMeta := func(agentID, instanceID string) (probeEndpointMeta, error) {
		if agentID == "" {
			return probeEndpointMeta{}, nil
		}
		key := agentID + "\x00" + instanceID
		if cached, ok := metaCache[key]; ok {
			return cached, nil
		}
		meta, metaErr := loadProbeEndpointMeta(ctx, s.pool, agentID, instanceID, now)
		if metaErr != nil {
			return meta, metaErr
		}
		metaCache[key] = meta
		return meta, nil
	}

	for index := range pairs {
		p := &pairs[index]
		if list := samplesByPair[p.ID]; len(list) > 0 {
			p.History = list
			latest := list[len(list)-1]
			p.Latest = &latest
		}
		eps := pairEndpoints(*p)
		metas := make([]probeEndpointMeta, len(eps))
		for i, ep := range eps {
			if p.TargetMode == "ip" && i == len(eps)-1 {
				continue
			}
			m, err := getMeta(ep.AgentID, ep.InstanceID)
			if err != nil {
				return nil, err
			}
			metas[i] = m
		}
		evaluateDashboardProbeAvailability(p, metas)
	}
	return pairs, nil
}

func extractPingDirectionMetrics(res core.DiagnosticResult, ok bool, sessionState string) *core.DashboardProbeDirectionMetrics {
	if !ok {
		if sessionState == "" || sessionState == "preparing" || sessionState == "running" {
			return nil
		}
		return &core.DashboardProbeDirectionMetrics{
			State:       sessionState,
			LossPercent: 100,
			Reached:     false,
		}
	}
	out := &core.DashboardProbeDirectionMetrics{
		State:   res.State,
		Error:   core.RedactDiagnostic(strings.TrimSpace(res.Error)),
		Reached: false,
	}
	if out.State == "" {
		out.State = sessionState
	}
	var parsed struct {
		Sent        int      `json:"sent"`
		Received    int      `json:"received"`
		LossPercent *float64 `json:"loss_percent"`
		MinMS       *float64 `json:"min_ms"`
		AvgMS       *float64 `json:"avg_ms"`
		MaxMS       *float64 `json:"max_ms"`
		StdevMS     *float64 `json:"stdev_ms"`
		Reached     *bool    `json:"reached"`
	}
	if len(res.Output) > 0 && json.Unmarshal(res.Output, &parsed) == nil {
		out.Sent = parsed.Sent
		out.Received = parsed.Received
		if parsed.LossPercent != nil {
			out.LossPercent = *parsed.LossPercent
		} else if parsed.Sent > 0 {
			out.LossPercent = float64(parsed.Sent-parsed.Received) * 100 / float64(parsed.Sent)
		} else {
			out.LossPercent = 100
		}
		if parsed.Reached != nil {
			out.Reached = *parsed.Reached
		} else {
			out.Reached = parsed.Received > 0
		}
		if parsed.Received > 0 {
			out.MinMS = parsed.MinMS
			out.AvgMS = parsed.AvgMS
			out.MaxMS = parsed.MaxMS
			out.StdevMS = parsed.StdevMS
		}
		return out
	}
	if out.State != "succeeded" {
		out.LossPercent = 100
	}
	return out
}

func aggregateChainDirectionMetrics(dirs []*core.DashboardProbeDirectionMetrics, path []core.DiagnosticEndpoint, reverseOrder bool, sessionState string) *core.DashboardProbeDirectionMetrics {
	if len(dirs) == 0 {
		return nil
	}
	allReached := true
	allHaveAvg := true
	allHaveMinMax := true
	var minSum, avgSum, maxSum, stdevSqSum float64
	var hasStdev bool
	survival := 1.0
	var totalSent, totalRecv int
	state := "succeeded"
	var errs []string

	stepCount := len(dirs)
	for step := 0; step < stepCount; step++ {
		idx := step
		if reverseOrder {
			idx = stepCount - 1 - step
		}
		d := dirs[idx]
		fromEp, toEp := path[idx], path[idx+1]
		if reverseOrder {
			fromEp, toEp = path[idx+1], path[idx]
		}
		fromLabel := fromEp.Name
		if fromLabel == "" {
			fromLabel = fromEp.AgentID
		}
		toLabel := toEp.Name
		if toLabel == "" {
			toLabel = toEp.AgentID
			if toLabel == "" {
				toLabel = toEp.Address
			}
		}
		if d == nil {
			allReached = false
			allHaveAvg = false
			allHaveMinMax = false
			survival = 0
			state = "failed"
			continue
		}
		totalSent += d.Sent
		totalRecv += d.Received
		if d.State == "failed" {
			state = "failed"
		} else if d.State == "canceled" && state != "failed" {
			state = "canceled"
		} else if d.State != "succeeded" && state == "succeeded" {
			state = d.State
		}
		if !d.Reached {
			allReached = false
			survival = 0
		} else {
			loss := math.Max(0, math.Min(100, d.LossPercent))
			survival *= (1.0 - loss/100.0)
		}
		if d.AvgMS != nil {
			avgSum += *d.AvgMS
		} else {
			allHaveAvg = false
		}
		if d.MinMS != nil && d.MaxMS != nil {
			minSum += *d.MinMS
			maxSum += *d.MaxMS
		} else {
			allHaveMinMax = false
		}
		if d.StdevMS != nil {
			stdevSqSum += (*d.StdevMS) * (*d.StdevMS)
			hasStdev = true
		}
		if d.Error != "" {
			errs = append(errs, fmt.Sprintf("[%s→%s] %s", fromLabel, toLabel, d.Error))
		}
	}
	if state == "" {
		state = sessionState
	}
	combinedLoss := math.Round((1.0-math.Max(0, math.Min(1, survival)))*10000) / 100
	out := &core.DashboardProbeDirectionMetrics{
		State:       state,
		Sent:        totalSent,
		Received:    totalRecv,
		LossPercent: combinedLoss,
		Reached:     allReached,
		Error:       strings.Join(errs, "; "),
	}
	if allReached && allHaveAvg {
		v := math.Round(avgSum*100) / 100
		out.AvgMS = &v
	}
	if allReached && allHaveMinMax {
		vMin := math.Round(minSum*100) / 100
		vMax := math.Round(maxSum*100) / 100
		out.MinMS = &vMin
		out.MaxMS = &vMax
	}
	if allReached && hasStdev {
		vStd := math.Round(math.Sqrt(stdevSqSum)*100) / 100
		out.StdevMS = &vStd
	}
	return out
}

// syncDashboardProbeSampleTx persists a compact latency/loss sample whenever a
// network diagnostic session referenced by any dashboard probe pair finishes.
func syncDashboardProbeSampleTx(ctx context.Context, tx pgx.Tx, d core.DiagnosticSession) error {
	if !slices.Contains([]string{"succeeded", "failed", "canceled"}, d.State) {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM dashboard_probe_pairs WHERE last_session_id=$1`, d.ID)
	if err != nil {
		return err
	}
	pairIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(pairIDs) == 0 {
		return nil
	}

	path := d.Request.PathEndpoints()
	var forward, reverse *core.DashboardProbeDirectionMetrics
	var hops []core.DashboardProbeHopMetrics
	var errs []string

	if len(path) >= 3 && d.Request.TargetMode != "ip" {
		hopCount := len(path) - 1
		hops = make([]core.DashboardProbeHopMetrics, 0, hopCount)
		fwdDirs := make([]*core.DashboardProbeDirectionMetrics, 0, hopCount)
		revDirs := make([]*core.DashboardProbeDirectionMetrics, 0, hopCount)
		for i := 0; i < hopCount; i++ {
			fRes, fOK := d.Results[fmt.Sprintf("%s-f%d", d.ID, i)]
			fwd := extractPingDirectionMetrics(fRes, fOK, d.State)
			var rev *core.DashboardProbeDirectionMetrics
			if d.Request.Bidirectional && path[i+1].AgentID != "" {
				rRes, rOK := d.Results[fmt.Sprintf("%s-r%d", d.ID, i)]
				rev = extractPingDirectionMetrics(rRes, rOK, d.State)
				revDirs = append(revDirs, rev)
			}
			fwdDirs = append(fwdDirs, fwd)
			toID := path[i+1].AgentID
			if toID == "" {
				toID = path[i+1].Address
			}
			hops = append(hops, core.DashboardProbeHopMetrics{
				HopIndex: i,
				From:     path[i].AgentID,
				To:       toID,
				Forward:  fwd,
				Reverse:  rev,
			})
		}
		forward = aggregateChainDirectionMetrics(fwdDirs, path, false, d.State)
		if d.Request.Bidirectional && len(revDirs) == hopCount {
			reverse = aggregateChainDirectionMetrics(revDirs, path, true, d.State)
		}
	} else {
		fwdRes, fwdOK := d.Results[d.ID+"-forward"]
		if !fwdOK {
			fwdRes, fwdOK = d.Results[d.ID]
		}
		forward = extractPingDirectionMetrics(fwdRes, fwdOK, d.State)
		if d.Request.Bidirectional {
			revRes, revOK := d.Results[d.ID+"-reverse"]
			reverse = extractPingDirectionMetrics(revRes, revOK, d.State)
		}
		toID := path[len(path)-1].AgentID
		if toID == "" {
			toID = path[len(path)-1].Address
		}
		hops = []core.DashboardProbeHopMetrics{{
			HopIndex: 0,
			From:     path[0].AgentID,
			To:       toID,
			Forward:  forward,
			Reverse:  reverse,
		}}
	}

	if forward != nil && forward.Error != "" {
		errs = append(errs, forward.Error)
	}
	if reverse != nil && reverse.Error != "" {
		errs = append(errs, reverse.Error)
	}
	errSummary := strings.Join(errs, "; ")
	fwdRaw, _ := json.Marshal(forward)
	revRaw, _ := json.Marshal(reverse)
	hopsRaw, _ := json.Marshal(hops)
	sampledAt := time.Now().UTC()
	for _, pairID := range pairIDs {
		sampleID, err := core.NewID("dps")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO dashboard_probe_samples(id, pair_id, session_id, state, forward, reverse, hops, error, sampled_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (pair_id, session_id) DO NOTHING`,
			sampleID, pairID, d.ID, d.State, fwdRaw, revRaw, hopsRaw, errSummary, sampledAt,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM dashboard_probe_samples
			WHERE pair_id=$1
			  AND id NOT IN (
				SELECT id FROM dashboard_probe_samples
				WHERE pair_id=$1
				ORDER BY sampled_at DESC, id DESC
				LIMIT $2
			  )`, pairID, dashboardProbeMaxRetainedSamples); err != nil {
			return err
		}
	}
	return nil
}

// RunDueDashboardProbes expires timed-out probe sessions and launches due
// automated A/B ping sessions without colliding with active node locks.
func (s *Store) RunDueDashboardProbes(ctx context.Context, now time.Time) error {
	now = now.UTC()
	if err := s.expireStaleDashboardProbeSessions(ctx, now); err != nil {
		slog.Warn("expire stale dashboard probe sessions", "error", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id
		FROM dashboard_probe_pairs
		WHERE auto_enabled AND (next_run_at IS NULL OR next_run_at <= $1)
		ORDER BY next_run_at NULLS FIRST, id
		LIMIT 32`, now)
	if err != nil {
		return err
	}
	type candidate struct{ id, ownerID string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.ownerID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var failures []error
	for _, item := range candidates {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		if err := s.runScheduledDashboardProbe(ctx, item.id, item.ownerID, now); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Store) expireStaleDashboardProbeSessions(ctx context.Context, now time.Time) error {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ds.id
		FROM dashboard_probe_pairs p
		JOIN network_diagnostic_sessions ds ON ds.id = p.last_session_id
		WHERE ds.state IN ('preparing','running','stopping','cleanup_pending')
		  AND ds.deadline <= $1
		LIMIT 32`, now)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		var doc []byte
		if err := tx.QueryRow(ctx, `
			SELECT document FROM network_diagnostic_sessions
			WHERE id=$1 AND state IN ('preparing','running','stopping','cleanup_pending') AND deadline <= $2
			FOR UPDATE`, id, now).Scan(&doc); err != nil {
			_ = tx.Rollback(ctx)
			continue
		}
		var d core.DiagnosticSession
		if json.Unmarshal(doc, &d) != nil {
			_ = tx.Rollback(ctx)
			continue
		}
		if d.Results == nil {
			d.Results = map[string]core.DiagnosticResult{}
		}
		for _, cmd := range plannedDiagnosticCommands(d) {
			if r, ok := d.Results[cmd.ID]; !ok || !r.Terminal() {
				d.Results[cmd.ID] = core.DiagnosticResult{
					ID:      cmd.ID,
					State:   "failed",
					Cleaned: true,
					Error:   "探测超时或节点离线",
					At:      now,
				}
			}
		}
		d.State = "failed"
		nextDoc, _ := json.Marshal(d)
		if _, err := tx.Exec(ctx, `UPDATE network_diagnostic_sessions SET document=$2, state=$3 WHERE id=$1`, d.ID, nextDoc, d.State); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := syncDiagnosticTask(ctx, tx, d); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) runScheduledDashboardProbe(ctx context.Context, pairID, ownerID string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	admin, authorized := ownerID == "", true
	if ownerID != "" && !strings.HasPrefix(ownerID, "token_") {
		var role core.Role
		var disabled bool
		var permRaw []byte
		err := tx.QueryRow(ctx, `SELECT role, disabled, permissions FROM panel_users WHERE id=$1 FOR SHARE`, ownerID).Scan(&role, &disabled, &permRaw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		admin = err == nil && role == core.RoleAdmin
		authorized = err == nil && !disabled
		if authorized && !admin {
			var perms []core.Permission
			_ = json.Unmarshal(permRaw, &perms)
			authorized = core.HasPermission(perms, core.PermissionAgentsRead) &&
				core.HasPermission(perms, core.PermissionTasksRead) &&
				core.HasPermission(perms, core.PermissionTasksExecute)
		}
	}
	scoped := WithConfigScope(ctx, ownerID, admin)
	if authorized {
		if keyCtx, keyErr := s.apiKeyWorkContext(scoped, "dashboard-probe", pairID); keyErr != nil {
			if !errors.Is(keyErr, ErrForbidden) {
				return keyErr
			}
			authorized = false
		} else {
			scoped = keyCtx
		}
	}

	pair, err := scanDashboardProbePairTx(ctx, tx, pairID, ownerID, true)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if !pair.AutoEnabled || (pair.NextRunAt != nil && pair.NextRunAt.After(now)) {
		return nil
	}
	if !authorized {
		if _, err := tx.Exec(ctx, `UPDATE dashboard_probe_pairs SET auto_enabled=false, updated_at=$2 WHERE id=$1`, pair.ID, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if pair.LastSessionID != "" {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM network_diagnostic_sessions WHERE id=$1 AND state IN ('preparing','running','stopping','cleanup_pending'))`, pair.LastSessionID).Scan(&active); err != nil {
			return err
		}
		if active {
			next := now.Add(5 * time.Second)
			_, err := tx.Exec(ctx, `UPDATE dashboard_probe_pairs SET next_run_at=$2, updated_at=$3 WHERE id=$1`, pair.ID, next, now)
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
	}

	requestID, err := core.NewID("dpr")
	if err != nil {
		return err
	}
	eps := pairEndpoints(pair)
	diagReq := core.DiagnosticRequest{
		RequestID:     requestID,
		TargetMode:    pair.TargetMode,
		Node1:         eps[0],
		Node2:         eps[len(eps)-1],
		Nodes:         eps,
		Bidirectional: pair.Bidirectional,
		Parameters:    pair.Parameters,
	}
	session, createErr := createDiagnosticTx(scoped, tx, diagReq, now, dashboardProbeDeadline(now, pair.Parameters, max(1, len(eps)-1)))
	if createErr != nil {
		if errors.Is(createErr, ErrConflict) || errors.Is(createErr, ErrForbidden) || errors.Is(createErr, ErrNotFound) || errors.Is(createErr, ErrInvalid) {
			backoff := time.Duration(pair.AutoIntervalSeconds) * time.Second
			if strings.Contains(createErr.Error(), "已有未完成诊断") {
				backoff = 5 * time.Second
			}
			if _, err := tx.Exec(ctx, `UPDATE dashboard_probe_pairs SET next_run_at=$2, updated_at=$3 WHERE id=$1`, pair.ID, now.Add(backoff), now); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		return createErr
	}

	next := now.Add(time.Duration(pair.AutoIntervalSeconds) * time.Second)
	if _, err := tx.Exec(ctx, `
		UPDATE dashboard_probe_pairs
		SET last_session_id=$2, last_run_at=$3, next_run_at=$4, updated_at=$3
		WHERE id=$1`, pair.ID, session.ID, now, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
