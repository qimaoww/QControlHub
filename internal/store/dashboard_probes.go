package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const dashboardProbeMaxRetainedSamples = 30

func dashboardProbeDeadline(now time.Time, params core.DiagnosticParameters) time.Time {
	count := max(1, params.Count)
	perProbeMS := max(100, params.IntervalMS) + max(100, params.TimeoutMS)
	span := time.Duration(count*perProbeMS)*time.Millisecond + 30*time.Second
	if span < 60*time.Second {
		span = 60 * time.Second
	}
	return now.Add(span)
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

func evaluateDashboardProbeAvailability(pair *core.DashboardProbePair, m1, m2 probeEndpointMeta) {
	avail := core.DashboardProbeAvailability{
		Running: pair.ActiveState != "" && slices.Contains([]string{"preparing", "running", "stopping", "cleanup_pending"}, pair.ActiveState),
	}
	if m1.exists {
		pair.Node1Name = m1.name
		pair.Node1Region = m1.region
		avail.Node1Online = m1.online
		avail.Node1Granted = m1.granted
		avail.Node1Ping = m1.hasDiagFeat && m1.hasPing
	}
	if pair.TargetMode != "ip" && m2.exists {
		pair.Node2Name = m2.name
		pair.Node2Region = m2.region
		avail.Node2Online = m2.online
		avail.Node2Granted = m2.granted
		avail.Node2Ping = m2.hasDiagFeat && m2.hasPing
	}

	var reason string
	switch {
	case !m1.exists:
		reason = "源节点已删除或不可访问"
	case !m1.granted:
		if pair.Node1.InstanceID != "" {
			reason = "源节点共享实例未获网络诊断授权"
		} else {
			reason = "当前账号无权探测源节点"
		}
	case !m1.hasDiagFeat:
		reason = "源节点 Agent 需升级后支持网络诊断"
	case !m1.online:
		reason = "源节点当前离线"
	case !m1.hasPing:
		if m1.pingError != "" {
			reason = core.RedactDiagnostic(m1.pingError)
		} else {
			reason = "源节点缺少 ping 检测能力"
		}
	case !slices.Contains(m1.addresses, pair.Node1.Address):
		reason = "源节点探测地址已变更或未分配"
	case pair.TargetMode != "ip" && !m2.exists:
		reason = "目标节点已删除或不可访问"
	case pair.TargetMode != "ip" && !m2.granted:
		if pair.Node2.InstanceID != "" {
			reason = "目标节点共享实例未获网络诊断授权"
		} else {
			reason = "当前账号无权探测目标节点"
		}
	case pair.TargetMode != "ip" && !m2.hasDiagFeat:
		reason = "目标节点 Agent 需升级后支持网络诊断"
	case pair.TargetMode != "ip" && !m2.online:
		reason = "目标节点当前离线"
	case pair.TargetMode != "ip" && !m2.hasPing:
		if m2.pingError != "" {
			reason = core.RedactDiagnostic(m2.pingError)
		} else {
			reason = "目标节点缺少 ping 检测能力"
		}
	case pair.TargetMode != "ip" && !slices.Contains(m2.addresses, pair.Node2.Address):
		reason = "目标节点探测地址已变更或未分配"
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
		return core.DashboardProbePair{}, fmt.Errorf("%w: 每个账号最多配置 %d 个 A/B 探测对", ErrConflict, core.DashboardProbeMaxPairsPerOwner)
	}
	if err := validateDashboardProbeEndpointWritableTx(ctx, tx, req.Node1, true); err != nil {
		return core.DashboardProbePair{}, err
	}
	if req.TargetMode != "ip" {
		if err := validateDashboardProbeEndpointWritableTx(ctx, tx, req.Node2, true); err != nil {
			return core.DashboardProbePair{}, err
		}
	}
	id, err := core.NewID("dpb")
	if err != nil {
		return core.DashboardProbePair{}, err
	}
	paramsRaw, _ := json.Marshal(req.Parameters)
	now := time.Now().UTC()
	var nextRunAt *time.Time
	if req.AutoEnabled {
		next := now
		nextRunAt = &next
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO dashboard_probe_pairs(
			id, owner_id, name, target_mode,
			node1_agent_id, node1_instance_id, node1_address,
			node2_agent_id, node2_instance_id, node2_address,
			bidirectional, parameters, auto_enabled, auto_interval_seconds,
			last_session_id, next_run_at, revision, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'',$15,1,$16,$16)`,
		id, ownerID, req.Name, req.TargetMode,
		req.Node1.AgentID, req.Node1.InstanceID, req.Node1.Address,
		req.Node2.AgentID, req.Node2.InstanceID, req.Node2.Address,
		req.Bidirectional, paramsRaw, req.AutoEnabled, req.AutoIntervalSeconds,
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

// UpdateDashboardProbePair updates an existing A/B probe pair with optimistic concurrency.
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
	var curAutoEnabled bool
	var curLastRunAt, curNextRunAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT revision, target_mode, node1_agent_id, node1_instance_id, node1_address,
		       node2_agent_id, node2_instance_id, node2_address, auto_enabled, last_run_at, next_run_at
		FROM dashboard_probe_pairs
		WHERE id=$1 AND owner_id=$2
		FOR UPDATE`, id, ownerID).Scan(
		&curRevision, &curTargetMode, &curN1ID, &curN1Inst, &curN1Addr,
		&curN2ID, &curN2Inst, &curN2Addr, &curAutoEnabled, &curLastRunAt, &curNextRunAt,
	)
	if err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if req.Revision > 0 && req.Revision != curRevision {
		return core.DashboardProbePair{}, fmt.Errorf("%w: 探测配置已更新，请刷新后重试", ErrConflict)
	}
	n1Changed := req.Node1.AgentID != curN1ID || req.Node1.InstanceID != curN1Inst || req.Node1.Address != curN1Addr
	if req.AutoEnabled || n1Changed {
		if err := validateDashboardProbeEndpointWritableTx(ctx, tx, req.Node1, n1Changed || req.AutoEnabled); err != nil {
			return core.DashboardProbePair{}, err
		}
	} else if err := diagnosticEndpointAccess(ctx, tx, req.Node1); err != nil {
		return core.DashboardProbePair{}, err
	}
	if req.TargetMode != "ip" {
		n2Changed := req.TargetMode != curTargetMode || req.Node2.AgentID != curN2ID || req.Node2.InstanceID != curN2Inst || req.Node2.Address != curN2Addr
		if req.AutoEnabled || n2Changed {
			if err := validateDashboardProbeEndpointWritableTx(ctx, tx, req.Node2, n2Changed || req.AutoEnabled); err != nil {
				return core.DashboardProbePair{}, err
			}
		} else if err := diagnosticEndpointAccess(ctx, tx, req.Node2); err != nil {
			return core.DashboardProbePair{}, err
		}
	}

	now := time.Now().UTC()
	nextRunAt := curNextRunAt
	if !req.AutoEnabled {
		nextRunAt = nil
	} else if !curAutoEnabled {
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
	if _, err := tx.Exec(ctx, `
		UPDATE dashboard_probe_pairs
		SET name=$3, target_mode=$4,
		    node1_agent_id=$5, node1_instance_id=$6, node1_address=$7,
		    node2_agent_id=$8, node2_instance_id=$9, node2_address=$10,
		    bidirectional=$11, parameters=$12, auto_enabled=$13, auto_interval_seconds=$14,
		    next_run_at=$15, revision=revision+1, updated_at=$16
		WHERE id=$1 AND owner_id=$2`,
		id, ownerID, req.Name, req.TargetMode,
		req.Node1.AgentID, req.Node1.InstanceID, req.Node1.Address,
		req.Node2.AgentID, req.Node2.InstanceID, req.Node2.Address,
		req.Bidirectional, paramsRaw, req.AutoEnabled, req.AutoIntervalSeconds,
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
	diagReq := core.DiagnosticRequest{
		RequestID:     requestID,
		TargetMode:    pair.TargetMode,
		Node1:         pair.Node1,
		Node2:         pair.Node2,
		Bidirectional: pair.Bidirectional,
		Parameters:    pair.Parameters,
	}
	session, err := createDiagnosticTx(ctx, tx, diagReq, now, dashboardProbeDeadline(now, pair.Parameters))
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
	var paramsJSON []byte
	err := q.QueryRow(ctx, `
		SELECT id, name, target_mode,
		       node1_agent_id, node1_instance_id, node1_address,
		       node2_agent_id, node2_instance_id, node2_address,
		       bidirectional, parameters, auto_enabled, auto_interval_seconds,
		       last_session_id, last_run_at, next_run_at, revision, created_at, updated_at
		FROM dashboard_probe_pairs
		WHERE id=$1 AND owner_id=$2`+suffix, id, ownerID).Scan(
		&pair.ID, &pair.Name, &pair.TargetMode,
		&pair.Node1.AgentID, &pair.Node1.InstanceID, &pair.Node1.Address,
		&pair.Node2.AgentID, &pair.Node2.InstanceID, &pair.Node2.Address,
		&pair.Bidirectional, &paramsJSON, &pair.AutoEnabled, &pair.AutoIntervalSeconds,
		&pair.LastSessionID, &pair.LastRunAt, &pair.NextRunAt, &pair.Revision, &pair.CreatedAt, &pair.UpdatedAt,
	)
	if err != nil {
		return core.DashboardProbePair{}, mapError(err)
	}
	if len(paramsJSON) > 0 {
		_ = json.Unmarshal(paramsJSON, &pair.Parameters)
	}
	return pair, nil
}

func (s *Store) listDashboardProbePairs(ctx context.Context, filterID string) ([]core.DashboardProbePair, error) {
	ownerID := scopeForConfig(ctx).OwnerID
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.name, p.target_mode,
		       p.node1_agent_id, p.node1_instance_id, p.node1_address,
		       p.node2_agent_id, p.node2_instance_id, p.node2_address,
		       p.bidirectional, p.parameters, p.auto_enabled, p.auto_interval_seconds,
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
		var paramsJSON []byte
		if err := rows.Scan(
			&pair.ID, &pair.Name, &pair.TargetMode,
			&pair.Node1.AgentID, &pair.Node1.InstanceID, &pair.Node1.Address,
			&pair.Node2.AgentID, &pair.Node2.InstanceID, &pair.Node2.Address,
			&pair.Bidirectional, &paramsJSON, &pair.AutoEnabled, &pair.AutoIntervalSeconds,
			&pair.LastSessionID, &pair.ActiveState, &pair.LastRunAt, &pair.NextRunAt,
			&pair.Revision, &pair.CreatedAt, &pair.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(paramsJSON) > 0 {
			_ = json.Unmarshal(paramsJSON, &pair.Parameters)
		}
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
		SELECT pair_id, id, session_id, state, forward, reverse, error, sampled_at
		FROM (
			SELECT pair_id, id, session_id, state, forward, reverse, error, sampled_at,
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
		var fwdRaw, revRaw []byte
		if err := sampleRows.Scan(&pairID, &sample.ID, &sample.SessionID, &sample.State, &fwdRaw, &revRaw, &sample.Error, &sample.SampledAt); err != nil {
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
		m1, err := getMeta(p.Node1.AgentID, p.Node1.InstanceID)
		if err != nil {
			return nil, err
		}
		var m2 probeEndpointMeta
		if p.TargetMode != "ip" {
			m2, err = getMeta(p.Node2.AgentID, p.Node2.InstanceID)
			if err != nil {
				return nil, err
			}
		} else {
			p.Node2Name = p.Node2.Address
		}
		evaluateDashboardProbeAvailability(p, m1, m2)
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
	fwdRes, fwdOK := d.Results[d.ID+"-forward"]
	if !fwdOK {
		fwdRes, fwdOK = d.Results[d.ID]
	}
	forward := extractPingDirectionMetrics(fwdRes, fwdOK, d.State)
	var reverse *core.DashboardProbeDirectionMetrics
	if d.Request.Bidirectional {
		revRes, revOK := d.Results[d.ID+"-reverse"]
		reverse = extractPingDirectionMetrics(revRes, revOK, d.State)
	}
	var errs []string
	if forward != nil && forward.Error != "" {
		errs = append(errs, forward.Error)
	}
	if reverse != nil && reverse.Error != "" {
		errs = append(errs, reverse.Error)
	}
	errSummary := strings.Join(errs, "; ")
	fwdRaw, _ := json.Marshal(forward)
	revRaw, _ := json.Marshal(reverse)
	sampledAt := time.Now().UTC()
	for _, pairID := range pairIDs {
		sampleID, err := core.NewID("dps")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO dashboard_probe_samples(id, pair_id, session_id, state, forward, reverse, error, sampled_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (pair_id, session_id) DO NOTHING`,
			sampleID, pairID, d.ID, d.State, fwdRaw, revRaw, errSummary, sampledAt,
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
		for _, cmd := range diagnosticCommands(d) {
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
	diagReq := core.DiagnosticRequest{
		RequestID:     requestID,
		TargetMode:    pair.TargetMode,
		Node1:         pair.Node1,
		Node2:         pair.Node2,
		Bidirectional: pair.Bidirectional,
		Parameters:    pair.Parameters,
	}
	session, createErr := createDiagnosticTx(scoped, tx, diagReq, now, dashboardProbeDeadline(now, pair.Parameters))
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
