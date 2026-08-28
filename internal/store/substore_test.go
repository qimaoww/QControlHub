package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestSubStoreSyncSettingsAndSelectionsLifecycle(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("substore-sync"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if _, err := dataStore.pool.Exec(ctx, `DELETE FROM substore_sync_items; DELETE FROM substore_sync_settings`); err != nil {
		t.Fatal(err)
	}

	endpoint := "http://substore:3001/qch-test-secret"
	settings, err := dataStore.SaveSubStoreSyncSettings(ctx, endpoint, "QControlHub")
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Configured || settings.EndpointURL != endpoint || settings.SubscriptionName != "QControlHub" || settings.IntegrationID == "" {
		t.Fatalf("saved settings = %+v", settings)
	}
	firstIntegrationID := settings.IntegrationID
	var storedEndpoint string
	if err := dataStore.pool.QueryRow(ctx, `SELECT endpoint_ciphertext FROM substore_sync_settings WHERE id=1`).Scan(&storedEndpoint); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(storedEndpoint, keyedEncryptedPrefix) || strings.Contains(storedEndpoint, "qch-test-secret") {
		t.Fatalf("Sub-Store endpoint was not protected at rest: %q", storedEndpoint)
	}

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	selections, err := dataStore.ReplaceSubStoreSyncSelections(ctx, []core.SubStoreSyncSelection{
		{AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "Tokyo A"},
		{AgentID: agent.ID, Engine: core.EngineXray, ProfileTag: "ss-in", CustomName: "Tokyo B"},
	})
	if err != nil || len(selections) != 2 || selections[0].CustomName != "Tokyo A" || selections[1].CustomName != "Tokyo B" {
		t.Fatalf("saved selections = %+v, %v", selections, err)
	}
	if _, err := dataStore.ReplaceSubStoreSyncSelections(ctx, []core.SubStoreSyncSelection{
		{AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "duplicate"},
		{AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "duplicate"},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate selection error = %v", err)
	}

	settings, err = dataStore.SaveSubStoreSyncSettings(ctx, endpoint, "QControlHub renamed")
	if err != nil || settings.IntegrationID != firstIntegrationID {
		t.Fatalf("settings update changed integration identity: %+v, %v", settings, err)
	}
	if err := dataStore.RecordSubStoreSyncResult(ctx, nil); err != nil {
		t.Fatal(err)
	}
	settings, err = dataStore.SubStoreSyncSettings(ctx)
	if err != nil || settings.LastSyncStatus != "success" || settings.LastSyncedAt == nil {
		t.Fatalf("successful sync status = %+v, %v", settings, err)
	}
	if err := dataStore.RecordSubStoreSyncResult(ctx, errors.New("remote unavailable")); err != nil {
		t.Fatal(err)
	}
	settings, err = dataStore.SubStoreSyncSettings(ctx)
	if err != nil || settings.LastSyncStatus != "failed" || settings.LastSyncError != "remote unavailable" {
		t.Fatalf("failed sync status = %+v, %v", settings, err)
	}

	withoutKey := &Store{pool: dataStore.pool}
	if _, err := withoutKey.SaveSubStoreSyncSettings(ctx, endpoint, "QControlHub"); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("save without encryption key error = %v", err)
	}
	if err := dataStore.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	selections, err = dataStore.ListSubStoreSyncSelections(ctx)
	if err != nil || len(selections) != 0 {
		t.Fatalf("selections after deleting node = %+v, %v", selections, err)
	}
}

func TestOpenMigratesAppliedV29SubStoreSyncSchema(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := schema.Close(cleanupCtx); err != nil {
			t.Errorf("drop isolated v29 schema: %v", err)
		}
	}()
	setup, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `
		DROP TABLE substore_sync_items;
		DROP TABLE substore_sync_settings;
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (29);
	`); err != nil {
		t.Fatal(err)
	}
	if err := setup.Close(ctx); err != nil {
		t.Fatal(err)
	}

	dataStore, err := OpenWithConfigKey(ctx, schema.URL, true, testEncryptionKey("substore-v29"))
	if err != nil {
		t.Fatalf("migrate applied v29 schema: %v", err)
	}
	defer dataStore.Close()
	var version int
	var settingsTable, itemsTable bool
	if err := dataStore.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.pool.QueryRow(ctx, `SELECT to_regclass('substore_sync_settings') IS NOT NULL, to_regclass('substore_sync_items') IS NOT NULL`).Scan(&settingsTable, &itemsTable); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || !settingsTable || !itemsTable {
		t.Fatalf("migrated v29 version=%d settings=%t items=%t", version, settingsTable, itemsTable)
	}
}
