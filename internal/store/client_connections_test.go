package store

import (
	"net/netip"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

func TestClientConnectionHistoryDefaultsToPublicSources(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	addresses := []string{"8.8.8.8", "2606:4700:4700::1111", "::ffff:1.1.1.1", "::ffff:192.168.1.1", "172.16.1.1", "192.168.1.1", "127.0.0.1", "100.127.255.255", "fe80::1234", "fd12::1"}
	for _, prefix := range netpolicy.NonPublicPrefixes() {
		address := prefix.Addr()
		if address.IsUnspecified() || address.IsMulticast() {
			continue // These cannot enter history: the log parser rejects them.
		}
		addresses = append(addresses, address.String())
	}
	report := clientConnectionFixtures{Status: "ok"}
	publicCount := 0
	for i, ip := range addresses {
		if netpolicy.IsPublicAddress(netip.MustParseAddr(ip)) {
			publicCount++
		}
		report.Connections = append(report.Connections, core.ClientConnection{Engine: core.EngineXray, Protocol: "vless", Inbound: "entry", Transport: "tcp", ClientIP: ip, ClientPort: 50000 + i, LocalIP: "127.0.0.1", LocalPort: 443})
	}
	if err := db.storeClientConnectionFixtures(ctx, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	query := ClientConnectionQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 2, Bucket: "hour"}
	result, err := db.ClientConnectionHistory(ctx, query)
	if err != nil || result.Flows != int64(publicCount) || result.IPs != int64(publicCount) || len(result.Records) != 2 || result.NextBefore == 0 || len(result.Timeline) != 1 || result.Timeline[0].Flows != int64(publicCount) {
		t.Fatalf("public history=%+v err=%v", result, err)
	}
	for _, row := range result.Records {
		if !netpolicy.IsPublicAddress(netip.MustParseAddr(row.ClientIP)) {
			t.Fatalf("non-public source leaked: %+v", row)
		}
	}
	query.Before = result.NextBefore
	next, err := db.ClientConnectionHistory(ctx, query)
	if err != nil || len(next.Records) != 1 || next.NextBefore != 0 || next.Flows != int64(publicCount) || next.Records[0].ClientIP != "8.8.8.8" {
		t.Fatalf("public pagination=%+v err=%v", next, err)
	}
	query.Before = 0
	query.ClientIP = "::ffff:192.168.1.1"
	private, err := db.ClientConnectionHistory(ctx, query)
	if err != nil || private.Flows != 0 || len(private.Records) != 0 || len(private.Timeline) != 0 {
		t.Fatalf("private exact search bypassed default: %+v %v", private, err)
	}
	query.ClientIP = ""
	query.IncludeNonPublic = true
	query.Limit = 200
	all, err := db.ClientConnectionHistory(ctx, query)
	if err != nil || len(all.Records) != len(addresses) || all.Flows != int64(len(addresses)) || len(all.Timeline) != 1 || all.Timeline[0].Flows != all.Flows {
		t.Fatalf("opt-in history=%+v err=%v", all, err)
	}
}

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
	report := clientConnectionFixtures{Status: "partial", Detail: "fixture partial coverage", Connections: []core.ClientConnection{first, first, second}}
	for i := 0; i < 2; i++ {
		if err := db.storeClientConnectionFixtures(ctx, agent.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	query := ClientConnectionQuery{IncludeNonPublic: true, Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 1, Bucket: "minute"}
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
	// A later status update without observations cannot erase durable history.
	if err := db.storeClientConnectionFixtures(ctx, agent.ID, clientConnectionFixtures{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.ClientConnectionHistory(ownerCtx, query)
	if err != nil || remaining.Flows != 1 || remaining.Sources[0].Status != "ok" {
		t.Fatalf("empty sample erased history: %+v %v", remaining, err)
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
