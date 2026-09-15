package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
