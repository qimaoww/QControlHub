package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Reports lock grants before policies, in ID order. Lock the complete affected
// set before editing any policy so a multi-recipient save cannot invert that
// order or deadlock with a combined traffic report.
func lockAgentSharesTx(ctx context.Context, tx pgx.Tx, agentIDs []string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM agent_shares WHERE agent_id=ANY($1::text[])
		ORDER BY id FOR NO KEY UPDATE`, agentIDs)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// The caller holds the recipient and Agent locks, in that order. Owner and
// administrator edits share one consent transition, preserving the ledger.
func (s *Store) setAgentShareTx(ctx context.Context, tx pgx.Tx, userID, agentID string, limit uint64, ports []int, enabled, reinvite bool) error {
	var previous core.AgentShare
	err := tx.QueryRow(ctx, `SELECT id,enabled,status,limit_bytes,
		ARRAY(SELECT port FROM agent_share_ports WHERE share_id=agent_shares.id ORDER BY port)
		FROM agent_shares WHERE user_id=$1 AND agent_id=$2`,
		userID, agentID).Scan(&previous.ID, &previous.Enabled, &previous.Status, &previous.LimitBytes, &previous.Ports)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if reinvite && (!enabled || previous.Status != core.AgentShareRejected) {
		return fmt.Errorf("%w: only a rejected share can be reinvited", ErrInvalid)
	}
	status := previous.Status
	if errors.Is(err, pgx.ErrNoRows) {
		previous.ID, err = core.NewID("shr")
		if err != nil {
			return err
		}
		status = core.AgentSharePending
		if _, err := tx.Exec(ctx, `INSERT INTO agent_shares(id,user_id,agent_id,limit_bytes,enabled)
			VALUES($1,$2,$3,$4,$5)`, previous.ID, userID, agentID, limit, enabled); err != nil {
			return err
		}
	} else if previous.Enabled != enabled || previous.LimitBytes != limit || !slices.Equal(previous.Ports, ports) || reinvite {
		// Ordinary edits cannot undo rejection. Re-enabling a withdrawn share
		// always requires fresh consent; active accepted shares keep consent
		// when the owner adjusts their ports or total allowance.
		if enabled && (!previous.Enabled || reinvite) {
			status = core.AgentSharePending
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_shares SET enabled=$2,limit_bytes=$3,status=$4,
			invitation_revision=invitation_revision+1,updated_at=now() WHERE id=$1`,
			previous.ID, enabled, limit, status); err != nil {
			return err
		}
	}
	if err := s.setSharedPortsTx(ctx, tx, previous.ID, userID, agentID, ports); err != nil {
		return err
	}
	// Pending invitations reserve ports but must not activate runtime policy.
	if enabled && status == core.AgentShareAccepted {
		return s.bindUnmeteredSharedDeploymentTx(ctx, tx, previous.ID, userID, agentID)
	}
	return nil
}

// RespondAgentShare never accepts a user ID from the caller. Even an
// administrator cannot respond on another account's behalf.
func (s *Store) RespondAgentShare(ctx context.Context, shareID string, request core.AgentShareResponseRequest) (core.AgentAccess, error) {
	if request.Revision < 1 || (request.Decision != "accept" && request.Decision != "reject") {
		return core.AgentAccess{}, fmt.Errorf("%w: an invitation revision and accept or reject decision are required", ErrInvalid)
	}
	userID := scopeForConfig(ctx).OwnerID
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.AgentAccess{}, err
	}
	defer tx.Rollback(ctx)
	var user string
	if err := tx.QueryRow(ctx, `SELECT id FROM panel_users WHERE id=$1 AND role='user' AND NOT disabled FOR UPDATE`,
		userID).Scan(&user); err != nil {
		return core.AgentAccess{}, mapError(err)
	}
	var agentID string
	if err := tx.QueryRow(ctx, `SELECT agent_id FROM agent_shares WHERE id=$1 AND user_id=$2`,
		shareID, userID).Scan(&agentID); err != nil {
		return core.AgentAccess{}, mapError(err)
	}
	var features []string
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`,
		agentID).Scan(&features); err != nil {
		return core.AgentAccess{}, mapError(err)
	}
	var enabled bool
	var status core.AgentShareStatus
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT enabled,status,invitation_revision FROM agent_shares WHERE id=$1 FOR NO KEY UPDATE`,
		shareID).Scan(&enabled, &status, &revision); err != nil {
		return core.AgentAccess{}, mapError(err)
	}
	if !enabled || revision != request.Revision || status == core.AgentShareRejected ||
		(request.Decision == "accept" && status != core.AgentSharePending) {
		return core.AgentAccess{}, fmt.Errorf("%w: Agent invitation changed; reload before responding", ErrConflict)
	}
	next := core.AgentShareRejected
	if request.Decision == "accept" {
		if !containsFeature(features, core.AgentFeatureSharedTraffic) {
			return core.AgentAccess{}, fmt.Errorf("%w: upgrade the Agent before accepting a share", ErrConflict)
		}
		// Adoption of a legacy service uses its exact deployed snapshot and
		// may fail closed until the owner stops an uncertain deployment.
		if err := s.bindUnmeteredSharedDeploymentTx(ctx, tx, shareID, userID, agentID); err != nil {
			return core.AgentAccess{}, err
		}
		next = core.AgentShareAccepted
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_shares SET status=$2,invitation_revision=invitation_revision+1,updated_at=now() WHERE id=$1`,
		shareID, next); err != nil {
		return core.AgentAccess{}, err
	}
	if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agentID, features); err != nil {
		return core.AgentAccess{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET sharing_revision=sharing_revision+1 WHERE id=$1`, agentID); err != nil {
		return core.AgentAccess{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE panel_users SET agent_access_revision=agent_access_revision+1 WHERE id=$1`, userID); err != nil {
		return core.AgentAccess{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.AgentAccess{}, err
	}
	return s.UserAgentAccess(ctx, userID)
}
