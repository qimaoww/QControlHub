package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestReusableAddNodeCredentialReinstallsOneBoundNode(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	credential, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "reinstall-bound-node", Reusable: true,
	})
	if err != nil {
		t.Fatalf("create reusable add-node credential: %v", err)
	}
	var agentID string
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if agentID != "" {
			_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM agents WHERE id=$1`, agentID)
		}
		_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM enrollment_tokens WHERE id=$1`, credential.ID)
	}()
	if !credential.Reusable || credential.ExpiresAt != nil || credential.MaxUses != 0 {
		t.Fatalf("reusable credential metadata = %+v", credential.EnrollmentToken)
	}
	if !dataStore.EnrollmentTokenUsable(ctx, credential.Token) {
		t.Fatal("new reusable credential is not usable for protected downloads")
	}
	if _, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "reinstall-bound-node", Reusable: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate reusable node name error = %v, want conflict", err)
	}

	firstKey := testEnrollmentPublicKey(t)
	first, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "reinstall-bound-node", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(firstKey),
	}, credential.Token)
	if err != nil {
		t.Fatalf("first enrollment: %v", err)
	}
	agentID = first.ID
	if first.Reinstalled {
		t.Fatal("first enrollment was reported as a reinstall")
	}

	_, err = dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "different-node", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t)),
	}, credential.Token)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("credential accepted a different node name: %v", err)
	}

	secondKey := testEnrollmentPublicKey(t)
	second, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "reinstall-bound-node", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo, core.EngineXray}, PublicKey: base64.RawURLEncoding.EncodeToString(secondKey),
	}, credential.Token)
	if err != nil {
		t.Fatalf("repeat enrollment: %v", err)
	}
	if !second.Reinstalled || second.ID != first.ID {
		t.Fatalf("repeat enrollment = %+v, want reinstalled agent %s", second, first.ID)
	}
	if !dataStore.EnrollmentTokenUsable(ctx, credential.Token) {
		t.Fatal("reusable credential expired after repeat enrollment")
	}
	storedKey, err := dataStore.AgentPublicKey(ctx, first.ID)
	if err != nil || string(storedKey) != string(secondKey) {
		t.Fatalf("stored public key was not replaced: %v", err)
	}
	tokens, err := dataStore.ListEnrollmentTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundCredential := false
	for _, token := range tokens {
		if token.ID == credential.ID && token.UsedCount != 2 {
			t.Fatalf("used count = %d, want 2 successful installations", token.UsedCount)
		}
		if token.ID == credential.ID {
			foundCredential = true
		}
	}
	if !foundCredential {
		t.Fatal("reusable credential is missing from add-node records")
	}

	if err := dataStore.DeleteEnrollmentToken(ctx, credential.ID); err != nil {
		t.Fatalf("delete add-node credential: %v", err)
	}
	if dataStore.EnrollmentTokenUsable(ctx, credential.Token) {
		t.Fatal("deleted add-node credential remains usable")
	}
}

func testEnrollmentPublicKey(t *testing.T) []byte {
	t.Helper()
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}
