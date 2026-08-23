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
