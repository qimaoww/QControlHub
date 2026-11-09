package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestClientConnectionSyncCookieIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "sync-alice", core.EngineMihomo)
	b, _ := ownedConfigScopeAPIAgent(t, ctx, db, bob, "sync-bob", core.EngineMihomo)
	for _, agent := range []core.Agent{a, b} {
		id, _ := core.NewID("log")
		if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "[TCP] 8.8.8.8:50123 --> example.invalid:443 using DIRECT"}}}); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(db, Config{AdminToken: admin.token}).Handler()
	ac, _ := managementLogin(t, handler, "alice", "config scope password fixture", http.StatusOK)
	bc, _ := managementLogin(t, handler, "bob", "config scope password fixture", http.StatusOK)
	manager, _ := managementLogin(t, handler, "admin", admin.token, http.StatusOK)
	q := url.Values{"sync": {"true"}, "group_by": {"ip"}, "timeline": {"false"}, "locations": {"cached"}, "since": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, "until": {time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
	var first, update, foreign, private core.ClientConnectionHistory
	ac.call("GET", "/client-connections?"+q.Encode(), nil, http.StatusOK, &first)
	if len(first.Records) != 1 || first.Records[0].AgentID != a.ID || first.SyncToken == "" {
		t.Fatalf("invalid cold session: %+v", first)
	}
	q.Set("sync_token", first.SyncToken)
	ac.call("GET", "/client-connections?"+q.Encode(), nil, http.StatusOK, &update)
	if !update.Delta || len(update.Records) != 0 || update.IPs != 1 {
		t.Fatalf("invalid delta: %+v", update)
	}
	bc.call("GET", "/client-connections?"+q.Encode(), nil, http.StatusOK, &foreign)
	if foreign.Delta || len(foreign.Records) != 1 || foreign.Records[0].AgentID != b.ID {
		t.Fatalf("cross-login baseline: %+v", foreign)
	}
	q.Set("agent_id", a.ID)
	bc.call("GET", "/client-connections?"+q.Encode(), nil, http.StatusOK, &private)
	if len(private.Records) != 0 || private.Flows != 0 {
		t.Fatalf("forged node: %+v", private)
	}
	if err := db.SetAgentAdminHidden(store.WithConfigScope(ctx, alice.userID, false), a.ID, true); err != nil {
		t.Fatal(err)
	}
	manager.call("GET", "/client-connections?"+q.Encode(), nil, http.StatusOK, &private)
	if len(private.Records) != 0 {
		t.Fatalf("administrator private host bypass: %+v", private)
	}
	if _, err := db.SetUserDisabled(ctx, alice.userID, true); err != nil {
		t.Fatal(err)
	}
	ac.call("GET", "/client-connections?"+q.Encode(), nil, http.StatusUnauthorized, nil)
}
