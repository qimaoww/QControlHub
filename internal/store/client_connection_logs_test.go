package store

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionLogsPersistAtomicallyAndReplay(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	entry := core.CoreLogEntry{Engine: core.EngineSingBox, Level: "info", Message: "inbound/vless[entry]: inbound connection from 8.8.8.8:50123", LoggedAt: start.Add(10 * time.Second)}
	later := entry
	later.LoggedAt = start.Add(20 * time.Second)
	expired := entry
	expired.Message = "inbound/vless[entry]: inbound connection from 1.1.1.1:50123"
	expired.LoggedAt = time.Now().Add(-8 * 24 * time.Hour)
	outbound := entry
	outbound.Message = "outbound/direct[direct]: outbound connection to 9.9.9.9:443"
	batch := core.CoreLogBatch{ID: "log_0123456789abcdef", Entries: []core.CoreLogEntry{later, entry, entry, expired, outbound}}
	for range 2 {
		if err := db.StoreCoreLogs(ctx, agent.ID, batch); err != nil {
			t.Fatal(err)
		}
	}
	q := ClientConnectionQuery{Since: start, Until: start.Add(time.Minute), Limit: 100, Bucket: "minute"}
	history, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(history.Records) != 1 || history.Flows != 1 || history.IPs != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	row := history.Records[0]
	if row.ClientIP != "8.8.8.8" || row.LocalIP != "" || row.LocalPort != 0 || row.Inbound != "entry" || !row.FirstSeen.Equal(entry.LoggedAt) || !row.LastSeen.Equal(later.LoggedAt) {
		t.Fatalf("row=%+v", row)
	}
	// Backfill existing logs after an upgrade, without receiving another heartbeat.
	if _, err := db.pool.Exec(ctx, `DELETE FROM client_connections; DELETE FROM client_connection_sources;
 UPDATE client_connection_log_backfill SET last_id=0,upper_id=(SELECT max(id) FROM core_logs)`); err != nil {
		t.Fatal(err)
	}
	if done, err := db.BackfillClientConnectionLogs(ctx); err != nil || !done {
		t.Fatalf("backfill done=%v err=%v", done, err)
	}
	history, err = db.ClientConnectionHistory(ctx, q)
	if err != nil || len(history.Records) != 1 || !history.Records[0].FirstSeen.Equal(entry.LoggedAt) {
		t.Fatalf("backfill=%+v err=%v", history, err)
	}
	if done, err := db.BackfillClientConnectionLogs(ctx); err != nil || !done {
		t.Fatalf("repeat backfill done=%v err=%v", done, err)
	}
	// Panel log minimum level applies to the derived observations too.
	if _, err := db.pool.Exec(ctx, `UPDATE panel_settings SET core_log_minimum_level='error'`); err != nil {
		t.Fatal(err)
	}
	batch.ID = "log_fedcba9876543210"
	batch.Entries = []core.CoreLogEntry{entry}
	batch.Entries[0].Message = "inbound/vless[entry]: inbound connection from 1.1.1.1:50123"
	if err := db.StoreCoreLogs(ctx, agent.ID, batch); err != nil {
		t.Fatal(err)
	}
	history, err = db.ClientConnectionHistory(ctx, q)
	if err != nil || history.Flows != 1 {
		t.Fatalf("filtered log entered history: %+v %v", history, err)
	}
}

func TestClientConnectionLogMigrationFrom66(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_0123456789abcdef", Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] 8.8.8.8:50123 --> example.invalid:443 using DIRECT"}}}); err != nil {
		t.Fatal(err)
	}
	// Recreate v66 metadata and legacy Agent history. Logs remain on the panel.
	if _, err := db.pool.Exec(ctx, `DELETE FROM client_connections;
 ALTER TABLE client_connections DROP COLUMN source;
 ALTER TABLE client_connection_sources DROP COLUMN source;
 ALTER TABLE client_connections DROP CONSTRAINT client_connections_local_port_check;
 ALTER TABLE client_connections ADD CONSTRAINT client_connections_local_port_check CHECK(local_port BETWEEN 1 AND 65535);
 ALTER TABLE client_connections DROP CONSTRAINT client_connections_transport_check;
 ALTER TABLE client_connections ADD CONSTRAINT client_connections_transport_check CHECK(transport IN ('tcp','udp'));
 DROP TABLE client_connection_log_backfill;
 INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen)
 SELECT agent_id,date_trunc('minute',logged_at),'xray','vless','legacy','tcp','1.1.1.1',123,'192.0.2.1',443,logged_at,logged_at FROM core_logs;
 DELETE FROM qcontrolhub_schema_migrations WHERE version>=67;
 INSERT INTO qcontrolhub_schema_migrations(version) VALUES(66) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Minute), Limit: 100, Bucket: "hour"}
	before, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(before.Records) != 0 || before.Sources[0].UpdatedAt != nil {
		t.Fatalf("legacy samples leaked: %+v %v", before, err)
	}
	if done, err := db.BackfillClientConnectionLogs(ctx); err != nil || !done {
		t.Fatalf("backfill done=%v err=%v", done, err)
	}
	after, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(after.Records) != 1 || after.Records[0].ClientIP != "8.8.8.8" {
		t.Fatalf("upgraded=%+v %v", after, err)
	}
}

func TestClientConnectionLogBackfillPagesAndLiveUploads(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	removed := sharedTestAgent(t, db, ctx)
	// Seed retained logs directly, as they existed before this feature.
	if _, err := db.pool.Exec(ctx, `INSERT INTO core_log_batches(id,agent_id,received_at) VALUES('log_0000000000000001',$1,now()),('log_0000000000000002',$2,now())`, agent.ID, removed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
 SELECT 'log_0000000000000001',n,$1,'mihomo','info','[TCP] 8.8.8.8:' || (10000+n)::text || ' --> example.invalid:443 using DIRECT',now(),now()
 FROM generate_series(1,1001) n;
 `, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
 VALUES('log_0000000000000002',0,$1,'mihomo','info','[TCP] 9.9.9.9:123 --> example.invalid:443 using DIRECT',now(),now())`, removed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id=$1`, removed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE client_connection_log_backfill SET last_id=0,upper_id=(SELECT max(id) FROM core_logs)`); err != nil {
		t.Fatal(err)
	}
	if done, err := db.BackfillClientConnectionLogs(ctx); err != nil || done {
		t.Fatalf("first page done=%v err=%v", done, err)
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_0000000000000003", Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] 1.1.1.1:123 --> example.invalid:443 using DIRECT"}}}); err != nil {
		t.Fatal(err)
	}
	// A separate store simulates resuming the durable cursor after restart.
	resumed := &Store{pool: db.pool}
	if done, err := resumed.BackfillClientConnectionLogs(ctx); err != nil || !done {
		t.Fatalf("second page done=%v err=%v", done, err)
	}
	history, err := db.ClientConnectionHistory(ctx, ClientConnectionQuery{Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Minute), Limit: 1, Bucket: "hour"})
	if err != nil || history.Flows != 1002 || history.IPs != 2 || len(history.Sources) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM client_connections WHERE agent_id=$1`, removed.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked telemetry recreated: count=%d err=%v", count, err)
	}
}
