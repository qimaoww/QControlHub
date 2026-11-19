package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionMonthChurnMeasurement(t *testing.T) {
	if os.Getenv("QCH_CONNECTION_MEASURE") != "1" {
		t.Skip("opt-in monthly high-churn workload")
	}
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer stop()
	end := time.Now().UTC().Truncate(24 * time.Hour).Add(24*time.Hour - time.Minute)
	q := ClientConnectionQuery{Since: end.AddDate(0, 0, -30), Until: end, GroupByIP: true, SkipTimeline: true, Limit: 100, Bucket: "day"}
	for batch := 0; batch < 4; batch++ {
		if _, err := db.pool.Exec(ctx, `INSERT INTO client_connections
	 (agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen,source)
	 SELECT $1,b,'mihomo','vless','test','tcp','8.0.0.0'::inet+i,1024+(m*499+i*317)%60000,'0.0.0.0',443,b,b,'core_logs'
	 FROM generate_series(1,1000)i CROSS JOIN generate_series($3::int+1,$3::int+1200)m
	 CROSS JOIN LATERAL (SELECT $2::timestamptz - m*interval '9 minutes' b) t`, agent.ID, end, batch*1200); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.pool.Exec(ctx, "ANALYZE client_connections"); err != nil {
		t.Fatal(err)
	}
	where, args := connectionGroupWhere(ctx, q)
	legacySQL := connectionGroupsSQL(where, len(args)+1)
	t.Log("legacy read starts")
	started := time.Now()
	rows, err := db.pool.Query(ctx, legacySQL, append(args, connectionSyncGroups)...)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if rows.Err() != nil || n != 1000 {
		t.Fatalf("legacy groups: %d %v", n, rows.Err())
	}
	t.Logf("legacy SQL: %s", time.Since(started))
	for _, prepared := range []bool{false, true} {
		if prepared {
			start := time.Now()
			for {
				done, err := db.BuildClientConnectionRollup(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if done {
					break
				}
			}
			t.Logf("background preparation: %v", time.Since(start))
		}
		if prepared {
			sql, args := connectionMonthlyGroups(ctx, q, nil, connectionSyncGroups)
			tx, err := db.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SET LOCAL jit = off"); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE,BUFFERS) "+sql, args...)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				t.Log(line)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		}
		for _, readers := range []int{1, 4} {
			db := &Store{pool: db.pool}
			run := make(chan struct{})
			results := make(chan error, readers)
			for range readers {
				go func() {
					<-run
					ctx, cancel := context.WithTimeout(ctx, time.Minute)
					defer cancel()
					page, used, err := db.SyncClientConnections(ctx, q, "monthly", "")
					if err == nil && (!used || page.Flows != 4800000 || page.IPs != 1000 || len(page.Records) != q.Limit) {
						t.Errorf("incorrect page: used=%t flows=%d ips=%d records=%d", used, page.Flows, page.IPs, len(page.Records))
					}
					results <- err
				}()
			}
			started := time.Now()
			close(run)
			for range readers {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			t.Logf("monthly cold read, prepared=%t simultaneous readers=%d: %v", prepared, readers, time.Since(started))
		}
	}
	measureConnectionReadStates(t, db, ctx, q, agent.ID)
}

func measureConnectionReadStates(t *testing.T, db *Store, ctx context.Context, q ClientConnectionQuery, agent string) {
	t.Helper()
	type scenario struct {
		name  string
		query ClientConnectionQuery
		sync  bool
	}
	scenarios := []scenario{}
	add := func(name string, sync bool, edit func(*ClientConnectionQuery)) {
		sample := q
		edit(&sample)
		scenarios = append(scenarios, scenario{name, sample, sync})
	}
	add("current day", true, func(q *ClientConnectionQuery) {
		q.Since = q.Until.Truncate(24 * time.Hour)
		q.Until = q.Since.Add(24 * time.Hour)
	})
	add("historical day", true, func(q *ClientConnectionQuery) {
		q.Until = q.Until.Truncate(24 * time.Hour).Add(-24 * time.Hour)
		q.Since = q.Until.Add(-24 * time.Hour)
	})
	add("filtered IP", true, func(q *ClientConnectionQuery) {
		q.ClientIP = "8.0.0.1"
		q.AgentID = agent
		q.Port = 443
		q.Engine = "mihomo"
	})
	add("cold locations only", false, func(q *ClientConnectionQuery) { q.RecordsOnly = true })
	add("database page", false, func(q *ClientConnectionQuery) {})
	add("daily timeline and raw page", false, func(q *ClientConnectionQuery) { q.GroupByIP = false; q.SkipTimeline = false })
	add("hourly timeline and raw page", false, func(q *ClientConnectionQuery) { q.GroupByIP = false; q.SkipTimeline = false; q.Bucket = "hour" })
	add("minute timeline and raw page", false, func(q *ClientConnectionQuery) { q.GroupByIP = false; q.SkipTimeline = false; q.Bucket = "minute" })
	for _, s := range scenarios {
		start := time.Now()
		var h core.ClientConnectionHistory
		var err error
		if s.sync {
			var used bool
			h, used, err = db.SyncClientConnections(ctx, s.query, "measurement-states", "")
			if !used {
				t.Fatal("missing snapshot", s.name)
			}
		} else {
			h, err = db.ClientConnectionHistory(ctx, s.query)
		}
		if err != nil || len(h.Records) == 0 {
			t.Fatalf("%s: records=%d err=%v", s.name, len(h.Records), err)
		}
		if !s.query.SkipTimeline {
			var n int64
			for _, b := range h.Timeline {
				n += b.Flows
			}
			if n != 4800000 || n != h.Flows {
				t.Fatalf("%s: invalid timeline: %d", s.name, n)
			}
		}
		t.Logf("%s: %s (records=%d, flows=%d, buckets=%d)", s.name, time.Since(start), len(h.Records), h.Flows, len(h.Timeline))
	}
}
