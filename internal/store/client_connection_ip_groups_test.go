package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionIPGroupingKeepsHistoricalPorts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["xray"]',supported_capabilities='["xray"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	// Both deployments occur within one minute; separate connections must still
	// retain their own listener ports when they become one IP row.
	deployed := old.Add(30 * time.Second)
	input := core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "group port history", Content: `{"inbounds":[{"tag":"entry","protocol":"vless","port":8443}]}`}
	config, err := db.SaveAgentConfig(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	input.Content = `{"inbounds":[{"tag":"entry","protocol":"vless","port":9443}]}`
	if _, err := db.SaveAgentConfig(ctx, input, 1); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{old, deployed} {
		if _, err := db.pool.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,config_id,config_version,status,created_at,started_at,finished_at)
 VALUES($1,$2,'deploy','xray',$3,$4,'succeeded',$5,$5,$5)`, fmt.Sprintf("tsk_group_port_%d", i), agent.ID, config.ID, i+1, at); err != nil {
			t.Fatal(err)
		}
	}
	entries := []core.CoreLogEntry{}
	for i, at := range []time.Time{old.Add(time.Second), old.Add(2 * time.Second), deployed.Add(time.Second), deployed.Add(time.Minute)} {
		entries = append(entries, core.CoreLogEntry{Engine: core.EngineXray, Level: "info", Message: fmt.Sprintf("from 8.8.8.8:%d accepted tcp:1.1.1.1:443 [entry -> direct]", 50123+i), LoggedAt: at})
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_0123456789abcdef", Entries: entries}); err != nil {
		t.Fatal(err)
	}
	query := ClientConnectionQuery{GroupByIP: true, AgentID: agent.ID, Since: old, Until: time.Now(), Limit: 100, Bucket: "hour"}
	history, err := db.ClientConnectionHistory(ctx, query)
	if err != nil || len(history.Records) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	r := history.Records[0]
	if r.ClientIP != "8.8.8.8" || len(r.Endpoints) != 2 || r.Endpoints[0].LocalPort != 8443 || r.Endpoints[1].LocalPort != 9443 || !r.FirstSeen.Equal(entries[0].LoggedAt) || !r.LastSeen.Equal(entries[3].LoggedAt) {
		t.Fatalf("historical endpoints=%+v", r)
	}
}

func TestClientConnectionIPGroupingPaginationAndIsolation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner, ownerCtx := sharedTestUser(t, db, ctx, "ip-group-owner")
	_, strangerCtx := sharedTestUser(t, db, ctx, "ip-group-stranger")
	first := sharedTestAgent(t, db, ctx)
	second := sharedTestAgent(t, db, ctx)
	hidden := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id=$1 WHERE id=ANY($2)`, owner.ID, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	insert := func(agentID, ip string, sourcePort, localPort int, seen time.Time) {
		t.Helper()
		_, err := db.pool.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen,source)
 VALUES($1,date_trunc('minute',$5::timestamptz),'xray','vless','entry','tcp',$2::inet,$3,'192.0.2.1',$4,$5,$5,'core_logs')`, agentID, ip, sourcePort, localPort, seen)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(first.ID, "8.8.8.8", 50001, 443, now.Add(-2*time.Minute))
	insert(first.ID, "1.1.1.1", 50002, 443, now.Add(-time.Minute))
	insert(first.ID, "8.8.8.8", 50003, 443, now.Add(-time.Minute))
	insert(second.ID, "8.8.8.8", 50004, 8443, now)
	insert(hidden.ID, "8.8.8.8", 50005, 9443, now)
	insert(first.ID, "10.0.0.1", 50006, 443, now)
	q := ClientConnectionQuery{GroupByIP: true, Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 1, Bucket: "hour"}
	page, err := db.ClientConnectionHistory(ownerCtx, q)
	if err != nil || len(page.Records) != 1 || page.Records[0].ClientIP != "1.1.1.1" || page.NextBefore == 0 || page.IPs != 2 || page.Flows != 4 {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	q.Before = page.NextBefore
	next, err := db.ClientConnectionHistory(ownerCtx, q)
	if err != nil || len(next.Records) != 1 || next.Records[0].ClientIP != "8.8.8.8" || next.NextBefore != 0 {
		t.Fatalf("second page=%+v err=%v", next, err)
	}
	group := next.Records[0]
	if len(group.Endpoints) != 2 || !group.FirstSeen.Equal(now.Add(-2*time.Minute)) || !group.LastSeen.Equal(now) {
		t.Fatalf("group=%+v", group)
	}
	ports := map[int]bool{}
	for _, endpoint := range group.Endpoints {
		ports[endpoint.LocalPort] = true
		if endpoint.AgentID == hidden.ID {
			t.Fatal("hidden endpoint leaked")
		}
	}
	if !ports[443] || !ports[8443] {
		t.Fatalf("ports=%v", ports)
	}
	// A new connection to an existing IP must not move/split its pagination group.
	insert(first.ID, "8.8.8.8", 50007, 443, now)
	again, err := db.ClientConnectionHistory(ownerCtx, q)
	if err != nil || len(again.Records) != 1 || again.Records[0].ID != group.ID {
		t.Fatalf("unstable group=%+v %v", again, err)
	}
	q.RecordsOnly = true
	locations, err := db.ClientConnectionHistory(ownerCtx, q)
	if err != nil || len(locations.Records) != 1 || locations.Records[0].ID != group.ID || len(locations.Records[0].Endpoints) != 0 {
		t.Fatalf("location grouping=%+v %v", locations, err)
	}
	q.RecordsOnly = false
	q.Before = 0
	q.Limit = 100
	q.AgentID = first.ID
	scoped, err := db.ClientConnectionHistory(ownerCtx, q)
	if err != nil || scoped.IPs != 2 {
		t.Fatalf("node filter=%+v %v", scoped, err)
	}
	for _, r := range scoped.Records {
		if len(r.Endpoints) != 1 || r.Endpoints[0].AgentID != first.ID {
			t.Fatalf("node endpoints=%+v", r)
		}
	}
	q.IncludeNonPublic = true
	all, err := db.ClientConnectionHistory(ownerCtx, q)
	if err != nil || all.IPs != 3 || len(all.Records) != 3 {
		t.Fatalf("private opt-in=%+v %v", all, err)
	}
	denied, err := db.ClientConnectionHistory(strangerCtx, q)
	if err != nil || len(denied.Records) != 0 || len(denied.Sources) != 0 {
		t.Fatalf("unauthorized grouping=%+v %v", denied, err)
	}
}
