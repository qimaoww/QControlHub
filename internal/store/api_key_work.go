package store

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func recordAPIKeyWork(ctx context.Context, executor storeExecutor, kind, id string) error {
	key, _ := ctx.Value(apiKeyAuthContextKey{}).(apiKeyAuth)
	if key.ID == "" {
		_, err := executor.Exec(ctx, `DELETE FROM api_key_work WHERE kind=$1 AND work_id=$2`, kind, id)
		return err
	}
	permissions, _ := json.Marshal(key.Permissions)
	_, err := executor.Exec(ctx, `INSERT INTO api_key_work(kind,work_id,api_key_id,permissions) VALUES($1,$2,$3,$4)
 ON CONFLICT(kind,work_id) DO UPDATE SET api_key_id=EXCLUDED.api_key_id,permissions=EXCLUDED.permissions`, kind, id, key.ID, permissions)
	return err
}
func apiKeyWorkAllowed(ctx context.Context, executor storeExecutor, kind, id string) (bool, error) {
	var denied bool
	err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_key_work w WHERE w.kind=$1 AND w.work_id=$2
 AND NOT EXISTS(SELECT 1 FROM admin_api_keys k WHERE k.id=w.api_key_id AND NOT k.disabled AND k.revoked_at IS NULL
 AND (k.expires_at IS NULL OR k.expires_at>now()) AND k.mode='read_write' AND k.permissions @> w.permissions
 AND ((k.creator_type='environment' AND EXISTS(SELECT 1 FROM api_key_environment_creators e WHERE e.id=k.creator_id)) OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=k.creator_id AND NOT u.disabled
 AND (u.role='admin' OR u.permissions @> (w.permissions - ARRAY['certificates.read','certificates.manage','certificates.grant','preferences.read','preferences.write','agent-access.read','agent-access.write']::text[]))))))`, kind, id).Scan(&denied)
	return !denied, err
}

// Environment credentials remain scoped to the current server registry. A
// restored credential retains its original read-only authorization semantics.
func (s *Store) SetAPIKeyEnvironmentCreators(ctx context.Context, creators []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM api_key_environment_creators WHERE NOT(id=ANY($1::text[]))`, creators); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO api_key_environment_creators(id) SELECT unnest($1::text[]) ON CONFLICT DO NOTHING`, creators); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) apiKeyWorkContext(ctx context.Context, kind, id string) (context.Context, error) {
	allowed, err := apiKeyWorkAllowed(ctx, s.pool, kind, id)
	if err != nil {
		return ctx, err
	}
	if !allowed {
		return ctx, ErrForbidden
	}
	var keyID string
	var raw []byte
	err = s.pool.QueryRow(ctx, `SELECT api_key_id,permissions FROM api_key_work WHERE kind=$1 AND work_id=$2`, kind, id).Scan(&keyID, &raw)
	if mapError(err) == ErrNotFound {
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	var permissions []core.Permission
	if err = json.Unmarshal(raw, &permissions); err != nil {
		return ctx, err
	}
	return WithAPIKeyAuth(ctx, keyID, permissions, permissions), nil
}

// Downstream certificate refreshes retain every credential that supplied the
// changed certificate, while the recipient's existing binding grants use.
func copyAPIKeyWork(ctx context.Context, ex storeExecutor, sourceKind, sourceID, destinationKind, destinationID string) error {
	_, err := ex.Exec(ctx, `INSERT INTO api_key_work(kind,work_id,api_key_id,permissions)
 SELECT $3,$4,api_key_id,permissions FROM api_key_work WHERE kind=$1 AND work_id=$2
 ON CONFLICT(kind,work_id) DO UPDATE SET api_key_id=EXCLUDED.api_key_id,permissions=EXCLUDED.permissions`, sourceKind, sourceID, destinationKind, destinationID)
	return err
}
func lockAPIKeyWork(ctx context.Context, tx pgx.Tx, kind, id string) error {
	rows, err := tx.Query(ctx, `SELECT k.id FROM admin_api_keys k JOIN api_key_work w ON w.api_key_id=k.id WHERE w.kind=$1 AND w.work_id=$2 ORDER BY k.id FOR SHARE OF k`, kind, id)
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
	allowed, err := apiKeyWorkAllowed(ctx, tx, kind, id)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
