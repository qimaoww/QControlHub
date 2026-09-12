package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// This file pins the storage-level properties the panel's throughput depends
// on. Each of them was established by measurement (see docs/performance.md) and
// each of them regresses silently: the queries keep returning correct rows
// while doing hundreds of extra page reads, or a hot update quietly stops being
// a heap-only update and starts rewriting every index that points at the row.
//
// They are asserted as database invariants rather than as timings on purpose. A
// wall-clock threshold on shared CI runners is noise, while a plan shape or a
// heap-fetch count is deterministic.

// TestCoreLogWindowStaysIndexOnly covers the fix for the log page hanging on
// "loading logs" on a 2 GiB instance.
//
// Every column the window selects is carried by
// core_logs_agent_engine_covering_idx. If a column is added to the projection or
// the index stops covering it, the plan degrades to an index scan plus one
// random heap fetch per row: at a measured 7.2 ms per page, a 2000-row window
// becomes roughly 14 seconds of pure I/O wait instead of an index-only read.
func TestCoreLogWindowStaysIndexOnly(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID, _ := seedPerformanceAgent(t, s, 0)

	// Enough rows that the planner has a real choice between the covering index
	// and a sequential scan.
	now := time.Now().UTC().Truncate(time.Microsecond)
	for batchIndex := range 40 {
		entries := make([]core.CoreLogEntry, 0, core.MaxCoreLogBatchEntries)
		for entryIndex := range core.MaxCoreLogBatchEntries {
			entries = append(entries, core.CoreLogEntry{
				Engine:   core.EngineMihomo,
				Level:    "info",
				Message:  fmt.Sprintf("window fixture %d/%d", batchIndex, entryIndex),
				LoggedAt: now.Add(-time.Duration(batchIndex) * time.Second),
			})
		}
		batch := core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", batchIndex), Entries: entries}
		if err := s.StoreCoreLogs(ctx, agentID, batch); err != nil {
			t.Fatalf("seed core logs: %v", err)
		}
	}
	// VACUUM keeps the visibility map current, which is what lets an index-only
	// scan skip the heap. Without it the assertion below would fail for a reason
	// that has nothing to do with the schema.
	if _, err := s.pool.Exec(ctx, `VACUUM (ANALYZE) core_logs`); err != nil {
		t.Fatal(err)
	}

	// Run the store's own query, then plan the same SQL the same way with
	// EXPLAIN ANALYZE so the actual heap-fetch count is observable.
	entries, err := s.ListCoreLogs(ctx, CoreLogQuery{AgentID: agentID, Limit: 2000})
	if err != nil {
		t.Fatalf("list core logs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("window query returned no rows; the fixture did not seed")
	}

	statement, args := coreLogWindowStatement(CoreLogQuery{AgentID: agentID, Limit: 2000},
		[]string{string(core.EngineMihomo), string(core.EngineXray), string(core.EngineSingBox), string(core.EngineShadowsocksRust)})
	// The fixture is far smaller than a real window, so a cost-based planner
	// would pick a sequential scan on page count alone and this test would
	// measure the fixture instead of the schema. Disabling sequential scans
	// forces the index path, which is what has to stay able to serve the window
	// without touching the heap.
	plan := explainJSONOnDedicatedConnection(t, s, statement, args...)
	if !planUsesIndexOnlyScan(plan) {
		t.Fatalf("log window plan is not an index-only scan: %s", planSummary(plan))
	}
	if fetches, ok := planHeapFetches(plan); !ok {
		t.Fatalf("log window plan reported no heap fetch count: %s", planSummary(plan))
	} else if fetches != 0 {
		t.Fatalf("log window performed %.0f heap fetches; the covering index no longer serves the window: %s", fetches, planSummary(plan))
	}
}

// TestTrafficUsageUpdatesStayHeapOnly covers the accounting path, which is the
// highest-frequency write in the panel: every policy reports roughly once per
// second.
//
// Those tables were rewritten without bound at the default fillfactor, because
// a full page forces each new row version onto a fresh page. Reserving page
// space is what keeps the storage bounded, so a regression here would not fail
// any correctness test while the tables grew without limit.
func TestTrafficUsageUpdatesStayHeapOnly(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID, usages := seedPerformanceAgent(t, s, 25)
	// Autovacuum is disabled for the duration of the measurement: the database
	// is shared with tests writing to their own tables, and a vacuum that
	// reclaims pages under this one would move the size it is measured against.
	// This table is private to this schema.
	if _, err := s.pool.Exec(ctx, `ALTER TABLE port_traffic_policies SET (autovacuum_enabled = false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `VACUUM (FULL, ANALYZE) port_traffic_policies`); err != nil {
		t.Fatal(err)
	}
	// A slow remote sync may hold its ownership lock throughout these reports.
	// The lock must not pin a database transaction or prevent page reuse.
	release, err := s.TryLockSubStoreOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Storage growth is the assertion rather than the heap-only counter from
	// pg_stat_user_tables. Those counters belong to the whole database and other
	// tests running against it reset them, which made this test fail only in a
	// full-suite run. Relation size is exact and private to this schema.
	//
	// Without reserved page space every one of these reports lands at the end of
	// a page, splits it, and a 25-row table grows without bound. With it, the new
	// row version goes into space the same page already had.
	before := relationSize(t, s, "port_traffic_policies")

	// Each report has to carry a timestamp newer than the stored baseline, and
	// this fixture reports far faster than a real Agent, so the clock is
	// advanced explicitly instead of relying on wall time between iterations.
	const reports = 200
	reportedAt := time.Now().UTC()
	for report := range reports {
		reportedAt = reportedAt.Add(time.Second)
		// Monotonic counters, so every report is accepted and actually writes.
		increment := uint64(report + 1)
		next := make([]core.PortTrafficUsage, 0, len(usages))
		for _, usage := range usages {
			updated := usage
			updated.ReceivedBytes = usage.ReceivedBytes + increment
			updated.UsedBytes = updated.ReceivedBytes
			updated.ReceiveBPS = increment
			next = append(next, updated)
		}
		if err := s.UpdatePortTrafficUsage(ctx, agentID, next, reportedAt); err != nil {
			t.Fatalf("traffic report %d: %v", report, err)
		}
	}

	// The writes have to have landed, otherwise the size assertion is vacuous.
	var received int64
	if err := s.pool.QueryRow(ctx, `
		SELECT max(received_bytes) FROM port_traffic_policies WHERE agent_id=$1`, agentID).Scan(&received); err != nil {
		t.Fatal(err)
	}
	if received != int64(100+reports) {
		t.Fatalf("stored received_bytes = %d, want %d; the reports did not reach the table", received, 100+reports)
	}

	after := relationSize(t, s, "port_traffic_policies")
	// 200 accepted reports across 25 policies is 5000 row versions. Even a
	// couple of page splits would stay under this bound, while losing reserved
	// page space grows the table by orders of magnitude more.
	const growthBudget = 256 << 10
	if growth := after - before; growth > growthBudget {
		t.Fatalf("port_traffic_policies grew by %d bytes over %d reports; hot updates are no longer staying on-page", growth, reports)
	}
}

func relationSize(t *testing.T, s *Store, table string) int64 {
	t.Helper()
	var size int64
	if err := s.pool.QueryRow(context.Background(), `SELECT pg_relation_size(to_regclass($1))`, table).Scan(&size); err != nil {
		t.Fatalf("read relation size for %s: %v", table, err)
	}
	return size
}

// TestAgentWritePathKeepsReservedPageSpace covers the settings that make the
// agent write path work, including after an upgrade.
//
// A database restored from an older dump can carry the previous fillfactors and
// the last_seen index back in, which restores the original write amplification
// without breaking a single query result.
func TestAgentWritePathKeepsReservedPageSpace(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()

	for _, table := range []string{"agents", "agent_live_state"} {
		var fillfactor string
		if err := s.pool.QueryRow(ctx, `
			SELECT COALESCE(array_to_string(reloptions,','),'')
			FROM pg_class WHERE oid = to_regclass($1)`, table).Scan(&fillfactor); err != nil {
			t.Fatalf("read reloptions for %s: %v", table, err)
		}
		if !strings.Contains(fillfactor, "fillfactor=70") {
			t.Fatalf("%s reloptions = %q, want fillfactor=70", table, fillfactor)
		}
	}

	// The last_seen index is the reason a heartbeat could never be a heap-only
	// update, so its absence is part of the contract rather than an oversight.
	assertIndexExists(t, s, "agents_active_seen_idx", false)

	// The agents row must no longer carry the snapshot.
	assertNoColumn(t, s, "agents", "metrics")

	// And the read path must still assemble both halves.
	agentID := seedLiveStateAgent(t, s, "invariant fixture")
	if err := s.UpdateAgentMetrics(ctx, agentID, core.HostMetrics{
		CPUAvailable: true, CPUPercent: 33,
		NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"9.9.9.9"}}},
	}); err != nil {
		t.Fatalf("metrics push: %v", err)
	}
	if err := s.UpdateAgentObservedPublicIP(ctx, agentID, "8.8.4.4"); err != nil {
		t.Fatalf("observed address: %v", err)
	}
	agent, err := s.GetAgent(ctx, agentID)
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if agent.Metrics.CPUPercent != 33 || agent.Metrics.ObservedPublicIP != "8.8.4.4" || len(agent.Metrics.NetworkInterfaces) != 1 {
		t.Fatalf("assembled metrics = %+v", agent.Metrics)
	}
}

// planSnapshot is the subset of an EXPLAIN (FORMAT JSON) document these tests
// inspect. Node types are compared case-insensitively so the assertions keep
// working across PostgreSQL releases that restyle the names.
type planSnapshot struct {
	Plan planNode `json:"Plan"`
}

type planNode struct {
	NodeType         string     `json:"Node Type"`
	RelationName     string     `json:"Relation Name"`
	IndexName        string     `json:"Index Name"`
	HeapFetches      *float64   `json:"Heap Fetches"`
	SharedHitBlocks  float64    `json:"Shared Hit Blocks"`
	SharedReadBlocks float64    `json:"Shared Read Blocks"`
	Plans            []planNode `json:"Plans"`
}

// explainJSONOnDedicatedConnection plans a statement on a connection of its own
// so the planner settings it applies cannot leak into the pool other tests
// share.
func explainJSONOnDedicatedConnection(t *testing.T, s *Store, statement string, args ...any) planSnapshot {
	t.Helper()
	ctx := context.Background()
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire planning connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatalf("disable sequential scans: %v", err)
	}
	var raw []byte
	if err := connection.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement, args...).Scan(&raw); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var decoded []planSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("explain returned no plan")
	}
	return decoded[0]
}

// readTableWriteStatsSettled waits until the collector reports at least want
// more updates than the baseline and the counters stop moving, so the returned
// total covers the whole series and not a partially flushed batch.
func readTableWriteStatsSettled(t *testing.T, s *Store, table string, want int64, baseline tableWriteStats) tableWriteStats {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	previous := baseline
	for time.Now().Before(deadline) {
		stats := readTableWriteStats(t, s, table)
		if stats.updates-baseline.updates >= want && stats.updates == previous.updates {
			return stats
		}
		previous = stats
		time.Sleep(250 * time.Millisecond)
	}
	stats := readTableWriteStats(t, s, table)
	t.Fatalf("%s reported %d updates (%d above baseline), want at least %d", table, stats.updates, stats.updates-baseline.updates, want)
	return stats
}

func explainJSON(t *testing.T, s *Store, statement string, args ...any) planSnapshot {
	t.Helper()
	var raw []byte
	explain := "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + statement
	if err := s.pool.QueryRow(context.Background(), explain, args...).Scan(&raw); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var decoded []planSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("explain returned no plan")
	}
	return decoded[0]
}

func walkPlan(node planNode, visit func(planNode) bool) bool {
	if visit(node) {
		return true
	}
	for _, child := range node.Plans {
		if walkPlan(child, visit) {
			return true
		}
	}
	return false
}

func planUsesIndexOnlyScan(plan planSnapshot) bool {
	return walkPlan(plan.Plan, func(node planNode) bool {
		return strings.EqualFold(node.NodeType, "Index Only Scan")
	})
}

func planHeapFetches(plan planSnapshot) (float64, bool) {
	total := 0.0
	found := false
	walkPlan(plan.Plan, func(node planNode) bool {
		if node.HeapFetches != nil {
			total += *node.HeapFetches
			found = true
		}
		return false
	})
	return total, found
}

// planSharedBlocks totals the buffer traffic of a plan. It is the signal that
// separates a bounded sweep from one that probes per candidate row, and unlike a
// wall-clock measurement it does not depend on how busy the runner is.
func planSharedBlocks(plan planSnapshot) int64 {
	total := 0.0
	walkPlan(plan.Plan, func(node planNode) bool {
		total += node.SharedHitBlocks + node.SharedReadBlocks
		return false
	})
	return int64(total)
}

// planNodeTypes reports whether the plan contains a nested loop.
func planNodeTypes(plan planSnapshot) ([]string, bool) {
	types := make([]string, 0, 8)
	nested := false
	walkPlan(plan.Plan, func(node planNode) bool {
		types = append(types, node.NodeType)
		if strings.Contains(node.NodeType, "Nested Loop") {
			nested = true
		}
		return false
	})
	return types, nested
}

func planSummary(plan planSnapshot) string {
	nodes := make([]string, 0, 8)
	walkPlan(plan.Plan, func(node planNode) bool {
		label := node.NodeType
		if node.IndexName != "" {
			label += " on " + node.IndexName
		} else if node.RelationName != "" {
			label += " on " + node.RelationName
		}
		nodes = append(nodes, label)
		return false
	})
	return strings.Join(nodes, " -> ")
}

// TestCoreLogBatchPruneAvoidsNestedLoopProbe covers the hourly batch-marker
// sweep.
//
// The planner turns the anti-join into a hash right anti join: one indexed scan
// of the expired markers against one scan of the retained rows. A correlated
// shape that probes core_logs once per candidate marker would still return
// correct rows, so the plan is what has to be asserted. That form is exactly
// what a nested loop in this plan would mean.
//
// The block count is deliberately not pinned: the retained side is a full scan
// of the log partitions, so its cost follows the retention window and the ingest
// rate rather than the statement. The first pass after a PostgreSQL restart also
// reads those pages from disk, which on a live instance made a single pass look
// like 7.1 seconds while a warm pass of the same statement takes 89 ms.
func TestCoreLogBatchPruneAvoidsNestedLoopProbe(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID, _ := seedPerformanceAgent(t, s, 0)

	const batches = 200
	now := time.Now().UTC().Truncate(time.Microsecond)
	for index := range batches {
		batch := core.CoreLogBatch{
			ID: fmt.Sprintf("log_%016x", index),
			Entries: []core.CoreLogEntry{{
				Engine: core.EngineMihomo, Level: "info",
				Message:  fmt.Sprintf("prune fixture %d", index),
				LoggedAt: now,
			}},
		}
		if err := s.StoreCoreLogs(ctx, agentID, batch); err != nil {
			t.Fatalf("seed batch %d: %v", index, err)
		}
	}
	if _, err := s.pool.Exec(ctx, `VACUUM (ANALYZE) core_logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `VACUUM (ANALYZE) core_log_batches`); err != nil {
		t.Fatal(err)
	}

	// Every row is still retained, so the sweep has to retire nothing while
	// still evaluating every candidate.
	retired, err := s.PruneCoreLogBatches(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if retired != 0 {
		t.Fatalf("pruned %d batches while every row is still retained", retired)
	}

	plan := explainJSON(t, s, coreLogBatchPruneSQL, now.Add(time.Hour))
	if nodeTypes, loops := planNodeTypes(plan); loops {
		t.Fatalf("batch prune plan contains a nested loop, so it may be probing per candidate marker: %s", planSummary(plan))
	} else if len(nodeTypes) == 0 {
		t.Fatalf("batch prune plan is empty: %s", planSummary(plan))
	}
}
