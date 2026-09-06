package store

import (
	"context"
	"encoding/json"
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

const trafficPolicyColumns = `id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,monitoring_enabled,discovered,reset_generation,
       received_bytes,sent_bytes,used_bytes,receive_bps,send_bps,period_start,period_end,blocked,
       enforcement_available,enforcement_error,last_reported_at,created_at,updated_at`

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
	)
	return policy, err
}

func (s *Store) ListPortTrafficPolicies(ctx context.Context) ([]core.PortTrafficPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies ORDER BY agent_id,port`)
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
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE agent_id=$1 AND monitoring_enabled=true ORDER BY port`, agentID)
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
// removing a quota from a discovered listener never stops accounting.
func (s *Store) ReconcilePortTrafficEndpoints(ctx context.Context, raw []core.PortTrafficEndpoint) ([]string, error) {
	endpoints, err := normalizePortTrafficEndpoints(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:traffic-endpoints'))`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies ORDER BY id FOR UPDATE`)
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
		if policy.MonitoringEnabled && (!policy.Discovered || policy.QuotaEnabled) {
			finalCountByAgent[policy.AgentID]++
		}
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
			protocolChanged := !policy.QuotaEnabled && policy.Protocol != endpoint.Protocol
			metadataChanged := !policy.QuotaEnabled && (policy.Name != endpoint.Name || policy.Engine != endpoint.Engine || protocolChanged)
			if policy.Discovered && !metadataChanged {
				continue
			}
			writes.Queue(`
				UPDATE port_traffic_policies SET
					discovered=true,
					name=CASE WHEN quota_enabled THEN name ELSE $2 END,
					engine=CASE WHEN quota_enabled THEN engine ELSE $3 END,
					protocol=CASE WHEN quota_enabled THEN protocol ELSE $4::varchar(8) END,
					reset_generation=reset_generation+CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 1 ELSE 0 END,
					received_bytes=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE received_bytes END,
					sent_bytes=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE sent_bytes END,
					reported_received_bytes=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE reported_received_bytes END,
					reported_sent_bytes=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE reported_sent_bytes END,
					used_bytes=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE used_bytes END,
					receive_bps=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE receive_bps END,
					send_bps=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN 0 ELSE send_bps END,
					period_start=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN NULL ELSE period_start END,
					period_end=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN NULL ELSE period_end END,
					blocked=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN false ELSE blocked END,
					last_reported_at=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN NULL ELSE last_reported_at END,
					traffic_history_initialized=CASE WHEN NOT quota_enabled AND protocol<>$4::varchar(8) THEN true ELSE traffic_history_initialized END,
					updated_at=now()
				WHERE id=$1`, policy.ID, endpoint.Name, endpoint.Engine, endpoint.Protocol)
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
	for key, policy := range existing {
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
		if counts[endpoint.AgentID] > 256 {
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
	if err := validateTrafficPolicyAgent(ctx, tx, request.AgentID, request.Engine); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	var existingID string
	var quotaEnabled bool
	err = tx.QueryRow(ctx, `SELECT id,quota_enabled FROM port_traffic_policies WHERE agent_id=$1 AND port=$2 FOR UPDATE`, request.AgentID, request.Port).Scan(&existingID, &quotaEnabled)
	if err == nil {
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
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		INSERT INTO port_traffic_policies (id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,traffic_history_initialized,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true,true,$11,$11)
		RETURNING `+trafficPolicyColumns,
		id, request.AgentID, request.Name, request.Engine, request.Port, request.Protocol,
		request.Cycle, request.CycleAnchor, request.LimitBytes, *request.AutoBlock, now))
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
	var currentAgentID string
	if err := tx.QueryRow(ctx, `SELECT agent_id FROM port_traffic_policies WHERE id=$1 FOR UPDATE`, id).Scan(&currentAgentID); errors.Is(err, pgx.ErrNoRows) {
		return core.PortTrafficPolicy{}, ErrNotFound
	} else if err != nil {
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
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		UPDATE port_traffic_policies SET
			name=$2,engine=$3,port=$4,protocol=$5::varchar(8),cycle=$6::varchar(8),cycle_anchor=$7::date,limit_bytes=$8,auto_block=$9,quota_enabled=true,monitoring_enabled=true,
			reset_generation=reset_generation + CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 1 ELSE 0 END,
			received_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE received_bytes END,
			sent_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE sent_bytes END,
			reported_received_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE reported_received_bytes END,
			reported_sent_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE reported_sent_bytes END,
			used_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE used_bytes END,
			receive_bps=0,send_bps=0,blocked=false,enforcement_available=false,enforcement_error='',
			period_start=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE period_start END,
			period_end=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE period_end END,
			last_reported_at=NULL,updated_at=now()
		WHERE id=$1 RETURNING `+trafficPolicyColumns,
		id, request.Name, request.Engine, request.Port, request.Protocol, request.Cycle,
		request.CycleAnchor, request.LimitBytes, *request.AutoBlock))
	if err != nil {
		return core.PortTrafficPolicy{}, mapError(err)
	}
	return policy, nil
}

func (s *Store) ResetPortTrafficPolicy(ctx context.Context, id string) (core.PortTrafficPolicy, error) {
	policy, err := scanTrafficPolicy(s.pool.QueryRow(ctx, `
		UPDATE port_traffic_policies SET reset_generation=reset_generation+1,received_bytes=0,sent_bytes=0,
			reported_received_bytes=0,reported_sent_bytes=0,used_bytes=0,receive_bps=0,send_bps=0,period_start=NULL,period_end=NULL,blocked=false,
			enforcement_available=false,enforcement_error='',last_reported_at=NULL,updated_at=now()
		WHERE id=$1 RETURNING `+trafficPolicyColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.PortTrafficPolicy{}, ErrNotFound
	}
	return policy, err
}

func (s *Store) DeletePortTrafficPolicy(ctx context.Context, id string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
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
	var agentID string
	if err := tx.QueryRow(ctx, `SELECT agent_id FROM port_traffic_policies WHERE id=$1 FOR UPDATE`, id).Scan(&agentID); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM port_traffic_daily_usage WHERE policy_id=$1`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE port_traffic_policies SET monitoring_enabled=false,quota_enabled=false,auto_block=false,blocked=false,
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
		if _, exists := seen[usage.PolicyID]; exists {
			return fmt.Errorf("%w: duplicate traffic policy usage", ErrInvalid)
		}
		seen[usage.PolicyID] = struct{}{}
		if !core.ValidPortTrafficPolicyID(usage.PolicyID) || usage.ResetGeneration == 0 || usage.ResetGeneration > math.MaxInt64 ||
			usage.ReceivedBytes > math.MaxInt64 || usage.SentBytes > math.MaxInt64 ||
			usage.UsedBytes > math.MaxInt64 || usage.ReceiveBPS > math.MaxInt64 || usage.SendBPS > math.MaxInt64 ||
			usage.ReceivedBytes > math.MaxUint64-usage.SentBytes || usage.UsedBytes != usage.ReceivedBytes+usage.SentBytes ||
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
	// Read and lock all baselines together, in a consistent order even when
	// concurrent reports list ports differently. Ownership and monitoring
	// checks stay inside the transaction, including for skipped generations.
	rows, err := tx.Query(ctx, `
		SELECT id,reset_generation,received_bytes,sent_bytes,reported_received_bytes,reported_sent_bytes,
		       traffic_history_initialized,period_start,period_end,last_reported_at
		FROM port_traffic_policies
		WHERE id=ANY($1::text[]) AND agent_id=$2 AND monitoring_enabled=true
		ORDER BY id FOR UPDATE`, ids, agentID)
	if err != nil {
		return err
	}
	type trafficBaseline struct {
		generation                     uint64
		received, sent                 uint64
		reportedReceived, reportedSent uint64
		historyInitialized             bool
		periodStart, periodEnd         *time.Time
		lastReported                   *time.Time
	}
	baselines := make(map[string]trafficBaseline, len(ids))
	for rows.Next() {
		var id string
		var current trafficBaseline
		if err := rows.Scan(&id,
			&current.generation, &current.received, &current.sent, &current.reportedReceived, &current.reportedSent,
			&current.historyInitialized,
			&current.periodStart, &current.periodEnd, &current.lastReported,
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
		if current.lastReported != nil && !reportedAt.After(current.lastReported.UTC()) {
			continue
		}
		// A full heartbeat and a metrics push can contain the same snapshot at
		// the same ticker boundary. Treat sub-second arrivals as duplicates so
		// they neither zero a valid live rate nor manufacture a short-interval
		// spike. The unchanged raw baseline means any real increment remains in
		// the next accepted sample.
		if current.lastReported != nil && reportedAt.Sub(current.lastReported.UTC()) < 500*time.Millisecond {
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
			if current.lastReported != nil {
				receiveBPS = trafficAverageRate(receivedDelta, current.lastReported.UTC(), reportedAt)
				sendBPS = trafficAverageRate(sentDelta, current.lastReported.UTC(), reportedAt)
			}
		} else {
			// A new calendar period starts at zero on the Agent. Its first
			// report is both the new total and the first daily increment.
			receivedDelta, sentDelta = usage.ReceivedBytes, usage.SentBytes
		}
		if receivedDelta > math.MaxUint64-sentDelta {
			return fmt.Errorf("%w: invalid traffic usage delta", ErrInvalid)
		}
		usedDelta := receivedDelta + sentDelta
		if usedDelta > math.MaxInt64 {
			return fmt.Errorf("%w: invalid traffic usage delta", ErrInvalid)
		}
		newUsed := saturatedStoredTrafficAdd(newReceived, newSent)
		writes = append(writes, trafficUsageWrite{
			ID: usage.PolicyID, Generation: usage.ResetGeneration,
			Received: newReceived, Sent: newSent, Used: newUsed, ReceiveBPS: receiveBPS, SendBPS: sendBPS,
			PeriodStart: usage.PeriodStart, PeriodEnd: usage.PeriodEnd,
			Blocked: usage.Blocked, EnforcementAvailable: usage.EnforcementAvailable,
			EnforcementError: strings.TrimSpace(usage.EnforcementError),
			ReportedReceived: usage.ReceivedBytes, ReportedSent: usage.SentBytes,
			ReceivedDelta: receivedDelta, SentDelta: sentDelta, UsedDelta: usedDelta,
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
	return tx.Commit(ctx)
}

type trafficUsageWrite struct {
	ID                   string    `json:"id"`
	Generation           uint64    `json:"generation"`
	Received             uint64    `json:"received"`
	Sent                 uint64    `json:"sent"`
	Used                 uint64    `json:"used"`
	ReceiveBPS           uint64    `json:"receive_bps"`
	SendBPS              uint64    `json:"send_bps"`
	PeriodStart          time.Time `json:"period_start"`
	PeriodEnd            time.Time `json:"period_end"`
	Blocked              bool      `json:"blocked"`
	EnforcementAvailable bool      `json:"enforcement_available"`
	EnforcementError     string    `json:"enforcement_error"`
	ReportedReceived     uint64    `json:"reported_received"`
	ReportedSent         uint64    `json:"reported_sent"`
	ReceivedDelta        uint64    `json:"received_delta"`
	SentDelta            uint64    `json:"sent_delta"`
	UsedDelta            uint64    `json:"used_delta"`
}

const applyTrafficUsageSQL = `
	WITH input AS (
		SELECT * FROM jsonb_to_recordset($3::jsonb) AS value(
			id text,generation bigint,received bigint,sent bigint,used bigint,receive_bps bigint,send_bps bigint,
			period_start timestamptz,period_end timestamptz,blocked boolean,enforcement_available boolean,
			enforcement_error text,reported_received bigint,reported_sent bigint,
			received_delta bigint,sent_delta bigint,used_delta bigint)
	), updated AS (
		UPDATE port_traffic_policies policy SET
			received_bytes=input.received,sent_bytes=input.sent,used_bytes=input.used,
			receive_bps=input.receive_bps,send_bps=input.send_bps,period_start=input.period_start,period_end=input.period_end,
			blocked=input.blocked,enforcement_available=input.enforcement_available,enforcement_error=input.enforcement_error,
			last_reported_at=$2,traffic_history_initialized=true,
			reported_received_bytes=input.reported_received,reported_sent_bytes=input.reported_sent
		FROM input WHERE policy.id=input.id AND policy.agent_id=$1 AND policy.reset_generation=input.generation
		RETURNING policy.id,policy.agent_id,policy.name,policy.engine,policy.port,policy.protocol
	)
	INSERT INTO port_traffic_daily_usage (
		policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,
		received_bytes,sent_bytes,used_bytes,peak_receive_bps,peak_send_bps,sample_count,first_reported_at,last_reported_at)
	SELECT updated.id,input.generation,($2::timestamptz AT TIME ZONE 'UTC')::date,updated.agent_id,
		updated.name,updated.engine,updated.port,updated.protocol,input.received_delta,input.sent_delta,input.used_delta,
		input.receive_bps,input.send_bps,1,$2,$2
	FROM updated JOIN input ON input.id=updated.id
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
			FROM port_traffic_daily_usage WHERE `+where+`
			WINDOW day AS (PARTITION BY policy_id,usage_date)
			ORDER BY policy_id,usage_date,last_reported_at DESC,reset_generation DESC
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
