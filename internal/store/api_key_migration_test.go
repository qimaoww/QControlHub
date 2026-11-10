package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"testing"
)

func TestLegacyAPIKeyMigrationPreservesReadOnlyCredential(t *testing.T) {
	for _, version := range []int{95, 96} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db, ctx, url := isolatedConfigScopeStore(t)
			input := core.APIKeyInput{Name: "legacy diagnostic", Permissions: []core.Permission{core.PermissionConfigsRead}}
			key, secret, err := db.CreateAPIKey(ctx, "environment", "legacy-environment", input, core.AuditLogEntry{Actor: "test"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.pool.Exec(ctx, `ALTER TABLE admin_api_keys DROP COLUMN mode;
 DROP TABLE api_key_work;DROP TABLE api_key_environment_creators;
 ALTER TABLE tasks DROP COLUMN api_key_id;ALTER TABLE tasks DROP COLUMN api_key_permissions;
 DELETE FROM qcontrolhub_schema_migrations WHERE version>=96;`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.pool.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, version); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenWithConfigKey(ctx, url, true, testEncryptionKey("config-scope"))
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			migrated, err := reopened.APIKeyForAuthentication(ctx, secret)
			if err != nil {
				t.Fatal(err)
			}
			if migrated.ID != key.ID || migrated.Mode != core.APIKeyReadOnly || migrated.Revision != key.Revision || migrated.Status != "active" || len(migrated.Permissions) != 1 || migrated.Permissions[0] != core.PermissionConfigsRead {
				t.Fatal("migration expanded or replaced legacy authorization")
			}
			if err = reopened.migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.APIKeyForAuthentication(ctx, secret); err != nil {
				t.Fatal("migration retry lost original digest")
			}
		})
	}
}
func TestAPIKeyBackgroundWorkUsesCurrentCreatorAndMode(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	user, err := db.CreateUser(admin, core.UserRequest{Username: "key-worker-owner", Role: core.RoleUser, Permissions: []core.Permission{core.PermissionConfigsRead}}, "test-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	owner := WithConfigScope(ctx, user.ID, false)
	key, _, err := db.CreateAPIKey(owner, "user", user.ID, core.APIKeyInput{Name: "certificates", Mode: core.APIKeyReadWrite, Permissions: []core.Permission{core.PermissionCertificatesManage}}, core.AuditLogEntry{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	delegated := WithAPIKeyAuth(owner, key.ID, key.Permissions, key.Permissions)
	if err = recordAPIKeyWork(delegated, db.pool, "certificate-order", "test-order"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := apiKeyWorkAllowed(ctx, db.pool, "certificate-order", "test-order"); err != nil || !allowed {
		t.Fatal("personal creator capability rejected")
	}
	if err = copyAPIKeyWork(ctx, db.pool, "certificate-order", "test-order", "certificate-sync-task:test-cert", "test-task"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateAPIKey(owner, "user", user.ID, key.ID, core.APIKeyInput{Name: key.Name, Mode: core.APIKeyReadOnly, Permissions: []core.Permission{core.PermissionCertificatesRead}, Revision: key.Revision}, false, core.AuditLogEntry{Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"certificate-order", "certificate-sync-task:test-cert"} {
		id := "test-order"
		if kind != "certificate-order" {
			id = "test-task"
		}
		if allowed, err := apiKeyWorkAllowed(context.Background(), db.pool, kind, id); err != nil || allowed {
			t.Fatal("downgrade did not stop background work")
		}
	}
}

func TestAPIKeyExpiryUsesCurrentTimeInsideTransaction(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, err := db.CreateUser(WithConfigScope(ctx, "", true), core.UserRequest{Username: "expiry-owner", Role: core.RoleUser}, "fixture-hash")
	if err != nil {
		t.Fatal(err)
	}
	owner := WithConfigScope(ctx, user.ID, false)
	key, _, err := db.CreateAPIKey(owner, "user", user.ID, core.APIKeyInput{Name: "expiring worker", Mode: core.APIKeyReadWrite, Permissions: []core.Permission{core.PermissionCertificatesManage}}, core.AuditLogEntry{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	request, finish, err := db.BeginAPIKeyMutation(WithAPIKeyAuth(owner, key.ID, key.Permissions, key.Permissions), key, key.Permissions)
	if err != nil {
		t.Fatal(err)
	}
	defer finish(false)
	if err := recordAPIKeyWork(request, db.pool, "certificate-order", "expiry-order"); err != nil {
		t.Fatal(err)
	}
	// Keep transaction_timestamp() before expiry while the actual clock has
	// passed it. No sleeps or credential mutation races are needed.
	if _, err := db.pool.Exec(request, `UPDATE admin_api_keys SET expires_at=now()+interval '1 microsecond' WHERE id=$1`, key.ID); err != nil {
		t.Fatal(err)
	}
	var expired bool
	if err := db.pool.QueryRow(request, `SELECT expires_at<=clock_timestamp() FROM admin_api_keys WHERE id=$1`, key.ID).Scan(&expired); err != nil || !expired {
		t.Fatal("fixture did not cross expiry", err)
	}
	if err := db.LockAPIKeyExternalEffect(request); !errors.Is(err, ErrForbidden) {
		t.Errorf("expired Key reached external effect: %v", err)
	}
	if allowed, err := apiKeyWorkAllowed(request, db.pool, "certificate-order", "expiry-order"); err != nil || allowed {
		t.Errorf("expired Key continued background work: allowed=%v err=%v", allowed, err)
	}
}
