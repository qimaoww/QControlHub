package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const apiKeyColumns = `id,name,prefix,creator_type,creator_id,owner_id,permissions,ip_allowlist,disabled,created_at,expires_at,last_used_at,revoked_at,revision`

func scanAPIKey(row pgx.Row) (core.APIKey, error) {
	var k core.APIKey
	var permissions, ips []byte
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatorType, &k.CreatorID, &k.OwnerID, &permissions, &ips, &k.Disabled, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt, &k.Revision)
	if err != nil {
		return k, mapError(err)
	}
	if err = json.Unmarshal(permissions, &k.Permissions); err != nil {
		return k, err
	}
	if err = json.Unmarshal(ips, &k.IPAllowlist); err != nil {
		return k, err
	}
	k.Status = "active"
	if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()) {
		k.Status = "expired"
	}
	if k.Disabled {
		k.Status = "disabled"
	}
	if k.RevokedAt != nil {
		k.Status = "revoked"
	}
	return k, nil
}

func validateAPIKeyInput(input *core.APIKeyInput) error {
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) == 0 || utf8.RuneCountInString(input.Name) > 100 {
		return fmt.Errorf("%w: 名称须为 1–100 个字符", ErrInvalid)
	}
	if len(input.Permissions) == 0 {
		return fmt.Errorf("%w: 至少选择一项读取权限", ErrInvalid)
	}
	for _, p := range input.Permissions {
		if !core.HasPermission(core.DiagnosticPermissions(), p) {
			return fmt.Errorf("%w: API Key 仅支持诊断读取权限", ErrInvalid)
		}
	}
	input.Permissions = core.NormalizePermissions(input.Permissions)
	ips, err := core.NormalizeIPAllowlist(input.IPAllowlist)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	input.IPAllowlist = ips
	return nil
}

// Mutations and their audit entries commit together. Creator scope is never
// accepted from JSON; it comes from the authenticated administrator.
func (s *Store) CreateAPIKey(ctx context.Context, creatorType, creatorID string, input core.APIKeyInput, audit core.AuditLogEntry) (core.APIKey, string, error) {
	if !scopeForConfig(ctx).Admin {
		return core.APIKey{}, "", ErrForbidden
	}
	if err := validateAPIKeyInput(&input); err != nil {
		return core.APIKey{}, "", err
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return core.APIKey{}, "", fmt.Errorf("%w: 到期时间须晚于当前时间", ErrInvalid)
	}
	id, err := core.NewID("key")
	if err != nil {
		return core.APIKey{}, "", err
	}
	secret, err := core.NewToken()
	if err != nil {
		return core.APIKey{}, "", err
	}
	secret = "qch_key_" + secret
	digest := sha256.Sum256([]byte(secret))
	permissions, _ := json.Marshal(input.Permissions)
	ips, _ := json.Marshal(input.IPAllowlist)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.APIKey{}, "", err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return core.APIKey{}, "", err
	}
	k, err := scanAPIKey(tx.QueryRow(ctx, `INSERT INTO admin_api_keys(id,name,prefix,digest,creator_type,creator_id,owner_id,permissions,ip_allowlist,expires_at,disabled)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+apiKeyColumns, id, input.Name, secret[:16], digest[:], creatorType, creatorID, scopeForConfig(ctx).OwnerID, permissions, ips, input.ExpiresAt, input.Disabled))
	if err != nil {
		return k, "", err
	}
	audit.Action = "api_key.created"
	audit.Target = k.ID
	audit.Detail = "diagnostic read permissions and IP allowlist configured"
	if err = recordAuditWithExecutor(ctx, tx, audit); err != nil {
		return core.APIKey{}, "", err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return core.APIKey{}, "", err
	}
	return k, secret, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, creatorType, creatorID string) ([]core.APIKey, error) {
	if !scopeForConfig(ctx).Admin {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT `+apiKeyColumns+` FROM admin_api_keys WHERE creator_type=$1 AND creator_id=$2 AND owner_id=$3 ORDER BY created_at DESC,id DESC`, creatorType, creatorID, scopeForConfig(ctx).OwnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []core.APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, k)
	}
	return result, rows.Err()
}

func (s *Store) UpdateAPIKey(ctx context.Context, creatorType, creatorID, id string, input core.APIKeyInput, revoke bool, audit core.AuditLogEntry) (core.APIKey, error) {
	if !scopeForConfig(ctx).Admin {
		return core.APIKey{}, ErrForbidden
	}
	if !revoke {
		if err := validateAPIKeyInput(&input); err != nil {
			return core.APIKey{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.APIKey{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return core.APIKey{}, err
	}
	k, err := scanAPIKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM admin_api_keys WHERE id=$1 AND creator_type=$2 AND creator_id=$3 AND owner_id=$4 FOR UPDATE`, id, creatorType, creatorID, scopeForConfig(ctx).OwnerID))
	if err != nil {
		return k, err
	}
	if k.RevokedAt != nil {
		return k, ErrConflict
	}
	if !revoke && k.Revision != input.Revision {
		return k, ErrConflict
	}
	if !revoke && input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) && (k.ExpiresAt == nil || !input.ExpiresAt.Equal(*k.ExpiresAt)) {
		return k, fmt.Errorf("%w: 到期时间须晚于当前时间", ErrInvalid)
	}
	action := "api_key.updated"
	if revoke {
		_, err = tx.Exec(ctx, `UPDATE admin_api_keys SET revoked_at=now(),revision=revision+1 WHERE id=$1`, id)
		action = "api_key.revoked"
	} else {
		p, _ := json.Marshal(input.Permissions)
		ips, _ := json.Marshal(input.IPAllowlist)
		_, err = tx.Exec(ctx, `UPDATE admin_api_keys SET name=$2,permissions=$3,ip_allowlist=$4,expires_at=$5,disabled=$6,revision=revision+1 WHERE id=$1`, id, input.Name, p, ips, input.ExpiresAt, input.Disabled)
		if input.Disabled && !k.Disabled {
			action = "api_key.disabled"
		}
	}
	if err != nil {
		return k, err
	}
	audit.Action = action
	audit.Target = id
	audit.Detail = "API Key authorization changed"
	if err = recordAuditWithExecutor(ctx, tx, audit); err != nil {
		return k, err
	}
	k, err = scanAPIKey(tx.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM admin_api_keys WHERE id=$1`, id))
	if err != nil {
		return k, err
	}
	return k, tx.Commit(ctx)
}

func (s *Store) APIKeyForAuthentication(ctx context.Context, secret string) (core.APIKey, error) {
	digest := sha256.Sum256([]byte(secret))
	return scanAPIKey(s.pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM admin_api_keys WHERE digest=$1`, digest[:]))
}

func (s *Store) TouchAPIKey(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE admin_api_keys SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<now()-interval '5 minutes')`, id)
	return err
}
