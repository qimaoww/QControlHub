package store

import (
	"context"
	"fmt"
)

type configScopeKey struct{}

type configScope struct {
	OwnerID string
	Admin   bool
	// System marks trusted internal maintenance that must observe every node,
	// including owner-hidden ones. API requests never carry this flag.
	System bool
}

// WithConfigScope binds configuration access to an authenticated principal.
// API middleware must set it before calling the store. Background Agent and
// maintenance operations without a panel principal remain trusted.
func WithConfigScope(ctx context.Context, ownerID string, admin bool) context.Context {
	if ownerID == "" && !admin {
		// Empty ownership is reserved for pre-isolation administrator data.
		ownerID = "unassigned"
	}
	return context.WithValue(ctx, configScopeKey{}, configScope{OwnerID: ownerID, Admin: admin})
}

// WithSystemScope marks trusted internal maintenance. It sees owner-hidden
// nodes so accounting, retention and monitoring keep working; request
// principals are always bound with WithConfigScope instead.
func WithSystemScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, configScopeKey{}, configScope{Admin: true, System: true})
}

func scopeForConfig(ctx context.Context) configScope {
	if scope, ok := ctx.Value(configScopeKey{}).(configScope); ok {
		return scope
	}
	return configScope{Admin: true, System: true}
}

// requestConfigScope distinguishes an authenticated request principal (a
// session user or an API token, including the break-glass administrator token)
// from trusted background maintenance, which runs without a scope. Owner-hidden
// nodes are invisible to every request principal except their owner and
// explicit share recipients.
func requestConfigScope(ctx context.Context) (configScope, bool) {
	scope, ok := ctx.Value(configScopeKey{}).(configScope)
	return scope, ok && !scope.System
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
