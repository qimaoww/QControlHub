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
