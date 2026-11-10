package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type apiKeyAuthContextKey struct{}
type apiKeyAuth struct {
	ID          string
	Permissions []core.Permission
	Effective   []core.Permission
}

// WithAPIKeyAuth carries the original delegating credential into queued work.
func WithAPIKeyAuth(ctx context.Context, id string, permissions []core.Permission, effective ...[]core.Permission) context.Context {
	value := apiKeyAuth{ID: id, Permissions: append([]core.Permission(nil), permissions...)}
	if len(effective) > 0 {
		value.Effective = append([]core.Permission(nil), effective[0]...)
	}
	return context.WithValue(ctx, apiKeyAuthContextKey{}, value)
}

// BeginAPIKeyMutation makes existing store transactions nested savepoints. The
// handler's buffered success is released only after current authorization and
// the business changes have committed together.
func (s *Store) BeginAPIKeyMutation(ctx context.Context, expected core.APIKey, permissions []core.Permission) (context.Context, func(bool) error, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ctx, nil, err
	}
	callbacks := []func(){}
	requestContext := context.WithValue(context.WithValue(ctx, requestCommitCallbacksKey{}, &callbacks), requestTransactionKey{}, tx)
	finish := func(commit bool) error {
		defer tx.Rollback(context.WithoutCancel(ctx))
		if !commit {
			return nil
		}
		// Existing business locks are already held. Recheck creator and credential
		// in the same transaction that publishes the actual changes.
		if expected.CreatorType == "user" {
			var disabled bool
			var role core.Role
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT disabled,role,permissions FROM panel_users WHERE id=$1 FOR SHARE`, expected.CreatorID).Scan(&disabled, &role, &raw); err != nil {
				return mapError(err)
			}
			if disabled {
				return ErrForbidden
			}
			var current []core.Permission
			if err := json.Unmarshal(raw, &current); err != nil {
				return err
			}
			for _, p := range permissions {
				if !role.Allows(p) && !core.HasPermission(current, p) && !core.PersonalPermission(p) {
					return ErrForbidden
				}
			}
		}
		current, err := scanAPIKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM admin_api_keys WHERE id=$1 FOR SHARE`, expected.ID))
		if err != nil {
			return err
		}
		if current.Status != "active" || current.Revision != expected.Revision || current.Mode != core.APIKeyReadWrite || current.OwnerID != expected.OwnerID || current.CreatorID != expected.CreatorID {
			return fmt.Errorf("%w: API Key 授权已变更", ErrForbidden)
		}
		for _, p := range permissions {
			if !core.HasPermission(current.Permissions, p) {
				return ErrForbidden
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		for _, callback := range callbacks {
			callback()
		}
		return nil
	}
	return requestContext, finish, nil
}

func apiKeyTaskIdentity(ctx context.Context) (string, []byte) {
	value, _ := ctx.Value(apiKeyAuthContextKey{}).(apiKeyAuth)
	raw, _ := json.Marshal(value.Permissions)
	if value.ID == "" {
		raw = []byte(`[]`)
	}
	return value.ID, raw
}

// A missing credential or creator is denial. This predicate is shared by claim,
// reconnect, cancellation and archived result access through the existing task policy.
const unauthorizedTaskAPIKeySQL = unauthorizedDirectTaskAPIKeySQL + ` OR EXISTS(
 SELECT 1 FROM api_key_work w WHERE w.work_id=t.id AND starts_with(w.kind,'certificate-sync-task:')
 AND NOT EXISTS(SELECT 1 FROM admin_api_keys k WHERE k.id=w.api_key_id AND NOT k.disabled AND k.revoked_at IS NULL
 AND (k.expires_at IS NULL OR k.expires_at>now()) AND k.mode='read_write' AND k.permissions @> w.permissions
 AND ((k.creator_type='environment' AND EXISTS(SELECT 1 FROM api_key_environment_creators e WHERE e.id=k.creator_id))
 OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=k.creator_id AND NOT u.disabled AND (u.role='admin' OR
 u.permissions @> (w.permissions - ARRAY['certificates.read','certificates.manage','certificates.grant','preferences.read','preferences.write','agent-access.read','agent-access.write']::text[]))))))`

const unauthorizedDirectTaskAPIKeySQL = `(t.api_key_id<>'' AND NOT EXISTS(
 SELECT 1 FROM admin_api_keys key WHERE key.id=t.api_key_id AND NOT key.disabled
 AND key.revoked_at IS NULL AND (key.expires_at IS NULL OR key.expires_at>now())
 AND key.mode='read_write' AND key.permissions @> t.api_key_permissions
 AND ((key.creator_type='environment' AND EXISTS(SELECT 1 FROM api_key_environment_creators e WHERE e.id=key.creator_id)) OR EXISTS(SELECT 1 FROM panel_users creator
 WHERE creator.id=key.creator_id AND NOT creator.disabled
 AND (creator.role='admin' OR creator.permissions @> (t.api_key_permissions - ARRAY['certificates.read','certificates.manage','certificates.grant','preferences.read','preferences.write','agent-access.read','agent-access.write']::text[]))))))`

// LockAPIKeyExternalEffect serializes an irreversible external action with
// credential changes. The surrounding request transaction holds the lock until
// completion; remote failures remain observable through the existing result APIs.
func (s *Store) LockAPIKeyExternalEffect(ctx context.Context) error {
	auth, ok := ctx.Value(apiKeyAuthContextKey{}).(apiKeyAuth)
	if !ok {
		return nil
	}
	tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx)
	if !ok {
		return ErrForbidden
	}
	// Match credential management's creator-before-key lock order.
	rows, err := tx.Query(ctx, `SELECT u.id FROM panel_users u JOIN admin_api_keys k ON k.creator_type='user' AND k.creator_id=u.id WHERE k.id=$1 FOR SHARE OF u`, auth.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT NOT disabled AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())
 AND mode='read_write' AND permissions @> $2::jsonb AND ((creator_type='environment' AND EXISTS(SELECT 1 FROM api_key_environment_creators e WHERE e.id=creator_id)) OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=creator_id AND NOT u.disabled AND (u.role='admin' OR u.permissions @> ($2::jsonb - ARRAY['certificates.read','certificates.manage','certificates.grant','preferences.read','preferences.write','agent-access.read','agent-access.write']::text[])))) FROM admin_api_keys WHERE id=$1 FOR SHARE`, auth.ID, mustJSONPermissions(auth.Permissions)).Scan(&valid)
	if err != nil {
		return mapError(err)
	}
	if !valid {
		return ErrForbidden
	}
	return nil
}
func mustJSONPermissions(permissions []core.Permission) []byte {
	raw, _ := json.Marshal(permissions)
	return raw
}
