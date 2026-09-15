package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

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
