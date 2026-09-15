package store

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestBatchedTrafficRollsBackAllPorts(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, usages := seedPerformanceAgent(t, s, 64)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.UpdatePortTrafficUsage(ctx, agentID, usages, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE port_traffic_daily_usage ADD CONSTRAINT reject_large_fixture CHECK (received_bytes<9000)`); err != nil {
		t.Fatal(err)
	}
	for index := range usages {
		usages[index].ReceivedBytes = 200
		usages[index].UsedBytes = 200
	}
	usages[63].ReceivedBytes, usages[63].UsedBytes = 10000, 10000
	if err := s.UpdatePortTrafficUsage(ctx, agentID, usages, now.Add(time.Second)); err == nil {
		t.Fatal("expected database error in the last batched daily write")
	}
	policies, err := s.AgentPortTrafficPolicies(ctx, agentID)
	if err != nil || len(policies) != 64 {
		t.Fatalf("policies = %d, %v", len(policies), err)
	}
	for _, policy := range policies {
		if policy.UsedBytes != 100 {
			t.Fatalf("partial policy write: %s=%d", policy.ID, policy.UsedBytes)
		}
	}
	daily, err := s.ListPortTrafficDailyUsage(ctx, agentID, "", now)
	if err != nil || len(daily) != 64 {
		t.Fatalf("daily = %d, %v", len(daily), err)
	}
	for _, item := range daily {
		if item.UsedBytes != 100 || item.SampleCount != 1 {
			t.Fatalf("partial daily write: %+v", item)
		}
	}
	// The connection remains usable after draining a failed batch.
	usages[63].ReceivedBytes, usages[63].UsedBytes = 200, 200
	if err := s.UpdatePortTrafficUsage(ctx, agentID, usages, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	duplicate := append(append([]core.PortTrafficUsage(nil), usages...), usages[0])
	if err := s.UpdatePortTrafficUsage(ctx, agentID, duplicate, now.Add(3*time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate accepted: %v", err)
	}
}

func TestSetTrafficWriteIsIsolatedAndConcurrent(t *testing.T) {
	s := openPerformanceStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agentID, usages := seedPerformanceAgent(t, s, 16)
	_, foreign := seedPerformanceAgent(t, s, 1)
	if _, err := s.pool.Exec(ctx, `UPDATE port_traffic_policies SET monitoring_enabled=false WHERE id=$1`, usages[0].PolicyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE port_traffic_policies SET reset_generation=2 WHERE id=$1`, usages[1].PolicyID); err != nil {
		t.Fatal(err)
	}
	report := append(slices.Clone(usages), foreign[0])
	reversed := slices.Clone(report)
	slices.Reverse(reversed)
	now := time.Now().UTC()
	var group sync.WaitGroup
	for _, input := range [][]core.PortTrafficUsage{report, reversed} {
		group.Add(1)
		go func(input []core.PortTrafficUsage) {
			defer group.Done()
			if err := s.UpdatePortTrafficUsage(ctx, agentID, input, now); err != nil {
				t.Error(err)
			}
		}(input)
	}
	group.Wait()
	daily, err := s.ListPortTrafficDailyUsage(ctx, "", "", now)
	if err != nil || len(daily) != 14 {
		t.Fatalf("daily rows=%d, %v; want 14 owned, enabled, matching-generation ports", len(daily), err)
	}
	for _, item := range daily {
		if item.AgentID != agentID || item.UsedBytes != 100 || item.SampleCount != 1 {
			t.Fatalf("duplicate or cross-agent write: %+v", item)
		}
	}
}

func TestSetTrafficWritePreservesBigintPrecisionAndSaturation(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID, usages := seedPerformanceAgent(t, s, 1)
	usages[0].ReceivedBytes = math.MaxInt64 - 1
	usages[0].UsedBytes = math.MaxInt64 - 1
	now := time.Now().UTC()
	if err := s.UpdatePortTrafficUsage(ctx, agentID, usages, now); err != nil {
		t.Fatal(err)
	}
	policies, err := s.AgentPortTrafficPolicies(ctx, agentID)
	if err != nil || len(policies) != 1 || policies[0].ReceivedBytes != math.MaxInt64-1 {
		t.Fatalf("JSON bigint lost precision: %+v, %v", policies, err)
	}
	// Simulate a counter restart. Accumulated totals saturate, never overflow
	// or lose low bits through a floating-point JSON intermediate.
	usages[0].ReceivedBytes, usages[0].UsedBytes = 10, 10
	if err := s.UpdatePortTrafficUsage(ctx, agentID, usages, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	daily, err := s.ListPortTrafficDailyUsage(ctx, agentID, "", now)
	if err != nil || len(daily) != 1 || daily[0].ReceivedBytes != math.MaxInt64 || daily[0].UsedBytes != math.MaxInt64 {
		t.Fatalf("saturation failed: %+v, %v", daily, err)
	}
}
