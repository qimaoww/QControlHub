package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type queryBudget struct{ queries, batches atomic.Int64 }

func (trace *queryBudget) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	trace.queries.Add(1)
	return ctx
}
func (*queryBudget) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (trace *queryBudget) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	trace.batches.Add(1)
	return ctx
}
func (*queryBudget) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}
func (*queryBudget) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData)     {}

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
		{"traffic64", 3, 1, func() error { return measured.UpdatePortTrafficUsage(ctx, agentID, usages, time.Now()) }},
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
	indexes := []string{"core_logs_agent_engine_recent_idx", "tasks_status_created_idx", "tasks_agent_created_idx", "tasks_retention_idx", "metric_samples_retention_idx", "port_traffic_daily_date_idx"}
	for _, index := range indexes {
		if _, err := s.pool.Exec(ctx, "DROP INDEX "+pgx.Identifier{index}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations; INSERT INTO qcontrolhub_schema_migrations(version) VALUES (40)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
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
