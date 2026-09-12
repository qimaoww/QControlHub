package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const trafficPolicyColumns = `id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,monitoring_enabled,discovered,reset_generation,
       received_bytes,sent_bytes,used_bytes,receive_bps,send_bps,period_start,period_end,blocked,
       enforcement_available,enforcement_error,last_reported_at,created_at,updated_at,last_collected_at,accounting,metadata_managed,
	   COALESCE(share_id,''),share_used_bytes,
	   (SELECT jsonb_build_object('id',share.id,'limit_bytes',share.limit_bytes,
			'used_bytes',share.used_bytes,'port_used_bytes',port_traffic_policies.share_used_bytes,'engines',share.engines,
			'revoked',shared_user.disabled OR NOT share.enabled OR share.status<>'accepted'
				OR NOT EXISTS(SELECT 1 FROM agents shared_agent WHERE shared_agent.id=share.agent_id
					AND shared_agent.features ? 'shared-engines-v1' AND shared_agent.features ? 'shared-traffic-v1'
					AND shared_agent.features ? 'independent-egress-v1'))
		FROM agent_shares share JOIN panel_users shared_user ON shared_user.id=share.user_id WHERE share.id=port_traffic_policies.share_id)`

type trafficPolicyScanner interface {
	Scan(dest ...any) error
}

func scanTrafficPolicy(row trafficPolicyScanner) (core.PortTrafficPolicy, error) {
	var policy core.PortTrafficPolicy
	err := row.Scan(
		&policy.ID, &policy.AgentID, &policy.Name, &policy.Engine, &policy.Port,
		&policy.Protocol, &policy.Cycle, &policy.CycleAnchor, &policy.LimitBytes, &policy.AutoBlock,
		&policy.QuotaEnabled, &policy.MonitoringEnabled, &policy.Discovered, &policy.ResetGeneration, &policy.ReceivedBytes, &policy.SentBytes,
		&policy.UsedBytes, &policy.ReceiveBPS, &policy.SendBPS, &policy.PeriodStart,
		&policy.PeriodEnd, &policy.Blocked, &policy.EnforcementAvailable,
		&policy.EnforcementError, &policy.LastReportedAt, &policy.CreatedAt, &policy.UpdatedAt,
		&policy.LastCollectedAt, &policy.Accounting,
		&policy.MetadataManaged,
		&policy.ShareID, &policy.ShareUsedBytes, &policy.SharedQuota,
	)
	return policy, err
}

func (s *Store) ListPortTrafficPolicies(ctx context.Context) ([]core.PortTrafficPolicy, error) {
	args := []any{}
	where := trafficAccessClause(ctx, "port_traffic_policies.agent_id", "port_traffic_policies.id", &args)
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE true`+where+` ORDER BY agent_id,port`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.PortTrafficPolicy, 0)
	for rows.Next() {
		policy, scanErr := scanTrafficPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

func (s *Store) AgentPortTrafficPolicies(ctx context.Context, agentID string) ([]core.PortTrafficPolicy, error) {
	args := []any{agentID}
	where := trafficAccessClause(ctx, "port_traffic_policies.agent_id", "port_traffic_policies.id", &args)
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE agent_id=$1 AND monitoring_enabled=true`+where+` ORDER BY port`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.PortTrafficPolicy, 0)
	for rows.Next() {
		policy, scanErr := scanTrafficPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

// ReconcilePortTrafficEndpoints makes listener discovery the source of
// monitor-only records. Quotas remain operator-managed metadata on top of
// those records: losing a configuration never deletes an enabled quota, and
// removing a quota from a discovered listener never stops accounting. When
// prune is true, monitoring-only records for listeners that left the saved
// configuration are removed; manual sync keeps every existing record and only
// adds or updates newly discovered listeners.
func (s *Store) ReconcilePortTrafficEndpoints(ctx context.Context, raw []core.PortTrafficEndpoint, prune bool) ([]string, error) {
	return s.reconcilePortTrafficEndpoints(ctx, raw, prune, nil, "")
}

// ReconcileAgentPortTrafficEndpoints limits both discovery and pruning to one
// node. Other nodes' monitors must never be removed by a partial snapshot.
func (s *Store) ReconcileAgentPortTrafficEndpoints(ctx context.Context, agentID string, raw []core.PortTrafficEndpoint, prune bool) ([]string, error) {
	if agentID == "" {
		return nil, ErrInvalid
	}
	for _, endpoint := range raw {
		if endpoint.AgentID != agentID {
			return nil, ErrInvalid
		}
	}
	return s.reconcilePortTrafficEndpoints(ctx, raw, prune, nil, agentID)
}

// TrafficSyncCandidates includes tombstones even when their configuration is gone.
func TrafficSyncCandidates(raw []core.PortTrafficEndpoint, policies []core.PortTrafficPolicy) ([]core.TrafficSyncCandidate, error) {
	endpoints, err := normalizePortTrafficEndpointsWithLimit(raw, false)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool)
	result := make([]core.TrafficSyncCandidate, 0)
	for _, policy := range policies {
		existing[trafficPortKey(policy.AgentID, policy.Port)] = true
		if !policy.MonitoringEnabled {
			result = append(result, core.TrafficSyncCandidate{PortTrafficEndpoint: core.PortTrafficEndpoint{AgentID: policy.AgentID, Name: policy.Name, Engine: policy.Engine, Port: policy.Port, Protocol: policy.Protocol}, Kind: "deleted"})
		}
	}
	for _, endpoint := range endpoints {
		if !existing[trafficPortKey(endpoint.AgentID, endpoint.Port)] {
			result = append(result, core.TrafficSyncCandidate{PortTrafficEndpoint: endpoint, Kind: "new"})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AgentID != result[j].AgentID {
			return result[i].AgentID < result[j].AgentID
		}
		return result[i].Port < result[j].Port
	})
	return result, nil
}

func (s *Store) SyncSelectedPortTrafficEndpoints(ctx context.Context, raw []core.PortTrafficEndpoint, selections []core.TrafficSyncSelection) ([]string, error) {
	if len(selections) == 0 || len(selections) > 4096 {
		return nil, fmt.Errorf("%w: select between 1 and 4096 ports", ErrInvalid)
	}
	return s.reconcilePortTrafficEndpoints(ctx, raw, false, selections, "")
}

func (s *Store) reconcilePortTrafficEndpoints(ctx context.Context, raw []core.PortTrafficEndpoint, prune bool, selections []core.TrafficSyncSelection, agentID string) ([]string, error) {
	// Selective sync budgets the selected final set, not every available candidate.
	endpoints, err := normalizePortTrafficEndpointsWithLimit(raw, selections == nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return nil, err
	}
	if agentID != "" {
		if err := requireAgentAdministration(ctx, tx, agentID); err != nil {
			return nil, err
		}
	}
	for _, endpoint := range endpoints {
		if err := requireAgentAdministration(ctx, tx, endpoint.AgentID); err != nil {
			return nil, err
		}
	}
	for _, selection := range selections {
		if err := requireAgentAdministration(ctx, tx, selection.AgentID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:traffic-endpoints'))`); err != nil {
		return nil, err
	}
	// Config/task creation locks Agent -> policy. Match that order before
	// inserting FK-referencing monitors, including during reconnect.
	locked, err := tx.Query(ctx, `SELECT id FROM agents WHERE ($1='' OR id=$1) ORDER BY id FOR UPDATE`, agentID)
	if err != nil {
		return nil, err
	}
	locked.Close()
	if err := locked.Err(); err != nil {
		return nil, err
	}
	where := ""
	var args []any
	if agentID != "" {
		where = " WHERE agent_id=$1"
		args = []any{agentID}
	}
	rows, err := tx.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies`+where+` ORDER BY id FOR UPDATE`, args...)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]core.PortTrafficPolicy)
	for rows.Next() {
		policy, scanErr := scanTrafficPolicy(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		existing[trafficPortKey(policy.AgentID, policy.Port)] = policy
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if selections != nil {
		available := make(map[string]core.PortTrafficEndpoint)
		for _, endpoint := range endpoints {
			available[trafficPortKey(endpoint.AgentID, endpoint.Port)] = endpoint
		}
		selected := make(map[string]bool)
		endpoints = nil
		for _, selection := range selections {
			key := trafficPortKey(selection.AgentID, selection.Port)
			if selection.AgentID == "" || selection.Port < 1 || selection.Port > 65535 || selected[key] {
				return nil, fmt.Errorf("%w: invalid or duplicate selected port", ErrInvalid)
			}
			selected[key] = true
			if policy, ok := existing[key]; ok {
				// Repeated submissions are harmless; existing monitors are never edited.
				if policy.MonitoringEnabled {
					continue
				}
				endpoints = append(endpoints, core.PortTrafficEndpoint{AgentID: policy.AgentID, Name: policy.Name, Engine: policy.Engine, Port: policy.Port, Protocol: policy.Protocol})
			} else if endpoint, ok := available[key]; ok {
				endpoints = append(endpoints, endpoint)
			} else {
				return nil, fmt.Errorf("%w: selected port is no longer available; refresh the list", ErrConflict)
			}
		}
	}

	desired := make(map[string]struct{}, len(endpoints))
	finalCountByAgent := make(map[string]int)
	for _, endpoint := range endpoints {
		desired[trafficPortKey(endpoint.AgentID, endpoint.Port)] = struct{}{}
		finalCountByAgent[endpoint.AgentID]++
	}
	for key, policy := range existing {
		if _, coveredByDiscovery := desired[key]; coveredByDiscovery {
			continue
		}
		if !policy.MonitoringEnabled {
			continue
		}
		if prune && policy.Discovered && !policy.QuotaEnabled {
			// This stale monitor-only record is pruned below, so it does not
			// occupy a slot in the agent's monitoring budget.
			continue
		}
		finalCountByAgent[policy.AgentID]++
	}
	for agentID, count := range finalCountByAgent {
		if count > 256 {
			return nil, fmt.Errorf("%w: agent %s would exceed 256 monitored ports", ErrConflict, agentID)
		}
	}
	changedAgents := make(map[string]struct{})
	writes := &pgx.Batch{}
	now := time.Now().UTC()
	anchor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, endpoint := range endpoints {
		key := trafficPortKey(endpoint.AgentID, endpoint.Port)
		if policy, exists := existing[key]; exists {
			if policy.ShareID != "" {
				continue // Reservations and cumulative billing survive draft edits.
			}
			if selections != nil {
				// Deleted history stays deleted. Preserve metadata and the reset generation
				// established by deletion; restoring never re-enables a quota or blocking.
				writes.Queue(`UPDATE port_traffic_policies SET monitoring_enabled=true,quota_enabled=false,auto_block=false,blocked=false,discovered=false,metadata_managed=true,updated_at=now() WHERE id=$1`, policy.ID)
				changedAgents[endpoint.AgentID] = struct{}{}
				continue
			}
			updateMetadata := !policy.QuotaEnabled && !policy.MetadataManaged
			protocolChanged := updateMetadata && policy.Protocol != endpoint.Protocol
			metadataChanged := updateMetadata && (policy.Name != endpoint.Name || policy.Engine != endpoint.Engine || protocolChanged)
			if policy.Discovered && !metadataChanged {
				continue
			}
			writes.Queue(`
				UPDATE port_traffic_policies SET
					discovered=true,
					name=CASE WHEN $5 THEN $2 ELSE name END,
					engine=CASE WHEN $5 THEN $3 ELSE engine END,
					protocol=CASE WHEN $5 THEN $4::varchar(8) ELSE protocol END,
					reset_generation=reset_generation+CASE WHEN $6 THEN 1 ELSE 0 END,
					received_bytes=CASE WHEN $6 THEN 0 ELSE received_bytes END,
					sent_bytes=CASE WHEN $6 THEN 0 ELSE sent_bytes END,
					reported_received_bytes=CASE WHEN $6 THEN 0 ELSE reported_received_bytes END,
					reported_sent_bytes=CASE WHEN $6 THEN 0 ELSE reported_sent_bytes END,
					used_bytes=CASE WHEN $6 THEN 0 ELSE used_bytes END,
					receive_bps=CASE WHEN $6 THEN 0 ELSE receive_bps END,
					send_bps=CASE WHEN $6 THEN 0 ELSE send_bps END,
					period_start=CASE WHEN $6 THEN NULL ELSE period_start END,
					last_collected_at=CASE WHEN $6 THEN NULL ELSE last_collected_at END,
					accounting=CASE WHEN $6 THEN NULL ELSE accounting END,
					counter_epoch=CASE WHEN $6 THEN '' ELSE counter_epoch END,
					reported_lifetime_received_bytes=CASE WHEN $6 THEN 0 ELSE reported_lifetime_received_bytes END,
					reported_lifetime_sent_bytes=CASE WHEN $6 THEN 0 ELSE reported_lifetime_sent_bytes END,
					period_end=CASE WHEN $6 THEN NULL ELSE period_end END,
					blocked=CASE WHEN $6 THEN false ELSE blocked END,
					last_reported_at=CASE WHEN $6 THEN NULL ELSE last_reported_at END,
					traffic_history_initialized=CASE WHEN $6 THEN true ELSE traffic_history_initialized END,
					updated_at=now()
				WHERE id=$1`, policy.ID, endpoint.Name, endpoint.Engine, endpoint.Protocol, updateMetadata, protocolChanged)
			if protocolChanged {
				changedAgents[endpoint.AgentID] = struct{}{}
			}
			continue
		}
		id, idErr := core.NewID("trf")
		if idErr != nil {
			return nil, idErr
		}
		writes.Queue(`
			INSERT INTO port_traffic_policies
				(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,discovered,traffic_history_initialized,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,'monthly',$7,$8,false,false,true,true,$9,$9)`,
			id, endpoint.AgentID, endpoint.Name, endpoint.Engine, endpoint.Port, endpoint.Protocol, anchor, int64(math.MaxInt64), now)
		changedAgents[endpoint.AgentID] = struct{}{}
	}
	if prune {
		for key, policy := range existing {
			if policy.ShareID != "" {
				continue
			}
			if !policy.Discovered {
				continue
			}
			if _, exists := desired[key]; exists {
				continue
			}
			if policy.QuotaEnabled {
				writes.Queue(`UPDATE port_traffic_policies SET discovered=false,updated_at=now() WHERE id=$1`, policy.ID)
				continue
			}
			writes.Queue(`DELETE FROM port_traffic_policies WHERE id=$1`, policy.ID)
			changedAgents[policy.AgentID] = struct{}{}
		}
	}
	if writes.Len() > 0 {
		if err := tx.SendBatch(ctx, writes).Close(); err != nil {
			return nil, mapError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(changedAgents))
	for agentID := range changedAgents {
		result = append(result, agentID)
	}
	sort.Strings(result)
	return result, nil
}

func normalizePortTrafficEndpoints(raw []core.PortTrafficEndpoint) ([]core.PortTrafficEndpoint, error) {
	return normalizePortTrafficEndpointsWithLimit(raw, true)
}

func normalizePortTrafficEndpointsWithLimit(raw []core.PortTrafficEndpoint, enforceLimit bool) ([]core.PortTrafficEndpoint, error) {
	byPort := make(map[string]core.PortTrafficEndpoint)
	counts := make(map[string]int)
	for _, endpoint := range raw {
		endpoint.AgentID = strings.TrimSpace(endpoint.AgentID)
		endpoint.Name = strings.TrimSpace(endpoint.Name)
		if endpoint.AgentID == "" || !endpoint.Engine.Valid() || endpoint.Port < 1 || endpoint.Port > 65535 || !endpoint.Protocol.Valid() {
			return nil, errors.New("discovered endpoint is invalid")
		}
		if endpoint.Name == "" {
			endpoint.Name = fmt.Sprintf("Port %d", endpoint.Port)
		}
		if utf8.RuneCountInString(endpoint.Name) > 100 {
			return nil, errors.New("discovered endpoint name exceeds 100 characters")
		}
		key := trafficPortKey(endpoint.AgentID, endpoint.Port)
		if current, exists := byPort[key]; exists {
			if current.Protocol != endpoint.Protocol {
				current.Protocol = core.TrafficProtocolBoth
				byPort[key] = current
			}
			continue
		}
		counts[endpoint.AgentID]++
		if enforceLimit && counts[endpoint.AgentID] > 256 {
			return nil, errors.New("an agent can have at most 256 monitored ports")
		}
		byPort[key] = endpoint
	}
	result := make([]core.PortTrafficEndpoint, 0, len(byPort))
	for _, endpoint := range byPort {
		result = append(result, endpoint)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].AgentID != result[right].AgentID {
			return result[left].AgentID < result[right].AgentID
		}
		return result[left].Port < result[right].Port
	})
	return result, nil
}

func trafficPortKey(agentID string, port int) string {
	return agentID + "\x00" + fmt.Sprint(port)
}

func (s *Store) CreatePortTrafficPolicy(ctx context.Context, raw core.PortTrafficPolicyRequest) (core.PortTrafficPolicy, error) {
	request, err := core.NormalizePortTrafficPolicyRequest(raw, time.Now().UTC())
	if err != nil {
		return core.PortTrafficPolicy{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	if err := requireAgentAdministration(ctx, tx, request.AgentID); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	if err := validateTrafficPolicyAgent(ctx, tx, request.AgentID, request.Engine); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	var existingID string
	var quotaEnabled bool
	err = tx.QueryRow(ctx, `SELECT id,quota_enabled FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 FOR UPDATE`, request.AgentID, request.Port).Scan(&existingID, &quotaEnabled)
	if err == nil {
		if _, err := lockTrafficMutationPolicy(ctx, tx, existingID); err != nil {
			return core.PortTrafficPolicy{}, err
		}
		if quotaEnabled {
			return core.PortTrafficPolicy{}, fmt.Errorf("%w: this port already has a traffic quota", ErrConflict)
		}
		policy, updateErr := updatePortTrafficPolicyRow(ctx, tx, existingID, request)
		if updateErr != nil {
			return core.PortTrafficPolicy{}, updateErr
		}
		if err := tx.Commit(ctx); err != nil {
			return core.PortTrafficPolicy{}, err
		}
		return policy, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return core.PortTrafficPolicy{}, err
	}
	var policyCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM port_traffic_policies WHERE agent_id=$1`, request.AgentID).Scan(&policyCount); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	if policyCount >= 256 {
		return core.PortTrafficPolicy{}, fmt.Errorf("%w: an agent can have at most 256 traffic policies", ErrConflict)
	}
	id, err := core.NewID("trf")
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	now := time.Now().UTC()
	enableQuota := request.LimitBytes > 0
	effectiveAutoBlock := enableQuota && *request.AutoBlock
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		INSERT INTO port_traffic_policies (id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,traffic_history_initialized,metadata_managed,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,true,$12,$12)
		RETURNING `+trafficPolicyColumns,
		id, request.AgentID, request.Name, request.Engine, request.Port, request.Protocol,
		request.Cycle, request.CycleAnchor, request.LimitBytes, effectiveAutoBlock, enableQuota, now))
	if err != nil {
		return core.PortTrafficPolicy{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	return policy, nil
}

func (s *Store) UpdatePortTrafficPolicy(ctx context.Context, id string, raw core.PortTrafficPolicyRequest) (core.PortTrafficPolicy, error) {
	request, err := core.NormalizePortTrafficPolicyRequest(raw, time.Now().UTC())
	if err != nil {
		return core.PortTrafficPolicy{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	defer tx.Rollback(ctx)
	currentAgentID, err := lockTrafficMutationPolicy(ctx, tx, id)
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	if currentAgentID != request.AgentID {
		return core.PortTrafficPolicy{}, fmt.Errorf("%w: a traffic policy cannot be moved to another agent", ErrConflict)
	}
	if err := validateTrafficPolicyAgent(ctx, tx, request.AgentID, request.Engine); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	policy, err := updatePortTrafficPolicyRow(ctx, tx, id, request)
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	return policy, nil
}

func updatePortTrafficPolicyRow(ctx context.Context, tx pgx.Tx, id string, request core.PortTrafficPolicyRequest) (core.PortTrafficPolicy, error) {
	quotaEnabled := request.LimitBytes > 0
	// A policy without a quota never auto-blocks, matching quota removal and
	// auto-discovered monitoring records. This keeps monitor-only entries from
	// holding a stale auto_block toggle in the management view.
	autoBlock := quotaEnabled && *request.AutoBlock
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		UPDATE port_traffic_policies SET
			name=$2,engine=$3,port=$4,protocol=$5::varchar(8),cycle=$6::varchar(8),cycle_anchor=$7::date,limit_bytes=$8,auto_block=$9,quota_enabled=$10,monitoring_enabled=true,metadata_managed=true,
			reset_generation=reset_generation + CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 1 ELSE 0 END,
			received_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE received_bytes END,
			sent_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE sent_bytes END,
			reported_received_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE reported_received_bytes END,
			reported_sent_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE reported_sent_bytes END,
			used_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE used_bytes END,
			receive_bps=0,send_bps=0,blocked=false,enforcement_available=false,enforcement_error='',
			period_start=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE period_start END,
			last_collected_at=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE last_collected_at END,
			accounting=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE accounting END,
			counter_epoch=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN '' ELSE counter_epoch END,
			reported_lifetime_received_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE reported_lifetime_received_bytes END,
			reported_lifetime_sent_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE reported_lifetime_sent_bytes END,
			period_end=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE period_end END,
			last_reported_at=NULL,updated_at=now()
		WHERE id=$1 RETURNING `+trafficPolicyColumns,
		id, request.Name, request.Engine, request.Port, request.Protocol, request.Cycle,
		request.CycleAnchor, request.LimitBytes, autoBlock, quotaEnabled))
	if err != nil {
		return core.PortTrafficPolicy{}, mapError(err)
	}
	return policy, nil
}

func (s *Store) ResetPortTrafficPolicy(ctx context.Context, id string) (core.PortTrafficPolicy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockTrafficMutationPolicy(ctx, tx, id); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		UPDATE port_traffic_policies SET reset_generation=reset_generation+1,received_bytes=0,sent_bytes=0,
			last_collected_at=NULL,counter_epoch='',reported_lifetime_received_bytes=0,reported_lifetime_sent_bytes=0,accounting=NULL,
			reported_received_bytes=0,reported_sent_bytes=0,used_bytes=0,receive_bps=0,send_bps=0,period_start=NULL,period_end=NULL,blocked=false,
			enforcement_available=false,enforcement_error='',last_reported_at=NULL,updated_at=now()
		WHERE id=$1 RETURNING `+trafficPolicyColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.PortTrafficPolicy{}, ErrNotFound
	}
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	return policy, tx.Commit(ctx)
}

func (s *Store) DeletePortTrafficPolicy(ctx context.Context, id string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := lockTrafficMutationPolicy(ctx, tx, id); err != nil {
		return "", err
	}
	var agentID string
	var discovered bool
	err = tx.QueryRow(ctx, `SELECT agent_id,discovered FROM port_traffic_policies WHERE id=$1 FOR UPDATE`, id).Scan(&agentID, &discovered)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if discovered {
		_, err = tx.Exec(ctx, `UPDATE port_traffic_policies SET quota_enabled=false,limit_bytes=$2,auto_block=false,blocked=false,updated_at=now() WHERE id=$1`, id, int64(math.MaxInt64))
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM port_traffic_policies WHERE id=$1`, id)
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return agentID, nil
}

func (s *Store) DeletePortTrafficMonitoring(ctx context.Context, id string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := lockTrafficMutationPolicy(ctx, tx, id); err != nil {
		return "", err
	}
	var agentID string
	if err := tx.QueryRow(ctx, `SELECT agent_id FROM port_traffic_policies WHERE id=$1 FOR UPDATE`, id).Scan(&agentID); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_usage WHERE policy_id=$1`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_accounting WHERE policy_id=$1`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_accounting_epochs WHERE policy_id=$1`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE port_traffic_policies SET monitoring_enabled=false,quota_enabled=false,auto_block=false,blocked=false,
		last_collected_at=NULL,counter_epoch='',reported_lifetime_received_bytes=0,reported_lifetime_sent_bytes=0,accounting=NULL,
		received_bytes=0,sent_bytes=0,reported_received_bytes=0,reported_sent_bytes=0,used_bytes=0,receive_bps=0,send_bps=0,
		period_start=NULL,period_end=NULL,enforcement_available=false,enforcement_error='',last_reported_at=NULL,
		reset_generation=reset_generation+1,updated_at=now() WHERE id=$1`, id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return agentID, nil
}

func validateTrafficPolicyAgent(ctx context.Context, tx pgx.Tx, agentID string, engine core.Engine) error {
	var capabilities []core.Engine
	if err := tx.QueryRow(ctx, `SELECT capabilities FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&capabilities); errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("agent: %w", ErrNotFound)
	} else if err != nil {
		return err
	}
	if !containsEngine(capabilities, engine) {
		return fmt.Errorf("%w: agent does not advertise the selected engine", ErrInvalid)
	}
	return nil
}

func (s *Store) UpdatePortTrafficUsage(ctx context.Context, agentID string, usages []core.PortTrafficUsage, reportedAt time.Time) error {
	if len(usages) > 256 {
		return fmt.Errorf("%w: too many traffic usage records", ErrInvalid)
	}
	if len(usages) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(usages))
	ids := make([]string, 0, len(usages))
	for _, usage := range usages {
		if (usage.ShareID != "" && !core.ValidAgentShareID(usage.ShareID)) ||
			usage.ShareUsedBytes > math.MaxInt64 || (usage.ShareID == "" && usage.ShareUsedBytes != 0) {
			return fmt.Errorf("%w: invalid shared traffic usage", ErrInvalid)
		}
		if !usage.Accounting.Valid() || usage.Accounting != nil && usage.CounterEpoch == "" {
			return fmt.Errorf("%w: invalid traffic accounting metadata", ErrInvalid)
		}
		if _, exists := seen[usage.PolicyID]; exists {
			return fmt.Errorf("%w: duplicate traffic policy usage", ErrInvalid)
		}
		seen[usage.PolicyID] = struct{}{}
		if !core.ValidPortTrafficPolicyID(usage.PolicyID) || usage.ResetGeneration == 0 || usage.ResetGeneration > math.MaxInt64 ||
			usage.ReceivedBytes > math.MaxInt64 || usage.SentBytes > math.MaxInt64 ||
			usage.UsedBytes > math.MaxInt64 || usage.ReceiveBPS > math.MaxInt64 || usage.SendBPS > math.MaxInt64 ||
			usage.UsedBytes != saturatedStoredTrafficAdd(usage.ReceivedBytes, usage.SentBytes) ||
			usage.LifetimeReceivedBytes > math.MaxInt64 || usage.LifetimeSentBytes > math.MaxInt64 ||
			(usage.CounterEpoch != "" && (!core.ValidTrafficCounterEpoch(usage.CounterEpoch) ||
				usage.LifetimeReceivedBytes < usage.ReceivedBytes || usage.LifetimeSentBytes < usage.SentBytes)) ||
			(usage.EnforcementAvailable && ((usage.CounterEpoch == "") != usage.CollectedAt.IsZero())) ||
			usage.CollectedAt.After(reportedAt.Add(5*time.Minute)) ||
			usage.PeriodStart.IsZero() || !usage.PeriodEnd.After(usage.PeriodStart) || usage.PeriodEnd.Sub(usage.PeriodStart) > 367*24*time.Hour ||
			utf8.RuneCountInString(usage.EnforcementError) > 500 || strings.ContainsRune(usage.EnforcementError, '\x00') {
			return fmt.Errorf("%w: invalid traffic usage record", ErrInvalid)
		}
		ids = append(ids, usage.PolicyID)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Grant -> policy is the allocation-update lock order. Only shared
	// reports need these extra locks; ordinary monitoring keeps its fast path.
	var shareIDs []string
	for _, usage := range usages {
		if usage.ShareID != "" {
			shareIDs = append(shareIDs, usage.ShareID)
		}
	}
	if len(shareIDs) > 0 {
		locked, err := tx.Query(ctx, `SELECT id FROM agent_shares WHERE agent_id=$1 AND id=ANY($2::text[]) ORDER BY id FOR NO KEY UPDATE`, agentID, shareIDs)
		if err != nil {
			return err
		}
		locked.Close()
		if err := locked.Err(); err != nil {
			return err
		}
	}
	// Read and lock all baselines together, in a consistent order even when
	// concurrent reports list ports differently. Ownership and monitoring
	// checks stay inside the transaction, including for skipped generations.
	rows, err := tx.Query(ctx, `
		SELECT id,reset_generation,received_bytes,sent_bytes,reported_received_bytes,reported_sent_bytes,
		       traffic_history_initialized,period_start,period_end,last_reported_at,last_collected_at,counter_epoch,
		       reported_lifetime_received_bytes,reported_lifetime_sent_bytes,cycle,cycle_anchor,accounting
		FROM port_traffic_policies
		WHERE id=ANY($1::text[]) AND agent_id=$2 AND monitoring_enabled=true
		ORDER BY id FOR UPDATE`, ids, agentID)
	if err != nil {
		return err
	}
	type trafficBaseline struct {
		accounting                     *core.TrafficAccounting
		generation                     uint64
		received, sent                 uint64
		reportedReceived, reportedSent uint64
		historyInitialized             bool
		periodStart, periodEnd         *time.Time
		lastReported                   *time.Time
		lastCollected                  *time.Time
		epoch                          string
		lifetimeReceived, lifetimeSent uint64
		cycle                          core.TrafficCycle
		anchor                         time.Time
	}
	baselines := make(map[string]trafficBaseline, len(ids))
	for rows.Next() {
		var id string
		var current trafficBaseline
		if err := rows.Scan(&id,
			&current.generation, &current.received, &current.sent, &current.reportedReceived, &current.reportedSent,
			&current.historyInitialized,
			&current.periodStart, &current.periodEnd, &current.lastReported,
			&current.lastCollected, &current.epoch, &current.lifetimeReceived, &current.lifetimeSent, &current.cycle, &current.anchor,
			&current.accounting,
		); err != nil {
			rows.Close()
			return err
		}
		baselines[id] = current
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// A restored checkpoint can return to an older epoch with a NEW sample
	// timestamp. Consult the durable per-epoch baseline, not only the latest
	// policy epoch. The policy locks above serialize all writes to these rows.
	type epochKey struct {
		id         string
		generation uint64
		epoch      string
	}
	type epochBaseline struct {
		received, sent uint64
		accounting     *core.TrafficAccounting
	}
	history := make(map[epochKey]epochBaseline)
	var historyIDs, historyEpochs []string
	var historyGenerations []int64
	for _, usage := range usages {
		if _, ok := baselines[usage.PolicyID]; ok && usage.CounterEpoch != "" {
			historyIDs = append(historyIDs, usage.PolicyID)
			historyEpochs = append(historyEpochs, usage.CounterEpoch)
			historyGenerations = append(historyGenerations, int64(usage.ResetGeneration))
		}
	}
	if len(historyIDs) > 0 {
		rows, err := tx.Query(ctx, `SELECT e.policy_id,e.reset_generation,e.counter_epoch,e.lifetime_received_bytes,e.lifetime_sent_bytes,e.accounting
			FROM port_traffic_accounting_epochs e JOIN unnest($1::text[],$2::bigint[],$3::text[]) AS wanted(id,generation,epoch)
			ON e.policy_id=wanted.id AND e.reset_generation=wanted.generation AND e.counter_epoch=wanted.epoch`, historyIDs, historyGenerations, historyEpochs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key epochKey
			var value epochBaseline
			if err := rows.Scan(&key.id, &key.generation, &key.epoch, &value.received, &value.sent, &value.accounting); err != nil {
				rows.Close()
				return err
			}
			history[key] = value
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	writes := make([]trafficUsageWrite, 0, len(baselines))
	reportedAt = reportedAt.UTC()
	for _, usage := range usages {
		current, exists := baselines[usage.PolicyID]
		if !exists {
			continue
		}
		if current.generation != usage.ResetGeneration {
			continue
		}
		if usage.CounterEpoch != "" && usage.CounterEpoch == current.epoch && !sameAccountingScope(current.accounting, usage.Accounting) {
			return fmt.Errorf("%w: accounting scope changed without a new counter epoch", ErrInvalid)
		}
		if current.lastReported != nil && !reportedAt.After(current.lastReported.UTC()) {
			continue
		}
		// A failed sample updates health only. It must not advance the byte
		// baseline, create history, or turn an old sample into a live rate.
		if !usage.EnforcementAvailable && (usage.CounterEpoch != "" || !usage.CollectedAt.IsZero()) {
			writes = append(writes, trafficUsageWrite{ID: usage.PolicyID, Generation: usage.ResetGeneration,
				Blocked: usage.Blocked, EnforcementError: strings.TrimSpace(usage.EnforcementError)})
			continue
		}
		sampledAt := reportedAt
		previousSample := current.lastReported
		if !usage.CollectedAt.IsZero() {
			sampledAt = usage.CollectedAt.UTC()
			previousSample = current.lastCollected
			if previousSample != nil && !sampledAt.After(*previousSample) {
				continue
			}
			start, end, err := core.TrafficPeriodAt(current.anchor, current.cycle, sampledAt)
			if err != nil || !start.Equal(usage.PeriodStart) || !end.Equal(usage.PeriodEnd) {
				return fmt.Errorf("%w: traffic sample does not match policy calendar", ErrInvalid)
			}
		} else if current.lastCollected != nil {
			// Do not let a timestamp-less replay roll back a modern baseline.
			continue
		}
		if current.periodStart != nil && usage.PeriodStart.Before(*current.periodStart) {
			continue
		}
		// A full heartbeat and a metrics push can contain the same snapshot at
		// the same ticker boundary. Treat sub-second arrivals as duplicates so
		// they neither zero a valid live rate nor manufacture a short-interval
		// spike. The unchanged raw baseline means any real increment remains in
		// the next accepted sample.
		if usage.CollectedAt.IsZero() && current.lastReported != nil && reportedAt.Sub(current.lastReported.UTC()) < 500*time.Millisecond {
			continue
		}
		receivedDelta, sentDelta := uint64(0), uint64(0)
		newReceived, newSent := usage.ReceivedBytes, usage.SentBytes
		receiveBPS, sendBPS := uint64(0), uint64(0)
		samePeriod := current.periodStart != nil && current.periodEnd != nil &&
			current.periodStart.UTC().Equal(usage.PeriodStart.UTC()) && current.periodEnd.UTC().Equal(usage.PeriodEnd.UTC())
		periodUnknownAfterUpgrade := !current.historyInitialized && current.periodStart == nil && current.periodEnd == nil
		if samePeriod || periodUnknownAfterUpgrade {
			receivedDelta = trafficCounterDelta(usage.ReceivedBytes, current.reportedReceived)
			sentDelta = trafficCounterDelta(usage.SentBytes, current.reportedSent)
			newReceived = saturatedStoredTrafficAdd(current.received, receivedDelta)
			newSent = saturatedStoredTrafficAdd(current.sent, sentDelta)
		} else {
			// A new calendar period starts at zero on the Agent. Its first
			// report is both the new total and the first daily increment.
			receivedDelta, sentDelta = usage.ReceivedBytes, usage.SentBytes
		}
		if usage.CounterEpoch != "" {
			if current.epoch == usage.CounterEpoch {
				// Within one epoch lifetime totals cannot decrease. An older
				// state/snapshot is not evidence of a fresh counter restart.
				if usage.LifetimeReceivedBytes < current.lifetimeReceived || usage.LifetimeSentBytes < current.lifetimeSent {
					continue
				}
				receivedDelta = usage.LifetimeReceivedBytes - current.lifetimeReceived
				sentDelta = usage.LifetimeSentBytes - current.lifetimeSent
			} else if prior, known := history[epochKey{usage.PolicyID, usage.ResetGeneration, usage.CounterEpoch}]; known {
				accounting := usage.Accounting
				if accounting == nil {
					accounting = &core.TrafficAccounting{Source: "listener"}
				}
				if !sameAccountingScope(prior.accounting, accounting) {
					return fmt.Errorf("%w: historical accounting scope changed", ErrInvalid)
				}
				if usage.LifetimeReceivedBytes < prior.received || usage.LifetimeSentBytes < prior.sent {
					continue
				}
				receivedDelta, sentDelta = usage.LifetimeReceivedBytes-prior.received, usage.LifetimeSentBytes-prior.sent
			} else if current.epoch != "" || (current.periodStart == nil && !periodUnknownAfterUpgrade) {
				receivedDelta, sentDelta = usage.LifetimeReceivedBytes, usage.LifetimeSentBytes
			}
			if samePeriod || periodUnknownAfterUpgrade {
				newReceived = saturatedStoredTrafficAdd(current.received, receivedDelta)
				newSent = saturatedStoredTrafficAdd(current.sent, sentDelta)
			}
		}
		if previousSample != nil {
			receiveBPS = trafficAverageRate(receivedDelta, *previousSample, sampledAt)
			sendBPS = trafficAverageRate(sentDelta, *previousSample, sampledAt)
		}
		usedDelta := saturatedStoredTrafficAdd(receivedDelta, sentDelta)
		newUsed := saturatedStoredTrafficAdd(newReceived, newSent)
		writes = append(writes, trafficUsageWrite{
			ID: usage.PolicyID, Generation: usage.ResetGeneration,
			Received: newReceived, Sent: newSent, Used: newUsed, ReceiveBPS: receiveBPS, SendBPS: sendBPS,
			PeriodStart: usage.PeriodStart, PeriodEnd: usage.PeriodEnd,
			Blocked: usage.Blocked, EnforcementAvailable: usage.EnforcementAvailable,
			EnforcementError: strings.TrimSpace(usage.EnforcementError),
			ReportedReceived: usage.ReceivedBytes, ReportedSent: usage.SentBytes,
			ReceivedDelta: receivedDelta, SentDelta: sentDelta, UsedDelta: usedDelta,
			RecordSample: true, SampledAt: sampledAt, CollectedAt: nilIfZeroTime(usage.CollectedAt), CounterEpoch: usage.CounterEpoch,
			LifetimeReceived: usage.LifetimeReceivedBytes, LifetimeSent: usage.LifetimeSentBytes, Accounting: usage.Accounting,
		})
	}
	// One set-based statement also reduces database executor work on local
	// connections. The daily increments come only from successfully updated
	// rows; all baselines remain locked and any error rolls back the report.
	if len(writes) > 0 {
		payload, err := json.Marshal(writes)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, applyTrafficUsageSQL, agentID, reportedAt, payload); err != nil {
			return err
		}
	}
	if err := applySharedTrafficUsageTx(ctx, tx, agentID, usages); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type trafficUsageWrite struct {
	Accounting           *core.TrafficAccounting `json:"accounting"`
	RecordSample         bool                    `json:"record_sample"`
	SampledAt            time.Time               `json:"sampled_at"`
	CollectedAt          *time.Time              `json:"collected_at"`
	CounterEpoch         string                  `json:"counter_epoch"`
	LifetimeReceived     uint64                  `json:"lifetime_received"`
	LifetimeSent         uint64                  `json:"lifetime_sent"`
	ID                   string                  `json:"id"`
	Generation           uint64                  `json:"generation"`
	Received             uint64                  `json:"received"`
	Sent                 uint64                  `json:"sent"`
	Used                 uint64                  `json:"used"`
	ReceiveBPS           uint64                  `json:"receive_bps"`
	SendBPS              uint64                  `json:"send_bps"`
	PeriodStart          time.Time               `json:"period_start"`
	PeriodEnd            time.Time               `json:"period_end"`
	Blocked              bool                    `json:"blocked"`
	EnforcementAvailable bool                    `json:"enforcement_available"`
	EnforcementError     string                  `json:"enforcement_error"`
	ReportedReceived     uint64                  `json:"reported_received"`
	ReportedSent         uint64                  `json:"reported_sent"`
	ReceivedDelta        uint64                  `json:"received_delta"`
	SentDelta            uint64                  `json:"sent_delta"`
	UsedDelta            uint64                  `json:"used_delta"`
}

const applyTrafficUsageSQL = `
	WITH input AS (
		SELECT * FROM jsonb_to_recordset($3::jsonb) AS value(
			id text,generation bigint,received bigint,sent bigint,used bigint,receive_bps bigint,send_bps bigint,
			period_start timestamptz,period_end timestamptz,blocked boolean,enforcement_available boolean,
			enforcement_error text,reported_received bigint,reported_sent bigint,
			received_delta bigint,sent_delta bigint,used_delta bigint,record_sample boolean,sampled_at timestamptz,
			collected_at timestamptz,counter_epoch text,lifetime_received bigint,lifetime_sent bigint,accounting jsonb)
	), updated AS (
		UPDATE port_traffic_policies policy SET
			accounting=CASE WHEN input.record_sample THEN NULLIF(input.accounting,'null'::jsonb) ELSE policy.accounting END,
			received_bytes=CASE WHEN input.record_sample THEN input.received ELSE policy.received_bytes END,
			sent_bytes=CASE WHEN input.record_sample THEN input.sent ELSE policy.sent_bytes END,
			used_bytes=CASE WHEN input.record_sample THEN input.used ELSE policy.used_bytes END,
			receive_bps=input.receive_bps,send_bps=input.send_bps,
			period_start=CASE WHEN input.record_sample THEN input.period_start ELSE policy.period_start END,
			period_end=CASE WHEN input.record_sample THEN input.period_end ELSE policy.period_end END,
			blocked=input.blocked,enforcement_available=input.enforcement_available,enforcement_error=input.enforcement_error,
			last_reported_at=$2,traffic_history_initialized=policy.traffic_history_initialized OR input.record_sample,
			last_collected_at=CASE WHEN input.record_sample THEN input.collected_at ELSE policy.last_collected_at END,
			counter_epoch=CASE WHEN input.record_sample THEN input.counter_epoch ELSE policy.counter_epoch END,
			reported_lifetime_received_bytes=CASE WHEN input.record_sample THEN input.lifetime_received ELSE policy.reported_lifetime_received_bytes END,
			reported_lifetime_sent_bytes=CASE WHEN input.record_sample THEN input.lifetime_sent ELSE policy.reported_lifetime_sent_bytes END,
			reported_received_bytes=CASE WHEN input.record_sample THEN input.reported_received ELSE policy.reported_received_bytes END,
			reported_sent_bytes=CASE WHEN input.record_sample THEN input.reported_sent ELSE policy.reported_sent_bytes END
		FROM input WHERE policy.id=input.id AND policy.agent_id=$1 AND policy.reset_generation=input.generation
		RETURNING policy.id,policy.agent_id,policy.name,policy.engine,policy.port,policy.protocol
	), scoped_daily AS (
		INSERT INTO port_traffic_daily_accounting (policy_id,agent_id,reset_generation,usage_date,source,received_bytes,sent_bytes)
		SELECT updated.id,updated.agent_id,input.generation,(input.sampled_at AT TIME ZONE 'UTC')::date,
			COALESCE(input.accounting->>'source','listener'),input.received_delta,input.sent_delta
		FROM updated JOIN input ON input.id=updated.id WHERE input.record_sample
		ON CONFLICT (policy_id,reset_generation,usage_date,source) DO UPDATE SET
			received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_accounting.received_bytes::numeric+EXCLUDED.received_bytes)::bigint,
			sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_accounting.sent_bytes::numeric+EXCLUDED.sent_bytes)::bigint
	), epochs AS (
		INSERT INTO port_traffic_accounting_epochs (policy_id,agent_id,reset_generation,counter_epoch,accounting,
			lifetime_received_bytes,lifetime_sent_bytes,first_collected_at,last_collected_at)
		SELECT updated.id,updated.agent_id,input.generation,input.counter_epoch,COALESCE(NULLIF(input.accounting,'null'::jsonb),'{"source":"listener"}'::jsonb),input.lifetime_received,input.lifetime_sent,
			input.collected_at,input.collected_at
		FROM updated JOIN input ON input.id=updated.id
		WHERE input.record_sample AND input.collected_at IS NOT NULL AND input.counter_epoch<>''
		ON CONFLICT (policy_id,reset_generation,counter_epoch) DO UPDATE SET
			accounting=EXCLUDED.accounting,lifetime_received_bytes=EXCLUDED.lifetime_received_bytes,
			lifetime_sent_bytes=EXCLUDED.lifetime_sent_bytes,last_collected_at=EXCLUDED.last_collected_at
	)
	INSERT INTO port_traffic_daily_usage (
		policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,
		received_bytes,sent_bytes,used_bytes,peak_receive_bps,peak_send_bps,sample_count,first_reported_at,last_reported_at)
	SELECT updated.id,input.generation,(input.sampled_at AT TIME ZONE 'UTC')::date,updated.agent_id,
		updated.name,updated.engine,updated.port,updated.protocol,input.received_delta,input.sent_delta,input.used_delta,
		input.receive_bps,input.send_bps,1,$2,$2
	FROM updated JOIN input ON input.id=updated.id WHERE input.record_sample
	ON CONFLICT (policy_id,reset_generation,usage_date) DO UPDATE SET
		agent_id=EXCLUDED.agent_id,name=EXCLUDED.name,engine=EXCLUDED.engine,port=EXCLUDED.port,protocol=EXCLUDED.protocol,
		received_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.received_bytes::numeric+EXCLUDED.received_bytes)::bigint,
		sent_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.sent_bytes::numeric+EXCLUDED.sent_bytes)::bigint,
		used_bytes=LEAST(9223372036854775807::numeric,port_traffic_daily_usage.used_bytes::numeric+EXCLUDED.used_bytes)::bigint,
		peak_receive_bps=GREATEST(port_traffic_daily_usage.peak_receive_bps,EXCLUDED.peak_receive_bps),
		peak_send_bps=GREATEST(port_traffic_daily_usage.peak_send_bps,EXCLUDED.peak_send_bps),
		sample_count=port_traffic_daily_usage.sample_count+1,last_reported_at=EXCLUDED.last_reported_at`

func saturatedStoredTrafficAdd(left, right uint64) uint64 {
	if left >= math.MaxInt64 || right > uint64(math.MaxInt64)-left {
		return math.MaxInt64
	}
	return left + right
}

func sameAccountingScope(a, b *core.TrafficAccounting) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Source == b.Source && a.Inbound == b.Inbound && a.Mark == b.Mark && slices.Equal(a.Outbounds, b.Outbounds)
}

func nilIfZeroTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func trafficAverageRate(delta uint64, previous, current time.Time) uint64 {
	elapsed := current.Sub(previous)
	if delta == 0 || elapsed <= 0 || elapsed > 2*time.Minute {
		return 0
	}
	rate := math.Round(float64(delta) / elapsed.Seconds())
	if rate <= 0 {
		return 0
	}
	if rate >= math.MaxInt64 {
		return math.MaxInt64
	}
	return uint64(rate)
}

func trafficCounterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	// A counter may restart inside the same policy generation after local state
	// recovery. Count only the new post-restart bytes instead of losing them.
	return current
}

func (s *Store) ListPortTrafficDailyUsage(ctx context.Context, agentID, policyID string, month time.Time) ([]core.PortTrafficDailyUsage, error) {
	start := time.Date(month.UTC().Year(), month.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	where := "usage_date >= $1::date AND usage_date < $2::date"
	args := []any{start, end}
	for _, filter := range []struct{ column, value string }{{"agent_id", strings.TrimSpace(agentID)}, {"policy_id", strings.TrimSpace(policyID)}} {
		if filter.value != "" {
			args = append(args, filter.value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
		}
	}
	where += trafficAccessClause(ctx, "usage.agent_id", "usage.policy_id", &args)
	if !scopeForConfig(ctx).Admin {
		args = append(args, scopeForConfig(ctx).OwnerID)
		where += fmt.Sprintf(` AND (NOT EXISTS(SELECT 1 FROM panel_users u WHERE u.id=$%[1]d)
			OR EXISTS(SELECT 1 FROM agents a WHERE a.id=usage.agent_id AND a.owner_id=$%[1]d)
			OR EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.id=usage.policy_id AND usage.reset_generation>=p.share_generation))`, len(args))
	}
	// Aggregate generations and select their latest metadata in one windowed
	// scan, avoiding the second scan and join of materialized intermediate
	// results that caused timeout spikes in the mixed-load regression test.
	rows, err := s.pool.Query(ctx, `
		WITH daily AS (
			SELECT DISTINCT ON (policy_id,usage_date)
			       policy_id,agent_id,name,engine,port,protocol,usage_date,
			       LEAST(9223372036854775807::numeric,SUM(received_bytes) OVER day)::bigint AS received_bytes,
			       LEAST(9223372036854775807::numeric,SUM(sent_bytes) OVER day)::bigint AS sent_bytes,
			       LEAST(9223372036854775807::numeric,SUM(used_bytes) OVER day)::bigint AS used_bytes,
			       MAX(peak_receive_bps) OVER day AS peak_receive_bps,MAX(peak_send_bps) OVER day AS peak_send_bps,
			       LEAST(9223372036854775807::numeric,SUM(sample_count) OVER day)::bigint AS sample_count,
			       MIN(first_reported_at) OVER day AS first_reported_at,MAX(last_reported_at) OVER day AS last_reported_at
			FROM port_traffic_daily_usage AS usage WHERE `+where+`
			WINDOW day AS (PARTITION BY policy_id,usage_date)
			ORDER BY policy_id,usage_date,usage.last_reported_at DESC,reset_generation DESC
		)
		SELECT * FROM daily ORDER BY usage_date,agent_id,port,policy_id LIMIT 100000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.PortTrafficDailyUsage, 0)
	for rows.Next() {
		var item core.PortTrafficDailyUsage
		var day time.Time
		if err := rows.Scan(
			&item.PolicyID, &item.AgentID, &item.Name, &item.Engine, &item.Port, &item.Protocol, &day,
			&item.ReceivedBytes, &item.SentBytes, &item.UsedBytes, &item.PeakReceiveBPS, &item.PeakSendBPS,
			&item.SampleCount, &item.FirstReportedAt, &item.LastReportedAt,
		); err != nil {
			return nil, err
		}
		item.Day = day.UTC().Format(time.DateOnly)
		result = append(result, item)
	}
	return result, rows.Err()
}
