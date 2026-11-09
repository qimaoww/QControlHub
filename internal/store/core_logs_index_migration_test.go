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

func TestSharedCoreLogIndexUpgradePreservesHistory(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agent, _ := seedPerformanceAgent(t, s, 0)
	batch := "log_abcdef0123456790"
	if err := s.StoreCoreLogs(ctx, agent, core.CoreLogBatch{ID: batch, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "retained main"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at,instance_id,node_owner_id,owner_id)
 VALUES($1,1,$2,'mihomo','info','retained shared history',now(),now(),'shr_0123456789abcdef','','historical-owner')`, batch, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DROP INDEX core_logs_shared_recent_idx; DELETE FROM qcontrolhub_schema_migrations WHERE version>=95; INSERT INTO qcontrolhub_schema_migrations(version) VALUES(94) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.migrate(ctx); err != nil {
			t.Fatal(err)
		}
		var rows, version, partitions, indexes int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core_logs WHERE batch_id=$1`, batch).Scan(&rows); err != nil || rows != 2 {
			t.Fatalf("retained history=%d: %v", rows, err)
		}
		if err := s.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion {
			t.Fatalf("schema=%d: %v", version, err)
		}
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent='core_logs'::regclass`).Scan(&partitions); err != nil {
			t.Fatal(err)
		}
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_inherits WHERE inhparent='core_logs_shared_recent_idx'::regclass`).Scan(&indexes); err != nil || indexes != partitions {
			t.Fatalf("partition indexes=%d/%d: %v", indexes, partitions, err)
		}
	}
}
