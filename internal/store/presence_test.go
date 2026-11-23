package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
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

func TestOwnerPresenceDefaultsAndSavedThresholdStayIndependent(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "presence-owner")
	agent := sharedTestAgent(t, db, owner)
	now := time.Now().UTC()
	if _, err := db.pool.Exec(ctx, `UPDATE panel_settings SET agent_offline_threshold_seconds=180;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET last_seen=$2 WHERE id=$1`, agent.ID, now.Add(-70*time.Second)); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		agents, err := db.ListAgents(owner)
		if err != nil || len(agents) != 1 || agents[0].Status != want {
			t.Fatalf("owner agents = %+v, %v; want %s", agents, err, want)
		}
		sources, err := db.ListCoreLogSources(owner)
		if err != nil || len(sources) == 0 {
			t.Fatalf("owner log sources: %d, %v", len(sources), err)
		}
		for _, source := range sources {
			// This fixture advertises no log feature. An online node therefore
			// reports unsupported, rather than offline or collecting.
			status := "unsupported"
			if want == "offline" {
				status = "offline"
			}
			if source.AgentID != agent.ID || source.Status != status {
				t.Fatalf("log source %s/%s: got %s, want %s", source.AgentID, source.Engine, source.Status, status)
			}
		}
		if _, err := db.AgentPresenceTransitions(ctx, now, 180*time.Second); err != nil {
			t.Fatal(err)
		}
		var state string
		if err := db.pool.QueryRow(ctx, `SELECT presence_notification_state FROM agents WHERE id=$1`, agent.ID).Scan(&state); err != nil || state != want {
			t.Fatalf("owner presence = %s, %v; want %s", state, err, want)
		}
	}
	check("offline") // Missing owner policy uses 45s, not the root account's 180s.
	settings := core.DefaultPanelSettings()
	settings.AgentOfflineThresholdSeconds = 180
	if _, err := db.SavePanelSettings(owner, settings); err != nil {
		t.Fatal(err)
	}
	check("online")
}
