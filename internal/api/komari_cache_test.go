package api

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestKomariNodeCacheServesStaleAndRefreshesOnce(t *testing.T) {
	server := &Server{}
	now := time.Now()
	node := core.KomariNode{UUID: "komari-alpha", TrafficUsed: 42}

	if _, ok := server.peekKomariNode("alpha", "komari-alpha"); ok {
		t.Fatal("an empty cache must not answer")
	}
	if _, ok := server.freshKomariNode("alpha", "komari-alpha", now); ok {
		t.Fatal("an empty cache has no fresh entry")
	}
	server.storeKomariNode("alpha", "komari-alpha", node, now)

	cached, ok := server.peekKomariNode("alpha", "komari-alpha")
	if !ok || cached.TrafficUsed != 42 {
		t.Fatalf("cached resource not served: %+v ok=%v", cached, ok)
	}
	if _, ok := server.peekKomariNode("alpha", "other-uuid"); ok {
		t.Fatal("a rebound node must not serve the previous server's traffic")
	}
	if _, ok := server.freshKomariNode("alpha", "komari-alpha", now.Add(komariNodeCacheTTL-time.Second)); !ok {
		t.Fatal("an entry inside its TTL is fresh")
	}
	if _, ok := server.freshKomariNode("alpha", "komari-alpha", now.Add(komariNodeCacheTTL)); ok {
		t.Fatal("an expired entry is not fresh")
	}

	// A fresh entry needs no provider read, and only one caller at a time may
	// claim the refresh of a stale one: an external read costs about a second.
	stale := now.Add(komariNodeCacheTTL + time.Second)
	if server.claimKomariRefresh("alpha", "komari-alpha", now) {
		t.Fatal("a fresh entry must not trigger a provider read")
	}
	if !server.claimKomariRefresh("alpha", "komari-alpha", stale) {
		t.Fatal("a stale entry must trigger a provider read")
	}
	if server.claimKomariRefresh("alpha", "komari-alpha", stale) {
		t.Fatal("a claimed entry must not start a second provider read")
	}
	if _, ok := server.peekKomariNode("alpha", "komari-alpha"); !ok {
		t.Fatal("a refresh claim must keep serving the stale value")
	}

	// A failed refresh releases the claim so the next render can retry.
	server.releaseKomariRefresh("alpha", "komari-alpha")
	if !server.claimKomariRefresh("alpha", "komari-alpha", stale) {
		t.Fatal("a released claim must be retryable")
	}
	server.storeKomariNode("alpha", "komari-alpha", core.KomariNode{UUID: "komari-alpha", TrafficUsed: 84}, stale)
	if _, ok := server.freshKomariNode("alpha", "komari-alpha", stale.Add(time.Second)); !ok {
		t.Fatal("a stored answer must be fresh again")
	}
	if server.claimKomariRefresh("alpha", "komari-alpha", stale.Add(time.Second)) {
		t.Fatal("a refreshed entry must not keep reading the provider")
	}

	server.forgetKomariNode("alpha")
	if _, ok := server.peekKomariNode("alpha", "komari-alpha"); ok {
		t.Fatal("forgetting a node must drop its cached traffic")
	}
}
