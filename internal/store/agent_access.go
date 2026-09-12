package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Column names are constants from store code. Evaluate grants in the database,
// not a login-time cache, so revocation also applies to existing sessions.
func agentAccessClause(ctx context.Context, column string, args *[]any) string {
	scope := scopeForConfig(ctx)
	if scope.Admin {
		return ""
	}
	*args = append(*args, scope.OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	return ` AND (NOT EXISTS (SELECT 1 FROM panel_users access_user WHERE access_user.id=` + owner + `)
		OR EXISTS (SELECT 1 FROM panel_users access_user WHERE access_user.id=` + owner + ` AND NOT access_user.disabled
			AND (EXISTS (SELECT 1 FROM agents owned_agent WHERE owned_agent.id=` + column + ` AND owned_agent.owner_id=` + owner + `)
				OR EXISTS (SELECT 1 FROM agent_shares access_share
				WHERE access_share.user_id=access_user.id AND access_share.agent_id=` + column + ` AND access_share.enabled))))`
}

// Host-wide operations belong to the node owner, not to recipients of a
// sharing grant. Compatibility tokens retain their pre-account authority.
func agentAdministrationClause(ctx context.Context, column string, args *[]any) string {
	if scopeForConfig(ctx).Admin {
		return ""
	}
	*args = append(*args, scopeForConfig(ctx).OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	return ` AND (NOT EXISTS(SELECT 1 FROM panel_users owner_user WHERE owner_user.id=` + owner + `)
		OR EXISTS(SELECT 1 FROM agents owned_agent JOIN panel_users owner_user ON owner_user.id=owned_agent.owner_id
			WHERE owned_agent.id=` + column + ` AND owner_user.id=` + owner + ` AND NOT owner_user.disabled))`
}

func configAgentAccessClause(ctx context.Context, column string, args *[]any) string {
	clause := agentAccessClause(ctx, column, args)
	if clause == "" {
		return ""
	}
	return ` AND (` + column + ` IS NULL OR (true` + clause + `))`
}

func requireAgentAccess(ctx context.Context, executor storeExecutor, id string) error {
	if scopeForConfig(ctx).Admin {
		return nil
	}
	args := []any{id}
	where := agentAccessClause(ctx, "agents.id", &args)
	var allowed bool
	if err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents
		WHERE id=$1 AND revoked_at IS NULL`+where+`)`, args...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CheckAgentAccess(ctx context.Context, id string, administration bool) error {
	if administration {
		return requireAgentAdministration(ctx, s.pool, id)
	}
	return requireAgentAccess(ctx, s.pool, id)
}

func (s *Store) IsAgentIsolated(ctx context.Context) (bool, error) {
	return isolatedAgentUser(ctx, s.pool)
}

func lockAgentUser(ctx context.Context, tx pgx.Tx) error {
	if scopeForConfig(ctx).Admin {
		return nil
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM panel_users WHERE id=$1 FOR SHARE`, scopeForConfig(ctx).OwnerID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // Compatibility token identities have no panel-user row.
	}
	return err
}

func isolatedAgentUser(ctx context.Context, executor storeExecutor) (bool, error) {
	if scopeForConfig(ctx).Admin {
		return false, nil
	}
	var isolated bool
	err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_users WHERE id=$1 AND role<>'admin')`,
		scopeForConfig(ctx).OwnerID).Scan(&isolated)
	return isolated, err
}

func requireFleetAdministration(ctx context.Context, executor storeExecutor) error {
	if !scopeForConfig(ctx).Admin {
		return fmt.Errorf("%w: only administrators may manage global infrastructure", ErrForbidden)
	}
	return nil
}

// Shared users manage their configurations, not host-wide services,
// enrollment credentials or accounting infrastructure.
func requireAgentAdministration(ctx context.Context, executor storeExecutor, agentID string) error {
	if err := requireAgentAccess(ctx, executor, agentID); err != nil {
		return err
	}
	args := []any{agentID}
	where := agentAdministrationClause(ctx, "agents.id", &args)
	var allowed bool
	if err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND revoked_at IS NULL`+where+`)`, args...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: only the Agent owner may manage its host", ErrForbidden)
	}
	return nil
}

func (s *Store) UserAgentAccess(ctx context.Context, userID string) (core.AgentAccess, error) {
	if scope := scopeForConfig(ctx); !scope.Admin && userID != scope.OwnerID {
		return core.AgentAccess{}, ErrNotFound
	}
	result := core.AgentAccess{Shares: []core.AgentShare{}, OwnedAgentIDs: []string{}}
	err := s.pool.QueryRow(ctx, `SELECT role<>'admin',agent_access_revision,
		ARRAY(SELECT id FROM agents WHERE owner_id=$1 AND revoked_at IS NULL ORDER BY id)
		FROM panel_users WHERE id=$1`, userID).Scan(&result.Isolated, &result.Revision, &result.OwnedAgentIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	rows, err := s.pool.Query(ctx, `SELECT share.id,share.user_id,share.agent_id,agent.name,share.enabled,
		share.limit_bytes,share.used_bytes,share.created_at,share.updated_at,
		ARRAY(SELECT reserved.port FROM agent_share_ports reserved WHERE reserved.share_id=share.id ORDER BY reserved.port)
		FROM agent_shares share JOIN agents agent ON agent.id=share.agent_id
		WHERE share.user_id=$1 AND agent.revoked_at IS NULL ORDER BY agent.name,share.agent_id`, userID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var share core.AgentShare
		if err := rows.Scan(&share.ID, &share.UserID, &share.AgentID, &share.AgentName, &share.Enabled,
			&share.LimitBytes, &share.UsedBytes, &share.CreatedAt, &share.UpdatedAt, &share.Ports); err != nil {
			return result, err
		}
		result.Shares = append(result.Shares, share)
	}
	return result, rows.Err()
}

func (s *Store) SetUserAgentAccess(ctx context.Context, userID string, request core.AgentAccessRequest) (core.AgentAccess, error) {
	if !scopeForConfig(ctx).Admin {
		return core.AgentAccess{}, ErrForbidden
	}
	if len(request.Shares) > 512 {
		return core.AgentAccess{}, fmt.Errorf("%w: at most 512 Agents may be shared with a user", ErrInvalid)
	}
	shares := append([]core.AgentShareRequest(nil), request.Shares...)
	sort.Slice(shares, func(i, j int) bool { return shares[i].AgentID < shares[j].AgentID })
	for index, share := range shares {
		if share.AgentID == "" || strings.TrimSpace(share.AgentID) != share.AgentID ||
			share.LimitBytes > math.MaxInt64 || (index > 0 && shares[index-1].AgentID == share.AgentID) {
			return core.AgentAccess{}, fmt.Errorf("%w: invalid or duplicate Agent allocation", ErrInvalid)
		}
		ports := append([]int(nil), share.Ports...)
		sort.Ints(ports)
		if len(ports) > 256 {
			return core.AgentAccess{}, fmt.Errorf("%w: at most 256 ports per Agent", ErrInvalid)
		}
		for i, port := range ports {
			if port < 1 || port > 65535 || port == 10085 || port == 10086 || (i > 0 && ports[i-1] == port) {
				return core.AgentAccess{}, fmt.Errorf("%w: invalid, reserved or duplicate shared port", ErrInvalid)
			}
		}
		shares[index].Ports = ports
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.AgentAccess{}, err
	}
	defer tx.Rollback(ctx)
	var role core.Role
	var revision int64
	var wasIsolated bool
	if err := tx.QueryRow(ctx, `SELECT role,agent_access_revision,agent_isolation FROM panel_users WHERE id=$1 FOR UPDATE`, userID).Scan(&role, &revision, &wasIsolated); err != nil {
		return core.AgentAccess{}, mapError(err)
	}
	if request.Revision != 0 && request.Revision != revision {
		return core.AgentAccess{}, fmt.Errorf("%w: Agent allocation changed; reload before saving", ErrConflict)
	}
	if role == core.RoleAdmin && (request.Isolated || len(shares) > 0) {
		return core.AgentAccess{}, fmt.Errorf("%w: administrators retain access to every Agent", ErrInvalid)
	}
	if role != core.RoleAdmin && !request.Isolated {
		return core.AgentAccess{}, fmt.Errorf("%w: account resources are always private; grant access by sharing an Agent", ErrInvalid)
	}
	// Lock every affected Agent, including revoked grants, in stable order.
	// Creation takes the user lock before the Agent lock as well.
	rows, err := tx.Query(ctx, `SELECT id FROM agents WHERE id IN
		(SELECT agent_id FROM agent_shares WHERE user_id=$1
		 UNION SELECT agent_id FROM agent_engine_ownership WHERE owner_id=$1)
		OR id=ANY($2::text[]) ORDER BY id FOR UPDATE`,
		userID, sharedRequestAgentIDs(shares))
	if err != nil {
		return core.AgentAccess{}, err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return core.AgentAccess{}, err
	}
	var unsafeRunning bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t LEFT JOIN configs c ON c.id=t.config_id
		WHERE (t.owner_id=$1 OR c.owner_id=$1) AND t.status='running' AND t.shared_traffic_id='')`, userID).Scan(&unsafeRunning); err != nil {
		return core.AgentAccess{}, err
	}
	if request.Isolated && unsafeRunning {
		return core.AgentAccess{}, fmt.Errorf("%w: wait for the user's running task before changing Agent isolation", ErrConflict)
	}
	if request.Isolated && !wasIsolated {
		enabledAgents := []string{}
		for _, share := range shares {
			if shareEnabled(share) {
				enabledAgents = append(enabledAgents, share.AgentID)
			}
		}
		var unallocated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership
			WHERE owner_id=$1 AND (running OR uncertain) AND NOT(agent_id=ANY($2::text[])))`,
			userID, enabledAgents).Scan(&unallocated); err != nil {
			return core.AgentAccess{}, err
		}
		if unallocated {
			return core.AgentAccess{}, fmt.Errorf("%w: stop the user's unallocated running cores before enabling isolation", ErrConflict)
		}
	}
	for _, share := range shares {
		var features []string
		var ownerID string
		if err := tx.QueryRow(ctx, `SELECT features,owner_id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, share.AgentID).Scan(&features, &ownerID); err != nil {
			return core.AgentAccess{}, mapError(err)
		}
		if ownerID == userID {
			return core.AgentAccess{}, fmt.Errorf("%w: the user already owns this Agent", ErrInvalid)
		}
		if request.Isolated && shareEnabled(share) && !containsFeature(features, core.AgentFeatureSharedTraffic) {
			return core.AgentAccess{}, fmt.Errorf("%w: upgrade the Agent before assigning a shared traffic allowance", ErrConflict)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE panel_users SET agent_isolation=$2,agent_access_revision=agent_access_revision+1,updated_at=now() WHERE id=$1`, userID, request.Isolated); err != nil {
		return core.AgentAccess{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_shares SET enabled=false,updated_at=now() WHERE user_id=$1`, userID); err != nil {
		return core.AgentAccess{}, err
	}
	for _, share := range shares {
		id, err := core.NewID("shr")
		if err != nil {
			return core.AgentAccess{}, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO agent_shares(id,user_id,agent_id,limit_bytes,enabled) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(user_id,agent_id) DO UPDATE SET enabled=EXCLUDED.enabled,limit_bytes=EXCLUDED.limit_bytes,updated_at=now()
			RETURNING id`,
			id, userID, share.AgentID, share.LimitBytes, shareEnabled(share)).Scan(&id); err != nil {
			return core.AgentAccess{}, err
		}
		if err := s.setSharedPortsTx(ctx, tx, id, userID, share.AgentID, share.Ports); err != nil {
			return core.AgentAccess{}, err
		}
		if shareEnabled(share) {
			if err := s.bindUnmeteredSharedDeploymentTx(ctx, tx, id, userID, share.AgentID); err != nil {
				return core.AgentAccess{}, err
			}
		}
	}
	if request.Isolated {
		// Do not let work queued before a revocation execute afterwards.
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='canceled',error='Agent access revoked',finished_at=now(),config_content=NULL,lease_id=NULL
			WHERE (owner_id=$1 OR EXISTS(SELECT 1 FROM configs c WHERE c.id=tasks.config_id AND c.owner_id=$1))
			AND EXISTS(SELECT 1 FROM agents a WHERE a.id=tasks.agent_id AND a.owner_id<>$1)
			AND status='pending' AND (shared_traffic_id='' OR NOT EXISTS(
				SELECT 1 FROM agent_shares share WHERE share.user_id=$1 AND share.agent_id=tasks.agent_id AND share.enabled))`, userID); err != nil {
			return core.AgentAccess{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET sharing_revision=sharing_revision+1
		WHERE id IN(SELECT agent_id FROM agent_shares WHERE user_id=$1)`, userID); err != nil {
		return core.AgentAccess{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.AgentAccess{}, err
	}
	return s.UserAgentAccess(ctx, userID)
}

func (s *Store) bindUnmeteredSharedDeploymentTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string) error {
	var needed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership state
		WHERE state.agent_id=$1 AND state.owner_id=$2 AND (state.running OR state.uncertain)
		AND NOT EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.agent_id=state.agent_id
			AND p.engine=state.engine AND p.share_id=$3))`, agentID, userID, shareID).Scan(&needed)
	if err != nil || !needed {
		return err
	}
	return s.bindCurrentSharedDeploymentTx(ctx, tx, shareID, userID, agentID)
}

func shareEnabled(request core.AgentShareRequest) bool {
	return request.Enabled == nil || *request.Enabled
}

func sharedRequestAgentIDs(shares []core.AgentShareRequest) []string {
	ids := make([]string, len(shares))
	for i, share := range shares {
		ids[i] = share.AgentID
	}
	return ids
}
