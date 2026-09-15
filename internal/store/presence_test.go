package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPresenceSkipsLockedAgentsAndClaimsOnce(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, _ := seedPerformanceAgent(t, s, 0)
	ctx := context.Background()
	now := time.Now().UTC()
	if got, err := s.AgentPresenceTransitions(ctx, now, 45*time.Second); err != nil || len(got) != 0 {
		t.Fatalf("baseline = %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET last_seen=$2 WHERE id=$1`, agentID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM agents WHERE id=$1 FOR UPDATE`, agentID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, time.Second)
	got, err := s.AgentPresenceTransitions(short, now, 45*time.Second)
	cancel()
	if err != nil || len(got) != 0 {
		t.Fatalf("monitor waited on heartbeat lock: %+v, %v", got, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	var group sync.WaitGroup
	for range 6 {
		group.Add(1)
		go func() {
			defer group.Done()
			got, err := s.AgentPresenceTransitions(ctx, now, 45*time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			for _, item := range got {
				if item.Agent.ID != agentID || item.Online {
					t.Errorf("unexpected transition: %+v", item)
				}
			}
			count.Add(int64(len(got)))
		}()
	}
	group.Wait()
	if count.Load() != 1 {
		t.Fatalf("duplicate/missing offline notification: %d", count.Load())
	}
}

func TestOverviewUsesConfiguredOfflineThreshold(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, _ := seedPerformanceAgent(t, s, 0)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE panel_settings SET agent_offline_threshold_seconds=90`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '60 seconds' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	overview, err := s.Overview(ctx)
	if err != nil || overview.AgentsOnline != 1 {
		t.Fatalf("overview=%+v, err=%v", overview, err)
	}
	agents, err := s.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Status != "online" {
		t.Fatalf("agents=%+v, err=%v", agents, err)
	}
}
