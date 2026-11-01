package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) SetAgentTimeZone(ctx context.Context, agentID, mode, zone string, expectedRevision int64, retry bool) (core.AgentTimeZoneState, bool, error) {
	if mode != "auto" && mode != "custom" || expectedRevision < 0 {
		return core.AgentTimeZoneState{}, false, fmt.Errorf("%w: invalid Agent time zone mode or revision", ErrInvalid)
	}
	if mode == "custom" && !core.ValidTimeZone(zone) || mode == "auto" && zone != "" {
		return core.AgentTimeZoneState{}, false, fmt.Errorf("%w: invalid IANA time zone", ErrInvalid)
	}
	tx, err := s.beginAgentAdministrationTx(ctx, agentID)
	if err != nil {
		return core.AgentTimeZoneState{}, false, err
	}
	defer tx.Rollback(ctx)
	var state core.AgentTimeZoneState
	err = tx.QueryRow(ctx, `SELECT time_zone_mode,time_zone,applied_time_zone,time_zone_status,time_zone_error,time_zone_revision,time_zone_auto_address_hash
		FROM agents WHERE id=$1 AND revoked_at IS NULL`, agentID).Scan(&state.Mode, &state.TimeZone, &state.AppliedTimeZone, &state.Status, &state.Error, &state.Revision, &state.AutoAddressHash)
	if err != nil {
		return core.AgentTimeZoneState{}, false, err
	}
	same := state.Mode == mode && (mode == "auto" || state.TimeZone == zone)
	if expectedRevision != state.Revision && !same {
		return state, false, fmt.Errorf("%w: Agent time zone settings changed; reload and retry", ErrConflict)
	}
	if same && state.Revision > 0 && (expectedRevision != state.Revision || state.Status == "applying" || !retry && state.Status != "failed" && state.Status != "unsupported") {
		if err := tx.Commit(ctx); err != nil {
			return core.AgentTimeZoneState{}, false, err
		}
		return state, false, nil
	}
	if mode == "auto" {
		zone = state.TimeZone
	}
	err = tx.QueryRow(ctx, `UPDATE agents SET time_zone_mode=$2,time_zone=$3,time_zone_status='pending',time_zone_error='',
		time_zone_revision=time_zone_revision+1,time_zone_auto_address_hash=''
		WHERE id=$1 AND revoked_at IS NULL
		RETURNING time_zone_mode,time_zone,time_zone_status,time_zone_error,time_zone_revision,time_zone_auto_address_hash`,
		agentID, mode, zone).
		Scan(&state.Mode, &state.TimeZone, &state.Status, &state.Error, &state.Revision, &state.AutoAddressHash)
	if err != nil {
		return core.AgentTimeZoneState{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.AgentTimeZoneState{}, false, err
	}
	return state, true, nil
}

func (s *Store) UpdateAgentTimeZoneResolution(ctx context.Context, agentID string, expectedRevision int64, expectedHash, zone, addressHash string, lookupErr error) (core.AgentTimeZoneState, error) {
	state, err := s.GetAgentTimeZone(ctx, agentID)
	if err != nil {
		return state, err
	}
	if state.Mode != "auto" || state.Revision == 0 || state.Revision != expectedRevision || state.AutoAddressHash != expectedHash {
		return state, nil
	}
	if lookupErr != nil {
		detail := core.AgentTimeZoneLookupFailure
		tag, updateErr := s.pool.Exec(ctx, `UPDATE agents SET time_zone_auto_address_hash=$3,time_zone_status='failed',time_zone_error=$4
			WHERE id=$1 AND revoked_at IS NULL AND time_zone_mode='auto' AND time_zone_revision=$2 AND time_zone_auto_address_hash=$5`, agentID, expectedRevision, addressHash, detail, expectedHash)
		if updateErr != nil {
			return state, updateErr
		}
		if tag.RowsAffected() == 0 {
			return s.GetAgentTimeZone(ctx, agentID)
		}
		state.Status, state.Error, state.AutoAddressHash = "failed", detail, addressHash
		return state, nil
	}
	if !core.ValidTimeZone(zone) {
		return state, fmt.Errorf("%w: invalid resolved IANA time zone", ErrInvalid)
	}
	if state.TimeZone == zone {
		tag, err := s.pool.Exec(ctx, `UPDATE agents SET time_zone_auto_address_hash=$3,
			time_zone_status=CASE WHEN applied_time_zone=$4 THEN 'applied' ELSE 'pending' END,
			time_zone_error=''
			WHERE id=$1 AND revoked_at IS NULL AND time_zone_mode='auto' AND time_zone_revision=$2 AND time_zone_auto_address_hash=$5`, agentID, expectedRevision, addressHash, zone, expectedHash)
		if err != nil {
			return state, err
		}
		if tag.RowsAffected() == 0 {
			return s.GetAgentTimeZone(ctx, agentID)
		}
		return s.GetAgentTimeZone(ctx, agentID)
	}
	var updated core.AgentTimeZoneState
	err = s.pool.QueryRow(ctx, `UPDATE agents SET time_zone=$3,time_zone_status='pending',time_zone_error='',time_zone_auto_address_hash=$4,
		time_zone_revision=time_zone_revision+1 WHERE id=$1 AND revoked_at IS NULL AND time_zone_mode='auto' AND time_zone_revision=$2 AND time_zone_auto_address_hash=$5
		RETURNING time_zone_mode,time_zone,applied_time_zone,time_zone_status,time_zone_error,time_zone_revision,time_zone_auto_address_hash`,
		agentID, expectedRevision, zone, addressHash, expectedHash).Scan(&updated.Mode, &updated.TimeZone, &updated.AppliedTimeZone, &updated.Status, &updated.Error, &updated.Revision, &updated.AutoAddressHash)
	if err == pgx.ErrNoRows {
		return s.GetAgentTimeZone(ctx, agentID)
	}
	return updated, err
}

func (s *Store) GetAgentTimeZone(ctx context.Context, agentID string) (core.AgentTimeZoneState, error) {
	var state core.AgentTimeZoneState
	err := s.pool.QueryRow(ctx, `SELECT time_zone_mode,time_zone,applied_time_zone,time_zone_status,time_zone_error,time_zone_revision,time_zone_auto_address_hash
		FROM agents WHERE id=$1 AND revoked_at IS NULL`, agentID).
		Scan(&state.Mode, &state.TimeZone, &state.AppliedTimeZone, &state.Status, &state.Error, &state.Revision, &state.AutoAddressHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrNotFound
	}
	return state, err
}

func (s *Store) GetAgentTimeZoneSource(ctx context.Context, agentID string) (core.Agent, error) {
	if err := requireAgentAccess(ctx, s.pool, agentID); err != nil {
		return core.Agent{}, err
	}
	var agent core.Agent
	var snapshot []byte
	var observed string
	err := s.pool.QueryRow(ctx, `SELECT agents.id,agents.owner_id,agents.time_zone_mode,agents.time_zone,agents.applied_time_zone,
		agents.time_zone_status,agents.time_zone_error,agents.time_zone_revision,agents.time_zone_auto_address_hash,
		COALESCE((SELECT metrics FROM agent_live_state WHERE agent_id=agents.id),'{}'::jsonb),agents.observed_public_ip
		FROM agents WHERE agents.id=$1 AND agents.revoked_at IS NULL`, agentID).
		Scan(&agent.ID, &agent.OwnerID, &agent.TimeZone.Mode, &agent.TimeZone.TimeZone, &agent.TimeZone.AppliedTimeZone,
			&agent.TimeZone.Status, &agent.TimeZone.Error, &agent.TimeZone.Revision, &agent.TimeZone.AutoAddressHash, &snapshot, &observed)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Agent{}, ErrNotFound
	}
	if err != nil {
		return core.Agent{}, err
	}
	if err := decodeAgentMetrics(snapshot, observed, &agent.Metrics); err != nil {
		return core.Agent{}, err
	}
	return agent, nil
}

func (s *Store) SetAgentTimeZoneStatus(ctx context.Context, agentID string, revision int64, status, detail string) error {
	_, err := s.pool.Exec(ctx, `UPDATE agents SET time_zone_status=$3,time_zone_error=$4
		WHERE id=$1 AND time_zone_revision=$2 AND ($3 <> 'failed' OR time_zone_status='applying')`, agentID, revision, status, truncateTimeZoneError(detail))
	return err
}

func (s *Store) AcknowledgeAgentTimeZone(ctx context.Context, agentID string, result core.AgentTimeZoneResult) (bool, error) {
	if result.Error != "" {
		detail := truncateTimeZoneError(result.Error)
		if detail == "" {
			detail = "Agent 无法应用该时区"
		}
		tag, err := s.pool.Exec(ctx, `UPDATE agents SET time_zone_status='failed',time_zone_error=$3
			WHERE id=$1 AND time_zone_revision=$2 AND time_zone_status='applying'`, agentID, result.Revision, detail)
		return err == nil && tag.RowsAffected() == 1, err
	}
	command, err := s.GetAgentTimeZone(ctx, agentID)
	if err != nil {
		return false, err
	}
	if command.Revision != result.Revision || command.TimeZone != result.TimeZone {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET applied_time_zone=$3,time_zone_status='applied',time_zone_error=''
		WHERE id=$1 AND time_zone_revision=$2 AND time_zone=$3`, agentID, result.Revision, result.TimeZone)
	return err == nil && tag.RowsAffected() == 1, err
}

func truncateTimeZoneError(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) > 200 {
		return detail[:200]
	}
	return detail
}
