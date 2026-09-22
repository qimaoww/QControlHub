package store

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionSourceMigrationFrom67(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	entries := []core.CoreLogEntry{
		{Engine: core.EngineSingBox, Level: "info", Message: "INFO [123 0ms] inbound/vless[entry]: inbound connection from 8.8.8.8:50123"},
		{Engine: core.EngineSingBox, Level: "info", Message: "INFO [123 0ms] inbound/vless[entry]: inbound connection to 9.9.9.9:443"},
		{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] 8.8.8.8:50123 --> 9.9.9.9:443 using DIRECT"},
		{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] Mihomo --> example.invalid:443 using [TCP] 9.9.9.9:443 --> 1.1.1.1:443 using DIRECT"},
		{Engine: core.EngineXray, Level: "info", Message: "from 8.8.4.4:50123 accepted tcp:1.1.1.1:443 [entry -> direct]"},
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_2123456789abcdef", Entries: entries}); err != nil {
		t.Fatal(err)
	}
	// Simulate stale destination rows in the old derived index, with backfill
	// already complete. Migration must remove them and re-read retained logs.
	if _, err := db.pool.Exec(ctx, `UPDATE client_connections SET client_ip='9.9.9.9' WHERE engine IN ('sing-box','mihomo');
 UPDATE client_connection_log_backfill SET last_id=(SELECT max(id) FROM core_logs),upper_id=(SELECT max(id) FROM core_logs);
 DELETE FROM qcontrolhub_schema_migrations WHERE version>=68;
 INSERT INTO qcontrolhub_schema_migrations(version) VALUES(67) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{GroupByIP: true, Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Minute), Limit: 100, Bucket: "hour"}
	before, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(before.Records) != 1 || before.Records[0].Engine != core.EngineXray {
		t.Fatalf("before rebuild=%+v err=%v", before, err)
	}
	if done, err := db.BackfillClientConnectionLogs(ctx); err != nil || !done {
		t.Fatalf("rebuild done=%v err=%v", done, err)
	}
	after, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(after.Records) != 3 {
		t.Fatalf("after rebuild=%+v err=%v", after, err)
	}
	for _, r := range after.Records {
		expected := "8.8.8.8"
		if r.Engine == core.EngineXray {
			expected = "8.8.4.4"
		}
		if r.ClientIP != expected {
			t.Fatalf("destination survived rebuild: %+v", r)
		}
	}
	// A normal restart must not clear observations again.
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(restarted.Records) != 3 {
		t.Fatalf("restart=%+v err=%v", restarted, err)
	}
}
