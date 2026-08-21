package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPortTrafficPolicyLifecycleWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	request := core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "edge 443", Engine: core.EngineMihomo, Port: 443,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 100 << 30,
	}
	created, err := dataStore.CreatePortTrafficPolicy(ctx, request)
	if err != nil || created.ResetGeneration != 1 || created.EnforcementAvailable {
		t.Fatalf("created policy = %+v, %v", created, err)
	}
	if _, err := dataStore.CreatePortTrafficPolicy(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate port error = %v", err)
	}

	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	usage := core.PortTrafficUsage{
		PolicyID: created.ID, ResetGeneration: created.ResetGeneration,
		ReceivedBytes: 3 << 30, SentBytes: 2 << 30, UsedBytes: 5 << 30,
		ReceiveBPS: 1200, SendBPS: 800, PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0),
		EnforcementAvailable: true,
	}
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, time.Now()); err != nil {
		t.Fatal(err)
	}
	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].UsedBytes != 5<<30 || !policies[0].EnforcementAvailable {
		t.Fatalf("reported policies = %+v, %v", policies, err)
	}

	request.LimitBytes = 4 << 30
	updated, err := dataStore.UpdatePortTrafficPolicy(ctx, created.ID, request)
	if err != nil || updated.ResetGeneration != 1 || updated.UsedBytes != 5<<30 {
		t.Fatalf("limit-only update = %+v, %v", updated, err)
	}
	request.Port = 8443
	updated, err = dataStore.UpdatePortTrafficPolicy(ctx, created.ID, request)
	if err != nil || updated.ResetGeneration != 2 || updated.UsedBytes != 0 {
		t.Fatalf("port update = %+v, %v", updated, err)
	}
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, time.Now()); err != nil {
		t.Fatal(err)
	}
	updatedPolicies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || updatedPolicies[0].UsedBytes != 0 {
		t.Fatalf("stale generation was accepted: %+v, %v", updatedPolicies, err)
	}
	reset, err := dataStore.ResetPortTrafficPolicy(ctx, created.ID)
	if err != nil || reset.ResetGeneration != 3 {
		t.Fatalf("reset policy = %+v, %v", reset, err)
	}
	agentID, err := dataStore.DeletePortTrafficPolicy(ctx, created.ID)
	if err != nil || agentID != agent.ID {
		t.Fatalf("delete policy agent = %q, %v", agentID, err)
	}
}
