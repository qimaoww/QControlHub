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
	if _, err := dataStore.pool.Exec(ctx, `DELETE FROM substore_sync_items; DELETE FROM substore_sync_targets; DELETE FROM substore_sync_settings`); err != nil {
		t.Fatal(err)
	}

	endpoint := "http://substore:3001/qch-test-secret"
	settings, err := dataStore.SaveSubStoreSyncSettings(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Configured || settings.EndpointURL != endpoint {
		t.Fatalf("saved settings = %+v", settings)
	}
	var storedEndpoint string
	if err := dataStore.pool.QueryRow(ctx, `SELECT endpoint_ciphertext FROM substore_sync_settings WHERE id=1`).Scan(&storedEndpoint); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(storedEndpoint, keyedEncryptedPrefix) || strings.Contains(storedEndpoint, "qch-test-secret") {
		t.Fatalf("Sub-Store endpoint was not protected at rest: %q", storedEndpoint)
	}

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	clientAddress := "client.example.test"
	clientName := "Tokyo Edge"
	clientAddressMode := core.SubStoreAddressModeIPv6
	if err := dataStore.SetAgentClientPreferences(ctx, agent.ID, &clientAddress, &clientName, &clientAddressMode); err != nil {
		t.Fatal(err)
	}
	clientAddressMode = core.SubStoreAddressModeAuto
	if err := dataStore.SetAgentClientPreferences(ctx, agent.ID, nil, nil, &clientAddressMode); err != nil {
		t.Fatal(err)
	}
	loadedAgent, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || loadedAgent.Labels["client_address"] != clientAddress || loadedAgent.Labels["client_name"] != clientName {
		t.Fatalf("partial client display update lost persisted values: %+v, %v", loadedAgent.Labels, err)
	}
	if _, exists := loadedAgent.Labels["client_address_mode"]; exists {
		t.Fatalf("automatic client address mode must remove the persisted override: %+v", loadedAgent.Labels)
	}
	clientAddressMode = core.SubStoreAddressModeIPv4
	if err := dataStore.SetAgentClientPreferences(ctx, agent.ID, nil, nil, &clientAddressMode); err != nil {
		t.Fatal(err)
	}
	loadedAgent, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || loadedAgent.Labels["client_address_mode"] != core.SubStoreAddressModeIPv4 || loadedAgent.Labels["client_address"] != clientAddress || loadedAgent.Labels["client_name"] != clientName {
		t.Fatalf("client address mode was not persisted independently: %+v, %v", loadedAgent.Labels, err)
	}
	target, err := dataStore.CreateSubStoreSyncTarget(ctx, "QControlHub")
	if err != nil || target.ID == "" || target.IntegrationID == "" || target.DisplayName != "QControlHub" || target.SubscriptionName != "QControlHub" || target.SyncMode != core.SubStoreSyncModeIncremental || target.LastSyncStatus != "never" {
		t.Fatalf("created target = %+v, %v", target, err)
	}
	firstIntegrationID := target.IntegrationID
	selections, err := dataStore.ReplaceSubStoreSyncSelections(ctx, target.ID, []core.SubStoreSyncSelection{
		{AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "Tokyo A", AddressMode: core.SubStoreAddressModeIPv4},
		{AgentID: agent.ID, Engine: core.EngineXray, ProfileTag: "ss-in", CustomName: "Tokyo B", AddressMode: core.SubStoreAddressModeBoth},
	})
	if err != nil || len(selections) != 2 || selections[0].CustomName != "Tokyo A" || selections[0].AddressMode != core.SubStoreAddressModeIPv4 || selections[1].CustomName != "Tokyo B" || selections[1].AddressMode != core.SubStoreAddressModeBoth {
		t.Fatalf("saved selections = %+v, %v", selections, err)
	}
	if _, err := dataStore.ReplaceSubStoreSyncSelections(ctx, target.ID, []core.SubStoreSyncSelection{
		{AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "duplicate"},
		{AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "duplicate"},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate selection error = %v", err)
	}

	if err := dataStore.RecordSubStoreSyncResult(ctx, target.ID, nil); err != nil {
		t.Fatal(err)
	}
	target, err = dataStore.UpdateSubStoreSyncTarget(ctx, target.ID, "Panel renamed", "QControlHub", core.SubStoreSyncModeManaged)
	if err != nil || target.IntegrationID != firstIntegrationID || target.DisplayName != "Panel renamed" || target.SubscriptionName != "QControlHub" || target.SyncMode != core.SubStoreSyncModeManaged || target.LastSyncStatus != "success" || target.LastSyncedAt == nil {
		t.Fatalf("target update changed integration identity: %+v, %v", target, err)
	}
	target, err = dataStore.UpdateSubStoreSyncTarget(ctx, target.ID, "Remote renamed", "Remote renamed", core.SubStoreSyncModeIncremental)
	if err != nil || target.DisplayName != "Remote renamed" || target.SubscriptionName != "Remote renamed" || target.SyncMode != core.SubStoreSyncModeIncremental {
		t.Fatalf("target remote rename = %+v, %v", target, err)
	}
	if err := dataStore.RecordSubStoreSyncResult(ctx, target.ID, nil); err != nil {
		t.Fatal(err)
	}
	target, err = dataStore.SubStoreSyncTarget(ctx, target.ID)
	if err != nil || target.LastSyncStatus != "success" || target.LastSyncedAt == nil {
		t.Fatalf("successful sync status = %+v, %v", target, err)
	}
	if err := dataStore.RecordSubStoreSyncResult(ctx, target.ID, errors.New("remote unavailable")); err != nil {
		t.Fatal(err)
	}
	target, err = dataStore.SubStoreSyncTarget(ctx, target.ID)
	if err != nil || target.LastSyncStatus != "failed" || target.LastSyncError != "remote unavailable" {
		t.Fatalf("failed sync status = %+v, %v", target, err)
	}
	secondTarget, err := dataStore.CreateSubStoreSyncTarget(ctx, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ReplaceSubStoreSyncSelections(ctx, secondTarget.ID, []core.SubStoreSyncSelection{{
		AgentID: agent.ID, Engine: core.EngineMihomo, ProfileTag: "vless-in", CustomName: "Backup A",
	}}); err != nil {
		t.Fatal(err)
	}
	if selections, err := dataStore.ListSubStoreSyncSelections(ctx, target.ID); err != nil || len(selections) != 2 {
		t.Fatalf("primary target selections = %+v, %v", selections, err)
	}
	if err := dataStore.DeleteSubStoreSyncTarget(ctx, secondTarget.ID); err != nil {
		t.Fatal(err)
	}
	if selections, err := dataStore.ListSubStoreSyncSelections(ctx, secondTarget.ID); err != nil || len(selections) != 0 {
		t.Fatalf("deleted target selections = %+v, %v", selections, err)
	}
	importedTarget, err := dataStore.ImportSubStoreSyncTarget(ctx, "Existing Sub-Store group", "ssi_existing_remote")
	if err != nil || importedTarget.DisplayName != "Existing Sub-Store group" || importedTarget.SubscriptionName != "Existing Sub-Store group" || importedTarget.IntegrationID != "ssi_existing_remote" || importedTarget.SyncMode != core.SubStoreSyncModeIncremental {
		t.Fatalf("imported target = %+v, %v", importedTarget, err)
	}

	withoutKey := &Store{pool: dataStore.pool}
	if _, err := withoutKey.SaveSubStoreSyncSettings(ctx, endpoint); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("save without encryption key error = %v", err)
	}
	if err := dataStore.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	selections, err = dataStore.ListSubStoreSyncSelections(ctx, target.ID)
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
		DROP TABLE substore_sync_targets;
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
	var settingsTable, targetsTable, itemsTable bool
	if err := dataStore.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.pool.QueryRow(ctx, `SELECT to_regclass('substore_sync_settings') IS NOT NULL, to_regclass('substore_sync_targets') IS NOT NULL, to_regclass('substore_sync_items') IS NOT NULL`).Scan(&settingsTable, &targetsTable, &itemsTable); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || !settingsTable || !targetsTable || !itemsTable {
		t.Fatalf("migrated v29 version=%d settings=%t targets=%t items=%t", version, settingsTable, targetsTable, itemsTable)
	}
}

func TestOpenMigratesAppliedV30SubStoreSyncTarget(t *testing.T) {
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
			t.Errorf("drop isolated v30 schema: %v", err)
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
		DROP TABLE substore_sync_targets;
		CREATE TABLE substore_sync_items (
			agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
			engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
			profile_tag text NOT NULL CHECK (octet_length(profile_tag) BETWEEN 1 AND 800),
			custom_name text NOT NULL CHECK (octet_length(custom_name) BETWEEN 1 AND 400),
			created_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL,
			PRIMARY KEY (agent_id,engine,profile_tag)
		);
		CREATE INDEX substore_sync_items_created_idx ON substore_sync_items(created_at,agent_id);
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (30);
		INSERT INTO agents (
			id,name,version,os,arch,capabilities,features,labels,runtime,metrics,
			public_key,last_seen,enrolled_at
		) VALUES (
			'agt_substore_v30','substore-v30','v30','linux','amd64',
			'["sing-box"]'::jsonb,'[]'::jsonb,'{}'::jsonb,'{}'::jsonb,'{}'::jsonb,
			decode(repeat('30',32),'hex'),now(),now()
		);
		INSERT INTO substore_sync_settings (
			id,endpoint_ciphertext,subscription_name,integration_id,last_synced_at,
			last_sync_status,last_sync_error,updated_at
		) VALUES (
			1,'legacy-ciphertext','Legacy Group','ssi_legacy','2026-08-28 00:00:00+00',
			'success','', '2026-08-28 00:00:00+00'
		);
		INSERT INTO substore_sync_items (
			agent_id,engine,profile_tag,custom_name,created_at,updated_at
		) VALUES (
			'agt_substore_v30','sing-box','vless-in','Legacy Node',
			'2026-08-28 00:00:00+00','2026-08-28 00:00:00+00'
		);
	`); err != nil {
		t.Fatal(err)
	}
	if err := setup.Close(ctx); err != nil {
		t.Fatal(err)
	}

	dataStore, err := OpenWithConfigKey(ctx, schema.URL, true, testEncryptionKey("substore-v30"))
	if err != nil {
		t.Fatalf("migrate applied v30 schema: %v", err)
	}
	defer dataStore.Close()
	targets, err := dataStore.ListSubStoreSyncTargets(ctx)
	if err != nil || len(targets) != 1 || targets[0].ID != "sst_default" || targets[0].DisplayName != "Legacy Group" || targets[0].SubscriptionName != "Legacy Group" || targets[0].SyncMode != core.SubStoreSyncModeManaged || targets[0].SelectionCount != 1 || targets[0].LastSyncStatus != "success" {
		t.Fatalf("migrated v30 targets = %+v, %v", targets, err)
	}
	selections, err := dataStore.ListSubStoreSyncSelections(ctx, targets[0].ID)
	if err != nil || len(selections) != 1 || selections[0].CustomName != "Legacy Node" || selections[0].AddressMode != core.SubStoreAddressModeAuto || selections[0].TargetID != targets[0].ID {
		t.Fatalf("migrated v30 selections = %+v, %v", selections, err)
	}
}

func TestOpenMigratesAppliedV31SubStoreDisplayName(t *testing.T) {
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
			t.Errorf("drop isolated v31 schema: %v", err)
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
		ALTER TABLE substore_sync_targets DROP COLUMN display_name;
		ALTER TABLE substore_sync_targets DROP COLUMN sync_mode;
		INSERT INTO substore_sync_targets (
			id,subscription_name,integration_id,last_sync_status,last_sync_error,created_at,updated_at
		) VALUES ('sst_v31','Existing remote group','ssi_v31','success','',now(),now());
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (31);
	`); err != nil {
		t.Fatal(err)
	}
	if err := setup.Close(ctx); err != nil {
		t.Fatal(err)
	}

	dataStore, err := OpenWithConfigKey(ctx, schema.URL, true, testEncryptionKey("substore-v31"))
	if err != nil {
		t.Fatalf("migrate applied v31 schema: %v", err)
	}
	defer dataStore.Close()
	target, err := dataStore.SubStoreSyncTarget(ctx, "sst_v31")
	if err != nil || target.DisplayName != "Existing remote group" || target.SubscriptionName != "Existing remote group" || target.SyncMode != core.SubStoreSyncModeManaged {
		t.Fatalf("migrated v31 target = %+v, %v", target, err)
	}
	var addressModeColumn, syncModeColumn bool
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='substore_sync_items' AND column_name='address_mode'),
			EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='substore_sync_targets' AND column_name='sync_mode')`).Scan(&addressModeColumn, &syncModeColumn); err != nil {
		t.Fatal(err)
	}
	if !addressModeColumn {
		t.Fatal("address_mode column was not added while migrating v31")
	}
	if !syncModeColumn {
		t.Fatal("sync_mode column was not added while migrating v31")
	}
	if _, err := dataStore.pool.Exec(ctx, `DELETE FROM substore_sync_targets`); err != nil {
		t.Fatal("remove local Sub-Store target: ", err)
	}
	if _, err := dataStore.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations; INSERT INTO qcontrolhub_schema_migrations (version) VALUES (39)`); err != nil {
		t.Fatal("prepare later schema migration: ", err)
	}
	dataStore.Close()
	dataStore, err = OpenWithConfigKey(ctx, schema.URL, true, testEncryptionKey("substore-v31"))
	if err != nil {
		t.Fatalf("reopen after removing Sub-Store target: %v", err)
	}
	defer dataStore.Close()
	targets, err := dataStore.ListSubStoreSyncTargets(ctx)
	if err != nil {
		t.Fatal("list targets after later migration: ", err)
	}
	if len(targets) != 0 {
		t.Fatalf("later schema migration recreated removed Sub-Store targets: %+v", targets)
	}
}
