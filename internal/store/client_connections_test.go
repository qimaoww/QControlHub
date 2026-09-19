package store

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"testing"
	"time"
)

func TestClientConnectionHistoryPersistenceAndIsolation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner, ownerCtx := sharedTestUser(t, db, ctx, "connection-owner")
	_, strangerCtx := sharedTestUser(t, db, ctx, "connection-stranger")
	recipient, recipientCtx := sharedTestUser(t, db, ctx, "connection-recipient")
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id=$2 WHERE id=$1`, agent.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	sharedTestAllocation(t, db, ctx, recipient.ID, agent.ID, 1000, 443)
	first := core.ClientConnection{Engine: core.EngineMihomo, Protocol: "trojan", Inbound: "entry", Transport: "tcp", ClientIP: "198.51.100.9", ClientPort: 50123, LocalIP: "192.0.2.1", LocalPort: 443}
	second := first
	second.ClientIP = "2001:db8::9"
	second.Transport = "udp"
	second.ClientPort = 50124
	report := core.ClientConnectionReport{Status: "partial", Detail: "fixture partial coverage", Connections: []core.ClientConnection{first, first, second}}
	for i := 0; i < 2; i++ {
		if err := db.StoreClientConnections(ctx, agent.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	query := ClientConnectionQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 1, Bucket: "minute"}
	result, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || result.Flows != 2 || result.IPs != 2 || len(result.Records) != 1 || result.NextBefore == 0 || len(result.Timeline) != 1 || result.Timeline[0].Flows != 2 || len(result.Sources) != 1 || result.Sources[0].Status != "partial" {
		t.Fatalf("history=%+v err=%v", result, err)
	}
	firstID := result.Records[0].ID
	query.Before = result.NextBefore
	next, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || len(next.Records) != 1 || next.Records[0].ID == firstID || next.NextBefore != 0 || next.Flows != 2 {
		t.Fatalf("pagination=%+v err=%v", next, err)
	}
	query.Before = 0
	query.ClientIP = first.ClientIP
	query.Engine = core.EngineMihomo
	query.Protocol = "trojan"
	query.Inbound = "entry"
	query.Port = 443
	query.Transport = "tcp"
	filtered, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || filtered.Flows != 1 || filtered.IPs != 1 || len(filtered.Records) != 1 || filtered.Records[0].ClientIP != first.ClientIP {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	query.Inbound = "absent"
	empty, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || empty.Flows != 0 || len(empty.Timeline) != 0 {
		t.Fatalf("inbound filter=%+v %v", empty, err)
	}
	query.Inbound = "entry"
	for _, scope := range []struct {
		name        string
		isRecipient bool
	}{{"stranger", false}, {"shared recipient", true}} {
		scoped := strangerCtx
		if scope.isRecipient {
			scoped = recipientCtx
		}
		hidden, err := db.ClientConnectionHistory(scoped, query)
		if err != nil || hidden.Flows != 0 || len(hidden.Records) != 0 || len(hidden.Timeline) != 0 || len(hidden.Sources) != 0 {
			t.Fatalf("%s leaked client IPs: %+v %v", scope.name, hidden, err)
		}
	}
	// Owner-hidden hosts stay private from fleet administrators, including status.
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET admin_hidden=true WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	hidden, err := db.ClientConnectionHistory(WithConfigScope(ctx, "", true), query)
	if err != nil || hidden.Flows != 0 || len(hidden.Sources) != 0 {
		t.Fatalf("hidden host leaked: %+v %v", hidden, err)
	}
	// A later empty sample cannot erase durable history.
	if err := db.StoreClientConnections(ctx, agent.ID, core.ClientConnectionReport{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || remaining.Flows != 1 || remaining.Sources[0].Status != "ok" {
		t.Fatalf("empty sample erased history: %+v %v", remaining, err)
	}
	invalid := report
	invalid.Connections = []core.ClientConnection{first}
	invalid.Connections[0].ClientPort = 0
	if err := db.StoreClientConnections(ctx, agent.ID, invalid); err == nil {
		t.Fatal("invalid report accepted")
	}
	if err := db.PruneClientConnections(ctx, now.Add(-core.ClientConnectionRetention)); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneClientConnections(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pruned, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || pruned.Flows != 0 || len(pruned.Records) != 0 {
		t.Fatalf("pruned=%+v %v", pruned, err)
	}
}
