package store

import (
	"encoding/json"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionRetentionDefaultsForExistingAccount(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, account := sharedTestUser(t, db, ctx, "source-policy-upgrade")
	settings := core.DefaultPanelSettings()
	settings.PanelName = "Existing workspace"
	if _, err := db.SavePanelSettings(account, settings); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "client_connection_retention_days")
	payload, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	content, err := db.encryptContent(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE user_panel_settings SET content=$1,runtime=runtime-'client_connection_retention_days' WHERE owner_id=$2`, content, user.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.PanelSettings(account)
	if err != nil || loaded.ClientConnectionRetentionDays != 30 || loaded.PanelName != settings.PanelName {
		t.Fatalf("legacy account policy: %+v %v", loaded, err)
	}
	loaded.ClientConnectionRetentionDays = 0
	if _, err := db.SavePanelSettingsRevision(account, loaded, loaded.Revision); err != nil {
		t.Fatal(err)
	}
	loaded, err = db.PanelSettings(account)
	if err != nil || loaded.ClientConnectionRetentionDays != 0 {
		t.Fatalf("explicit permanent policy was defaulted: %+v %v", loaded, err)
	}
}
