package store

import (
	"context"
	"fmt"
	"strings"

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
	// Revocation hides node configurations immediately, before background erasure.
	return ` AND (` + column + ` IS NULL OR (EXISTS(SELECT 1 FROM agents active_agent
		WHERE active_agent.id=` + column + ` AND active_agent.revoked_at IS NULL)` + clause + `))`
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
