package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPanelSettingsPersistAndValidate(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	original, err := dataStore.PanelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, restoreErr := dataStore.SavePanelSettings(cleanupCtx, original); restoreErr != nil {
			t.Errorf("restore panel settings: %v", restoreErr)
		}
	}()

	want := core.DefaultPanelSettings()
	want.PanelName = "  Edge Control  "
	want.PanelDescription = "  production fleet  "
	want.TaskPageSize = 50
	want.TaskPollIntervalMS = 2000
	want.UIFontScale = 110
	want.AgentMetricsIntervalSeconds = 5
	want.AgentCoreLogMaxMiB = 1
	want.AgentCoreLogRotateCount = 0
	want.CoreLogMinimumLevel = "warning"
	want.WebhookURL = "https://hooks.example.com/qcontrolhub?token=abc"
	saved, err := dataStore.SavePanelSettings(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PanelName != "Edge Control" || saved.PanelDescription != "production fleet" || saved.UIFontScale != 110 || saved.CoreLogMinimumLevel != "warning" || saved.WebhookURL != want.WebhookURL || saved.AgentMetricsIntervalSeconds != 5 || saved.AgentCoreLogMaxMiB != 1 || saved.AgentCoreLogRotateCount != 0 || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved settings = %+v", saved)
	}
	loaded, err := dataStore.PanelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PanelName != saved.PanelName || loaded.UIFontScale != 110 || loaded.TaskPageSize != 50 || loaded.TaskPollIntervalMS != 2000 || loaded.CoreLogMinimumLevel != "warning" || loaded.WebhookURL != want.WebhookURL || loaded.AgentMetricsIntervalSeconds != 5 || loaded.AgentCoreLogMaxMiB != 1 {
		t.Fatalf("loaded settings = %+v, want %+v", loaded, saved)
	}
	legacyClient := saved
	legacyClient.PanelName = "Legacy client update"
	legacyClient.CoreLogMinimumLevel = ""
	legacyClient.AgentHeartbeatIntervalSeconds = 0
	legacySaved, err := dataStore.SavePanelSettings(ctx, legacyClient)
	if err != nil || legacySaved.CoreLogMinimumLevel != "warning" || legacySaved.AgentCoreLogMaxMiB != 1 || legacySaved.AgentMetricsIntervalSeconds != 5 {
		t.Fatalf("legacy settings update changed core log policy: %+v, %v", legacySaved, err)
	}
	legacyV36 := saved
	legacyV36.PanelDescription = "v36 client update"
	legacyV36.UIFontScale = 0
	legacyV36Saved, err := dataStore.SavePanelSettings(ctx, legacyV36)
	if err != nil || legacyV36Saved.UIFontScale != 110 {
		t.Fatalf("v36 settings update changed UI font scale: %+v, %v", legacyV36Saved, err)
	}

	invalid := want
	invalid.TaskPageSize = 75
	if _, err := dataStore.SavePanelSettings(ctx, invalid); err == nil {
		t.Fatal("invalid settings were saved")
	}
	invalid.WebhookURL = "ftp://example.com/hook"
	if _, err := dataStore.SavePanelSettings(ctx, invalid); err == nil {
		t.Fatal("non-http webhook URL was saved")
	}
	invalid = want
	invalid.CoreLogMinimumLevel = "trace"
	if _, err := dataStore.SavePanelSettings(ctx, invalid); err == nil {
		t.Fatal("unsupported core log minimum level was saved")
	}
	invalid = want
	invalid.UIFontScale = 125
	if _, err := dataStore.SavePanelSettings(ctx, invalid); err == nil {
		t.Fatal("unsupported UI font scale was saved")
	}
}
