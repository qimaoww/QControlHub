package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestOpenMigratesAppliedV19TaskActionConstraint(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create isolated v19 schema: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := schema.Close(cleanupCtx); err != nil {
			t.Errorf("drop isolated v19 schema: %v", err)
		}
	}()

	setup, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatalf("connect to isolated v19 schema: %v", err)
	}
	defer func() {
		if err := setup.Close(context.Background()); err != nil {
			t.Errorf("close applied v19 fixture connection: %v", err)
		}
	}()
	if _, err := setup.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("create applied v19 schema fixture: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (19);
		DROP INDEX IF EXISTS tasks_latest_deployment_idx;
		CREATE INDEX tasks_latest_deployment_idx ON tasks(agent_id,engine,finished_at DESC)
			WHERE action='deploy' AND status='succeeded';
		ALTER TABLE tasks DROP CONSTRAINT tasks_action_check;
		ALTER TABLE tasks ADD CONSTRAINT tasks_action_check CHECK (
			action IN ('validate','deploy','read-config','start','stop','restart','status','install','upgrade-agent')
		)`); err != nil {
		t.Fatalf("restore applied v19 task constraint: %v", err)
	}
	var oldConstraint string
	if err := setup.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid='tasks'::regclass AND conname='tasks_action_check'
	`).Scan(&oldConstraint); err != nil {
		t.Fatalf("read applied v19 task constraint: %v", err)
	}
	if strings.Contains(oldConstraint, string(core.ActionImportExisting)) {
		t.Fatalf("v19 fixture unexpectedly accepts import-existing: %s", oldConstraint)
	}

	dataStore, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatalf("open and migrate applied v19 schema: %v", err)
	}
	defer dataStore.Close()
	var schemaVersion int
	if err := dataStore.pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM qcontrolhub_schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}
	var migratedConstraint string
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid='tasks'::regclass AND conname='tasks_action_check'
	`).Scan(&migratedConstraint); err != nil {
		t.Fatalf("read migrated task constraint: %v", err)
	}
	if !strings.Contains(migratedConstraint, string(core.ActionImportExisting)) {
		t.Fatalf("migrated task constraint does not accept import-existing: %s", migratedConstraint)
	}
	if !strings.Contains(migratedConstraint, string(core.ActionReadManagedConfig)) {
		t.Fatalf("migrated task constraint does not accept read-managed-config: %s", migratedConstraint)
	}

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID,
		Name:    "applied v19 import snapshot",
		Engine:  core.EngineMihomo,
		Content: "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save migrated node snapshot: %v", err)
	}
	task, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID:  agent.ID,
		Action:   core.ActionImportExisting,
		Engine:   core.EngineMihomo,
		ConfigID: config.ID,
	})
	if err != nil {
		t.Fatalf("create import-existing task after v19 migration: %v", err)
	}
	if task.ID == "" || task.AgentID != agent.ID || task.ConfigID != config.ID || task.Action != core.ActionImportExisting {
		t.Fatalf("import-existing task after v19 migration = %+v", task)
	}
	managedRead, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionReadManagedConfig, Engine: core.EngineMihomo,
	})
	if err != nil || managedRead.Action != core.ActionReadManagedConfig {
		t.Fatalf("create read-managed-config task after v19 migration = %+v, %v", managedRead, err)
	}
}

func TestOpenMigratesAppliedV20Schema(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create isolated v20 schema: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := schema.Close(cleanupCtx); err != nil {
			t.Errorf("drop isolated v20 schema: %v", err)
		}
	}()

	setup, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatalf("connect to isolated v20 schema: %v", err)
	}
	defer func() {
		if err := setup.Close(context.Background()); err != nil {
			t.Errorf("close applied v20 fixture connection: %v", err)
		}
	}()
	if _, err := setup.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("create current v21 schema fixture: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (20);
		ALTER TABLE tasks DROP COLUMN IF EXISTS core_source;
	`); err != nil {
		t.Fatalf("restore applied v20 schema fixture: %v", err)
	}
	var hasCoreSource bool
	if err := setup.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='tasks' AND column_name='core_source'
		)
	`).Scan(&hasCoreSource); err != nil {
		t.Fatalf("read v20 core_source presence: %v", err)
	}
	if hasCoreSource {
		t.Fatal("v20 fixture unexpectedly already has core_source")
	}

	dataStore, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatalf("open and migrate applied v20 schema: %v", err)
	}
	defer dataStore.Close()
	var schemaVersion int
	if err := dataStore.pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM qcontrolhub_schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='tasks' AND column_name='core_source'
		)
	`).Scan(&hasCoreSource); err != nil {
		t.Fatalf("read migrated core_source presence: %v", err)
	}
	if !hasCoreSource {
		t.Fatal("v21 migration did not add the core_source column")
	}
}

func TestOpenMigratesAppliedV21EnrollmentCiphertext(t *testing.T) {
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
			t.Errorf("drop isolated v21 schema: %v", err)
		}
	}()
	setup, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close(context.Background())
	if _, err := setup.Exec(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (21);
		ALTER TABLE enrollment_tokens DROP COLUMN token_ciphertext;
	`); err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenWithConfigKey(ctx, schema.URL, true, testEncryptionKey("migration-command-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	var version int
	var hasCiphertext bool
	if err := dataStore.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='enrollment_tokens' AND column_name='token_ciphertext')`).Scan(&hasCiphertext); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || !hasCiphertext {
		t.Fatalf("migrated v21 version=%d ciphertext_column=%t", version, hasCiphertext)
	}
}

func TestOpenMigratesAppliedV26TrafficColumns(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create isolated v26 schema: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := schema.Close(cleanupCtx); err != nil {
			t.Errorf("drop isolated v26 schema: %v", err)
		}
	}()

	setup, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatalf("connect to isolated v26 schema: %v", err)
	}
	defer setup.Close(context.Background())
	if _, err := setup.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("create current schema fixture: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (26);
		INSERT INTO agents (
			id,name,version,os,arch,capabilities,features,labels,runtime,metrics,
			public_key,last_seen,enrolled_at
		) VALUES (
			'agt_migration_v26','migration-v26','v26','linux','amd64',
			'["sing-box"]'::jsonb,'["port-traffic-v1"]'::jsonb,'{}'::jsonb,'{}'::jsonb,'{}'::jsonb,
			decode(repeat('26',32),'hex'),now(),now()
		);
		INSERT INTO port_traffic_policies (
			id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,
			auto_block,quota_enabled,discovered,traffic_history_initialized,reset_generation,
			received_bytes,sent_bytes,used_bytes,receive_bps,send_bps,
			blocked,enforcement_available,enforcement_error,period_start,period_end,last_reported_at,created_at,updated_at
		) VALUES (
			'trf_2626262626262626','agt_migration_v26','existing quota','sing-box',443,'tcp',
			'monthly','2026-08-01',1048576,true,true,false,true,4,
			123,198,321,12,19,false,true,'','2026-08-01','2026-09-01','2026-08-27 23:59:45+00',now(),now()
		);
		ALTER TABLE port_traffic_policies DROP COLUMN quota_enabled;
		ALTER TABLE port_traffic_policies DROP COLUMN monitoring_enabled;
		ALTER TABLE port_traffic_policies DROP COLUMN discovered;
		ALTER TABLE port_traffic_policies DROP COLUMN traffic_history_initialized;
		ALTER TABLE port_traffic_policies DROP COLUMN reported_received_bytes;
		ALTER TABLE port_traffic_policies DROP COLUMN reported_sent_bytes;
	`); err != nil {
		t.Fatalf("restore applied v26 traffic schema drift fixture: %v", err)
	}

	dataStore, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatalf("open and migrate applied v26 traffic schema: %v", err)
	}
	defer func() { dataStore.Close() }()

	var schemaVersion int
	if err := dataStore.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}
	var quotaEnabled, monitoringEnabled, discovered, historyInitialized, autoBlock bool
	var limitBytes, receivedBytes, sentBytes, usedBytes, reportedReceivedBytes, reportedSentBytes int64
	var resetGeneration int64
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT quota_enabled,monitoring_enabled,discovered,traffic_history_initialized,auto_block,
		       limit_bytes,received_bytes,sent_bytes,used_bytes,reset_generation,
		       reported_received_bytes,reported_sent_bytes
		FROM port_traffic_policies WHERE id='trf_2626262626262626'
	`).Scan(
		&quotaEnabled, &monitoringEnabled, &discovered, &historyInitialized, &autoBlock,
		&limitBytes, &receivedBytes, &sentBytes, &usedBytes, &resetGeneration,
		&reportedReceivedBytes, &reportedSentBytes,
	); err != nil {
		t.Fatalf("read migrated traffic policy: %v", err)
	}
	if !quotaEnabled || !monitoringEnabled || discovered || historyInitialized || !autoBlock {
		t.Fatalf(
			"migrated traffic flags quota=%t monitoring=%t discovered=%t history=%t auto_block=%t",
			quotaEnabled, monitoringEnabled, discovered, historyInitialized, autoBlock,
		)
	}
	if limitBytes != 1048576 || receivedBytes != 123 || sentBytes != 198 || usedBytes != 321 || resetGeneration != 4 {
		t.Fatalf(
			"migrated traffic counters limit=%d received=%d sent=%d used=%d generation=%d",
			limitBytes, receivedBytes, sentBytes, usedBytes, resetGeneration,
		)
	}
	if reportedReceivedBytes != receivedBytes || reportedSentBytes != sentBytes {
		t.Fatalf("migrated traffic baselines received=%d sent=%d", reportedReceivedBytes, reportedSentBytes)
	}
	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := dataStore.UpdatePortTrafficUsage(ctx, "agt_migration_v26", []core.PortTrafficUsage{{
		PolicyID: "trf_2626262626262626", ResetGeneration: 4,
		ReceivedBytes: 23, SentBytes: 98, UsedBytes: 121,
		PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0), EnforcementAvailable: true,
	}}, time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("report reset Agent counters after migration: %v", err)
	}
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT received_bytes,sent_bytes,used_bytes,reported_received_bytes,reported_sent_bytes
		FROM port_traffic_policies WHERE id='trf_2626262626262626'
	`).Scan(&receivedBytes, &sentBytes, &usedBytes, &reportedReceivedBytes, &reportedSentBytes); err != nil {
		t.Fatalf("read post-migration Agent counter reset: %v", err)
	}
	if receivedBytes != 146 || sentBytes != 296 || usedBytes != 442 || reportedReceivedBytes != 23 || reportedSentBytes != 98 {
		t.Fatalf("post-migration Agent reset totals=%d/%d/%d raw=%d/%d", receivedBytes, sentBytes, usedBytes, reportedReceivedBytes, reportedSentBytes)
	}

	// schemaSQL is intentionally rerun for every future schema bump. Existing
	// raw Agent baselines must not be overwritten by the cumulative totals on a
	// later pass, or counter-reset protection would silently stop working.
	if _, err := dataStore.pool.Exec(ctx, `
		UPDATE port_traffic_policies
		SET received_bytes=1123,sent_bytes=2198,used_bytes=3321,
		    reported_received_bytes=23,reported_sent_bytes=98
		WHERE id='trf_2626262626262626'
	`); err != nil {
		t.Fatalf("prepare repeated traffic migration: %v", err)
	}
	if _, err := dataStore.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version=$1`, currentSchemaVersion); err != nil {
		t.Fatalf("rewind repeated traffic migration ledger: %v", err)
	}
	dataStore.Close()
	dataStore, err = Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatalf("repeat traffic migration: %v", err)
	}
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT reported_received_bytes,reported_sent_bytes
		FROM port_traffic_policies WHERE id='trf_2626262626262626'
	`).Scan(&reportedReceivedBytes, &reportedSentBytes); err != nil {
		t.Fatalf("read repeated traffic migration baselines: %v", err)
	}
	if reportedReceivedBytes != 23 || reportedSentBytes != 98 {
		t.Fatalf("repeated traffic migration changed raw baselines to %d/%d", reportedReceivedBytes, reportedSentBytes)
	}
}

func TestOpenMigratesAppliedV35OperationalColumns(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create isolated v35 schema: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := schema.Close(cleanupCtx); err != nil {
			t.Errorf("drop isolated v35 schema: %v", err)
		}
	}()

	setup, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatalf("connect to isolated v35 schema: %v", err)
	}
	defer setup.Close(context.Background())
	if _, err := setup.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("create current schema fixture: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (35);

		ALTER TABLE agents DROP COLUMN presence_notification_state;

		ALTER TABLE panel_settings DROP COLUMN revision;
		ALTER TABLE panel_settings DROP COLUMN time_zone;
		ALTER TABLE panel_settings DROP COLUMN time_display;
		ALTER TABLE panel_settings DROP COLUMN ui_font_scale;
		ALTER TABLE panel_settings DROP COLUMN default_config_editor;
		ALTER TABLE panel_settings DROP COLUMN agent_heartbeat_interval_seconds;
		ALTER TABLE panel_settings DROP COLUMN agent_metrics_interval_seconds;
		ALTER TABLE panel_settings DROP COLUMN agent_offline_threshold_seconds;
		ALTER TABLE panel_settings DROP COLUMN task_stale_timeout_seconds;
		ALTER TABLE panel_settings DROP COLUMN install_task_stale_timeout_seconds;
		ALTER TABLE panel_settings DROP COLUMN task_max_attempts;
		ALTER TABLE panel_settings DROP COLUMN public_ip_probe_interval_seconds;
		ALTER TABLE panel_settings DROP COLUMN core_log_retention_days;
		ALTER TABLE panel_settings DROP COLUMN agent_core_log_max_mib;
		ALTER TABLE panel_settings DROP COLUMN agent_core_log_rotate_count;
		ALTER TABLE panel_settings DROP COLUMN metric_retention_days;
		ALTER TABLE panel_settings DROP COLUMN audit_retention_days;
		ALTER TABLE panel_settings DROP COLUMN task_retention_days;
		ALTER TABLE panel_settings DROP COLUMN config_revision_retention;
		ALTER TABLE panel_settings DROP COLUMN notify_task_failed;
		ALTER TABLE panel_settings DROP COLUMN notify_agent_offline;
		ALTER TABLE panel_settings DROP COLUMN notify_agent_online;
		ALTER TABLE panel_settings DROP COLUMN notify_traffic_quota;

		ALTER TABLE port_traffic_policies DROP COLUMN quota_notification_generation;
	`); err != nil {
		t.Fatalf("restore applied v35 schema fixture: %v", err)
	}

	missingColumns := map[string][]string{
		"agents": {"presence_notification_state"},
		"panel_settings": {
			"revision", "time_zone", "time_display", "ui_font_scale", "default_config_editor",
			"agent_heartbeat_interval_seconds", "agent_metrics_interval_seconds", "agent_offline_threshold_seconds",
			"task_stale_timeout_seconds", "install_task_stale_timeout_seconds", "task_max_attempts",
			"public_ip_probe_interval_seconds", "core_log_retention_days", "agent_core_log_max_mib",
			"agent_core_log_rotate_count", "metric_retention_days", "audit_retention_days",
			"task_retention_days", "config_revision_retention", "notify_task_failed", "notify_agent_offline",
			"notify_agent_online", "notify_traffic_quota",
		},
		"port_traffic_policies": {"quota_notification_generation"},
	}
	for table, columns := range missingColumns {
		for _, column := range columns {
			var exists bool
			if err := setup.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
				)
			`, table, column).Scan(&exists); err != nil {
				t.Fatalf("inspect v35 column %s.%s: %v", table, column, err)
			}
			if exists {
				t.Fatalf("v35 fixture unexpectedly contains %s.%s", table, column)
			}
		}
	}

	dataStore, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatalf("open and migrate applied v35 operational schema: %v", err)
	}
	defer dataStore.Close()
	var schemaVersion int
	if err := dataStore.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}
	for table, columns := range missingColumns {
		for _, column := range columns {
			var exists bool
			if err := dataStore.pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
				)
			`, table, column).Scan(&exists); err != nil {
				t.Fatalf("inspect migrated column %s.%s: %v", table, column, err)
			}
			if !exists {
				t.Errorf("v37 migration did not restore %s.%s", table, column)
			}
		}
	}
	if t.Failed() {
		return
	}
	settings, err := dataStore.PanelSettings(ctx)
	if err != nil {
		t.Fatalf("read panel settings after v35 migration: %v", err)
	}
	if settings.Revision != 1 || settings.UIFontScale != 100 || settings.AgentHeartbeatIntervalSeconds != 15 || !settings.NotifyAgentOffline {
		t.Fatalf("migrated panel setting defaults = %+v", settings)
	}
	if _, err := dataStore.AgentPresenceTransitions(ctx, time.Now().UTC(), 45*time.Second); err != nil {
		t.Fatalf("read agent presence state after v35 migration: %v", err)
	}
	if _, err := dataStore.ClaimTrafficQuotaTransitions(ctx); err != nil {
		t.Fatalf("read quota notification state after v35 migration: %v", err)
	}
}

func TestConcurrentOpenSkipsAppliedSchemaDuringCRUD(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	var schemaVersion int
	if err := dataStore.pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM qcontrolhub_schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema migration version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "concurrent migration v1", Engine: core.EngineMihomo,
		Content: "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save migration fixture: %v", err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 32)
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for attempt := range 3 {
				opened, openErr := Open(ctx, databaseURL, true)
				if openErr != nil {
					errorsFound <- fmt.Errorf("worker %d open %d: %w", worker, attempt, openErr)
					return
				}
				if pingErr := opened.Ping(ctx); pingErr != nil {
					errorsFound <- fmt.Errorf("worker %d ping %d: %w", worker, attempt, pingErr)
				}
				opened.Close()
			}
		}(worker)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		current := config
		for version := 2; version <= 16; version++ {
			content := fmt.Sprintf("mixed-port: %d\nmode: rule\nrules:\n  - MATCH,DIRECT\n", 7889+version)
			updated, updateErr := dataStore.SaveAgentConfig(ctx, core.Config{
				AgentID: agent.ID, Name: fmt.Sprintf("concurrent migration v%d", version),
				Engine: core.EngineMihomo, Content: content,
			}, current.Version)
			if updateErr != nil {
				errorsFound <- fmt.Errorf("update configuration v%d: %w", version, updateErr)
				return
			}
			current = updated
		}
	}()
	close(start)
	workers.Wait()
	close(errorsFound)
	for workerErr := range errorsFound {
		t.Error(workerErr)
	}
	if t.Failed() {
		return
	}
	current, err := dataStore.AgentConfig(ctx, agent.ID, core.EngineMihomo)
	if err != nil || current.Version != 16 {
		t.Fatalf("configuration after concurrent opens = %+v, %v", current, err)
	}
}

// TestMigrateAddsCoreSourceToLegacyTasks advances a database whose tasks table
// predates core_source (schema version 19) to the current version and verifies
// existing rows survive with an empty (official-default) core_source value.
func TestMigrateAddsCoreSourceToLegacyTasks(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schemaID, err := core.NewID("migration")
	if err != nil {
		t.Fatal(err)
	}
	schemaName := pgx.Identifier{schemaID}.Sanitize()
	setup, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for legacy migration fixture: %v", err)
	}
	defer setup.Close(ctx)
	if _, err := setup.Exec(ctx, `CREATE SCHEMA `+schemaName); err != nil {
		t.Fatalf("create legacy migration schema: %v", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := setup.Exec(cleanupContext, `DROP SCHEMA `+schemaName+` CASCADE`); err != nil {
			t.Errorf("drop legacy migration schema: %v", err)
		}
	}()
	if _, err := setup.Exec(ctx, `SET search_path TO `+schemaName); err != nil {
		t.Fatalf("select legacy migration schema: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE qcontrolhub_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO qcontrolhub_schema_migrations (version) VALUES (19);
		CREATE TABLE agents (
			id text PRIMARY KEY,
			name varchar(100) NOT NULL,
			version varchar(100) NOT NULL DEFAULT '',
			os varchar(50) NOT NULL,
			arch varchar(50) NOT NULL,
			capabilities jsonb NOT NULL,
			labels jsonb NOT NULL DEFAULT '{}'::jsonb,
			runtime jsonb NOT NULL DEFAULT '{}'::jsonb,
			public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
			last_seen timestamptz NOT NULL,
			enrolled_at timestamptz NOT NULL,
			revoked_at timestamptz
		);
		CREATE TABLE tasks (
			id text PRIMARY KEY,
			agent_id text NOT NULL REFERENCES agents(id),
			action varchar(20) NOT NULL,
			engine varchar(20) NOT NULL,
			config_id text,
			config_version integer,
			config_content text,
			core_version varchar(64),
			status varchar(20) NOT NULL,
			attempt integer NOT NULL DEFAULT 0,
			output text,
			error text,
			created_at timestamptz NOT NULL,
			started_at timestamptz,
			finished_at timestamptz
		)`); err != nil {
		t.Fatalf("create legacy schema without core_source: %v", err)
	}
	agentID, err := core.NewID("agt")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := core.NewID("tsk")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := setup.Exec(ctx, `
		INSERT INTO agents (id,name,version,os,arch,capabilities,runtime,public_key,last_seen,enrolled_at)
		VALUES ($1,'legacy-agent','1.0.0','linux','amd64','["mihomo"]'::jsonb,'{}'::jsonb,decode(repeat('01',32),'hex'),$2,$2)`,
		agentID, now); err != nil {
		t.Fatalf("insert legacy agent: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		INSERT INTO tasks (id,agent_id,action,engine,core_version,status,created_at)
		VALUES ($1,$2,'install','mihomo','stable','succeeded',$3)`,
		taskID, agentID, now); err != nil {
		t.Fatalf("insert legacy task: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse legacy migration database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaID
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open legacy migration pool: %v", err)
	}
	dataStore := &Store{pool: pool}
	defer dataStore.Close()
	if err := dataStore.migrate(ctx); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	var version int
	if err := dataStore.pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	var coreSource string
	if err := dataStore.pool.QueryRow(ctx, `SELECT COALESCE(core_source,'') FROM tasks WHERE id=$1`, taskID).Scan(&coreSource); err != nil {
		t.Fatalf("read migrated core_source: %v", err)
	}
	if coreSource != "" {
		t.Fatalf("legacy task core_source = %q, want empty (official default)", coreSource)
	}
}
