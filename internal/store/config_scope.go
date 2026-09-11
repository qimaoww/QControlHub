package store

import (
	"context"
	"fmt"
)

type configScopeKey struct{}

type configScope struct {
	OwnerID string
	Admin   bool
}

// WithConfigScope binds configuration access to an authenticated principal.
// API middleware must set it before calling the store. Background Agent and
// maintenance operations without a panel principal remain trusted.
func WithConfigScope(ctx context.Context, ownerID string, admin bool) context.Context {
	return context.WithValue(ctx, configScopeKey{}, configScope{OwnerID: ownerID, Admin: admin})
}

func scopeForConfig(ctx context.Context) configScope {
	if scope, ok := ctx.Value(configScopeKey{}).(configScope); ok {
		return scope
	}
	return configScope{Admin: true}
}

// ownerClause filters in SQL, before fetching or decrypting another user's
// content. Column names are constants supplied by store code, never input.
func ownerClause(ctx context.Context, column string, args *[]any) string {
	scope := scopeForConfig(ctx)
	if scope.Admin {
		return ""
	}
	*args = append(*args, scope.OwnerID)
	return fmt.Sprintf(" AND %s=$%d", column, len(*args))
}

// A node editor always opens the current principal's workspace, including for
// administrators. Fleet maintenance without a principal sees all workspaces.
func workspaceOwnerClause(ctx context.Context, column string, args *[]any) string {
	if scope, ok := ctx.Value(configScopeKey{}).(configScope); ok {
		*args = append(*args, scope.OwnerID)
		return fmt.Sprintf(" AND %s=$%d", column, len(*args))
	}
	return ""
}
