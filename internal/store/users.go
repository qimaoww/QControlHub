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
	rows, err := s.pool.Query(ctx, `
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at
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
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,password_hash
		FROM panel_users WHERE username=$1 AND disabled=false`, username)
	var record userRecord
	if err := scanUserWithHash(row, &record); errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, "", ErrNotFound
	} else if err != nil {
		return core.User{}, "", err
	}
	return record.User, record.PasswordHash, nil
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
		INSERT INTO panel_users (id,username,display_name,role,permissions,password_hash,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7)
		RETURNING id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at`,
		id, username, displayName, request.Role, permissions, passwordHash, now)
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.User{}, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `
		SELECT id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at,password_hash
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
		UPDATE panel_users SET display_name=$2,role=$3,permissions=$4,disabled=$5,password_hash=$6,updated_at=now()
		WHERE id=$1
		RETURNING id,username,display_name,role,permissions,disabled,created_at,updated_at,last_login_at`,
		id, displayName, role, permissions, disabled, passwordHash).Scan(
		&current.User.ID, &current.User.Username, &current.User.DisplayName, &current.User.Role, &updatedPermissions,
		&current.User.Disabled, &current.User.CreatedAt, &current.User.UpdatedAt, &current.User.LastLoginAt)
	if err == nil {
		err = json.Unmarshal(updatedPermissions, &current.User.Permissions)
	}
	if err != nil {
		return core.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.User{}, err
	}
	return current.User, nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id string, disabled bool) (core.User, error) {
	return s.UpdateUser(ctx, id, core.UserUpdate{Disabled: &disabled}, "")
}

func scanUser(row pgx.Row) (core.User, error) {
	var user core.User
	var permissions []byte
	err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &permissions, &user.Disabled, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt)
	if err == nil {
		err = json.Unmarshal(permissions, &user.Permissions)
	}
	return user, err
}

func scanUserWithHash(row pgx.Row, record *userRecord) error {
	var permissions []byte
	err := row.Scan(&record.User.ID, &record.User.Username, &record.User.DisplayName, &record.User.Role, &permissions, &record.User.Disabled,
		&record.User.CreatedAt, &record.User.UpdatedAt, &record.User.LastLoginAt, &record.PasswordHash)
	if err == nil {
		err = json.Unmarshal(permissions, &record.User.Permissions)
	}
	return err
}
