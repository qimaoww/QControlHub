package store

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"reflect"
	"testing"
)

func TestSiteBrandingUpgradeReopenAndCAS(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	settings, _ := db.PanelSettings(ctx)
	// Recreate an installation at the previous schema with no branding table.
	if _, err := db.pool.Exec(ctx, `DROP TABLE site_branding; DELETE FROM qcontrolhub_schema_migrations WHERE version>=91; INSERT INTO qcontrolhub_schema_migrations(version) VALUES(90) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if current, err := migrated.PanelSettings(ctx); err != nil || !reflect.DeepEqual(current, settings) {
		t.Fatal("upgrade changed settings")
	}
	var data bytes.Buffer
	png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
	saved, err := migrated.SaveSiteBranding(ctx, &logo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrated.SaveSiteBranding(ctx, &logo, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale write accepted")
	}
	if _, err := migrated.SaveSiteBranding(WithConfigScope(ctx, "personal", false), &logo, saved.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatal("personal branding write accepted")
	}
	reopened, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.SiteBranding(ctx)
	if err != nil || loaded.Logo != logo || loaded.Revision != saved.Revision {
		t.Fatal("branding not durable")
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM site_branding`).Scan(&count); err != nil || count != 1 {
		t.Fatal("branding kept history")
	}
}
