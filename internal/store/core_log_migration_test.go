package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// v74 shipped task constraint repairs while this log change was in progress.
// Reconstruct its actual missing columns/index projections, not just its stamp.
func TestCoreLogIdentityMigrationFromPreviousSchemas(t *testing.T) {
	for _, previous := range []int{73, 74} {
		t.Run(fmt.Sprint(previous), func(t *testing.T) {
			db := openPerformanceStore(t)
			ctx := context.Background()
			node, _ := seedPerformanceAgent(t, db, 0)
			batch, _ := core.NewID("log")
			if err := db.StoreCoreLogs(ctx, node, core.CoreLogBatch{ID: batch, Entries: []core.CoreLogEntry{{Engine: core.EngineXray, InstanceID: "main", Service: "qagent-xray.service", Level: "info", Message: "old source without proof"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.pool.Exec(ctx, `DROP TABLE core_log_source_states;
 DROP INDEX core_logs_instance_recent_idx;
 ALTER TABLE core_logs DROP COLUMN instance_id CASCADE, DROP COLUMN service CASCADE,
 DROP COLUMN owner_id CASCADE, DROP COLUMN node_owner_id CASCADE;
 CREATE INDEX core_logs_agent_engine_covering_idx ON core_logs(agent_id,engine,id DESC) INCLUDE(level,message,logged_at,received_at);
 CREATE INDEX core_logs_engine_covering_idx ON core_logs(engine,id DESC) INCLUDE(agent_id,level,message,logged_at,received_at);
 DELETE FROM qcontrolhub_schema_migrations`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.pool.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations(version) VALUES($1)`, previous); err != nil {
				t.Fatal(err)
			}
			for range 2 { // Retry/startup must preserve the conservative snapshot.
				if err := db.migrate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var version, count int
			if err := db.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion {
				t.Fatalf("migration version=%d: %v", version, err)
			}
			// The dropped state table must come back with the transport and last
			// delivered line columns, and a retried startup must not duplicate them.
			var stateColumns int
			if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema=current_schema() AND table_name='core_log_source_states'
				AND column_name IN ('source','last_at')`).Scan(&stateColumns); err != nil || stateColumns != 2 {
				t.Fatalf("core log source state columns=%d: %v", stateColumns, err)
			}
			// Replaying the schema step (for example after an interrupted
			// deployment) must keep the columns and must not fail.
			if _, err := db.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version=$1`, currentSchemaVersion); err != nil {
				t.Fatal(err)
			}
			if err := db.migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema=current_schema() AND table_name='core_log_source_states'
				AND column_name IN ('source','last_at')`).Scan(&stateColumns); err != nil || stateColumns != 2 {
				t.Fatalf("replayed core log source state columns=%d: %v", stateColumns, err)
			}
			if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_logs WHERE node_owner_id='unverified' AND owner_id='' AND instance_id='' AND message='old source without proof'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("historical provenance count=%d: %v", count, err)
			}
			for _, index := range []string{"core_logs_agent_engine_covering_idx", "core_logs_engine_covering_idx", "core_logs_instance_recent_idx"} {
				var definition string
				if err := db.pool.QueryRow(ctx, `SELECT pg_get_indexdef($1::regclass)`, index).Scan(&definition); err != nil {
					t.Fatal(err)
				}
				for _, column := range []string{"instance_id", "service", "owner_id", "node_owner_id"} {
					if !strings.Contains(definition, column) {
						t.Fatalf("%s missing %s: %s", index, column, definition)
					}
				}
			}
			logs, err := db.ListCoreLogs(WithConfigScope(ctx, "", true), CoreLogQuery{AgentID: node})
			if err != nil || len(logs) != 0 {
				t.Fatalf("migration guessed ownership: %+v %v", logs, err)
			}
			assertTaskConstraintSet(t, db, ctx)
		})
	}
}
