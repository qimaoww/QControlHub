package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestEnrollmentStaysOfflineUntilFirstHeartbeat(t *testing.T) {
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
	if agent.Status != "offline" || !agent.LastSeen.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("newly enrolled agent = %+v, want offline before its first heartbeat", agent)
	}
	stored, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || stored.Status != "offline" {
		t.Fatalf("stored newly enrolled agent = %+v, %v", stored, err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "test-heartbeat"}); err != nil {
		t.Fatal(err)
	}
	stored, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || stored.Status != "online" || stored.Version != "test-heartbeat" {
		t.Fatalf("agent after first heartbeat = %+v, %v", stored, err)
	}
}

func TestHeartbeatClearsStaleFeaturesWithPostgreSQL(t *testing.T) {
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

	agent, enrollmentID := enrollTaskTestAgentWithSource(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	current, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || !containsFeature(current.Features, core.AgentFeatureMihomoDevelopmentSource) {
		t.Fatalf("agent did not start advertising the source feature: %+v, %v", current.Features, err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
		Version: "platform-v2", OS: "Debian", Arch: "amd64",
	}); err != nil {
		t.Fatalf("store platform heartbeat: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || current.OS != "Debian" || current.Arch != "amd64" {
		t.Fatalf("agent platform after heartbeat = %q/%q, %v", current.OS, current.Arch, err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "legacy-platform"}); err != nil {
		t.Fatalf("store legacy platform heartbeat: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || current.OS != "Debian" || current.Arch != "amd64" {
		t.Fatalf("legacy heartbeat overwrote platform = %q/%q, %v", current.OS, current.Arch, err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{OS: strings.Repeat("x", 51)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized heartbeat platform error = %v; want ErrInvalid", err)
	}
	if err := dataStore.HeartbeatWithPublicIPProbeTrust(ctx, agent.ID, core.HeartbeatRequest{
		Version: "probe-v2", Features: []string{core.AgentFeatureManagedPublicIPProbe},
		Metrics: &core.HostMetrics{PublicIPv4: "198.35.26.96", PublicIPv4Source: core.PublicIPProbeSourceControlPlane},
	}, PublicIPProbeTrust{ControlPlaneIPv4: true}); err != nil {
		t.Fatalf("store managed probe heartbeat: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || current.Metrics.PublicIPv4 != "198.35.26.96" || current.Metrics.PublicIPv4Source != core.PublicIPProbeSourceControlPlane {
		t.Fatalf("trusted managed probe was not seeded: metrics=%+v, %v", current.Metrics, err)
	}

	// A complete heartbeat with an omitted/empty feature list must clear the
	// stale capability; it must not inherit a previous session's value.
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "legacy"}); err != nil {
		t.Fatalf("legacy empty-feature heartbeat: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || len(current.Features) != 0 || current.Metrics.PublicIPv4 != "" || current.Metrics.PublicIPv4Source != "" {
		t.Fatalf("agent stale state after empty-feature heartbeat = features=%+v metrics=%+v, %v", current.Features, current.Metrics, err)
	}

	// Re-advertising a non-empty feature set and then a metrics-only refresh
	// must preserve those features.
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
		Version:  "v2",
		Features: []string{core.AgentFeatureSelfUpgrade, core.AgentFeaturePortTraffic},
	}); err != nil {
		t.Fatalf("re-advertise feature heartbeat: %v", err)
	}
	if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{CPUAvailable: true, CPUPercent: 10}); err != nil {
		t.Fatalf("metrics-only refresh: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || !containsFeature(current.Features, core.AgentFeatureSelfUpgrade) || containsFeature(current.Features, core.AgentFeatureMihomoDevelopmentSource) {
		t.Fatalf("metrics-only refresh clobbered features: %+v, %v", current.Features, err)
	}
}
