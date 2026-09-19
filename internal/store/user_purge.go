package store

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// PurgedUser includes the exact affected fleet so callers can refresh live
// policies after commit, when the account and its grants no longer exist.
type PurgedUser struct {
	core.User
	AgentIDs []string
}

// PurgeUser preserves fleet data but never transfers the deleted principal's
// authority: installation credentials and outstanding task leases are revoked.
// Colliding node workspaces become archives instead of overwriting admin data.
func (s *Store) PurgeUser(ctx context.Context, id string) (PurgedUser, error) {
	if !scopeForConfig(ctx).Admin {
		return PurgedUser{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PurgedUser{}, err
	}
	defer tx.Rollback(ctx)

	// Sharing changes lock users before Agents. Lock surviving recipients as
	// well, in ID order, so their stale allocation drafts can be invalidated.
	rows, err := tx.Query(ctx, `SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision
		FROM panel_users WHERE id=$1 OR id IN (
			SELECT share.user_id FROM agent_shares share JOIN agents agent ON agent.id=share.agent_id
			WHERE agent.owner_id=$1
		) ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return PurgedUser{}, err
	}
	var result PurgedUser
	lockedUsers := []string{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return PurgedUser{}, err
		}
		lockedUsers = append(lockedUsers, user.ID)
		if user.ID == id {
			result.User = user
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PurgedUser{}, err
	}
	if result.ID == "" {
		return PurgedUser{}, ErrNotFound
	}
	if result.Role == core.RoleAdmin {
		return PurgedUser{}, fmt.Errorf("%w: administrator accounts cannot be deleted", ErrConflict)
	}

	// Enrollment consumes its credential before locking an Agent. Wait for
	// those transactions before discovering the complete set of owned nodes.
	rows, err = tx.Query(ctx, `SELECT id FROM enrollment_tokens WHERE owner_id=$1 ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return PurgedUser{}, err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PurgedUser{}, err
	}
	rows, err = tx.Query(ctx, `SELECT id,features FROM agents WHERE owner_id=$1 OR id IN (
		SELECT agent_id FROM agent_shares WHERE user_id=$1
		UNION SELECT agent_id FROM configs WHERE owner_id=$1
		UNION SELECT agent_id FROM tasks WHERE owner_id=$1
		UNION SELECT agent_id FROM agent_engine_ownership WHERE owner_id=$1
	) ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return PurgedUser{}, err
	}
	var affected []core.Agent
	result.AgentIDs = []string{}
	for rows.Next() {
		var agent core.Agent
		if err := rows.Scan(&agent.ID, &agent.Features); err != nil {
			rows.Close()
			return PurgedUser{}, err
		}
		affected = append(affected, agent)
		result.AgentIDs = append(result.AgentIDs, agent.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PurgedUser{}, err
	}
	// An administrator may have added a recipient while the initial user
	// locks were being acquired. Retry instead of locking a user after Agents.
	var changed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM agent_shares share JOIN agents agent ON agent.id=share.agent_id
		WHERE agent.owner_id=$1 AND NOT(share.user_id=ANY($2::text[]))
	)`, id, lockedUsers).Scan(&changed); err != nil {
		return PurgedUser{}, err
	}
	if changed {
		return PurgedUser{}, fmt.Errorf("%w: sharing changed while deleting the account; reload and retry", ErrConflict)
	}
	// Traffic reports lock grants before policies; use the same order.
	if err := lockAgentSharesTx(ctx, tx, result.AgentIDs); err != nil {
		return PurgedUser{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE panel_users SET disabled=true,auth_revision=auth_revision+1 WHERE id=$1`, id); err != nil {
		return PurgedUser{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_shares SET enabled=false
		WHERE user_id=$1 OR agent_id IN (SELECT id FROM agents WHERE owner_id=$1)`, id); err != nil {
		return PurgedUser{}, err
	}
	// Reauthorize before clearing owner/share IDs, otherwise a user's queued
	// work is promoted into unrestricted administrator work. Running outcomes
	// remain uncertain; their old result leases must no longer be accepted.
	for _, agent := range affected {
		if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agent.ID, agent.Features); err != nil {
			return PurgedUser{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE panel_users SET agent_access_revision=agent_access_revision+1
		WHERE id=ANY($1::text[])`, lockedUsers); err != nil {
		return PurgedUser{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET sharing_revision=sharing_revision+1
		WHERE id=ANY($1::text[])`, result.AgentIDs); err != nil {
		return PurgedUser{}, err
	}
	// Detach accounting and task records from the grants that are about to go.
	if _, err := tx.Exec(ctx, `
		UPDATE port_traffic_policies SET share_id=NULL
		WHERE share_id IN (SELECT id FROM agent_shares
			WHERE user_id=$1 OR agent_id IN (SELECT id FROM agents WHERE owner_id=$1))`, id); err != nil {
		return PurgedUser{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET shared_traffic_id='' WHERE shared_traffic_id IN
			(SELECT id FROM agent_shares WHERE user_id=$1 OR agent_id IN (SELECT id FROM agents WHERE owner_id=$1))`, id); err != nil {
		return PurgedUser{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_shares
		WHERE user_id=$1 OR agent_id IN (SELECT id FROM agents WHERE owner_id=$1)`, id); err != nil {
		return PurgedUser{}, err
	}
	// Keep both versions when the administrator already has a workspace for
	// the same node/engine. IDs, revisions and deployed snapshots stay intact.
	if _, err := tx.Exec(ctx, `UPDATE configs moved SET agent_id=NULL
		WHERE moved.owner_id=$1 AND moved.deleted_at IS NULL AND EXISTS(
			SELECT 1 FROM configs existing WHERE existing.owner_id=''
				AND existing.agent_id=moved.agent_id AND existing.engine=moved.engine AND existing.deleted_at IS NULL
		)`, id); err != nil {
		return PurgedUser{}, err
	}
	// The fleet stays intact: owned records join the administrator scope.
	for _, statement := range []string{
		`UPDATE agents SET owner_id='',admin_hidden=false WHERE owner_id=$1`,
		`UPDATE configs SET owner_id='' WHERE owner_id=$1`,
		`UPDATE config_templates SET owner_id='' WHERE owner_id=$1`,
		// Retire reusable commands as well as their secrets. Clearing reusable
		// releases the account-scoped unbound-name index without renaming or
		// overwriting the administrator's existing command.
		`UPDATE enrollment_tokens SET owner_id='',admin_hidden=false,reusable=false,
			revoked_at=COALESCE(revoked_at,now()),token_ciphertext=NULL WHERE owner_id=$1`,
		`UPDATE tasks SET owner_id='' WHERE owner_id=$1`,
		`UPDATE agent_engine_ownership SET owner_id='' WHERE owner_id=$1`,
	} {
		if _, err := tx.Exec(ctx, statement, id); err != nil {
			return PurgedUser{}, err
		}
	}
	// Personal, account-scoped data leaves with the account.
	for _, statement := range []string{
		`DELETE FROM user_panel_settings WHERE owner_id=$1`,
		`DELETE FROM ip_quality_schedules WHERE owner_id=$1`,
		`DELETE FROM user_substore_settings WHERE owner_id=$1`,
		`DELETE FROM substore_sync_targets WHERE owner_id=$1`,
	} {
		if _, err := tx.Exec(ctx, statement, id); err != nil {
			return PurgedUser{}, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM panel_users WHERE id=$1`, id); err != nil {
		if isForeignKeyViolation(err) {
			return PurgedUser{}, fmt.Errorf("%w: the account still owns records that must be moved first", ErrConflict)
		}
		return PurgedUser{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PurgedUser{}, err
	}
	return result, nil
}
