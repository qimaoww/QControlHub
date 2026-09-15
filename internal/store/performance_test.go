package store

import (
	"context"
	"encoding/json"
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
