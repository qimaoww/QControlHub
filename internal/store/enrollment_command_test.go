package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProtectedEnrollmentCommandReadsAreIdempotentAndConcurrent(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("enrollment-command-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	created, err := dataStore.CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "idempotent-command", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	var agentID string
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if agentID != "" {
			_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM agents WHERE id=$1`, agentID)
		}
		_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM enrollment_tokens WHERE id=$1`, created.ID)
	})
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: created.Name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
	}, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	agentID = agent.ID
	var ciphertext string
	if err := dataStore.pool.QueryRow(ctx, `SELECT token_ciphertext FROM enrollment_tokens WHERE id=$1`, created.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if ciphertext == created.Token || strings.Contains(ciphertext, created.Token) || !strings.HasPrefix(ciphertext, keyedEncryptedPrefix) {
		t.Fatal("protected enrollment credential was not sealed at rest")
	}

	const readers = 12
	results := make(chan core.EnrollmentTokenCreated, readers)
	errorsSeen := make(chan error, readers)
	var group sync.WaitGroup
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			value, readErr := dataStore.EnrollmentCommandForAgent(ctx, agent.ID)
			if readErr != nil {
				errorsSeen <- readErr
				return
			}
			results <- value
		}()
	}
	group.Wait()
	close(results)
	close(errorsSeen)
	for readErr := range errorsSeen {
		t.Errorf("concurrent read: %v", readErr)
	}
	for value := range results {
		if value.ID != created.ID || value.Token != created.Token || value.AgentID != agent.ID {
			t.Errorf("read command = %+v, want original record and token", value)
		}
	}
	var records, usedCount int
	if err := dataStore.pool.QueryRow(ctx, `SELECT count(*),max(used_count) FROM enrollment_tokens WHERE agent_id=$1`, agent.ID).Scan(&records, &usedCount); err != nil {
		t.Fatal(err)
	}
	if records != 1 || usedCount != 1 {
		t.Fatalf("after reads records=%d used_count=%d, want 1 and 1", records, usedCount)
	}
	availability, err := dataStore.ListEnrollmentCommandAvailability(ctx, []string{agent.ID})
	if err != nil || !availability[agent.ID] {
		t.Fatalf("valid command availability=%v err=%v", availability, err)
	}
	if _, err := dataStore.pool.Exec(ctx, `UPDATE enrollment_tokens SET token_ciphertext='rf2:corrupt' WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	availability, err = dataStore.ListEnrollmentCommandAvailability(ctx, []string{agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if availability[agent.ID] {
		t.Fatal("corrupt ciphertext was reported as command available")
	}
}

func TestProtectedEnrollmentCommandFailsClosedAndHonorsLifecycle(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("command-current-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	if _, err := (&Store{pool: dataStore.pool}).CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "missing-key-command", Reusable: true}); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("create without key error=%v, want ErrSecretUnavailable", err)
	}
	legacy, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "legacy-command", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.DeleteEnrollmentToken(context.Background(), legacy.ID)
	if _, err := dataStore.EnrollmentCommandByID(ctx, legacy.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("legacy read error=%v, want ErrSecretUnavailable", err)
	}

	protected, err := dataStore.CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "lifecycle-command", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.EnrollmentCommandByID(ctx, protected.ID); err != nil {
		t.Fatal(err)
	}
	wrong, err := newConfigCryptor(testEncryptionKey("wrong key"))
	if err != nil {
		t.Fatal(err)
	}
	wrongStore := &Store{pool: dataStore.pool, cryptor: wrong}
	if _, err := wrongStore.EnrollmentCommandByID(ctx, protected.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("wrong key read error=%v, want ErrSecretUnavailable", err)
	}
	if _, err := dataStore.pool.Exec(ctx, `UPDATE enrollment_tokens SET expires_at=now()-interval '1 minute' WHERE id=$1`, protected.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.EnrollmentCommandByID(ctx, protected.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired read error=%v, want ErrNotFound", err)
	}
	if removed, err := dataStore.CleanupExpiredEnrollmentTokens(ctx); err != nil || removed < 1 {
		t.Fatalf("expired credential cleanup removed=%d err=%v", removed, err)
	}
	if err := dataStore.DeleteEnrollmentToken(ctx, protected.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired credential delete error=%v, want not found", err)
	}
	if _, err := dataStore.EnrollmentCommandByID(ctx, protected.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted read error=%v, want ErrNotFound", err)
	}
}

func TestProtectedEnrollmentCommandKeyRotation(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := OpenWithConfigKeyring(ctx, databaseURL, true, "", []string{"orphaned-previous-key"}); err == nil {
		t.Fatal("previous encryption key without a current write key was accepted")
	}
	oldKey := testEncryptionKey("enrollment-old-key")
	currentKey := testEncryptionKey("enrollment-current-key")
	oldStore, err := OpenWithConfigKey(ctx, databaseURL, true, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	defer oldStore.Close()
	oldCommand, err := oldStore.CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "rotated-old-command", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	defer oldStore.DeleteEnrollmentToken(context.Background(), oldCommand.ID)

	rotatedStore, err := OpenWithConfigKeyring(ctx, databaseURL, true, currentKey, []string{oldKey})
	if err != nil {
		t.Fatal(err)
	}
	defer rotatedStore.Close()
	readOld, err := rotatedStore.EnrollmentCommandByID(ctx, oldCommand.ID)
	if err != nil || readOld.Token != oldCommand.Token {
		t.Fatalf("read with previous key = %+v, %v", readOld, err)
	}
	newCommand, err := rotatedStore.CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "rotated-new-command", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rotatedStore.DeleteEnrollmentToken(context.Background(), newCommand.ID)
	if _, err := oldStore.EnrollmentCommandByID(ctx, newCommand.ID); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("old key read new command error=%v, want ErrSecretUnavailable", err)
	}
}
