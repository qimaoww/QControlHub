package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// TestEncryptedConfigStorageRoundTrip opens the store with an encryption key,
// writes and reads configurations and verifies the payload is sealed at rest
// while the API keeps returning plaintext.
func TestEncryptedConfigStorageRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, "integration-test-key")
	if err != nil {
		t.Fatalf("open encrypted store: %v", err)
	}
	defer dataStore.Close()

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	content := "mixed-port: 7890\nmode: rule\nproxies: []\nrules:\n  - MATCH,DIRECT\n"
	saved, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "encrypted config", Engine: core.EngineMihomo, Content: content,
	}, 0)
	if err != nil {
		t.Fatalf("save encrypted config: %v", err)
	}
	loaded, err := dataStore.AgentConfig(ctx, agent.ID, core.EngineMihomo)
	if err != nil || loaded.Content != content {
		t.Fatalf("loaded encrypted config = %+v, %v", loaded, err)
	}

	// The row at rest must not contain the plaintext.
	var stored string
	if err := dataStore.pool.QueryRow(ctx, `SELECT content FROM configs WHERE id=$1`, saved.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "mixed-port") || !strings.HasPrefix(stored, encryptedPrefix) {
		t.Fatalf("stored content is not sealed: %q", stored)
	}
	var revisionStored string
	if err := dataStore.pool.QueryRow(ctx, `SELECT content FROM config_revisions WHERE config_id=$1 AND version=1`, saved.ID).Scan(&revisionStored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(revisionStored, encryptedPrefix) {
		t.Fatalf("stored revision is not sealed: %q", revisionStored)
	}

	// Deploy/validate tasks carry the decrypted payload to the agent.
	task, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigID: saved.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.ConfigContent != content {
		t.Fatalf("task config content = %q, want plaintext", task.ConfigContent)
	}
	claimed, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ConfigContent != content {
		t.Fatalf("claimed task content = %+v, %v", claimed, err)
	}

	// Revisions and restores decrypt too.
	revision, err := dataStore.ConfigRevision(ctx, saved.ID, 1)
	if err != nil || revision.Content != content {
		t.Fatalf("revision = %+v, %v", revision, err)
	}
	restored, err := dataStore.RestoreConfigRevision(ctx, saved.ID, 1, saved.Version)
	if err != nil || restored.Content != content {
		t.Fatalf("restored = %+v, %v", restored, err)
	}
}

// TestEncryptedStoreReadsLegacyPlaintext verifies the transparent migration:
// rows written before encryption was enabled still read back correctly.
func TestEncryptedStoreReadsLegacyPlaintext(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Write plaintext with a plaintext store.
	plainStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, plainStore)
	defer cleanupTaskTestAgent(plainStore, agent.ID, enrollmentID)
	content := "mixed-port: 7891\nproxies: []\n"
	legacy, err := plainStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "legacy plaintext", Engine: core.EngineMihomo, Content: content,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	plainStore.Close()

	// Read it back with an encrypted store; the legacy row must pass through.
	encryptedStore, err := OpenWithConfigKey(ctx, databaseURL, true, "integration-test-key")
	if err != nil {
		t.Fatal(err)
	}
	defer encryptedStore.Close()
	loaded, err := encryptedStore.AgentConfig(ctx, agent.ID, core.EngineMihomo)
	if err != nil || loaded.Content != content {
		t.Fatalf("legacy config read through encrypted store = %+v, %v", loaded, err)
	}
	revision, err := encryptedStore.ConfigRevision(ctx, legacy.ID, 1)
	if err != nil || revision.Content != content {
		t.Fatalf("legacy revision through encrypted store = %+v, %v", revision, err)
	}
}
