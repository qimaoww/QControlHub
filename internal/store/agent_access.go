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

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Column names are constants from store code. Evaluate grants in the database,
// not a login-time cache, so revocation also applies to existing sessions.
func agentAccessClause(ctx context.Context, column string, args *[]any) string {
	return agentEngineAccessClause(ctx, column, "", args)
}

func agentEngineAccessClause(ctx context.Context, column, engineColumn string, args *[]any) string {
	scope, requestScoped := requestConfigScope(ctx)
	if !requestScoped {
		// Background maintenance and Agent execution have no request
		// principal, so ownership hiding never blocks fleet operations.
		return ""
	}
	*args = append(*args, scope.OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	// An owner-hidden node stays visible to its owner and to recipients of an
	// accepted share. Administrators and compatibility tokens that only hold
	// fleet-wide access must not see or manage it.
	hidden := ` AND (NOT EXISTS (SELECT 1 FROM agents hidden_agent WHERE hidden_agent.id=` + column + ` AND hidden_agent.admin_hidden)
		OR EXISTS (SELECT 1 FROM agents owned_agent WHERE owned_agent.id=` + column + ` AND owned_agent.owner_id=` + owner + `)`
	if !scope.Admin {
		hidden += ` OR EXISTS (SELECT 1 FROM agent_shares visibility_share
			WHERE visibility_share.user_id=` + owner + ` AND visibility_share.agent_id=` + column + `
				AND visibility_share.enabled AND visibility_share.status='accepted' AND cardinality(visibility_share.engines)>0)`
	}
	hidden += `)`
	if scope.Admin {
		return hidden
	}
	engineWhere := ""
	if engineColumn != "" {
		engineWhere = ` AND (` + engineColumn + `='' OR ` + engineColumn + `=ANY(access_share.engines))`
	}
	return hidden + ` AND (starts_with(` + owner + `::text,'token_')
		OR EXISTS (SELECT 1 FROM panel_users access_user WHERE access_user.id=` + owner + ` AND NOT access_user.disabled
			AND (EXISTS (SELECT 1 FROM agents owned_agent WHERE owned_agent.id=` + column + ` AND owned_agent.owner_id=` + owner + `)
				OR EXISTS (SELECT 1 FROM agent_shares access_share
				WHERE access_share.user_id=access_user.id AND access_share.agent_id=` + column + `
					AND access_share.enabled AND access_share.status='accepted' AND cardinality(access_share.engines)>0` + engineWhere + `))))`
}

// Host-wide operations belong to the node owner, not to recipients of a
// sharing grant. Administrators and compatibility tokens keep fleet-wide
// authority, but an owner-hidden node stays administrable only by its owner.
func agentAdministrationClause(ctx context.Context, column string, args *[]any) string {
	scope, requestScoped := requestConfigScope(ctx)
	if !requestScoped {
		return ""
	}
	*args = append(*args, scope.OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	hidden := ` AND (NOT EXISTS(SELECT 1 FROM agents hidden_agent WHERE hidden_agent.id=` + column + ` AND hidden_agent.admin_hidden)
		OR EXISTS(SELECT 1 FROM agents owned_agent WHERE owned_agent.id=` + column + ` AND owned_agent.owner_id=` + owner + `))`
	if scope.Admin {
		return hidden
	}
	return hidden + ` AND (starts_with(` + owner + `::text,'token_')
		OR EXISTS(SELECT 1 FROM agents owned_agent JOIN panel_users owner_user ON owner_user.id=owned_agent.owner_id
			WHERE owned_agent.id=` + column + ` AND owner_user.id=` + owner + ` AND NOT owner_user.disabled))`
}

func configAgentAccessClause(ctx context.Context, column, engineColumn string, args *[]any) string {
	clause := agentEngineAccessClause(ctx, column, engineColumn, args)
	if clause == "" {
		return ""
	}
	return ` AND (` + column + ` IS NULL OR (true` + clause + `))`
}

func requireAgentEngineAccess(ctx context.Context, executor storeExecutor, id string, engine core.Engine) error {
	if _, requestScoped := requestConfigScope(ctx); !requestScoped {
		return nil
	}
	if scopeForConfig(ctx).Admin {
		// Administrators are not scoped by shared engines, and the owner-hidden
		// rule needs no engine argument. Delegate so the positional arguments
		// stay dense instead of leaving an unreferenced $2 for PostgreSQL.
		return requireAgentAccess(ctx, executor, id)
	}
	args := []any{id, engine}
	where := agentEngineAccessClause(ctx, "agents.id", "$2::text", &args)
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

func (s *Store) CheckAgentEngineAccess(ctx context.Context, id string, engine core.Engine) error {
	return requireAgentEngineAccess(ctx, s.pool, id, engine)
}

func requireAgentAccess(ctx context.Context, executor storeExecutor, id string) error {
	if _, requestScoped := requestConfigScope(ctx); !requestScoped {
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

// requireAgentDeletion authorizes node removal. Owners remove their own
// nodes; administrators and compatibility tokens may remove any node,
// including an owner-hidden one, so a stale node can always be cleaned up
// without granting them any other access to it.
func requireAgentDeletion(ctx context.Context, executor storeExecutor, id string) error {
	scope, requestScoped := requestConfigScope(ctx)
	if requestScoped && (scope.Admin || strings.HasPrefix(scope.OwnerID, "token_")) {
		var exists bool
		if err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND revoked_at IS NULL)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return nil
	}
	return requireAgentAdministration(ctx, executor, id)
}

func (s *Store) CheckAgentDeletion(ctx context.Context, id string) error {
	return requireAgentDeletion(ctx, s.pool, id)
}

// requireNodeOwner grants host-wide authority only to the node owner. Unlike
// requireAgentAdministration it never widens to administrators, which is what
// keeps the hide toggle and every other owner-only decision out of their hands.
func requireNodeOwner(ctx context.Context, executor storeExecutor, agentID string) error {
	scope, requestScoped := requestConfigScope(ctx)
	if !requestScoped {
		return requireAgentAdministration(ctx, executor, agentID)
	}
	args := []any{agentID, scope.OwnerID}
	var allowed bool
	if err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents
		WHERE id=$1 AND revoked_at IS NULL AND owner_id=$2
		  AND ($2='' OR starts_with($2::text,'token_') OR EXISTS(
			SELECT 1 FROM panel_users owner_user WHERE owner_user.id=$2 AND NOT owner_user.disabled)))`, args...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

// SetAgentAdminHidden toggles owner-hidden visibility. Only the node owner can
// change it: administrators cannot see or manage a hidden node, and they can
// never hide or unhide a node that belongs to another account.
func (s *Store) SetAgentAdminHidden(ctx context.Context, id string, hidden bool) error {
	if err := requireNodeOwner(ctx, s.pool, id); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `UPDATE agents SET admin_hidden=$2 WHERE id=$1 AND revoked_at IS NULL`, id, hidden)
	if err != nil {
		return fmt.Errorf("set node administration visibility: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAgentDirectory returns the read-only cross-account summary behind the
// "other users' nodes" dialog. Administrators and compatibility tokens see
// every node, including owner-hidden ones, but only summary fields: bodies,
// logs, metrics, tasks and traffic stay behind the normal ownership rules.
func (s *Store) ListAgentDirectory(ctx context.Context) ([]core.AgentDirectoryEntry, error) {
	scope, requestScoped := requestConfigScope(ctx)
	if !requestScoped || (!scope.Admin && !strings.HasPrefix(scope.OwnerID, "token_")) {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT agents.id,agents.name,agents.owner_id,COALESCE(owner.username,''),agents.last_seen,
			`+agentOfflineThresholdSQL+`,agents.capabilities,agents.admin_hidden,agents.enrolled_at,
			COALESCE((SELECT array_agg(DISTINCT policy.port ORDER BY policy.port)
				FROM port_traffic_policies policy WHERE policy.agent_id=agents.id),'{}'::int[])
		FROM agents LEFT JOIN panel_users owner ON owner.id=agents.owner_id
		WHERE agents.revoked_at IS NULL AND agents.owner_id <> '' AND agents.owner_id <> $1
		ORDER BY agents.enrolled_at DESC
		LIMIT 1000`, scope.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("list agent directory: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	entries := make([]core.AgentDirectoryEntry, 0, 32)
	for rows.Next() {
		var entry core.AgentDirectoryEntry
		var capabilitiesJSON []byte
		var offlineThresholdSeconds int
		if err := rows.Scan(&entry.ID, &entry.Name, &entry.OwnerID, &entry.OwnerUsername, &entry.LastSeen,
			&offlineThresholdSeconds, &capabilitiesJSON, &entry.AdminHidden, &entry.EnrolledAt, &entry.Ports); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilitiesJSON, &entry.Capabilities); err != nil {
			return nil, err
		}
		entry.Status = "offline"
		if entry.LastSeen.After(now.Add(-time.Duration(offlineThresholdSeconds) * time.Second)) {
			entry.Status = "online"
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// hiddenAgentClause keeps rows tied to an owner-hidden node out of every
// request principal view except that node owner. Background maintenance runs
// without a scope and is never filtered.
// auditHiddenNodeClause drops audit rows that name an owner-hidden node
// directly, through one of its tasks or configurations, or inside the
// free-form detail text. Credentials created for a node that is hidden from
// the start are dropped as well because their row already carries the flag.
// Request principals keep their own rows; system maintenance is never filtered.
func auditHiddenNodeClause(ctx context.Context, args *[]any) string {
	scope, requestScoped := requestConfigScope(ctx)
	if !requestScoped {
		return ""
	}
	*args = append(*args, scope.OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	return ` AND NOT EXISTS(SELECT 1 FROM agents hidden_agent
		WHERE hidden_agent.admin_hidden AND hidden_agent.owner_id<>` + owner + `
		  AND (hidden_agent.id=audit_logs.target
		    OR position(hidden_agent.id in COALESCE(audit_logs.detail,'')) > 0
		    OR EXISTS(SELECT 1 FROM tasks hidden_task WHERE hidden_task.id=audit_logs.target AND hidden_task.agent_id=hidden_agent.id)
		    OR EXISTS(SELECT 1 FROM configs hidden_config WHERE hidden_config.id=audit_logs.target AND hidden_config.agent_id=hidden_agent.id)))
		AND NOT EXISTS(SELECT 1 FROM enrollment_tokens hidden_token
			WHERE hidden_token.admin_hidden AND hidden_token.owner_id<>` + owner + ` AND hidden_token.id=audit_logs.target)`
}

func hiddenAgentClause(ctx context.Context, column string, args *[]any) string {
	scope, requestScoped := requestConfigScope(ctx)
	if !requestScoped {
		return ""
	}
	*args = append(*args, scope.OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	return ` AND NOT EXISTS(SELECT 1 FROM agents hidden_agent
		WHERE hidden_agent.id=` + column + ` AND hidden_agent.admin_hidden AND hidden_agent.owner_id<>` + owner + `)`
}

func (s *Store) IsAgentIsolated(ctx context.Context) (bool, error) {
	return isolatedAgentUser(ctx, s.pool)
}

func lockAgentUser(ctx context.Context, tx pgx.Tx) error {
	scope := scopeForConfig(ctx)
	if scope.Admin || strings.HasPrefix(scope.OwnerID, "token_") {
		return nil
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM panel_users WHERE id=$1 AND NOT disabled FOR SHARE`, scope.OwnerID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// A request that waited behind account deletion must fail closed.
		// Only explicit token identities may lack a durable panel-user row.
		return ErrNotFound
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

// A sharing recipient manages personal configurations, not the owner's
// host-wide services, enrollment credentials or accounting infrastructure.
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
		if scopeForConfig(ctx).Admin {
			return ErrNotFound
		}
		return fmt.Errorf("%w: only the Agent owner may manage its host", ErrForbidden)
	}
	return nil
}

func requireHostConfigRead(ctx context.Context, executor storeExecutor, agentID string, engine core.Engine) error {
	if err := requireAgentAdministration(ctx, executor, agentID); err != nil {
		return err
	}
	if scopeForConfig(ctx).Admin {
		return nil
	}
	var allowed bool
	if err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND owner_id=$2)
		AND NOT EXISTS(SELECT 1 FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$3
			AND (config_uncertain OR (owner_id<>$2 AND config_id<>'')))`,
		agentID, scopeForConfig(ctx).OwnerID, engine).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: another user's deployed configuration cannot be read or imported", ErrForbidden)
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
		share.status,share.invitation_revision,COALESCE(agent_owner.username,''),
		share.limit_bytes,share.used_bytes,share.created_at,share.updated_at,share.engines,
		ARRAY(SELECT reserved.port FROM agent_share_ports reserved WHERE reserved.share_id=share.id ORDER BY reserved.port)
		FROM agent_shares share JOIN agents agent ON agent.id=share.agent_id
		LEFT JOIN panel_users agent_owner ON agent_owner.id=agent.owner_id
		WHERE share.user_id=$1 AND agent.revoked_at IS NULL ORDER BY agent.name,share.agent_id`, userID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var share core.AgentShare
		if err := rows.Scan(&share.ID, &share.UserID, &share.AgentID, &share.AgentName, &share.Enabled,
			&share.Status, &share.InvitationRevision, &share.OwnerUsername,
			&share.LimitBytes, &share.UsedBytes, &share.CreatedAt, &share.UpdatedAt, &share.Engines, &share.Ports); err != nil {
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
		engines, err := normalizeSharedEngines(share.Engines, shareEnabled(share))
		if err != nil {
			return core.AgentAccess{}, err
		}
		shares[index].Engines = engines
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
	rows, err := tx.Query(ctx, `SELECT id,features FROM agents WHERE id IN
		(SELECT agent_id FROM agent_shares WHERE user_id=$1
		 UNION SELECT agent_id FROM agent_engine_ownership WHERE owner_id=$1)
		OR id=ANY($2::text[]) ORDER BY id FOR UPDATE`,
		userID, sharedRequestAgentIDs(shares))
	if err != nil {
		return core.AgentAccess{}, err
	}
	var affected []core.Agent
	var affectedIDs []string
	for rows.Next() {
		var agent core.Agent
		if err := rows.Scan(&agent.ID, &agent.Features); err != nil {
			rows.Close()
			return core.AgentAccess{}, err
		}
		affected = append(affected, agent)
		affectedIDs = append(affectedIDs, agent.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return core.AgentAccess{}, err
	}
	if err := lockAgentSharesTx(ctx, tx, affectedIDs); err != nil {
		return core.AgentAccess{}, err
	}
	var unsafeRunning bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t LEFT JOIN configs c ON c.id=t.config_id
		JOIN agents a ON a.id=t.agent_id
		WHERE (t.owner_id=$1 OR c.owner_id=$1) AND a.owner_id<>$1
			AND t.status='running' AND t.shared_traffic_id='')`, userID).Scan(&unsafeRunning); err != nil {
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
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership state JOIN agents a ON a.id=state.agent_id
			WHERE state.owner_id=$1 AND a.owner_id<>$1 AND (running OR uncertain) AND NOT(agent_id=ANY($2::text[])))`,
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
		var supported []core.Engine
		if err := tx.QueryRow(ctx, `SELECT features,owner_id,COALESCE(supported_capabilities,capabilities)
			FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, share.AgentID).Scan(&features, &ownerID, &supported); err != nil {
			return core.AgentAccess{}, mapError(err)
		}
		if ownerID == userID {
			return core.AgentAccess{}, fmt.Errorf("%w: the user already owns this Agent", ErrInvalid)
		}
		if request.Isolated && shareEnabled(share) && !supportsSharedEngines(features) {
			return core.AgentAccess{}, fmt.Errorf("%w: upgrade the Agent before assigning a shared traffic allowance", ErrConflict)
		}
		if shareEnabled(share) && len(core.IntersectEngines(share.Engines, supported)) != len(share.Engines) {
			return core.AgentAccess{}, fmt.Errorf("%w: an allocated engine is not supported by this Agent", ErrInvalid)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE panel_users SET agent_isolation=$2,agent_access_revision=agent_access_revision+1,updated_at=now() WHERE id=$1`, userID, request.Isolated); err != nil {
		return core.AgentAccess{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_shares SET enabled=false,invitation_revision=invitation_revision+1,updated_at=now()
		WHERE user_id=$1 AND enabled AND NOT(agent_id=ANY($2::text[]))`, userID, sharedRequestAgentIDs(shares)); err != nil {
		return core.AgentAccess{}, err
	}
	for _, share := range shares {
		if err := s.setAgentShareTx(ctx, tx, userID, share.AgentID, share.LimitBytes, share.Engines, share.Ports, shareEnabled(share), share.Reinvite); err != nil {
			return core.AgentAccess{}, err
		}
	}
	for _, agent := range affected {
		if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agent.ID, agent.Features); err != nil {
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

func supportsSharedEngines(features []string) bool {
	return containsFeature(features, core.AgentFeatureSharedTraffic) &&
		containsFeature(features, core.AgentFeatureSharedEngines) &&
		containsFeature(features, core.AgentFeatureIndependentEgress)
}

func (s *Store) bindUnmeteredSharedDeploymentTx(ctx context.Context, tx pgx.Tx, shareID, userID, agentID string) error {
	var needed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership state
		WHERE state.agent_id=$1 AND state.owner_id=$2 AND (state.running OR state.uncertain)
		AND state.engine=ANY((SELECT engines FROM agent_shares WHERE id=$3)::text[])
		AND NOT EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.agent_id=state.agent_id
			AND p.engine=state.engine AND p.share_id=$3))`, agentID, userID, shareID).Scan(&needed)
	if err != nil || !needed {
		return err
	}
	return s.bindCurrentSharedDeploymentTx(ctx, tx, shareID, userID, agentID)
}

func normalizeSharedEngines(engines []core.Engine, enabled bool) ([]core.Engine, error) {
	if err := core.ValidateEngineCapabilities(engines); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if enabled && len(engines) == 0 {
		return nil, fmt.Errorf("%w: select at least one shared engine", ErrInvalid)
	}
	return core.IntersectEngines(core.AllEngines(), engines), nil
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
