package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
