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
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("enrollment-reinstall-test-key"))
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

func TestAdditionalAgentCredentialsRemainIndependentlyUsable(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("enrollment-reinstall-test-key"))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	firstCredential, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "multiple-install-commands", Reusable: true,
	})
	if err != nil {
		t.Fatalf("create first credential: %v", err)
	}
	var agentID string
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if agentID != "" {
			_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM agents WHERE id=$1`, agentID)
		}
		_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM enrollment_tokens WHERE id=$1`, firstCredential.ID)
	})

	first, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "multiple-install-commands", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t)),
	}, firstCredential.Token)
	if err != nil {
		t.Fatalf("first enrollment: %v", err)
	}
	agentID = first.ID
	additional, err := dataStore.CreateAgentEnrollmentToken(ctx, first.ID)
	if err != nil {
		t.Fatalf("create additional credential: %v", err)
	}
	if additional.ID == firstCredential.ID || additional.AgentID != first.ID || additional.Token == "" {
		t.Fatalf("additional credential = %+v", additional)
	}
	if !dataStore.EnrollmentTokenUsable(ctx, firstCredential.Token) || !dataStore.EnrollmentTokenUsable(ctx, additional.Token) {
		t.Fatal("creating another install command invalidated a credential")
	}
	credentials, err := dataStore.ListEnrollmentTokens(ctx)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	bound := 0
	for _, credential := range credentials {
		if (credential.ID == firstCredential.ID || credential.ID == additional.ID) && credential.AgentID == first.ID {
			bound++
		}
	}
	if bound != 2 {
		t.Fatalf("credentials bound to agent %s = %d, want 2", first.ID, bound)
	}

	reinstalled, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "multiple-install-commands", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo, core.EngineXray}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t)),
	}, additional.Token)
	if err != nil {
		t.Fatalf("enroll with additional credential: %v", err)
	}
	if !reinstalled.Reinstalled || reinstalled.ID != first.ID {
		t.Fatalf("additional credential enrollment = %+v, want agent %s", reinstalled, first.ID)
	}
	if err := dataStore.DeleteEnrollmentToken(ctx, additional.ID); err != nil {
		t.Fatalf("delete additional credential: %v", err)
	}
	if dataStore.EnrollmentTokenUsable(ctx, additional.Token) {
		t.Fatal("deleted additional credential remains usable")
	}
	if !dataStore.EnrollmentTokenUsable(ctx, firstCredential.Token) {
		t.Fatal("deleting one install command invalidated the original command")
	}

	reinstalled, err = dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "multiple-install-commands", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t)),
	}, firstCredential.Token)
	if err != nil || !reinstalled.Reinstalled || reinstalled.ID != first.ID {
		t.Fatalf("original credential after deleting additional = %+v, %v", reinstalled, err)
	}
}

func TestDeleteAgentInvalidatesBoundReusableCredential(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("enrollment-reinstall-test-key"))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	credential, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "deleted-bound-node", Reusable: true,
	})
	if err != nil {
		t.Fatalf("create reusable add-node credential: %v", err)
	}
	var agentID string
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if agentID != "" {
			_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM agents WHERE id=$1`, agentID)
		}
		_, _ = dataStore.pool.Exec(cleanupCtx, `DELETE FROM enrollment_tokens WHERE id=$1`, credential.ID)
	})
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "deleted-bound-node", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t)),
	}, credential.Token)
	if err != nil {
		t.Fatalf("enroll agent: %v", err)
	}
	agentID = agent.ID
	additional, err := dataStore.CreateAgentEnrollmentToken(ctx, agent.ID)
	if err != nil {
		t.Fatalf("create additional credential: %v", err)
	}
	trafficPolicy, err := dataStore.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "deleted node port", Engine: core.EngineMihomo, Port: 8443,
		Protocol: core.TrafficProtocolTCP, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 1 << 30,
	})
	if err != nil {
		t.Fatalf("create deleted node traffic policy: %v", err)
	}

	if err := dataStore.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if dataStore.EnrollmentTokenUsable(ctx, credential.Token) {
		t.Fatal("deleted agent's reusable credential remains usable")
	}
	if dataStore.EnrollmentTokenUsable(ctx, additional.Token) {
		t.Fatal("deleted agent's additional credential remains usable")
	}
	var trafficRows int
	if err := dataStore.pool.QueryRow(ctx, `SELECT count(*) FROM port_traffic_policies WHERE id=$1`, trafficPolicy.ID).Scan(&trafficRows); err != nil || trafficRows != 0 {
		t.Fatalf("deleted agent traffic rows = %d, %v", trafficRows, err)
	}
	if _, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "deleted-bound-node", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t)),
	}, credential.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted agent credential enrollment error = %v, want not found", err)
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
