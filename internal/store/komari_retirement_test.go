package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestKomariRetirementPreservesAccountSettings(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	before := time.Now().UTC().Truncate(time.Microsecond)
	payload := `{"panel_name":"Private panel","komari_url":"https://obsolete.invalid","komari_api_key":"retired-secret","future_field":{"keep":true}}`
	content, err := db.encryptContent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE panel_settings ADD COLUMN komari_url text NOT NULL DEFAULT '', ADD COLUMN komari_api_key text NOT NULL DEFAULT '';
 UPDATE panel_settings SET komari_url='https://obsolete.invalid',komari_api_key='retired-secret';
 DELETE FROM qcontrolhub_schema_migrations WHERE version>=89;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO user_panel_settings(owner_id,content,runtime,revision,updated_at) VALUES ('retirement-test',$1,'{"panel_name":"Private panel","komari_api_key":"retired-secret"}',9,$2)`, content, before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET labels='{"komari_uuid":"old-binding","region_code":"SG"}' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var stored, runtime string
	var revision int
	var updated time.Time
	if err := db.pool.QueryRow(ctx, `SELECT content,runtime::text,revision,updated_at FROM user_panel_settings WHERE owner_id='retirement-test'`).Scan(&stored, &runtime, &revision, &updated); err != nil {
		t.Fatal(err)
	}
	plain, err := db.decryptContent(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "komari") || strings.Contains(runtime, "komari") || strings.Contains(stored, "retired-secret") {
		t.Fatal("obsolete integration data remains")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plain), &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["panel_name"]) != `"Private panel"` || string(fields["future_field"]) != `{"keep":true}` || revision != 9 || !updated.Equal(before) {
		t.Fatalf("settings changed: %s revision=%d updated=%v", plain, revision, updated)
	}
	var oldColumns int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='panel_settings' AND column_name IN ('komari_url','komari_api_key')`).Scan(&oldColumns); err != nil || oldColumns != 0 {
		t.Fatalf("obsolete columns=%d err=%v", oldColumns, err)
	}
	var labels string
	if err := db.pool.QueryRow(ctx, `SELECT labels::text FROM agents WHERE id=$1`, agent.ID).Scan(&labels); err != nil || strings.Contains(labels, "komari") || !strings.Contains(labels, "SG") {
		t.Fatalf("labels=%s err=%v", labels, err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
}
