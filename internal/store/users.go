package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type userRecord struct {
	User         core.User
	PasswordHash string
}

func (s *Store) ListUsers(ctx context.Context) ([]core.User, error) {
	if !scopeForConfig(ctx).Admin {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision
		FROM panel_users ORDER BY disabled ASC, username ASC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]core.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) UserForLogin(ctx context.Context, username string) (core.User, string, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision,password_hash
		FROM panel_users WHERE username=$1 AND disabled=false`, username)
	var record userRecord
	if err := scanUserWithHash(row, &record); errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, "", ErrNotFound
	} else if err != nil {
		return core.User{}, "", err
	}
	return record.User, record.PasswordHash, nil
}

// UserForSession rechecks the durable authentication revision on each request.
// A password/role/permission change or disable on another control-plane
// process must invalidate this process's previously cached login too.
func (s *Store) UserForSession(ctx context.Context, id string) (core.User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, `
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision
		FROM panel_users WHERE id=$1 AND NOT disabled`, id))
	return user, mapError(err)
}

func (s *Store) RecordUserLogin(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, `UPDATE panel_users SET last_login_at=now(),updated_at=now() WHERE id=$1 AND disabled=false`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, request core.UserRequest, passwordHash string) (core.User, error) {
	if !scopeForConfig(ctx).Admin {
		return core.User{}, ErrForbidden
	}
	if !request.Role.Valid() || strings.TrimSpace(passwordHash) == "" {
		return core.User{}, fmt.Errorf("%w: invalid user role or password hash", ErrInvalid)
	}
	id, err := core.NewID("usr")
	if err != nil {
		return core.User{}, err
	}
	username := strings.TrimSpace(request.Username)
	displayName := strings.TrimSpace(request.DisplayName)
	permissions, _ := json.Marshal(core.NormalizePermissions(request.Permissions))
	if request.Role == core.RoleAdmin {
		permissions, _ = json.Marshal(core.AllPermissions())
	}
	now := time.Now().UTC()
	row := s.pool.QueryRow(ctx, `
		INSERT INTO panel_users (id,username,display_name,role,permissions,password_hash,created_at,updated_at,agent_isolation)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8)
		RETURNING id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision`,
		id, username, displayName, request.Role, permissions, passwordHash, now, request.Role != core.RoleAdmin)
	user, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return core.User{}, fmt.Errorf("%w: username already exists", ErrConflict)
		}
		return core.User{}, err
	}
	return user, nil
}

// UpdateUser applies a complete partial update in one transaction. It refuses
// to remove the last active administrator, keeping the panel recoverable.
func (s *Store) UpdateUser(ctx context.Context, id string, update core.UserUpdate, passwordHash string) (core.User, error) {
	if !scopeForConfig(ctx).Admin {
		return core.User{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.User{}, err
	}
	defer tx.Rollback(ctx)
	// Two administrators being demoted concurrently must not both observe
	// the other as the remaining administrator.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:users'))`); err != nil {
		return core.User{}, err
	}
	row := tx.QueryRow(ctx, `
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision,password_hash
		FROM panel_users WHERE id=$1 FOR UPDATE`, id)
	var current userRecord
	if err := scanUserWithHash(row, &current); errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, ErrNotFound
	} else if err != nil {
		return core.User{}, err
	}
	role := current.User.Role
	disabled := current.User.Disabled
	displayName := current.User.DisplayName
	permissions := append([]core.Permission(nil), current.User.Permissions...)
	if permissions == nil {
		permissions = []core.Permission{}
	}
	if update.Role != nil {
		if !update.Role.Valid() {
			return core.User{}, fmt.Errorf("%w: invalid user role", ErrInvalid)
		}
		role = *update.Role
	}
	if update.Disabled != nil {
		disabled = *update.Disabled
	}
	if update.DisplayName != nil {
		displayName = strings.TrimSpace(*update.DisplayName)
	}
	if update.Permissions != nil {
		permissions = core.NormalizePermissions(*update.Permissions)
	} else if current.User.Role == core.RoleAdmin && role != core.RoleAdmin {
		permissions = []core.Permission{}
	}
	if role == core.RoleAdmin {
		permissions = core.AllPermissions()
	}
	if current.User.Role == core.RoleAdmin && (!current.User.Disabled && (role != core.RoleAdmin || disabled)) {
		var otherAdmins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM panel_users WHERE role='admin' AND disabled=false AND id<>$1`, id).Scan(&otherAdmins); err != nil {
			return core.User{}, err
		}
		if otherAdmins == 0 {
			return core.User{}, fmt.Errorf("%w: cannot disable or demote the last active administrator", ErrConflict)
		}
	}
	if strings.TrimSpace(passwordHash) == "" {
		passwordHash = current.PasswordHash
	}
	var updatedPermissions []byte
	err = tx.QueryRow(ctx, `
		UPDATE panel_users SET display_name=$2,role=$3::varchar(20),permissions=$4,disabled=$5,password_hash=$6,
			agent_isolation=($3::varchar(20)<>'admin'),
			agent_access_revision=agent_access_revision+1,auth_revision=auth_revision+1,updated_at=now()
		WHERE id=$1
		RETURNING id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,auth_revision`,
		id, displayName, role, permissions, disabled, passwordHash).Scan(
		&current.User.ID, &current.User.Username, &current.User.DisplayName, &current.User.Role, &updatedPermissions,
		&current.User.Disabled, &current.User.CreatedAt, &current.User.UpdatedAt, &current.User.LastLoginAt, &current.User.AuthRevision)
	if err == nil {
		err = json.Unmarshal(updatedPermissions, &current.User.Permissions)
	}
	if err != nil {
		return core.User{}, err
	}
	// Invalidate work in this transaction, not on the next Agent poll. A
	// disable/re-enable or revoke/regrant while disconnected must never revive
	// an old lease. Keep the user -> Agent -> task lock order used by creation.
	rows, err := tx.Query(ctx, `SELECT id,features FROM agents WHERE id IN (
		SELECT t.agent_id FROM tasks t LEFT JOIN configs c ON c.id=t.config_id
		WHERE (t.owner_id=$1 OR c.owner_id=$1) AND t.status IN ('pending','running')
	) ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return core.User{}, err
	}
	var affected []core.Agent
	for rows.Next() {
		var agent core.Agent
		if err := rows.Scan(&agent.ID, &agent.Features); err != nil {
			rows.Close()
			return core.User{}, err
		}
		affected = append(affected, agent)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return core.User{}, err
	}
	for _, agent := range affected {
		if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agent.ID, agent.Features); err != nil {
			return core.User{}, err
		}
	}
	if disabled || update.Role != nil || update.Permissions != nil {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET config_content=NULL
			WHERE owner_id=$1 AND action IN ('read-config','read-managed-config')`, id); err != nil {
			return core.User{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return core.User{}, err
	}
	return current.User, nil
}

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

func (s *Store) SetUserDisabled(ctx context.Context, id string, disabled bool) (core.User, error) {
	return s.UpdateUser(ctx, id, core.UserUpdate{Disabled: &disabled}, "")
}

func scanUser(row pgx.Row) (core.User, error) {
	var user core.User
	var permissions []byte
	err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &permissions, &user.Disabled, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt, &user.AuthRevision)
	if err == nil {
		err = json.Unmarshal(permissions, &user.Permissions)
		if err == nil && user.Role == core.RoleAdmin {
			user.Permissions = core.AllPermissions()
		}
	}
	return user, err
}

func scanUserWithHash(row pgx.Row, record *userRecord) error {
	var permissions []byte
	err := row.Scan(&record.User.ID, &record.User.Username, &record.User.DisplayName, &record.User.Role, &permissions, &record.User.Disabled,
		&record.User.CreatedAt, &record.User.UpdatedAt, &record.User.LastLoginAt, &record.User.AuthRevision, &record.PasswordHash)
	if err == nil {
		err = json.Unmarshal(permissions, &record.User.Permissions)
		if err == nil && record.User.Role == core.RoleAdmin {
			record.User.Permissions = core.AllPermissions()
		}
	}
	return err
}
