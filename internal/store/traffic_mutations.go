package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
