package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentPublicKeyCacheInvalidationAndRequestTxBypassWithPostgreSQL(t *testing.T) {
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

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	firstKey, err := dataStore.AgentPublicKey(ctx, agent.ID)
	if err != nil {
		t.Fatalf("first AgentPublicKey: %v", err)
	}
	mutatedCopy := bytes.Clone(firstKey)
	mutatedCopy[0] ^= 0xff
	// Mutating caller slice must not corrupt cached key; second read hits clone.
	firstKey[0] ^= 0xff
	cachedKey, err := dataStore.AgentPublicKey(ctx, agent.ID)
	if err != nil {
		t.Fatalf("cached AgentPublicKey: %v", err)
	}
	firstKey[0] ^= 0xff
	if !bytes.Equal(cachedKey, firstKey) {
		t.Fatalf("cached public key mutated by caller: got %x want %x", cachedKey, firstKey)
	}

	// Verify DeleteAgent invalidates cache synchronously on commit even inside TTL.
	if err := dataStore.DeleteAgent(WithConfigScope(ctx, "", true), agent.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if _, err := dataStore.AgentPublicKey(ctx, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AgentPublicKey after DeleteAgent = %v; want ErrNotFound", err)
	}

	// Verify EnrollAgent reinstall also invalidates any cached key on commit.
	token, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "key-cache-reinstall", TTLMinutes: 5, Reusable: true})
	if err != nil {
		t.Fatalf("CreateEnrollmentToken: %v", err)
	}
	defer func() {
		_, _ = dataStore.pool.Exec(context.Background(), `DELETE FROM enrollment_tokens WHERE id=$1`, token.ID)
	}()
	pub1, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 1: %v", err)
	}
	enrolled1, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name:      "key-cache-reinstall",
		OS:        "linux",
		Arch:      "amd64",
		PublicKey: authn.EncodePublicKey(pub1),
	}, token.Token)
	if err != nil {
		t.Fatalf("EnrollAgent 1: %v", err)
	}
	defer cleanupTaskTestAgent(dataStore, enrolled1.ID, token.ID)
	got1, err := dataStore.AgentPublicKey(ctx, enrolled1.ID)
	if err != nil || !bytes.Equal(got1, pub1) {
		t.Fatalf("AgentPublicKey 1 = %x, %v; want %x", got1, err, pub1)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 2: %v", err)
	}
	reinstalled, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name:      "key-cache-reinstall",
		OS:        "linux",
		Arch:      "amd64",
		PublicKey: authn.EncodePublicKey(pub2),
	}, token.Token)
	if err != nil {
		t.Fatalf("EnrollAgent reinstall: %v", err)
	}
	if !reinstalled.Reinstalled || reinstalled.ID != enrolled1.ID {
		t.Fatalf("EnrollAgent did not reinstall existing agent: %+v (want id=%s)", reinstalled, enrolled1.ID)
	}
	got2, err := dataStore.AgentPublicKey(ctx, enrolled1.ID)
	if err != nil || !bytes.Equal(got2, pub2) {
		t.Fatalf("AgentPublicKey after reinstall = %x, %v; want %x", got2, err, pub2)
	}
}

func TestCleanupNoncesAndSingleCTEMetricsUpdateWithPostgreSQL(t *testing.T) {
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

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	now := time.Now().UTC()
	if err := dataStore.RecordNonce(ctx, agent.ID, "expired-nonce-1", now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("RecordNonce expired: %v", err)
	}
	if err := dataStore.RecordNonce(ctx, agent.ID, "active-nonce-1", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("RecordNonce active: %v", err)
	}
	if err := dataStore.CleanupNonces(ctx); err != nil {
		t.Fatalf("CleanupNonces: %v", err)
	}
	if err := dataStore.RecordNonce(ctx, agent.ID, "expired-nonce-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("expired nonce was not deleted by CleanupNonces: %v", err)
	}
	if err := dataStore.RecordNonce(ctx, agent.ID, "active-nonce-1", now.Add(5*time.Minute)); !errors.Is(err, ErrReplay) {
		t.Fatalf("active nonce after CleanupNonces = %v; want ErrReplay", err)
	}

	// Single-CTE UpdateAgentMetrics must persist full metrics, preserve omitted snapshots
	// on compact update, and return ErrNotFound once the agent is revoked.
	checkedAt := now.Truncate(time.Second)
	fullMetrics := core.HostMetrics{
		CollectedAt:      checkedAt,
		CPUAvailable:     true,
		CPUPercent:       18.5,
		MemoryAvailable:  true,
		MemoryUsedBytes:  1024,
		MemoryTotalBytes: 8192,
		PublicIPv4:       "198.35.26.96",
		PublicIPv4Source: core.PublicIPProbeSourceControlPlane,
		NetworkInterfaces: []core.HostNetworkInterface{
			{Name: "eth0", Addresses: []string{"10.0.0.2"}},
		},
		BBR: &core.SystemBBRStatus{
			CollectedAt:       checkedAt,
			Available:         true,
			CongestionControl: "bbr",
			DefaultQdisc:      "fq",
		},
		ClockSync: &core.ClockSyncStatus{
			Status:    "synchronized",
			CheckedAt: checkedAt,
		},
	}
	if err := dataStore.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, agent.ID, fullMetrics, PublicIPProbeTrust{ControlPlaneIPv4: true}); err != nil {
		t.Fatalf("UpdateAgentMetrics full: %v", err)
	}

	compactMetrics := core.HostMetrics{
		CollectedAt:      checkedAt.Add(time.Second),
		CPUAvailable:     true,
		CPUPercent:       42.0,
		MemoryAvailable:  true,
		MemoryUsedBytes:  2048,
		MemoryTotalBytes: 8192,
	}
	if err := dataStore.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, agent.ID, compactMetrics, PublicIPProbeTrust{ControlPlaneIPv4: true}); err != nil {
		t.Fatalf("UpdateAgentMetrics compact: %v", err)
	}
	stored, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgent after compact metrics: %v", err)
	}
	if stored.Metrics.CPUPercent != 42.0 || stored.Metrics.MemoryUsedBytes != 2048 {
		t.Fatalf("scalar metrics not updated: %+v", stored.Metrics)
	}
	if len(stored.Metrics.NetworkInterfaces) != 1 || stored.Metrics.NetworkInterfaces[0].Name != "eth0" {
		t.Fatalf("omitted network_interfaces not preserved on compact update: %+v", stored.Metrics.NetworkInterfaces)
	}
	if stored.Metrics.BBR == nil || stored.Metrics.BBR.CongestionControl != "bbr" {
		t.Fatalf("omitted bbr not preserved on compact update: %+v", stored.Metrics.BBR)
	}
	if stored.Metrics.ClockSync == nil || stored.Metrics.ClockSync.Status != "synchronized" {
		t.Fatalf("omitted clock_sync not preserved on compact update: %+v", stored.Metrics.ClockSync)
	}

	if err := dataStore.DeleteAgent(WithConfigScope(ctx, "", true), agent.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if err := dataStore.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, agent.ID, compactMetrics, PublicIPProbeTrust{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateAgentMetrics after revoke = %v; want ErrNotFound", err)
	}
}
