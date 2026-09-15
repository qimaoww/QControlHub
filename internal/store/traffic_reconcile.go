package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
