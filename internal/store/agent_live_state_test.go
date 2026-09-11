package store

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// legacyWideAgentMetrics is the shape a v49 row stored in agents.metrics: the
// Agent-reported snapshot plus the control-plane observed address that used to
// share the same column.
const legacyWideAgentMetrics = `{
	"cpu_available": true,
	"cpu_percent": 41.5,
	"memory_available": true,
	"memory_total_bytes": 1000,
	"memory_used_bytes": 250,
	"network_available": true,
	"network_rx_bps": 111,
	"network_tx_bps": 222,
	"public_ipv4": "93.184.216.34",
	"observed_public_ip": "8.8.4.4",
	"network_interfaces": [{"name":"eth0","addresses":["9.9.9.9"]}]
}`

func seedLiveStateAgent(t *testing.T, dataStore *Store, name string) string {
	t.Helper()
	ctx := context.Background()
	agentID, err := core.NewID("agt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.pool.Exec(ctx, `INSERT INTO agents
		(id,name,os,arch,capabilities,public_key,last_seen,enrolled_at)
		VALUES ($1,$2,'linux','amd64','["mihomo"]',decode(repeat(md5($1),2),'hex'),now(),now())`, agentID, name); err != nil {
		t.Fatal(err)
	}
	return agentID
}

// TestAgentLiveStateSplitMigratesWideAgentRows covers the v50 upgrade on a
// database that still stores the full metrics snapshot inside the agents row.
//
// The split has to preserve two values that end up in different places: the
// Agent-reported snapshot, which moves to its own table, and the control-plane
// observed address, which used to live inside that snapshot but is written on
// its own schedule and so needs a column that a metrics push cannot overwrite.
func TestAgentLiveStateSplitMigratesWideAgentRows(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()

	// Rebuild the previous shape: a wide agents row carrying the snapshot, the
	// last_seen index, and the default fillfactor.
	if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS agent_live_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE agents ADD COLUMN IF NOT EXISTS metrics jsonb NOT NULL DEFAULT '{}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE agents DROP COLUMN IF EXISTS observed_public_ip`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE agents SET (fillfactor = 100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS agents_active_seen_idx ON agents(last_seen DESC) WHERE revoked_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	agentID := seedLiveStateAgent(t, s, "legacy wide row")
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET metrics=$2::jsonb WHERE id=$1`, agentID, legacyWideAgentMetrics); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations; INSERT INTO qcontrolhub_schema_migrations(version) VALUES (49)`); err != nil {
		t.Fatal(err)
	}

	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate to v50: %v", err)
	}

	assertNoColumn(t, s, "agents", "metrics")
	assertIndexExists(t, s, "agents_active_seen_idx", false)
	assertFillfactor(t, s, "agents", 70)
	assertFillfactor(t, s, "agent_live_state", 70)

	// The Agent-reported snapshot moved across intact.
	var snapshot []byte
	if err := s.pool.QueryRow(ctx, `SELECT metrics FROM agent_live_state WHERE agent_id=$1`, agentID).Scan(&snapshot); err != nil {
		t.Fatalf("read migrated live state: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["cpu_percent"] != 41.5 || decoded["public_ipv4"] != "93.184.216.34" {
		t.Fatalf("migrated snapshot lost Agent-reported fields: %v", decoded)
	}

	// The observed address survives in its own column and still reaches the
	// panel through the read path.
	var observed string
	if err := s.pool.QueryRow(ctx, `SELECT observed_public_ip FROM agents WHERE id=$1`, agentID).Scan(&observed); err != nil {
		t.Fatalf("read migrated observed address: %v", err)
	}
	if observed != "8.8.4.4" {
		t.Fatalf("observed address = %q, want 8.8.4.4", observed)
	}
	agent, err := s.GetAgent(ctx, agentID)
	if err != nil {
		t.Fatalf("read migrated agent: %v", err)
	}
	if agent.Metrics.ObservedPublicIP != "8.8.4.4" || agent.Metrics.PublicIPv4 != "93.184.216.34" {
		t.Fatalf("rebuilt metrics = observed %q ipv4 %q", agent.Metrics.ObservedPublicIP, agent.Metrics.PublicIPv4)
	}
	if agent.Metrics.CPUPercent != 41.5 {
		t.Fatalf("rebuilt metrics lost cpu percent: %v", agent.Metrics.CPUPercent)
	}
}

// TestDuplicateTrafficPolicyIndexIsRetired covers the v51 cleanup.
//
// The unique constraint on (agent_id, port) has the same leading columns, so the
// separate index only added maintenance on a table that is rewritten roughly
// once per second per policy. schemaSQL no longer creates it, and this checks
// that a database which still carries it loses it on upgrade.
func TestDuplicateTrafficPolicyIndexIsRetired(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()

	if _, err := s.pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS port_traffic_policies_agent_idx ON port_traffic_policies(agent_id,port)`); err != nil {
		t.Fatal(err)
	}
	assertIndexExists(t, s, "port_traffic_policies_agent_idx", true)
	if _, err := s.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations; INSERT INTO qcontrolhub_schema_migrations(version) VALUES (50)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate to v51: %v", err)
	}
	assertIndexExists(t, s, "port_traffic_policies_agent_idx", false)

	// The constraint that serves the same lookups has to remain, or the upgrade
	// would have traded a redundant index for a missing one.
	assertIndexExists(t, s, "port_traffic_policies_agent_id_port_key", true)
}

// TestAgentHeartbeatWithoutMetricsClearsProbes covers the branch a heartbeat
// takes when it carries no metrics: the Agent can no longer probe, so the
// addresses it probed earlier must stop being advertised, while the interface
// snapshot it reported is preserved.
func TestAgentHeartbeatWithoutMetricsClearsProbes(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID := seedLiveStateAgent(t, s, "probe fixture")
	if err := s.UpdateAgentMetrics(ctx, agentID, core.HostMetrics{
		CPUAvailable: true, CPUPercent: 5,
		PublicIPv4: "93.184.216.34", PublicIPv4Source: core.PublicIPProbeSourceAgent,
		NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"9.9.9.9"}}},
	}); err != nil {
		t.Fatalf("prime metrics: %v", err)
	}
	if err := s.Heartbeat(ctx, agentID, core.HeartbeatRequest{Version: "v1"}); err != nil {
		t.Fatalf("heartbeat without metrics: %v", err)
	}

	var stored []byte
	if err := s.pool.QueryRow(ctx, `SELECT metrics FROM agent_live_state WHERE agent_id=$1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("read live state: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(stored, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"public_ipv4", "public_ipv6", "public_ipv4_source", "public_ipv6_source"} {
		if _, present := decoded[key]; present {
			t.Fatalf("stale probe %q survived a metrics-less heartbeat: %v", key, decoded)
		}
	}
	if _, present := decoded["network_interfaces"]; !present {
		t.Fatalf("Agent-reported interface snapshot was dropped: %v", decoded)
	}
}

// TestAgentObservedPublicIPSurvivesMetricsPush covers the other reason the
// observed address has its own column: it is written when a WSS connection
// opens, and the per-second metrics push must not undo it.
func TestAgentObservedPublicIPSurvivesMetricsPush(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID := seedLiveStateAgent(t, s, "observed address fixture")
	if err := s.UpdateAgentObservedPublicIP(ctx, agentID, "8.8.4.4"); err != nil {
		t.Fatalf("record observed address: %v", err)
	}
	if err := s.UpdateAgentMetrics(ctx, agentID, core.HostMetrics{CPUAvailable: true, CPUPercent: 7}); err != nil {
		t.Fatalf("metrics push: %v", err)
	}
	agent, err := s.GetAgent(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Metrics.ObservedPublicIP != "8.8.4.4" {
		t.Fatalf("observed address after a metrics push = %q, want 8.8.4.4", agent.Metrics.ObservedPublicIP)
	}
	// Clearing has to work too: the resolver falls back to reported interfaces
	// once the observation is stale.
	if err := s.UpdateAgentObservedPublicIP(ctx, agentID, ""); err != nil {
		t.Fatalf("clear observed address: %v", err)
	}
	agent, err = s.GetAgent(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Metrics.ObservedPublicIP != "" {
		t.Fatalf("observed address after clearing = %q, want empty", agent.Metrics.ObservedPublicIP)
	}
}

type tableWriteStats struct{ updates, hot int64 }

// readTableWriteStats waits for the statistics collector to publish the writes
// a test just made. The collector flushes roughly every two seconds, so a
// single read races it.
func readTableWriteStats(t *testing.T, s *Store, table string) tableWriteStats {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var stats tableWriteStats
	for time.Now().Before(deadline) {
		if err := s.pool.QueryRow(context.Background(), `
			SELECT n_tup_upd, n_tup_hot_upd FROM pg_stat_user_tables WHERE relname=$1`, table).
			Scan(&stats.updates, &stats.hot); err != nil {
			t.Fatalf("read write stats for %s: %v", table, err)
		}
		if stats.updates > 0 {
			return stats
		}
		time.Sleep(200 * time.Millisecond)
	}
	return stats
}

func assertNoColumn(t *testing.T, s *Store, table, column string) {
	t.Helper()
	var exists bool
	if err := s.pool.QueryRow(context.Background(), `
		SELECT EXISTS(
			SELECT 1 FROM pg_attribute
			WHERE attrelid = to_regclass($1) AND attname = $2 AND NOT attisdropped
		)`, table, column).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("%s.%s still exists after the split", table, column)
	}
}

func assertIndexExists(t *testing.T, s *Store, index string, want bool) {
	t.Helper()
	var exists bool
	if err := s.pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != want {
		t.Fatalf("index %s exists = %v, want %v", index, exists, want)
	}
}

func assertFillfactor(t *testing.T, s *Store, table string, want int) {
	t.Helper()
	var options []string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT reloptions FROM pg_class WHERE oid = to_regclass($1)`, table).Scan(&options); err != nil {
		t.Fatalf("read reloptions for %s: %v", table, err)
	}
	wantOption := "fillfactor=" + strconv.Itoa(want)
	for _, option := range options {
		if option == wantOption {
			return
		}
	}
	t.Fatalf("%s reloptions = %v, want %s", table, options, wantOption)
}
