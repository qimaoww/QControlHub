package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const trafficPolicyColumns = `id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,reset_generation,
       received_bytes,sent_bytes,used_bytes,receive_bps,send_bps,period_start,period_end,blocked,
       enforcement_available,enforcement_error,last_reported_at,created_at,updated_at`

type trafficPolicyScanner interface {
	Scan(dest ...any) error
}

func scanTrafficPolicy(row trafficPolicyScanner) (core.PortTrafficPolicy, error) {
	var policy core.PortTrafficPolicy
	err := row.Scan(
		&policy.ID, &policy.AgentID, &policy.Name, &policy.Engine, &policy.Port,
		&policy.Protocol, &policy.Cycle, &policy.CycleAnchor, &policy.LimitBytes,
		&policy.ResetGeneration, &policy.ReceivedBytes, &policy.SentBytes,
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
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE agent_id=$1 ORDER BY port`, agentID)
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

func (s *Store) CreatePortTrafficPolicy(ctx context.Context, raw core.PortTrafficPolicyRequest) (core.PortTrafficPolicy, error) {
	request, err := core.NormalizePortTrafficPolicyRequest(raw, time.Now().UTC())
	if err != nil {
		return core.PortTrafficPolicy{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	id, err := core.NewID("trf")
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.PortTrafficPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := validateTrafficPolicyAgent(ctx, tx, request.AgentID, request.Engine); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	var policyCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM port_traffic_policies WHERE agent_id=$1`, request.AgentID).Scan(&policyCount); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	if policyCount >= 256 {
		return core.PortTrafficPolicy{}, fmt.Errorf("%w: an agent can have at most 256 traffic policies", ErrConflict)
	}
	now := time.Now().UTC()
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		INSERT INTO port_traffic_policies (id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		RETURNING `+trafficPolicyColumns,
		id, request.AgentID, request.Name, request.Engine, request.Port, request.Protocol,
		request.Cycle, request.CycleAnchor, request.LimitBytes, now))
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
	policy, err := scanTrafficPolicy(tx.QueryRow(ctx, `
		UPDATE port_traffic_policies SET
			name=$2,engine=$3,port=$4,protocol=$5::varchar(8),cycle=$6::varchar(8),cycle_anchor=$7::date,limit_bytes=$8,
			reset_generation=reset_generation + CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 1 ELSE 0 END,
			received_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE received_bytes END,
			sent_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE sent_bytes END,
			used_bytes=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN 0 ELSE used_bytes END,
			receive_bps=0,send_bps=0,blocked=false,enforcement_available=false,enforcement_error='',
			period_start=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE period_start END,
			period_end=CASE WHEN port<>$4 OR protocol<>$5::varchar(8) OR cycle<>$6::varchar(8) OR cycle_anchor<>$7::date THEN NULL ELSE period_end END,
			last_reported_at=NULL,updated_at=now()
		WHERE id=$1 RETURNING `+trafficPolicyColumns,
		id, request.Name, request.Engine, request.Port, request.Protocol, request.Cycle,
		request.CycleAnchor, request.LimitBytes))
	if err != nil {
		return core.PortTrafficPolicy{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.PortTrafficPolicy{}, err
	}
	return policy, nil
}

func (s *Store) ResetPortTrafficPolicy(ctx context.Context, id string) (core.PortTrafficPolicy, error) {
	policy, err := scanTrafficPolicy(s.pool.QueryRow(ctx, `
		UPDATE port_traffic_policies SET reset_generation=reset_generation+1,received_bytes=0,sent_bytes=0,
			used_bytes=0,receive_bps=0,send_bps=0,period_start=NULL,period_end=NULL,blocked=false,
			enforcement_available=false,enforcement_error='',last_reported_at=NULL,updated_at=now()
		WHERE id=$1 RETURNING `+trafficPolicyColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.PortTrafficPolicy{}, ErrNotFound
	}
	return policy, err
}

func (s *Store) DeletePortTrafficPolicy(ctx context.Context, id string) (string, error) {
	var agentID string
	err := s.pool.QueryRow(ctx, `DELETE FROM port_traffic_policies WHERE id=$1 RETURNING agent_id`, id).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return agentID, err
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
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
		_, err := tx.Exec(ctx, `
			UPDATE port_traffic_policies SET received_bytes=$3,sent_bytes=$4,used_bytes=$5,receive_bps=$6,send_bps=$7,
				period_start=$8,period_end=$9,blocked=$10,enforcement_available=$11,enforcement_error=$12,
				last_reported_at=$13
			WHERE id=$1 AND agent_id=$2 AND reset_generation=$14`, usage.PolicyID, agentID, usage.ReceivedBytes, usage.SentBytes,
			usage.UsedBytes, usage.ReceiveBPS, usage.SendBPS, usage.PeriodStart, usage.PeriodEnd,
			usage.Blocked, usage.EnforcementAvailable, strings.TrimSpace(usage.EnforcementError), reportedAt.UTC(), usage.ResetGeneration)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
