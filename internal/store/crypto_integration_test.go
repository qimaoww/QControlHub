package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("integration-test-key"))
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
	if strings.Contains(stored, "mixed-port") || !strings.HasPrefix(stored, keyedEncryptedPrefix) {
		t.Fatalf("stored content is not sealed: %q", stored)
	}
	var revisionStored string
	if err := dataStore.pool.QueryRow(ctx, `SELECT content FROM config_revisions WHERE config_id=$1 AND version=1`, saved.ID).Scan(&revisionStored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(revisionStored, keyedEncryptedPrefix) {
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
	encryptedStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("integration-test-key"))
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

func TestEncryptedContentReadsFailClosedWithoutKeyAcrossStorePaths(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	keyedStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("missing-key-paths"))
	if err != nil {
		t.Fatal(err)
	}
	defer keyedStore.Close()

	enrollment, err := keyedStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "missing-key-paths", TTLMinutes: 5, MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := keyedStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "missing-key-paths", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineMihomo},
		PublicKey: base64.RawURLEncoding.EncodeToString(randomPublicKey(t)),
	}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTaskTestAgent(keyedStore, agent.ID, enrollment.ID)
	saved, err := keyedStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "missing-key config", Engine: core.EngineMihomo,
		Content: "mixed-port: 7892\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	template, err := keyedStore.CreateConfigTemplate(ctx, "missing-key template", "mihomo", "mixed-port: 7893\nmode: rule\n")
	if err != nil {
		t.Fatal(err)
	}
	defer keyedStore.DeleteConfigTemplate(context.Background(), template.ID)
	task, err := keyedStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionReadConfig, Engine: core.EngineMihomo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyedStore.pool.Exec(ctx, `UPDATE tasks SET status='succeeded',config_content=(SELECT content FROM configs WHERE id=$1),finished_at=now() WHERE id=$2`, saved.ID, task.ID); err != nil {
		t.Fatal(err)
	}

	plainStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer plainStore.Close()
	if _, err := plainStore.AgentConfig(ctx, agent.ID, core.EngineMihomo); err == nil || strings.Contains(err.Error(), "rf2:") {
		t.Fatalf("agent config without key error=%v, must fail closed without ciphertext", err)
	}
	if _, err := plainStore.ConfigRevision(ctx, saved.ID, 1); err == nil || strings.Contains(err.Error(), "rf2:") {
		t.Fatalf("config revision without key error=%v, must fail closed without ciphertext", err)
	}
	if _, err := plainStore.ListConfigTemplates(ctx); err == nil || strings.Contains(err.Error(), "rf2:") {
		t.Fatalf("template list without key error=%v, must fail closed without ciphertext", err)
	}
	if _, err := plainStore.ReadTaskConfigSnapshot(ctx, task.ID, agent.ID, core.EngineMihomo); err == nil || strings.Contains(err.Error(), "rf2:") {
		t.Fatalf("task snapshot without key error=%v, must fail closed without ciphertext", err)
	}
}

func randomPublicKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}
