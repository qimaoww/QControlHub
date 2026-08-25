package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMetricSamplesRecordQueryAndPrune(t *testing.T) {
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

	// Without metrics the recorder must not produce rows.
	count, err := dataStore.RecordAgentMetricSamples(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("record without metrics: %v", err)
	}
	if count != 0 {
		t.Fatalf("recorded %d samples without metrics, want 0", count)
	}

	now := time.Now().UTC()
	first := core.HeartbeatRequest{
		Metrics: &core.HostMetrics{
			CollectedAt: now, CPUAvailable: true, CPUPercent: 42.5,
			MemoryAvailable: true, MemoryUsedBytes: 2 << 30, MemoryTotalBytes: 4 << 30,
			NetworkAvailable: true, NetworkRXBPS: 1 << 20, NetworkTXBPS: 2 << 20,
		},
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, first); err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Metrics: &core.HostMetrics{
		CollectedAt: now.Add(90 * time.Second), CPUAvailable: true, CPUPercent: 12.25,
		MemoryAvailable: true, MemoryUsedBytes: 1 << 30, MemoryTotalBytes: 4 << 30,
		NetworkAvailable: true, NetworkRXBPS: 3 << 20, NetworkTXBPS: 4 << 20,
	}}); err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}

	recorded, err := dataStore.RecordAgentMetricSamples(ctx, now)
	if err != nil {
		t.Fatalf("record metric samples: %v", err)
	}
	if recorded != 1 {
		t.Fatalf("recorded %d samples, want 1", recorded)
	}
	// The same timestamp is idempotent.
	again, err := dataStore.RecordAgentMetricSamples(ctx, now)
	if err != nil || again != 0 {
		t.Fatalf("re-record at same timestamp = %d, %v; want 0", again, err)
	}

	samples, err := dataStore.MetricSamples(ctx, agent.ID, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("query metric samples: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("metric samples = %d rows, want 1", len(samples))
	}
	// The recorder snapshots the latest heartbeat metrics, so the second
	// heartbeat (CPU 12.25%, 25% memory, 3/4 MiB/s) wins.
	if samples[0].CPUPercent != 12.25 || samples[0].MemoryPercent != 25 || samples[0].RXRateBPS != 3<<20 || samples[0].TXRateBPS != 4<<20 {
		t.Fatalf("metric sample = %+v", samples[0])
	}
	empty, err := dataStore.MetricSamples(ctx, agent.ID, now.Add(time.Hour), 100)
	if err != nil || len(empty) != 0 {
		t.Fatalf("future window samples = %d, %v; want none", len(empty), err)
	}

	pruned, err := dataStore.PruneMetricSamples(ctx, now.Add(-time.Hour))
	if err != nil || pruned != 0 {
		t.Fatalf("prune future window = %d, %v; want 0", pruned, err)
	}
	pruned, err = dataStore.PruneMetricSamples(ctx, now.Add(time.Hour))
	if err != nil || pruned != 1 {
		t.Fatalf("prune past window = %d, %v; want 1", pruned, err)
	}
	remaining, err := dataStore.MetricSamples(ctx, agent.ID, now.Add(-time.Hour), 100)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("samples after prune = %d, %v; want none", len(remaining), err)
	}
}

func TestHeartbeatAndMetricsClearCloudflareProbesWithPostgreSQL(t *testing.T) {
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

	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Metrics: &core.HostMetrics{
		PublicIPv4: "172.69.135.152",
		PublicIPv6: "2400:cb00::1",
	}}); err != nil {
		t.Fatalf("heartbeat with relay probes: %v", err)
	}
	current, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("read heartbeat relay probes: %v", err)
	}
	if current.Metrics.PublicIPv4 != "" || current.Metrics.PublicIPv6 != "" {
		t.Fatalf("heartbeat persisted relay probes: %+v", current.Metrics)
	}

	if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{
		PublicIPv4: "::ffff:172.69.135.152",
		PublicIPv6: "2606:4700::1",
	}); err != nil {
		t.Fatalf("metrics-only update with relay probes: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("read metrics-only relay probes: %v", err)
	}
	if current.Metrics.PublicIPv4 != "" || current.Metrics.PublicIPv6 != "" {
		t.Fatalf("metrics-only update persisted relay probes: %+v", current.Metrics)
	}

	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Metrics: &core.HostMetrics{
		PublicIPv4: "93.184.216.34",
		PublicIPv6: "2001:4860:4860::8888",
	}}); err != nil {
		t.Fatalf("heartbeat with genuine probes: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("read genuine probes: %v", err)
	}
	if current.Metrics.PublicIPv4 != "93.184.216.34" || current.Metrics.PublicIPv6 != "2001:4860:4860::8888" {
		t.Fatalf("genuine probes were not retained: %+v", current.Metrics)
	}
}

func TestManagedPublicIPProbeTrustUsesCurrentSessionAndFamilyWithPostgreSQL(t *testing.T) {
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
	managed := core.HostMetrics{
		PublicIPv4: "198.35.26.96", PublicIPv4Source: core.PublicIPProbeSourceControlPlane,
		PublicIPv6: "2001:4860:4860::8888", PublicIPv6Source: core.PublicIPProbeSourceControlPlane,
	}

	// Direct store callers and metrics-only traffic before a complete current-
	// session heartbeat are fail closed for managed provenance.
	if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, managed); err != nil {
		t.Fatalf("untrusted metrics-only update: %v", err)
	}
	assertRawPublicIPProbeRow(t, ctx, dataStore, agent.ID, "", "", "", "")

	// Trust is per family and cannot be inferred merely from a persisted
	// managed feature. Here only the configured IPv4 endpoint is authoritative.
	if err := dataStore.HeartbeatWithPublicIPProbeTrust(ctx, agent.ID, core.HeartbeatRequest{
		Features: []string{core.AgentFeatureManagedPublicIPProbe}, Metrics: &managed,
	}, PublicIPProbeTrust{ControlPlaneIPv4: true}); err != nil {
		t.Fatalf("trusted managed heartbeat: %v", err)
	}
	assertRawPublicIPProbeRow(t, ctx, dataStore, agent.ID, "198.35.26.96", core.PublicIPProbeSourceControlPlane, "", "")

	// A later untrusted current session must not inherit the persisted feature.
	if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, managed); err != nil {
		t.Fatalf("downgraded metrics-only update: %v", err)
	}
	assertRawPublicIPProbeRow(t, ctx, dataStore, agent.ID, "", "", "", "")

	// Missing source remains the v1 local Agent endpoint compatibility path.
	local := core.HostMetrics{PublicIPv4: "198.35.26.96"}
	if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, local); err != nil {
		t.Fatalf("legacy local probe update: %v", err)
	}
	assertRawPublicIPProbeRow(t, ctx, dataStore, agent.ID, "198.35.26.96", core.PublicIPProbeSourceAgent, "", "")
}

func assertRawPublicIPProbeRow(t *testing.T, ctx context.Context, dataStore *Store, agentID, ipv4, ipv4Source, ipv6, ipv6Source string) {
	t.Helper()
	var gotIPv4, gotIPv4Source, gotIPv6, gotIPv6Source *string
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT metrics->>'public_ipv4', metrics->>'public_ipv4_source',
		       metrics->>'public_ipv6', metrics->>'public_ipv6_source'
		FROM agents WHERE id=$1`, agentID).Scan(&gotIPv4, &gotIPv4Source, &gotIPv6, &gotIPv6Source); err != nil {
		t.Fatalf("read raw public IP probe row: %v", err)
	}
	deref := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	if deref(gotIPv4) != ipv4 || deref(gotIPv4Source) != ipv4Source || deref(gotIPv6) != ipv6 || deref(gotIPv6Source) != ipv6Source {
		t.Fatalf("raw public IP probe row = ipv4 %q source %q ipv6 %q source %q; want %q %q %q %q", deref(gotIPv4), deref(gotIPv4Source), deref(gotIPv6), deref(gotIPv6Source), ipv4, ipv4Source, ipv6, ipv6Source)
	}
}
