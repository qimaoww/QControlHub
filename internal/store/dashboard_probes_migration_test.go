package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDashboardProbeGroupsMigrationPreservesLinksAndSamples(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Seed two users and agents, then downgrade schema marker to v104 without the
	// unique group constraint so legacy ungrouped and duplicate-group rows can exist.
	aliceID, err := core.NewID("usr")
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := core.NewID("usr")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `
		INSERT INTO panel_users(id, username, role, password_hash, permissions, created_at, updated_at)
		VALUES ($1, 'mig_alice', 'user', 'hash', '["agents.read","tasks.read","tasks.execute"]'::jsonb, now(), now()),
		       ($2, 'mig_bob', 'user', 'hash', '["agents.read","tasks.read","tasks.execute"]'::jsonb, now(), now())`, aliceID, bobID); err != nil {
		t.Fatal(err)
	}
	agent1, err := core.NewID("agt")
	if err != nil {
		t.Fatal(err)
	}
	agent2, err := core.NewID("agt")
	if err != nil {
		t.Fatal(err)
	}
	for i, aid := range []string{agent1, agent2} {
		pub := make([]byte, 32)
		pub[0] = byte(i + 1)
		if _, err := db.pool.Exec(ctx, `
			INSERT INTO agents(id, name, os, arch, capabilities, features, public_key, last_seen, enrolled_at, owner_id)
			VALUES ($1, $2, 'linux', 'amd64', '[]'::jsonb, '["network-diagnostics-v1"]'::jsonb, $3, now(), now(), $4)`,
			aid, "node-"+string(rune('a'+i)), pub, aliceID); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.pool.Exec(ctx, `
		DROP INDEX IF EXISTS dashboard_probe_pairs_owner_group_uidx;
		ALTER TABLE dashboard_probe_pairs DROP CONSTRAINT IF EXISTS dashboard_probe_pairs_group_nonempty_check;
		DELETE FROM qcontrolhub_schema_migrations WHERE version >= 105;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES (104) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	paramsRaw, _ := json.Marshal(core.DiagnosticParameters{Tool: "ping", Protocol: "icmp", Provider: "disable-geoip", Count: 5, IntervalMS: 200, TimeoutMS: 1000, MaxHops: 30, Size: 56, Port: 5201, Duration: 5, Streams: 1})
	nodesRaw, _ := json.Marshal([]core.DiagnosticEndpoint{
		{AgentID: agent1, Address: "192.0.2.1"},
		{AgentID: agent2, Address: "192.0.2.2"},
	})
	baseTime := time.Now().UTC().Add(-time.Hour)
	seedPair := func(id, ownerID, name, groupName string, offsetSec int) {
		if _, err := db.pool.Exec(ctx, `
			INSERT INTO dashboard_probe_pairs(
				id, owner_id, name, group_name, target_mode,
				node1_agent_id, node1_address, node2_agent_id, node2_address,
				nodes, bidirectional, parameters, auto_enabled, auto_interval_seconds,
				revision, created_at, updated_at
			) VALUES ($1,$2,$3,$4,'',$5,'192.0.2.1',$6,'192.0.2.2',$7,true,$8,false,60,1,$9,$9)`,
			id, ownerID, name, groupName, agent1, agent2, nodesRaw, paramsRaw, baseTime.Add(time.Duration(offsetSec)*time.Second)); err != nil {
			t.Fatalf("seed legacy pair %s: %v", id, err)
		}
	}

	seedPair("dpb_mig_1", aliceID, "第一条空组", "", 1)
	seedPair("dpb_mig_2", aliceID, "", "", 2)
	seedPair("dpb_mig_3", aliceID, "重复一号", "亚太专线", 3)
	seedPair("dpb_mig_4", aliceID, "重复二号", "亚太专线", 4)
	seedPair("dpb_mig_bob", bobID, "Bob 同名组", "亚太专线", 5)

	if _, err := db.pool.Exec(ctx, `
		INSERT INTO dashboard_probe_samples(id, pair_id, session_id, state, forward, error, sampled_at)
		VALUES ('dps_mig_1', 'dpb_mig_4', 'ndg_mig_4', 'succeeded', '{"state":"succeeded","avg_ms":18.5,"loss_percent":0,"reached":true}'::jsonb, '', now())`); err != nil {
		t.Fatal(err)
	}

	db.Close()
	migrated, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("reopen store for v105 migration: %v", err)
	}
	defer migrated.Close()

	var version int
	if err := migrated.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d (err=%v)", version, currentSchemaVersion, err)
	}

	// All 5 pairs and sample must survive intact, with non-empty unique group names per owner.
	rows, err := migrated.pool.Query(ctx, `SELECT id, owner_id, group_name FROM dashboard_probe_pairs ORDER BY created_at ASC, id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	groupsByID := map[string]string{}
	aliceGroups := map[string]bool{}
	for rows.Next() {
		var id, owner, g string
		if err := rows.Scan(&id, &owner, &g); err != nil {
			t.Fatal(err)
		}
		if g == "" {
			t.Fatalf("migrated pair %s still has empty group_name", id)
		}
		groupsByID[id] = g
		if owner == aliceID {
			if aliceGroups[g] {
				t.Fatalf("duplicate migrated group_name %q for alice", g)
			}
			aliceGroups[g] = true
		}
	}
	if len(groupsByID) != 5 {
		t.Fatalf("expected all 5 legacy pairs preserved, got %d (%+v)", len(groupsByID), groupsByID)
	}
	if groupsByID["dpb_mig_3"] != "亚太专线" {
		t.Fatalf("first duplicate owner pair should keep original group name, got %q", groupsByID["dpb_mig_3"])
	}
	if groupsByID["dpb_mig_4"] == "亚太专线" {
		t.Fatalf("second duplicate owner pair should receive distinct group name, got %q", groupsByID["dpb_mig_4"])
	}
	if groupsByID["dpb_mig_bob"] != "亚太专线" {
		t.Fatalf("other owner group with identical original name must remain untouched, got %q", groupsByID["dpb_mig_bob"])
	}

	var sampleCount int
	if err := migrated.pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_probe_samples WHERE id='dps_mig_1' AND pair_id='dpb_mig_4'`).Scan(&sampleCount); err != nil || sampleCount != 1 {
		t.Fatalf("legacy sample lost after migration: count=%d err=%v", sampleCount, err)
	}

	// Verify unique index rejects creating a duplicate group for alice while allowing bob's own namespace.
	aliceCtx := WithConfigScope(ctx, aliceID, false)
	_, err = migrated.CreateDashboardProbePair(aliceCtx, core.DashboardProbePairRequest{
		GroupName:     "亚太专线",
		Node1:         core.DiagnosticEndpoint{AgentID: agent1, Address: "192.0.2.1"},
		Node2:         core.DiagnosticEndpoint{AgentID: agent2, Address: "192.0.2.2"},
		Bidirectional: false,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict when creating duplicate group after migration, got %v", err)
	}
}
