package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/crypto/bcrypt"
)

func TestUserCredentialsSurviveMigrationRetryAndFailure(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	const password = " historical 中文 password "
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"user", "admin", "operator", "auditor", "readonly", "disabled"} {
		user, err := db.CreateUser(admin, core.UserRequest{Username: role, Role: core.RoleUser,
			Permissions: []core.Permission{core.PermissionConfigsRead}}, string(hash))
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.CreateConfig(WithConfigScope(ctx, user.ID, false), core.Config{Name: "owned", Engine: core.EngineMihomo,
			Content: "mixed-port: 7890\nmode: rule\nrules: [MATCH,DIRECT]\n"})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Restore a historical role layout, including a mixed-case backup record.
	if _, err := db.pool.Exec(ctx, `ALTER TABLE panel_users DROP CONSTRAINT panel_users_role_check;
		UPDATE panel_users SET role=CASE WHEN username='disabled' THEN 'user' ELSE username END,disabled=(username='disabled');
		UPDATE panel_users SET username='MiXeD' WHERE username='user';
		DELETE FROM qcontrolhub_schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations(version) VALUES ($1)`, currentSchemaVersion-1); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := db.pool.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(id,username,password_hash,disabled,role,permissions,auth_revision) ORDER BY id)::text FROM panel_users`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value // Never print snapshots: they contain password hashes.
	}
	before := snapshot()
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION reject_user_migration() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'injected account migration failure'; END $$;
		CREATE TRIGGER reject_user_migration BEFORE UPDATE ON panel_users FOR EACH STATEMENT EXECUTE FUNCTION reject_user_migration()`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err == nil {
		t.Fatal("expected injected migration failure")
	}
	if before != snapshot() {
		t.Fatal("failed migration changed credential records")
	}
	var version int
	if err := db.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion-1 {
		t.Fatal("failed migration advanced schema ledger")
	}
	if _, err := db.pool.Exec(ctx, `DROP TRIGGER reject_user_migration ON panel_users; DROP FUNCTION reject_user_migration()`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	for i := 0; i < 2; i++ {
		reopened, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
		if err != nil {
			t.Fatal(err)
		}
		reopened.Close()
		if after != snapshot() {
			t.Fatal("repeated startup/migration changed credentials")
		}
	}
	for _, name := range []string{"mixed", "admin", "operator", "auditor", "readonly"} {
		user, actualHash, err := db.UserForLogin(ctx, strings.ToUpper(name))
		if err != nil || actualHash != string(hash) || !authn.CheckPassword(actualHash, password) || user.AuthRevision != 1 {
			t.Fatal("migration lost original password, username lookup, or revision")
		}
		expectedRole := core.RoleUser
		if name == "admin" {
			expectedRole = core.RoleAdmin
		}
		if user.Role != expectedRole {
			t.Fatal("historical role migrated incorrectly")
		}
		if name == "operator" && !core.HasPermission(user.Permissions, core.PermissionTasksExecute) ||
			name == "auditor" && (!core.HasPermission(user.Permissions, core.PermissionAuditRead) || core.HasPermission(user.Permissions, core.PermissionConfigsRead)) ||
			name == "readonly" && (!core.HasPermission(user.Permissions, core.PermissionConfigsRead) || core.HasPermission(user.Permissions, core.PermissionConfigsWrite)) {
			t.Fatal("historical role capabilities were widened or lost")
		}
		configs, err := db.ListConfigs(WithConfigScope(ctx, user.ID, false))
		if err != nil || len(configs) != 1 || configs[0].OwnerID != user.ID {
			t.Fatal("migration changed configuration ownership")
		}
		persisted, err := db.UserForSession(ctx, user.ID)
		if err != nil || !reflect.DeepEqual(user.Permissions, persisted.Permissions) {
			t.Fatal("session permissions differ from login permissions")
		}
	}
	var disabledHash string
	var disabled bool
	if err := db.pool.QueryRow(ctx, `SELECT password_hash,disabled FROM panel_users WHERE username='disabled'`).Scan(&disabledHash, &disabled); err != nil || disabledHash != string(hash) || !disabled {
		t.Fatal("disabled historical account was changed")
	}
	if _, _, err := db.UserForLogin(ctx, "disabled"); err != ErrNotFound {
		t.Fatal("disabled account became available")
	}
}
