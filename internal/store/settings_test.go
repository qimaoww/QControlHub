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

	want := core.PanelSettings{
		PanelName: "  Edge Control  ", PanelDescription: "  production fleet  ",
		EnrollmentTTLMinutes: 30, TaskPageSize: 50, TaskPollIntervalMS: 2000,
		WebhookURL: "https://hooks.example.com/qcontrolhub?token=abc",
	}
	saved, err := dataStore.SavePanelSettings(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PanelName != "Edge Control" || saved.PanelDescription != "production fleet" || saved.WebhookURL != want.WebhookURL || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved settings = %+v", saved)
	}
	loaded, err := dataStore.PanelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PanelName != saved.PanelName || loaded.EnrollmentTTLMinutes != 30 || loaded.TaskPageSize != 50 || loaded.TaskPollIntervalMS != 2000 || loaded.WebhookURL != want.WebhookURL {
		t.Fatalf("loaded settings = %+v, want %+v", loaded, saved)
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
}
