package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestClientConnectionConditionalReadChecksCurrentAuthority(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "conditional-alice", core.EngineMihomo)
	at := time.Now().UTC()
	id, _ := core.NewID("log")
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", LoggedAt: at, Message: "[TCP] 8.8.8.8:50123 --> example.invalid:443 using DIRECT"}}}); err != nil {
		t.Fatal(err)
	}
	h := New(db, Config{AdminToken: admin.token}).Handler()
	manager, _ := managementLogin(t, h, "admin", admin.token, http.StatusOK)
	q := url.Values{"sync": {"true"}, "group_by": {"ip"}, "timeline": {"false"}, "locations": {"cached"},
		"since": {at.Add(-time.Hour).Format(time.RFC3339)}, "until": {at.Add(time.Hour).Format(time.RFC3339)}}
	fetch := func(client configScopeAPIClient, etag string, want int) (*httptest.ResponseRecorder, core.ClientConnectionHistory) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/client-connections?"+q.Encode(), nil)
		req.AddCookie(client.cookie)
		req.Header.Set(csrfHeader, client.csrf)
		req.Header.Set("If-None-Match", etag)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("status %d want %d: %s", res.Code, want, res.Body)
		}
		var result core.ClientConnectionHistory
		if want == http.StatusOK {
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return res, result
	}
	first, full := fetch(manager, "", http.StatusOK)
	tag := first.Header().Get("ETag")
	if tag == "" || len(full.Records) != 1 {
		t.Fatal("missing authorized entity")
	}
	q.Set("sync_token", full.SyncToken)
	confirmed, _ := fetch(manager, tag, http.StatusNotModified)
	if confirmed.Body.Len() != 0 || confirmed.Header().Get("ETag") != tag {
		t.Fatal("invalid 304")
	}
	relogin, _ := managementLogin(t, h, "admin", admin.token, http.StatusOK)
	fetch(relogin, tag, http.StatusNotModified)
	if err := db.SetAgentAdminHidden(store.WithConfigScope(ctx, alice.userID, false), agent.ID, true); err != nil {
		t.Fatal(err)
	}
	_, restricted := fetch(manager, tag, http.StatusOK)
	if len(restricted.Records) != 0 || restricted.Flows != 0 || restricted.Delta {
		t.Fatalf("revoked history used previous entity: %+v", restricted)
	}
}
