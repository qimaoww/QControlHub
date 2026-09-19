package store

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionLocationsPersistRetryAndPrune(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	if err := db.StoreClientConnections(ctx, agent.ID, core.ClientConnectionReport{Status: "ok", Connections: []core.ClientConnection{{Engine: core.EngineXray, Protocol: "vless", Inbound: "entry", Transport: "tcp", ClientIP: "8.8.8.8", ClientPort: 50123, LocalIP: "192.0.2.1", LocalPort: 443}}}); err != nil {
		t.Fatal(err)
	}
	next := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Microsecond)
	item := ClientConnectionLocation{IP: "8.8.8.8", RetryAfter: next, ClientIPLocation: core.ClientIPLocation{CountryCode: "CN", Country: "China", Province: "广东"}}
	if err := db.SaveClientConnectionLocations(ctx, []ClientConnectionLocation{item}); err != nil {
		t.Fatal(err)
	}
	locations, err := db.ClientConnectionLocations(ctx, []string{"8.8.8.8", "1.1.1.1"})
	if err != nil || len(locations) != 1 || locations[item.IP].Province != "广东" || !locations[item.IP].RetryAfter.Equal(next) {
		t.Fatalf("cached=%+v %v", locations, err)
	}
	// A refresh failure preserves previously resolved geography across restarts.
	retry := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Microsecond)
	if err := db.SaveClientConnectionLocations(ctx, []ClientConnectionLocation{{IP: item.IP, RetryAfter: retry}}); err != nil {
		t.Fatal(err)
	}
	locations, err = db.ClientConnectionLocations(ctx, []string{item.IP})
	if err != nil || locations[item.IP].Province != "广东" || !locations[item.IP].RetryAfter.Equal(retry) {
		t.Fatalf("retry cleared persisted location: %+v %v", locations, err)
	}
	item.CountryCode = "US"
	item.Country = "United States"
	item.Province = ""
	if err := db.SaveClientConnectionLocations(ctx, []ClientConnectionLocation{item}); err != nil {
		t.Fatal(err)
	}
	locations, err = db.ClientConnectionLocations(ctx, []string{item.IP})
	if err != nil || locations[item.IP].CountryCode != "US" || locations[item.IP].Province != "" {
		t.Fatalf("refresh kept Chinese province for foreign IP: %+v %v", locations, err)
	}
	if err := db.PruneClientConnectionLocations(ctx); err != nil {
		t.Fatal(err)
	}
	locations, _ = db.ClientConnectionLocations(ctx, []string{item.IP})
	if len(locations) != 1 {
		t.Fatal("pruned referenced IP")
	}
	if err := db.PruneClientConnections(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneClientConnectionLocations(ctx); err != nil {
		t.Fatal(err)
	}
	locations, err = db.ClientConnectionLocations(ctx, []string{item.IP})
	if err != nil || len(locations) != 0 {
		t.Fatalf("orphan cache not pruned: %+v %v", locations, err)
	}
}
