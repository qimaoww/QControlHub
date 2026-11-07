package store

import (
	"errors"
	"testing"
)

func TestClassicPreferenceMigrationPreservesRevision(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	if _, err := db.pool.Exec(ctx, `ALTER TABLE account_themes DROP CONSTRAINT account_themes_theme_id_check;
 INSERT INTO account_themes(account_id,theme_id,revision) VALUES ('user:legacy-theme','classic',7);
 DELETE FROM qcontrolhub_schema_migrations WHERE version>=89;`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var id string
	var revision int
	if err := db.pool.QueryRow(ctx, `SELECT theme_id,revision FROM account_themes WHERE account_id='user:legacy-theme'`).Scan(&id, &revision); err != nil || id != "analytics" || revision != 7 {
		t.Fatalf("migrated preference=%s/%d err=%v", id, revision, err)
	}
	scoped := WithConfigScope(ctx, "legacy-theme", false)
	if _, err := db.SaveTheme(scoped, "classic", 7); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retired save=%v", err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
}
