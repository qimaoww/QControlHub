package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionSyncChangesAndLightweightConfirmation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	now := time.Now().UTC().Truncate(time.Minute)
	q := ClientConnectionQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Hour), GroupByIP: true, SkipTimeline: true, Limit: 100, Bucket: "hour"}
	upload := func(batch int, ip string, port int, at time.Time) {
		t.Helper()
		err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", batch), Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", LoggedAt: at, Message: fmt.Sprintf("[TCP] %s:%d --> 1.1.1.1:443 using DIRECT", ip, port)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	sync := func(token string) core.ClientConnectionHistory {
		t.Helper()
		h, ok, err := db.SyncClientConnections(ctx, q, "verified-session", token)
		if err != nil || !ok {
			t.Fatalf("sync: %t %v", ok, err)
		}
		return h
	}
	upload(1, "8.8.8.8", 50123, now)
	first := sync("")
	legacy, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || first.Flows != legacy.Flows || first.IPs != legacy.IPs || len(first.Records) != 1 || first.SyncToken == "" || first.Delta {
		t.Fatalf("cold sync differs: %+v / %+v %v", first, legacy, err)
	}
	// A confirmed poll must not read or aggregate the history table at all.
	lock, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `LOCK TABLE client_connections IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	unchanged, ok, err := db.SyncClientConnections(deadline, q, "verified-session", first.SyncToken)
	cancel()
	lock.Rollback(ctx)
	if err != nil || !ok || !unchanged.Delta || len(unchanged.Records) != 0 || len(unchanged.RecordKeys) != 1 {
		t.Fatalf("confirmation touched history: %+v %v", unchanged, err)
	}
	// A different batch can repeat the same observation without adding a flow.
	upload(2, "8.8.8.8", 50123, now)
	duplicate := sync(first.SyncToken)
	if duplicate.Flows != 1 || duplicate.IPs != 1 {
		t.Fatalf("duplicate added counts: %+v", duplicate)
	}
	// Mutate the old row without a new ID, then insert an earlier observation.
	if _, err = db.pool.Exec(ctx, `UPDATE client_connections SET last_seen=last_seen+interval '10 seconds',inbound_port=443 WHERE agent_id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	updated := sync(duplicate.SyncToken)
	if len(updated.Records) != 1 || !updated.Records[0].LastSeen.After(first.Records[0].LastSeen) || updated.Records[0].Endpoints[0].LocalPort != 443 {
		t.Fatalf("existing group update lost: %+v", updated)
	}
	upload(3, "8.8.8.8", 50124, now.Add(-time.Minute))
	upload(4, "1.1.1.1", 50125, now.Add(-2*time.Minute))
	late := sync(updated.SyncToken)
	if len(late.Records) != 2 || late.Flows != 3 || late.IPs != 2 {
		t.Fatalf("late data missed: %+v", late)
	}
	if err = db.SaveClientConnectionLocations(ctx, []ClientConnectionLocation{{IP: "8.8.8.8", ClientIPLocation: core.ClientIPLocation{CountryCode: "CN", Province: "广东"}, RetryAfter: now.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	located := sync(late.SyncToken)
	if len(located.Records) != 1 || located.Records[0].Location.Province != "广东" || !reflect.DeepEqual(located.CountryCodes, []string{"CN"}) {
		t.Fatalf("geography correction missed: %+v", located)
	}
	if _, err = db.pool.Exec(ctx, `DELETE FROM client_connections WHERE client_ip='8.8.8.8'`); err != nil {
		t.Fatal(err)
	}
	deleted := sync(located.SyncToken)
	if deleted.Flows != 1 || deleted.IPs != 1 || len(deleted.RecordKeys) != 1 || deleted.RecordKeys[0] != connectionGroupKey(late.Records[0]) {
		t.Fatalf("deletion did not remove group or totals: %+v", deleted)
	}
	// A different login cannot use the first login's position as a delta baseline.
	other, _, err := db.SyncClientConnections(ctx, q, "another-session", deleted.SyncToken)
	if err != nil || other.Delta || len(other.Records) != 1 {
		t.Fatalf("session baseline was reused: %+v %v", other, err)
	}
}

func TestClientConnectionSyncPaginationAndCountryCounts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	now := time.Now().UTC()
	for i := 1; i <= 5; i++ {
		if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", i), Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: fmt.Sprintf("[TCP] 8.8.8.%d:50123 --> 1.1.1.1:443 using DIRECT", i)}}}); err != nil {
			t.Fatal(err)
		}
	}
	q := ClientConnectionQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Hour), GroupByIP: true, SkipTimeline: true, Limit: 2, Bucket: "hour"}
	seen := map[string]bool{}
	for {
		h, _, err := db.SyncClientConnections(ctx, q, "pagination", "")
		if err != nil {
			t.Fatal(err)
		}
		if h.Flows != 5 || h.IPs != 5 {
			t.Fatalf("page substituted for totals: %+v", h)
		}
		for _, r := range h.Records {
			key := connectionGroupKey(r)
			if seen[key] {
				t.Fatalf("duplicate group %s", key)
			}
			seen[key] = true
		}
		if h.NextCursor == "" {
			break
		}
		q.Cursor = h.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("missing groups: %v", seen)
	}
	q.Cursor = ""
	q.CountryCode = "CN"
	empty, _, err := db.SyncClientConnections(ctx, q, "country", "")
	if err != nil || empty.IPs != 0 {
		t.Fatal(err, empty)
	}
	if err = db.SaveClientConnectionLocations(ctx, []ClientConnectionLocation{{IP: "8.8.8.3", ClientIPLocation: core.ClientIPLocation{CountryCode: "CN", Province: "江苏"}, RetryAfter: now.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	country, _, err := db.SyncClientConnections(ctx, q, "country", empty.SyncToken)
	if err != nil || country.Flows != 1 || country.IPs != 1 || len(country.Records) != 1 {
		t.Fatalf("country membership missed: %+v %v", country, err)
	}
}

func TestClientConnectionSyncUpgradeKeepsHistory(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_aaabcdefabcdef01", Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] 8.8.8.8:50123 --> 1.1.1.1:443 using DIRECT"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `DROP TABLE client_connection_changes; DROP TABLE client_connection_revision; DROP INDEX client_connections_group_time_idx; DELETE FROM qcontrolhub_schema_migrations WHERE version>=93; INSERT INTO qcontrolhub_schema_migrations(version) VALUES(92) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{Since: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), GroupByIP: true, SkipTimeline: true, Limit: 100, Bucket: "hour"}
	h, _, err := db.SyncClientConnections(ctx, q, "upgrade", "")
	if err != nil || h.Flows != 1 || len(h.Records) != 1 {
		t.Fatalf("upgrade lost history: %+v %v", h, err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE client_connections SET inbound_port=8443`); err != nil {
		t.Fatal(err)
	}
	h, _, err = db.SyncClientConnections(ctx, q, "upgrade", h.SyncToken)
	if err != nil || len(h.Records) != 1 || h.Records[0].Endpoints[0].LocalPort != 8443 {
		t.Fatalf("upgrade did not install tracking: %+v %v", h, err)
	}
}

func TestClientConnectionSyncMeasurements(t *testing.T) {
	if os.Getenv("QCH_CONNECTION_MEASURE") != "1" {
		t.Skip("opt-in representative query measurement")
	}
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "connection-measure")
	agent := sharedTestAgent(t, db, owner)
	start := time.Now()
	_, err := db.pool.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,inbound_port,first_seen,last_seen,source)
 SELECT $1,date_trunc('minute',now())-m*interval '1 minute','mihomo','vless','test','tcp',('8.9.'||(i/250)::text||'.'||(1+i%250)::text)::inet,50123,'0.0.0.0',443,443,date_trunc('minute',now())-m*interval '1 minute',date_trunc('minute',now())-m*interval '1 minute'+interval '10 seconds','core_logs' FROM generate_series(1,4000)i CROSS JOIN generate_series(1,40)m`, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture insert/trigger: %s", time.Since(start))
	if _, err = db.pool.Exec(ctx, `ANALYZE client_connections`); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{Since: time.Now().Add(-24 * time.Hour), Until: time.Now().Add(time.Hour), GroupByIP: true, SkipTimeline: true, Limit: 100, Bucket: "hour"}
	med := func(values []float64) string {
		sort.Float64s(values)
		return fmt.Sprintf("%.3f/%.3f ms", values[len(values)/2], values[len(values)-1])
	}
	full, warm, delta := []float64{}, []float64{}, []float64{}
	var token string
	for i := 0; i < 5; i++ {
		start = time.Now()
		legacy, err := db.ClientConnectionHistory(owner, q)
		full = append(full, float64(time.Since(start))/float64(time.Millisecond))
		if err != nil || legacy.Flows != 4000 {
			t.Fatal(err, legacy.Flows)
		}
		snapshot, _, err := db.SyncClientConnections(owner, q, "measurement", "")
		if err != nil {
			t.Fatal(err)
		}
		token = snapshot.SyncToken
		start = time.Now()
		confirmation, _, err := db.SyncClientConnections(owner, q, "measurement", token)
		warm = append(warm, float64(time.Since(start))/float64(time.Millisecond))
		if err != nil || len(confirmation.Records) != 0 {
			t.Fatal(err)
		}
		before, _ := json.Marshal(legacy)
		after, _ := json.Marshal(confirmation)
		t.Logf("sample %d records=%d bytes full=%d confirmation=%d", i, len(legacy.Records), len(before), len(after))
		if _, err = db.pool.Exec(ctx, `UPDATE client_connections SET last_seen=last_seen+interval '1 second' WHERE id=(SELECT min(id) FROM client_connections)`); err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		change, _, err := db.SyncClientConnections(owner, q, "measurement", token)
		delta = append(delta, float64(time.Since(start))/float64(time.Millisecond))
		if err != nil || len(change.Records) != 1 {
			t.Fatalf("delta %v %+v", err, change)
		}
	}
	t.Logf("authorized 160000 observations/4000 groups/100 page; 5 samples median/max: full=%s unchanged=%s one-group=%s", med(full), med(warm), med(delta))
	where, args := connectionHistoryWhere(owner, q)
	data, _ := json.Marshal([]core.ClientConnectionRecord{{AgentID: agent.ID, Engine: core.EngineMihomo, ClientIP: "8.9.0.2"}})
	args = append(args, data)
	where += fmt.Sprintf(` AND (c.agent_id,c.engine,c.client_ip) IN (SELECT agent_id,engine,client_ip::inet FROM jsonb_to_recordset($%d::jsonb) AS wanted(agent_id text,engine text,client_ip text))`, len(args))
	rows, err := db.pool.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS) `+connectionGroupsSQL(where), args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
}
