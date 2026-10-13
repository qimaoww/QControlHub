package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type userManagementAuthKey struct{}

type userManagementAuth struct {
	UserID                      string
	AuthRevision                int64
	TokenAdministratorAvailable bool
}

// WithUserManagementAuth carries trusted server authentication state, never
// request JSON or a client's claim. It is separate from resource ownership:
// token administrators have no database account to protect as the current user.
func WithUserManagementAuth(ctx context.Context, userID string, authRevision int64, tokenAdministratorAvailable bool) context.Context {
	return context.WithValue(ctx, userManagementAuthKey{}, userManagementAuth{userID, authRevision, tokenAdministratorAvailable})
}

func accountActions(user core.User, auth userManagementAuth, activeAdmins int) *core.UserAccountActions {
	actions := &core.UserAccountActions{}
	if needsAdministratorLogin(user, auth) && activeAdmins == 1 {
		actions.DeleteRestriction = "last_administrator"
		actions.DisableRestriction = "last_administrator"
		actions.DemoteRestriction = "last_administrator"
	}
	if auth.UserID != "" && user.ID == auth.UserID {
		actions.DeleteRestriction = "current_account"
		actions.DisableRestriction = "current_account"
	}
	return actions
}

func needsAdministratorLogin(user core.User, auth userManagementAuth) bool {
	return user.Role == core.RoleAdmin && !user.Disabled && !auth.TokenAdministratorAvailable
}

// All account mutations take this lock before user/Agent locks, so purge,
// disable and demotion cannot each consume the other's remaining login.
func (s *Store) beginUserManagementTx(ctx context.Context) (pgx.Tx, error) {
	if !scopeForConfig(ctx).Admin {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:users'))`); err == nil {
		auth, _ := ctx.Value(userManagementAuthKey{}).(userManagementAuth)
		if auth.UserID != "" {
			// A request may have waited behind a concurrent role/password/disable
			// change. Recheck its authority after acquiring the mutation lock.
			var valid bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_users
				WHERE id=$1 AND role='admin' AND NOT disabled AND auth_revision=$2)`, auth.UserID, auth.AuthRevision).Scan(&valid)
			if err == nil && !valid {
				err = ErrForbidden
			}
		}
	}
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func protectAdministratorRemovalTx(ctx context.Context, tx pgx.Tx, user core.User) error {
	auth, _ := ctx.Value(userManagementAuthKey{}).(userManagementAuth)
	if !needsAdministratorLogin(user, auth) {
		return nil
	}
	var otherAdministrator bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_users
		WHERE role='admin' AND NOT disabled AND id<>$1)`, user.ID).Scan(&otherAdministrator); err != nil {
		return err
	}
	if !otherAdministrator {
		return fmt.Errorf("%w: cannot remove the last available administrator login", ErrConflict)
	}
	return nil
}
