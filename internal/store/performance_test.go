package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type queryBudget struct{ queries, batches, statements atomic.Int64 }

func (trace *queryBudget) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	trace.queries.Add(1)
	return ctx
}
func (*queryBudget) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (trace *queryBudget) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	trace.batches.Add(1)
	return ctx
}
func (trace *queryBudget) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {
	trace.statements.Add(1)
}
func (*queryBudget) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func TestRemoteDatabaseQueryBudgets(t *testing.T) {
	base := openPerformanceStore(t)
	agentID, usages := seedPerformanceAgent(t, base, 64)
	ctx := context.Background()
	trace := &queryBudget{}
	config := base.pool.Config()
	config.ConnConfig.Tracer = trace
	config.ShouldPing = func(context.Context, pgxpool.ShouldPingParams) bool { return false }
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	measured := &Store{pool: pool, cryptor: base.cryptor}
	entries := make([]core.CoreLogEntry, core.MaxCoreLogBatchEntries)
	for index := range entries {
		entries[index] = core.CoreLogEntry{Engine: core.EngineMihomo, Level: "warning", Message: "budget test"}
	}
	for _, test := range []struct {
		name             string
		queries, batches int64
		run              func() error
	}{
		{"logs32", 5, 0, func() error {
			return measured.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: "log_0000000000000001", Entries: entries})
		}},
		{"traffic64", 4, 0, func() error { return measured.UpdatePortTrafficUsage(ctx, agentID, usages, time.Now()) }},
		{"idle task", 1, 0, func() error { _, err := measured.ClaimTask(ctx, agentID); return err }},
		{"presence", 1, 0, func() error { _, err := measured.AgentPresenceTransitions(ctx, time.Now(), 45*time.Second); return err }},
		{"quota", 1, 0, func() error { _, err := measured.ClaimTrafficQuotaTransitions(ctx); return err }},
		{"panel agents", 0, 1, func() error { _, err := measured.ListAgentsWithEnrollmentCommands(ctx); return err }},
		{"deployed configs", 1, 0, func() error { _, err := measured.DeployedConfigs(ctx); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			trace.queries.Store(0)
			trace.batches.Store(0)
			if err := test.run(); err != nil {
				t.Fatal(err)
			}
			if trace.queries.Load() != test.queries || trace.batches.Load() != test.batches {
				t.Fatalf("queries/batches = %d/%d, want %d/%d", trace.queries.Load(), trace.batches.Load(), test.queries, test.batches)
			}
		})
	}
}

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

func TestDeployedConfigsResolveExactVersionsAndMetadata(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, _ := seedPerformanceAgent(t, s, 0)
	ctx := context.Background()
	input := core.Config{AgentID: agentID, Name: "deployed fixture", Engine: core.EngineMihomo, Content: "mixed-port: 7890\n"}
	first, err := s.SaveAgentConfigWithClientMetadata(ctx, input, 0, ConfigClientMetadataMutation{Tag: "inbound", Content: `{"secret":"first"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO tasks (id,agent_id,action,engine,config_id,config_version,status,created_at,finished_at)
		VALUES ('tsk_fixture',$1,'deploy','mihomo',$2,1,'succeeded',now(),now())`, agentID, first.ID); err != nil {
		t.Fatal(err)
	}
	input.Content = "mixed-port: 7891\n"
	if _, err := s.SaveAgentConfigWithClientMetadata(ctx, input, 1, ConfigClientMetadataMutation{Tag: "inbound", Content: `{"secret":"second"}`}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeployedConfigs(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("deployed = %+v, %v", got, err)
	}
	if got[0].Config.Version != 1 || got[0].Config.Content != first.Content || got[0].Metadata["inbound"] != `{"secret":"first"}` {
		t.Fatalf("mixed saved/deployed versions: %+v", got[0])
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1 AND version=1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeployedConfigs(ctx); err != nil || len(got) != 0 {
		t.Fatalf("pruned revision exposed: %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET config_version=2 WHERE id='tsk_fixture'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeployedConfigs(ctx); err != nil || len(got) != 1 || got[0].Config.Content != input.Content {
		t.Fatalf("current version = %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeployedConfigs(ctx); err != nil || len(got) != 0 {
		t.Fatalf("deleted config exposed: %+v, %v", got, err)
	}
}

func TestAgentConfigsDoNotReadOtherNodes(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	first, _ := seedPerformanceAgent(t, s, 0)
	second, _ := seedPerformanceAgent(t, s, 0)
	input := core.Config{AgentID: second, Name: "other node", Engine: core.EngineMihomo, Content: "mixed-port: 7890\n"}
	other, err := s.SaveAgentConfig(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated, undecryptable body must not break this node's workspace.
	if _, err := s.pool.Exec(ctx, `UPDATE configs SET content='rf2:corrupt' WHERE id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AgentConfigs(ctx, first); err != nil || len(got) != 0 {
		t.Fatalf("empty node = %+v, %v", got, err)
	}
	input.AgentID = first
	want, err := s.SaveAgentConfig(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.AgentConfigs(ctx, first); err != nil || len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("filtered node = %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentConfigs(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked node = %v", err)
	}
	if _, err := s.AgentConfigs(ctx, "agt_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing node = %v", err)
	}
}

func TestBatchedEnrollmentAvailabilityVerifiesSecrets(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agentID, _ := seedPerformanceAgent(t, s, 0)
	if _, err := s.CreateAgentEnrollmentToken(ctx, agentID); err != nil {
		t.Fatal(err)
	}
	assertAvailable := func(reader *Store, want bool) {
		t.Helper()
		agents, err := reader.ListAgentsWithEnrollmentCommands(ctx)
		if err != nil || len(agents) != 1 || agents[0].EnrollmentCommandAvailable != want {
			t.Fatalf("availability=%+v, %v; want %v", agents, err, want)
		}
	}
	assertAvailable(s, true)
	assertAvailable(&Store{pool: s.pool}, false)
	if _, err := s.pool.Exec(ctx, `UPDATE enrollment_tokens SET token_ciphertext='rf2:corrupt' WHERE agent_id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	assertAvailable(s, false)
}

func TestPerformanceIndexesMigrateFromV40(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	// The v49 migration converts core_logs into a table partitioned by receive
	// day. Rebuild a v48 heap table (it was replaced by the running migration)
	// with the three indexes that version shipped.
	if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS core_logs CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS core_logs_legacy_19700101000000 CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE TABLE core_logs (
		id bigserial PRIMARY KEY,
		batch_id text NOT NULL REFERENCES core_log_batches(id) ON DELETE CASCADE,
		entry_index smallint NOT NULL CHECK (entry_index >= 0),
		agent_id text NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
		engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
		level varchar(10) NOT NULL CHECK (level IN ('debug','info','warning','error','critical')),
		message text NOT NULL CHECK (octet_length(message) BETWEEN 1 AND 4096),
		logged_at timestamptz NOT NULL,
		received_at timestamptz NOT NULL,
		UNIQUE (batch_id,entry_index))`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE INDEX core_logs_agent_engine_recent_idx ON core_logs(agent_id,engine,id DESC)`,
		`CREATE INDEX core_logs_agent_recent_idx ON core_logs(agent_id,id DESC)`,
		`CREATE INDEX core_logs_engine_recent_idx ON core_logs(engine,id DESC)`,
	} {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	// Other performance indexes from that era must also be recreated.
	expected := []string{"tasks_status_created_idx", "tasks_agent_created_idx", "tasks_retention_idx", "metric_samples_retention_idx", "port_traffic_daily_date_idx"}
	for _, index := range expected {
		if _, err := s.pool.Exec(ctx, "DROP INDEX "+pgx.Identifier{index}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations; INSERT INTO qcontrolhub_schema_migrations(version) VALUES (48)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, index := range expected {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
	// The heap table is moved aside instead of migrated in place, and the new
	// core_logs must be the partitioned replacement. The set-aside copy carries
	// the timestamp its cleanup uses to decide when to drop it.
	var partitioned bool
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(bool_or(relkind = 'p'), false)
		FROM pg_class class JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
		WHERE namespace.nspname = current_schema() AND relname = 'core_logs'`).Scan(&partitioned); err != nil {
		t.Fatal(err)
	}
	if !partitioned {
		t.Fatal("core_logs was not converted to a partitioned table")
	}
	var legacyName string
	if err := s.pool.QueryRow(ctx, `
		SELECT class.relname
		FROM pg_class class JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
		WHERE namespace.nspname = current_schema() AND class.relname LIKE $1
		ORDER BY class.relname DESC LIMIT 1`, legacyCoreLogsPrefix+"%").Scan(&legacyName); err != nil {
		t.Fatalf("previous heap table was not set aside under %s<timestamp>: %v", legacyCoreLogsPrefix, err)
	}
	// The timestamp suffix is what lets the maintenance loop age the copy out.
	renamedAt, err := legacyCoreLogRenamedAt(legacyName)
	if err != nil {
		t.Fatalf("legacy copy %s has no readable timestamp: %v", legacyName, err)
	}
	if age := time.Since(renamedAt); age < 0 || age > time.Hour {
		t.Fatalf("legacy copy %s timestamp is not recent: %s", legacyName, age)
	}
	// Dropping it must wait for the grace period, then succeed.
	if dropped, err := s.DropLegacyCoreLogs(ctx, time.Now().UTC()); err != nil || dropped != 0 {
		t.Fatalf("legacy copy dropped before its grace period: dropped=%d err=%v", dropped, err)
	}
	dropped, err := s.DropLegacyCoreLogs(ctx, time.Now().UTC().Add(legacyCoreLogsGraceDays*24*time.Hour))
	if err != nil || dropped != 1 {
		t.Fatalf("legacy copy was not dropped after the grace period: dropped=%d err=%v", dropped, err)
	}
	var legacyGone bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, legacyName).Scan(&legacyGone); err != nil || !legacyGone {
		t.Fatalf("legacy copy %s still present: %v", legacyName, err)
	}
	// The log read path is served by the partition-level covering index; the
	// single-column recency indexes only added write cost per inserted row.
	for _, index := range []string{"core_logs_agent_engine_recent_idx", "core_logs_agent_recent_idx", "core_logs_engine_recent_idx"} {
		var revoked bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, index).Scan(&revoked); err != nil || !revoked {
			t.Fatalf("superseded index %s still present: %v", index, err)
		}
	}
	// Partitions must exist, otherwise the very first insert fails.
	if err := s.EnsureCoreLogPartitions(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var partitionCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_inherits
		JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
		JOIN pg_class child ON child.oid = pg_inherits.inhrelid
		WHERE parent.relname = 'core_logs' AND child.relname LIKE 'core_logs_p%'`).Scan(&partitionCount); err != nil {
		t.Fatal(err)
	}
	if partitionCount == 0 {
		t.Fatal("no core log partitions were created")
	}
	// The replacement must actually carry the payload columns, otherwise the log
	// window silently falls back to random heap fetches. PostgreSQL renames
	// the per-partition index, so match it through the index inheritance tree
	// rather than by name.
	var coveringIndexes int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_inherits
		JOIN pg_class child ON child.oid = pg_inherits.inhrelid
		JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
		JOIN pg_class partition ON partition.oid = (SELECT indrelid FROM pg_index WHERE indexrelid = child.oid)
		WHERE parent.relname = 'core_logs_agent_engine_covering_idx'
		  AND partition.relname LIKE 'core_logs_p%'`).Scan(&coveringIndexes); err != nil {
		t.Fatal(err)
	}
	if coveringIndexes == 0 {
		t.Fatal("partition-level covering index was not created")
	}
	included := []string{"level", "message", "logged_at", "received_at"}
	for _, column := range included {
		var present bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_index index
				JOIN pg_attribute attribute ON attribute.attrelid = index.indexrelid AND attribute.attnum > 0
				JOIN pg_inherits ON pg_inherits.inhrelid = index.indexrelid
				JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
				JOIN pg_class partition ON partition.oid = index.indrelid
				WHERE parent.relname = 'core_logs_agent_engine_covering_idx'
				  AND partition.relname LIKE 'core_logs_p%'
				  AND attribute.attname = $1
			)`, column).Scan(&present); err != nil || !present {
			t.Fatalf("covering index is missing column %s: %v", column, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

// Traffic accounting rewrites the same rows on every agent report, so without
// reserved page space each new row version lands at the end of a page, the page
// splits once it fills, and the table grows without bound. Pin the storage
// parameters that keep those updates on-page.
func TestTrafficTablesReserveSpaceForHotUpdates(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	expected := map[string]string{
		"port_traffic_policies":          "75",
		"port_traffic_daily_usage":       "85",
		"port_traffic_daily_accounting":  "85",
		"port_traffic_accounting_epochs": "85",
	}
	for table, want := range expected {
		var options []string
		if err := s.pool.QueryRow(ctx, `
			SELECT COALESCE(reloptions, '{}')
			FROM pg_class
			WHERE oid = to_regclass($1)`, table).Scan(&options); err != nil {
			t.Fatalf("read storage options for %s: %v", table, err)
		}
		found := ""
		for _, option := range options {
			if value, ok := strings.CutPrefix(option, "fillfactor="); ok {
				found = value
			}
		}
		if found != want {
			t.Fatalf("%s fillfactor=%q, want %q (options: %v)", table, found, want, options)
		}
	}
}
func TestCoreLogsLargeWindowsPreserveEveryEngine(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, _ := seedPerformanceAgent(t, s, 0)
	ctx := context.Background()
	engines := []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust}
	for _, engine := range engines {
		for offset := 0; offset < 2000; offset += core.MaxCoreLogBatchEntries {
			entries := make([]core.CoreLogEntry, min(core.MaxCoreLogBatchEntries, 2000-offset))
			for index := range entries {
				entries[index] = core.CoreLogEntry{Engine: engine, Level: "warning", Message: "large-window fixture"}
			}
			id, err := core.NewID("log")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, limit := range []int{0, 1000, 2000} {
		got, err := s.ListCoreLogs(ctx, CoreLogQuery{AgentID: agentID, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = 1000
		}
		if len(got) != 4*want {
			t.Fatalf("limit %d returned %d rows, want %d", limit, len(got), 4*want)
		}
		counts := map[core.Engine]int{}
		for index, entry := range got {
			counts[entry.Engine]++
			if index > 0 && entry.ID >= got[index-1].ID {
				t.Fatal("large result is not strictly newest first")
			}
		}
		for _, engine := range engines {
			if counts[engine] != want {
				t.Fatalf("%s count = %d, want %d", engine, counts[engine], want)
			}
		}
	}
	if _, err := s.ListCoreLogs(ctx, CoreLogQuery{Limit: 2001}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded query accepted: %v", err)
	}
}

func TestQuotaClaimsAreAtomicAcrossMonitors(t *testing.T) {
	s := openPerformanceStore(t)
	_, usages := seedPerformanceAgent(t, s, 3)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE port_traffic_policies SET blocked=true`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var count atomic.Int64
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			got, err := s.ClaimTrafficQuotaTransitions(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			for _, item := range got {
				if !item.Policy.Blocked || item.Policy.ResetGeneration != 1 {
					t.Errorf("invalid quota claim: %+v", item)
				}
			}
			count.Add(int64(len(got)))
		}()
	}
	group.Wait()
	if count.Load() != 3 {
		t.Fatalf("quota claims=%d, want 3", count.Load())
	}
	if _, err := s.pool.Exec(ctx, `UPDATE port_traffic_policies SET reset_generation=reset_generation+1 WHERE id=$1`, usages[0].PolicyID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClaimTrafficQuotaTransitions(ctx)
	if err != nil || len(got) != 1 || got[0].Policy.ResetGeneration != 2 {
		t.Fatalf("next generation: %+v, %v", got, err)
	}
}

func TestLocalTrafficBatchStatementBudget(t *testing.T) {
	base := openPerformanceStore(t)
	agentID, usages := seedPerformanceAgent(t, base, 256)
	ctx := context.Background()
	trace := &queryBudget{}
	config := base.pool.Config()
	config.MaxConns, config.MinConns = 1, 0
	config.ConnConfig.Tracer = trace
	config.ShouldPing = func(context.Context, pgxpool.ShouldPingParams) bool { return false }
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	measured := &Store{pool: pool, cryptor: base.cryptor}
	// UTC bucketing must not depend on the database session's timezone.
	if _, err := pool.Exec(ctx, `SET TIME ZONE 'America/Los_Angeles'`); err != nil {
		t.Fatal(err)
	}
	trace.queries.Store(0)
	now := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	if err := measured.UpdatePortTrafficUsage(ctx, agentID, usages, now); err != nil {
		t.Fatal(err)
	}
	if got := trace.queries.Load() + trace.statements.Load(); got != 4 {
		t.Fatalf("256 ports executed %d statements, want BEGIN/read/set-write/COMMIT", got)
	}
	daily, err := base.ListPortTrafficDailyUsage(ctx, agentID, "", now)
	if err != nil || len(daily) != 256 {
		t.Fatalf("daily rows=%d, %v", len(daily), err)
	}
	for _, item := range daily {
		if item.Day != "2026-09-06" || item.UsedBytes != 100 || item.SampleCount != 1 {
			t.Fatalf("wrong daily sample: %+v", item)
		}
	}
}

func TestLocalLatestDeploymentPlanIsBounded(t *testing.T) {
	s := seedDeploymentHistory(t)
	ctx := context.Background()
	var encoded []byte
	if err := s.pool.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) `+latestDeploymentsSQL).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	type planNode struct {
		Relation string            `json:"Relation Name"`
		Index    string            `json:"Index Name"`
		Rows     float64           `json:"Actual Rows"`
		Loops    float64           `json:"Actual Loops"`
		Filtered float64           `json:"Rows Removed by Filter"`
		Plans    []json.RawMessage `json:"Plans"`
	}
	var explain []struct {
		Plan        json.RawMessage `json:"Plan"`
		ExecutionMS float64         `json:"Execution Time"`
	}
	if err := json.Unmarshal(encoded, &explain); err != nil || len(explain) != 1 {
		t.Fatalf("explain result: %v", err)
	}
	var examined float64
	examinedByRelation := map[string]float64{}
	var walk func(json.RawMessage)
	walk = func(raw json.RawMessage) {
		var node planNode
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		examinedByRelation[node.Relation] += (node.Rows + node.Filtered) * node.Loops
		if node.Relation == "tasks" {
			if node.Index != "tasks_latest_deployment_idx" {
				t.Errorf("unexpected task access: %s", node.Index)
			}
			examined += (node.Rows + node.Filtered) * node.Loops
		}
		for _, child := range node.Plans {
			walk(child)
		}
	}
	walk(explain[0].Plan)
	if examined != 800 {
		t.Fatalf("read %.0f task rows for 800 services; history has 200000 rows", examined)
	}
	t.Logf("200000 deployments -> %.0f task rows visited, execution %.3fms", examined, explain[0].ExecutionMS)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO configs(id,agent_id,name,description,engine,content,version,created_at,updated_at)
		SELECT 'cfg_history_'||n,'agt_history_'||n,'fixture','','mihomo',E'mixed-port: 7890\n',1,now(),now()
		FROM generate_series(1,200) n;
		INSERT INTO config_revisions(config_id,version,agent_id,name,description,engine,content,created_at)
		SELECT id,version,agent_id,name,description,engine,content,updated_at FROM configs;
		UPDATE tasks SET config_id=replace(agent_id,'agt_history_','cfg_history_'),config_version=1
		WHERE id IN (SELECT 'tsk_history_'||n FROM generate_series(1,200) n);
		ANALYZE configs; ANALYZE config_revisions;`); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) `+deployedConfigsSQL).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &explain); err != nil {
		t.Fatal(err)
	}
	examined = 0
	clear(examinedByRelation)
	walk(explain[0].Plan)
	for _, relation := range []string{"configs", "config_revisions"} {
		if examinedByRelation[relation] > 1000 {
			t.Errorf("%s read %.0f rows; should not rescan all 200 rows per agent", relation, examinedByRelation[relation])
		}
	}
	deployed, err := s.DeployedConfigs(ctx)
	if err != nil || len(deployed) != 200 {
		t.Fatalf("deployed configs=%d, %v", len(deployed), err)
	}
	t.Logf("joined deployed config plan visited rows=%v, execution %.3fms", examinedByRelation, explain[0].ExecutionMS)
	// A revoked node and engines no longer in capabilities remain visible in
	// the historical list, matching the previous listing contract.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id='agt_history_1'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestDeployments(ctx)
	if err != nil || len(got) != 800 {
		t.Fatalf("history result=%d, %v", len(got), err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET status='failed' WHERE agent_id='agt_history_1' AND engine='mihomo'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.LatestDeployments(ctx)
	if err != nil || len(got) != 799 {
		t.Fatalf("failed deployment included: %d, %v", len(got), err)
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
