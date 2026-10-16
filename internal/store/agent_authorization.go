package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) IsAgentIsolated(ctx context.Context) (bool, error) {
	return isolatedAgentUser(ctx, s.pool)
}

func lockAgentUser(ctx context.Context, tx pgx.Tx, configIDs ...string) error {
	scope := scopeForConfig(ctx)
	auth, session := ctx.Value(principalAuthKey{}).(principalAuth)
	userID := scope.OwnerID
	if scope.System {
		userID = ""
		session = false
	} else if session {
		userID = auth.UserID
	} else if scope.Admin || strings.HasPrefix(userID, "token_") {
		userID = ""
	}
	if userID == "" && len(configIDs) == 0 {
		return nil
	}
	// Administrators can submit other users' configurations. Lock both owners
	// together in stable order, matching sharing and account deletion.
	rows, err := tx.Query(ctx, `SELECT id,role,disabled,auth_revision FROM panel_users
		WHERE id=$1 OR id IN(SELECT owner_id FROM configs WHERE id=ANY($2::text[]))
		ORDER BY id FOR SHARE`, userID, configIDs)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := userID == ""
	for rows.Next() {
		var id string
		var role core.Role
		var disabled bool
		var revision int64
		if err := rows.Scan(&id, &role, &disabled, &revision); err != nil {
			return err
		}
		if id == userID {
			found = !disabled
			if (session && auth.AuthRevision != revision) || (scope.Admin && role != core.RoleAdmin) {
				return fmt.Errorf("%w: account authorization changed; sign in again", ErrForbidden)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func (s *Store) beginAgentAdministrationTx(ctx context.Context, agentID string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = lockAgentUser(ctx, tx); err == nil {
		var id string
		err = tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
	}
	if err == nil {
		err = requireAgentAdministration(ctx, tx, agentID)
	}
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
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
