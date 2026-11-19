package store

import (
	"testing"
	"time"
)

func TestClientConnectionMonthRollupMatchesRawHistory(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "rollup-owner")
	_, other := sharedTestUser(t, db, ctx, "rollup-other")
	agent := sharedTestAgent(t, db, owner)
	hidden := sharedTestAgent(t, db, other)
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-5 * 24 * time.Hour)
	q := ClientConnectionQuery{Since: day.Add(-8 * time.Hour), Until: day.Add(64 * time.Hour), GroupByIP: true, SkipTimeline: true, Limit: 100, Bucket: "day"}
	insert := func(id string, hour, srcPort, endpoint int) {
		t.Helper()
		at := day.Add(time.Duration(hour) * time.Hour)
		if _, err := db.pool.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,inbound_port,first_seen,last_seen,source)
		 VALUES($1,$2,'mihomo','vless','test','tcp','8.8.8.8',$3,'0.0.0.0',443,$4,$2,$2,'core_logs')`, id, at, srcPort, endpoint); err != nil {
			t.Fatal(err)
		}
	}
	// Identical flows recur on different days and different derived endpoints.
	// Both UTC edge days also contain a row outside the requested range.
	for _, row := range [][3]int{{-9, 1, 80}, {-7, 65535, 443}, {1, 4095, 443}, {25, 4095, 8443}, {40, 4096, 8443}, {63, 65535, 443}, {64, 9000, 80}} {
		insert(agent.ID, row[0], row[1], row[2])
	}
	insert(hidden.ID, 4, 50000, 80)
	check := func(flows int64, ports int) {
		t.Helper()
		// A new process cannot rely on an earlier in-memory projection.
		store := &Store{pool: db.pool}
		raw, err := db.ClientConnectionHistory(owner, q)
		if err != nil {
			t.Fatal(err)
		}
		got, used, err := store.SyncClientConnections(owner, q, "rollup", "")
		if err != nil || !used || got.Flows != flows || got.Flows != raw.Flows || got.IPs != 1 || len(got.Records) != 1 {
			t.Fatalf("flows %d want %d raw %d: used=%v err=%v", got.Flows, flows, raw.Flows, used, err)
		}
		g, r := got.Records[0], raw.Records[0]
		if g.AgentID != agent.ID || g.ID != r.ID || !g.FirstSeen.Equal(r.FirstSeen) || !g.LastSeen.Equal(r.LastSeen) || len(g.Endpoints) != ports || len(g.Endpoints) != len(r.Endpoints) {
			t.Fatalf("groups differ: %+v vs %+v", g, r)
		}
		for i, port := range r.Endpoints {
			if g.Endpoints[i] != port {
				t.Fatalf("endpoints differ: %+v vs %+v", g.Endpoints, r.Endpoints)
			}
		}
	}
	drain := func() {
		t.Helper()
		for {
			done, err := db.BuildClientConnectionRollup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if done {
				return
			}
		}
	}
	// Simulate observations retained across upgrade before marker discovery.
	if _, err := db.pool.Exec(ctx, "DELETE FROM client_connection_rollup_days"); err != nil {
		t.Fatal(err)
	}
	check(3, 2)
	drain()
	check(3, 2)
	insert(agent.ID, 30, 42, 9443)
	check(4, 3)
	drain()
	check(4, 3)
	if _, err := db.pool.Exec(ctx, `DELETE FROM client_connections WHERE agent_id=$1 AND client_port IN (42,4096)`, agent.ID); err != nil {
		t.Fatal(err)
	}
	check(2, 2)
	drain()
	check(2, 2)
	if _, err := db.pool.Exec(ctx, `DELETE FROM client_connections WHERE agent_id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	drain()
	var count int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM client_connection_rollup_masks WHERE agent_id=$1", agent.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retained deleted observations: %d %v", count, err)
	}
}
