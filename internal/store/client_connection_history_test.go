package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionCalendarDayBoundaries(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	local := time.FixedZone("Asia/Shanghai", 8*3600)
	now := time.Now().In(local)
	start := time.Date(now.Year(), now.Month(), now.Day()-2, 0, 0, 0, 0, local)
	end := start.AddDate(0, 0, 1)
	entries := []core.CoreLogEntry{}
	for i, at := range []time.Time{start.Add(-time.Second), start, end.Add(-time.Second), end} {
		entries = append(entries, core.CoreLogEntry{Engine: core.EngineMihomo, Level: "info", LoggedAt: at,
			Message: fmt.Sprintf("[TCP] 8.8.8.8:%d --> 1.1.1.1:443 using DIRECT", 50000+i)})
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_abcdefabcdef0001", Entries: entries}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM core_logs`); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{Since: start, Until: end, GroupByIP: true, Limit: 100, Bucket: "hour"}
	history, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || history.Flows != 2 || len(history.Records) != 1 {
		t.Fatalf("day history: %+v %v", history, err)
	}
	r := history.Records[0]
	if r.ClientIP != "8.8.8.8" || !r.FirstSeen.Equal(start) || !r.LastSeen.Equal(end.Add(-time.Second)) {
		t.Fatalf("adjacent days or destination leaked into selected date: %+v", r)
	}
}

func TestClientConnectionRetentionFollowsOwnerSettings(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, alice := sharedTestUser(t, db, ctx, "source-retention-alice")
	_, bob := sharedTestUser(t, db, ctx, "source-retention-bob")
	_, carol := sharedTestUser(t, db, ctx, "source-retention-carol")
	agents := []core.Agent{sharedTestAgent(t, db, alice), sharedTestAgent(t, db, bob), sharedTestAgent(t, db, carol)}
	short, forever := core.DefaultPanelSettings(), core.DefaultPanelSettings()
	short.ClientConnectionRetentionDays, forever.ClientConnectionRetentionDays = 10, 0
	if _, err := db.SavePanelSettings(alice, short); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SavePanelSettings(bob, forever); err != nil {
		t.Fatal(err)
	}
	for i, agent := range agents {
		if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", i+1), Entries: []core.CoreLogEntry{
			{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] 8.8.8.8:50123 --> 1.1.1.1:443 using DIRECT"},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	old := now.Add(-20 * 24 * time.Hour).Truncate(time.Hour)
	if _, err := db.pool.Exec(ctx, `UPDATE client_connections SET bucket=$1,first_seen=$1,last_seen=$1`, old); err != nil {
		t.Fatal(err)
	}
	if err := db.MaintainAccountData(ctx, now, true); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{Since: old, Until: old.Add(24 * time.Hour), Limit: 100, Bucket: "day", GroupByIP: true}
	for i, agent := range agents {
		q.AgentID = agent.ID
		history, err := db.ClientConnectionHistory(ctx, q)
		want := 1
		if i == 0 {
			want = 0
		}
		if err != nil || len(history.Records) != want {
			t.Fatalf("owner %d: %+v %v", i, history, err)
		}
	}
	// Permanent history survives arbitrary age; absent policy uses the 30-day default.
	if err := db.MaintainAccountData(ctx, now.Add(20*24*time.Hour), true); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{0, 1, 0} {
		q.AgentID = agents[i].ID
		history, err := db.ClientConnectionHistory(ctx, q)
		if err != nil || len(history.Records) != want {
			t.Fatalf("aged owner %d: %+v %v", i, history, err)
		}
	}
}

// Repeated minute observations and listener-port annotations must not change
// the distinct tuple count. Every identity field still distinguishes a flow.
func TestClientConnectionSummaryDistinctObservationTuples(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	first, second := sharedTestAgent(t, db, ctx), sharedTestAgent(t, db, ctx)
	at := time.Now().UTC().Truncate(time.Hour)
	base := []any{first.ID, "mihomo", "tcp", "entry", "tcp", "8.8.8.8", 50000, "127.0.0.1", 443}
	alternatives := []any{second.ID, "xray", "udp", "other", "udp", "1.1.1.1", 50001, "127.0.0.2", 8443}
	for variant := -1; variant < len(base); variant++ {
		values := append([]any{}, base...)
		if variant >= 0 {
			values[variant] = alternatives[variant]
		}
		for minute := 0; minute < 2; minute++ {
			args := append(append([]any{}, values...), at.Add(time.Duration(minute)*time.Minute), 9443+minute)
			if _, err := db.pool.Exec(ctx, `INSERT INTO client_connections
 (agent_id,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,bucket,first_seen,last_seen,inbound_port,source)
 VALUES($1,$2,$3,$4,$5,$6::inet,$7,$8::inet,$9,$10,$10,$10,$11,'core_logs')`, args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, grouped := range []bool{false, true} {
		for _, skipTimeline := range []bool{false, true} {
			q := ClientConnectionQuery{Since: at, Until: at.Add(time.Hour), Limit: 1, Bucket: "hour", GroupByIP: grouped, SkipTimeline: skipTimeline}
			history, err := db.ClientConnectionHistory(ctx, q)
			if err != nil || history.Flows != 10 || history.IPs != 2 || len(history.Records) != 1 {
				t.Fatalf("grouped=%v skipTimeline=%v: %+v %v", grouped, skipTimeline, history, err)
			}
			if !skipTimeline && (len(history.Timeline) != 1 || history.Timeline[0].Flows != history.Flows || history.Timeline[0].IPs != history.IPs) {
				t.Fatalf("summary disagrees with unchanged timeline: %+v", history)
			}
		}
	}
}

func TestClientConnectionSummarySnapshotDuringIngestion(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	at := time.Now().UTC().Truncate(time.Minute)
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 30; i++ {
			_, err := db.pool.Exec(ctx, `INSERT INTO client_connections
 (agent_id,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,bucket,first_seen,last_seen,source)
 VALUES($1,'mihomo','tcp','entry','tcp',('8.8.8.' || $2::text)::inet,50000,'127.0.0.1',443,$3,$3,$3,'core_logs')`, agent.ID, fmt.Sprint(i), at)
			if err != nil {
				done <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
		done <- nil
	}()
	// Every observation is a unique IP/flow/group. All reads within a response
	// must agree, even if ingestion commits between the summary and page query.
	q := ClientConnectionQuery{Since: at, Until: at.Add(time.Minute), GroupByIP: true, SkipTimeline: true, Limit: 100, Bucket: "hour"}
	for i := 0; i < 30; i++ {
		history, err := db.ClientConnectionHistory(ctx, q)
		if err != nil || history.Flows != history.IPs || history.IPs != int64(len(history.Records)) {
			t.Errorf("inconsistent observation snapshot: %+v %v", history, err)
			break
		}
		for _, record := range history.Records {
			if len(record.Endpoints) != 1 || record.Endpoints[0].LocalPort != 443 {
				t.Errorf("page endpoints differ from observation snapshot: %+v", record)
			}
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
