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

	// Rolled-back transaction must never alter cached or stored identity.
	tx, err := dataStore.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin rollback tx: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET public_key=$2 WHERE id=$1`, enrolled1.ID, []byte(pub1)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("UPDATE agents in rollback tx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback tx: %v", err)
	}
	if afterRollback, err := dataStore.AgentPublicKey(ctx, enrolled1.ID); err != nil || !bytes.Equal(afterRollback, pub2) {
		t.Fatalf("AgentPublicKey after rollback = %x, %v; want %x", afterRollback, err, pub2)
	}

	// Concurrent slow AgentPublicKey read must not refill cache after committed reinstall or delete invalidation.
	pub3, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 3: %v", err)
	}
	runStaleReadRace := func(triggerCommit func()) {
		dataStore.pubKeyCacheMu.Lock()
		delete(dataStore.pubKeyCache, enrolled1.ID)
		dataStore.pubKeyCacheMu.Unlock()

		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		dataStore.SetAgentPublicKeyQueryHookForTesting(func(_ context.Context, targetID string) {
			if targetID != enrolled1.ID {
				return
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		})
		readErr := make(chan error, 1)
		go func() {
			_, err := dataStore.AgentPublicKey(ctx, enrolled1.ID)
			readErr <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("timed out waiting for paused AgentPublicKey query")
		}
		triggerCommit()
		close(release)
		dataStore.SetAgentPublicKeyQueryHookForTesting(nil)
		if err := <-readErr; err != nil {
			t.Fatalf("paused AgentPublicKey failed: %v", err)
		}
	}

	runStaleReadRace(func() {
		if _, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
			Name:      "key-cache-reinstall",
			OS:        "linux",
			Arch:      "amd64",
			PublicKey: authn.EncodePublicKey(pub3),
		}, token.Token); err != nil {
			t.Fatalf("concurrent EnrollAgent reinstall: %v", err)
		}
	})
	if got3, err := dataStore.AgentPublicKey(ctx, enrolled1.ID); err != nil || !bytes.Equal(got3, pub3) {
		t.Fatalf("stale key refilled cache after reinstall: got %x, %v; want %x", got3, err, pub3)
	}

	runStaleReadRace(func() {
		if err := dataStore.DeleteAgent(WithConfigScope(ctx, "", true), enrolled1.ID); err != nil {
			t.Fatalf("concurrent DeleteAgent: %v", err)
		}
	})
	if _, err := dataStore.AgentPublicKey(ctx, enrolled1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale key refilled cache after DeleteAgent: got %v; want ErrNotFound", err)
	}
}

func TestTrafficRebootEpochRotationAndPeriodRolloverWithPostgreSQL(t *testing.T) {
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

	p1Start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	p1End := p1Start.AddDate(0, 1, 0)
	policy, err := dataStore.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "reboot-epoch-pg", Engine: core.EngineMihomo, Port: 8443,
		Protocol: core.TrafficProtocolTCP, Cycle: core.TrafficCycleMonthly, CycleAnchor: p1Start, LimitBytes: 1000,
	})
	if err != nil {
		t.Fatalf("CreatePortTrafficPolicy: %v", err)
	}
	oldEpoch := "11111111111111111111111111111111"
	newEpoch := "22222222222222222222222222222222"

	// Server already billed 900 bytes on oldEpoch (500 checkpointed on disk + 400 uncheckpointed).
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: oldEpoch,
		CollectedAt: now, PeriodStart: p1Start, PeriodEnd: p1End, EnforcementAvailable: true,
		ReceivedBytes: 900, UsedBytes: 900, LifetimeReceivedBytes: 900,
	}}, now); err != nil {
		t.Fatalf("UpdatePortTrafficUsage pre-reboot: %v", err)
	}

	// After reboot, agent rotates to newEpoch with fresh zero-based counters (50 -> 110).
	// Total in P1 must be 900 + 110 = 1010 (neither 1510 double-bill nor 900 skip).
	for _, rx := range []uint64{50, 110} {
		now = now.Add(time.Second)
		if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
			PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: newEpoch,
			CollectedAt: now, PeriodStart: p1Start, PeriodEnd: p1End, EnforcementAvailable: true, Blocked: rx >= 100,
			ReceivedBytes: rx, UsedBytes: rx, LifetimeReceivedBytes: rx,
		}}, now); err != nil {
			t.Fatalf("UpdatePortTrafficUsage post-reboot (%d): %v", rx, err)
		}
	}
	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 {
		t.Fatalf("AgentPortTrafficPolicies = %+v, %v", policies, err)
	}
	if policies[0].ReceivedBytes != 1010 || policies[0].UsedBytes != 1010 || !policies[0].Blocked {
		t.Fatalf("after reboot P1 policy = %+v; want Received=1010 Used=1010 Blocked=true", policies[0])
	}

	// Across calendar rollover into P2, usage resets cleanly to the P2 sample only.
	p2Start := p1End
	p2End := p2Start.AddDate(0, 1, 0)
	p2Time := p2Start.Add(time.Minute)
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: newEpoch,
		CollectedAt: p2Time, PeriodStart: p2Start, PeriodEnd: p2End, EnforcementAvailable: true, Blocked: false,
		ReceivedBytes: 25, UsedBytes: 25, LifetimeReceivedBytes: 135,
	}}, p2Time); err != nil {
		t.Fatalf("UpdatePortTrafficUsage P2 rollover: %v", err)
	}
	policies, err = dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 {
		t.Fatalf("AgentPortTrafficPolicies P2 = %+v, %v", policies, err)
	}
	if policies[0].ReceivedBytes != 25 || policies[0].UsedBytes != 25 || policies[0].Blocked {
		t.Fatalf("P2 rolled-over policy = %+v; want Received=25 Used=25 Blocked=false", policies[0])
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
	// An explicit empty snapshot clears old addresses; the next omission
	// must keep that empty snapshot rather than resurrect the old interface.
	for _, interfaces := range [][]core.HostNetworkInterface{{}, nil} {
		compactMetrics.NetworkInterfaces = interfaces
		compactMetrics.CollectedAt = compactMetrics.CollectedAt.Add(time.Second)
		if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, compactMetrics); err != nil {
			t.Fatal(err)
		}
		var empty bool
		if err := dataStore.pool.QueryRow(ctx, `SELECT metrics->'network_interfaces'='[]'::jsonb FROM agent_live_state WHERE agent_id=$1`, agent.ID).Scan(&empty); err != nil || !empty {
			t.Fatalf("empty interface snapshot not persisted: empty=%v err=%v", empty, err)
		}
	}

	if err := dataStore.DeleteAgent(WithConfigScope(ctx, "", true), agent.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if err := dataStore.UpdateAgentMetricsWithPublicIPProbeTrust(ctx, agent.ID, compactMetrics, PublicIPProbeTrust{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateAgentMetrics after revoke = %v; want ErrNotFound", err)
	}
}
