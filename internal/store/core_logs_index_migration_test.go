package store

import (
	"context"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestFleetCoreLogIndexUpgradePreservesRows(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agent, _ := seedPerformanceAgent(t, s, 0)
	if err := s.StoreCoreLogs(ctx, agent, core.CoreLogBatch{ID: "log_abcdef0123456789", Entries: []core.CoreLogEntry{{Engine: core.EngineXray, Level: "info", Message: "retained through upgrade"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DROP INDEX core_logs_engine_covering_idx;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=72;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(71) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('core_logs_engine_covering_idx') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("fleet index not recreated on upgrade: %v", err)
	}
	entries, err := s.ListCoreLogs(ctx, CoreLogQuery{})
	if err != nil || len(entries) != 1 || entries[0].Message != "retained through upgrade" {
		t.Fatalf("upgrade changed retained logs: %v %v", entries, err)
	}
}
