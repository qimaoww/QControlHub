package store

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestACMEEmailSettingsMigrationAndStorage(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, user := sharedTestUser(t, db, ctx, "acme-settings")
	settings := core.DefaultPanelSettings()
	settings.ACMEEmail = "private-certs@example.test"
	if _, err := db.SavePanelSettings(user, settings); err != nil {
		t.Fatal(err)
	}
	var content, runtime string
	if err := db.pool.QueryRow(ctx, `SELECT content,runtime FROM user_panel_settings WHERE owner_id=$1`, scopeForConfig(user).OwnerID).Scan(&content, &runtime); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, settings.ACMEEmail) || strings.Contains(runtime, settings.ACMEEmail) {
		t.Fatal("private ACME account leaked out of encrypted personal settings")
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE panel_settings DROP COLUMN acme_email;
		UPDATE panel_settings SET panel_name='before-acme-upgrade';
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=73;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(72) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	global, err := db.PanelSettings(ctx)
	if err != nil || global.PanelName != "before-acme-upgrade" || global.ACMEEmail != "" {
		t.Fatalf("legacy settings migration lost data: %+v %v", global, err)
	}
	loaded, err := db.PanelSettings(user)
	if err != nil || loaded.ACMEEmail != settings.ACMEEmail {
		t.Fatalf("migration lost personal settings: %+v %v", loaded, err)
	}
}
