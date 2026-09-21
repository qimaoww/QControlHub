package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionSourceDirectionAndGroupScope(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	first, second := sharedTestAgent(t, db, ctx), sharedTestAgent(t, db, ctx)
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	for n, agent := range []core.Agent{first, second} {
		entries := []core.CoreLogEntry{}
		for i, destination := range []string{"1.1.1.1:443", "9.9.9.9:53", "[2606:4700:4700::1111]:443"} {
			for _, entry := range []core.CoreLogEntry{
				{Engine: core.EngineSingBox, Message: fmt.Sprintf("INFO [123 0ms] inbound/shadowsocks[entry]: [alice] inbound packet connection from 8.8.8.8:%d", 50123+i)},
				{Engine: core.EngineSingBox, Message: "INFO [123 0ms] inbound/shadowsocks[entry]: [alice] inbound packet connection to " + destination},
				{Engine: core.EngineSingBox, Message: "INFO [123 0ms] outbound/direct[direct]: outbound packet connection to " + destination},
				{Engine: core.EngineMihomo, Message: fmt.Sprintf("[UDP] 8.8.8.8:%d --> %s match Match using DIRECT", 50123+i, destination)},
			} {
				entry.LoggedAt = start.Add(time.Duration(n*5+i) * time.Minute)
				entry.Level = "info"
				entries = append(entries, entry)
			}
		}
		if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", n+1), Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	q := ClientConnectionQuery{GroupByIP: true, Since: start.Add(-time.Minute), Until: time.Now(), Limit: 100, Bucket: "hour"}
	history, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(history.Records) != 4 || history.IPs != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	scopes := map[string]bool{}
	for _, r := range history.Records {
		key := r.AgentID + "/" + string(r.Engine)
		if r.ClientIP != "8.8.8.8" || scopes[key] || len(r.Endpoints) != 1 || r.Endpoints[0].AgentID != r.AgentID || r.Endpoints[0].Engine != r.Engine {
			t.Fatalf("wrong source or mixed scope: %+v", r)
		}
		scopes[key] = true
		expected := start
		if r.AgentID == second.ID {
			expected = expected.Add(5 * time.Minute)
		}
		if !r.FirstSeen.Equal(expected) || !r.LastSeen.Equal(expected.Add(2*time.Minute)) {
			t.Fatalf("cross-node times: %+v", r)
		}
	}
	q.Engine = core.EngineSingBox
	q.AgentID = second.ID
	history, err = db.ClientConnectionHistory(ctx, q)
	if err != nil || len(history.Records) != 1 || history.Records[0].AgentID != second.ID || history.Records[0].Engine != core.EngineSingBox {
		t.Fatalf("filtered history=%+v err=%v", history, err)
	}
}
