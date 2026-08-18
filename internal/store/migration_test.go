package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
