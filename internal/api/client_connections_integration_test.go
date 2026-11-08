package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestClientConnectionCacheScopeRejectsConcurrentRevocation(t *testing.T) {
	for _, action := range []string{"hide-node", "disable-user", "logout"} {
		t.Run(action, func(t *testing.T) {
			db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "scope-race", core.EngineMihomo)
			if err := storeAPIConnectionLogs(ctx, db, agent.ID, clientConnectionFixtures{Connections: []core.ClientConnection{{ClientIP: "8.8.8.8", ClientPort: 50123}}}); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}, 1), make(chan struct{})
			handler := New(db, Config{AdminToken: admin.token, GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(r *http.Request) (*http.Response, error) {
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"country_code":"US"}`)), Header: make(http.Header)}, nil
			})}}).Handler()
			username, secret := "alice", "config scope password fixture"
			if action == "hide-node" {
				username, secret = "admin", admin.token
			}
			client, _ := managementLogin(t, handler, username, secret, http.StatusOK)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/client-connections?group_by=ip&timeline=false", nil)
			request.AddCookie(client.cookie)
			request.Header.Set(csrfHeader, client.csrf)
			response, done := httptest.NewRecorder(), make(chan struct{})
			go func() { defer close(done); handler.ServeHTTP(response, request) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("history did not reach held geography")
			}
			want := http.StatusConflict
			switch action {
			case "hide-node":
				if err := db.SetAgentAdminHidden(store.WithConfigScope(ctx, alice.userID, false), agent.ID, true); err != nil {
					t.Error(err)
				}
			case "disable-user":
				want = http.StatusUnauthorized
				if _, err := db.SetUserDisabled(ctx, alice.userID, true); err != nil {
					t.Error(err)
				}
			case "logout":
				client.call("POST", "/auth/logout", nil, http.StatusNoContent, nil)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("revoked history did not finish")
			}
			if response.Code != want || strings.Contains(response.Body.String(), "8.8.8.8") || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("concurrent revocation returned protected rows: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestClientConnectionCacheScope(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	getScope := func(client configScopeAPIClient) string {
		t.Helper()
		var result struct{ Scope string }
		client.call("GET", "/client-connections?scope_only=true", nil, http.StatusOK, &result)
		if len(result.Scope) != 64 || strings.Contains(result.Scope, client.csrf) {
			t.Fatalf("invalid opaque scope: %q", result.Scope)
		}
		return result.Scope
	}
	// Environment-token logins have a genuine per-login scope too; unlike
	// preference IDs, it never falls back to a username or role string.
	administrator, _ := managementLogin(t, admin.handler, "admin", "config-scope-administrator-fixture", http.StatusOK)
	a0, b0, admin0 := getScope(alice), getScope(bob), getScope(administrator)
	if a0 == b0 || a0 == admin0 {
		t.Fatal("different authenticated principals share a cache scope")
	}
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "scope-owner", core.EngineMihomo)
	a1, admin1 := getScope(alice), getScope(administrator)
	if a1 == a0 || admin1 == admin0 || getScope(bob) != b0 {
		t.Fatal("scope does not follow host administration authority")
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 23450)
	if getScope(bob) != b0 {
		t.Fatal("shared engine use expanded host telemetry scope")
	}
	var history struct {
		core.ClientConnectionHistory
		CacheScope string `json:"cache_scope"`
	}
	alice.call("GET", "/client-connections?group_by=ip&timeline=false&locations=cached", nil, http.StatusOK, &history)
	if history.CacheScope != a1 || len(history.Sources) != 1 {
		t.Fatalf("history and lightweight scope disagree: %+v", history)
	}
	if err := db.SetAgentAdminHidden(store.WithConfigScope(ctx, alice.userID, false), agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if getScope(administrator) != admin0 || getScope(alice) != a1 {
		t.Fatal("owner-hidden node did not invalidate the administrator alone")
	}
	otherLogin, _ := managementLogin(t, alice.handler, "alice", "config scope password fixture", http.StatusOK)
	if getScope(otherLogin) == a1 {
		t.Fatal("another login inherited the original session cache authority")
	}
	alice.call("POST", "/auth/logout", nil, http.StatusNoContent, nil)
	alice.call("GET", "/client-connections?scope_only=true", nil, http.StatusUnauthorized, nil)
	if _, err := db.SetUserDisabled(ctx, bob.userID, true); err != nil {
		t.Fatal(err)
	}
	bob.call("GET", "/client-connections?scope_only=true", nil, http.StatusUnauthorized, nil)
	t.Logf("scope authority checks completed in isolated fixture at %s", time.Now().UTC().Format(time.RFC3339))
}

func TestClientConnectionAPIIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "connection-owner", core.EngineMihomo)
	report := clientConnectionFixtures{Status: "ok", Connections: []core.ClientConnection{{Engine: core.EngineMihomo, Protocol: "trojan", Inbound: "entry", Transport: "tcp", ClientIP: "198.51.100.9", ClientPort: 50123, LocalIP: "192.0.2.1", LocalPort: 443}}}
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	for _, client := range []configScopeAPIClient{admin, alice} {
		var result core.ClientConnectionHistory
		for _, suffix := range []string{"", "&include_non_public=false"} {
			client.call("GET", "/client-connections?agent_id="+agent.ID+suffix, nil, http.StatusOK, &result)
			if result.Flows != 0 || result.IPs != 0 || len(result.Records) != 0 || len(result.Timeline) != 0 {
				t.Fatalf("non-public connection shown by default: %+v", result)
			}
		}
		client.call("GET", "/client-connections?agent_id="+agent.ID+"&include_non_public=true&client_ip=198.51.100.9", nil, http.StatusOK, &result)
		if result.Flows != 1 || len(result.Records) != 1 || result.Records[0].ClientIP != "198.51.100.9" {
			t.Fatalf("missing connection: %+v", result)
		}
	}
	var result core.ClientConnectionHistory
	bob.call("GET", "/client-connections?include_non_public=true&agent_id="+agent.ID, nil, http.StatusOK, &result)
	if result.Flows != 0 || len(result.Records) != 0 || len(result.Sources) != 0 {
		t.Fatalf("cross-account leak: %+v", result)
	}
}

func TestClientConnectionCountryFilter(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	owned, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "country-owner", core.EngineMihomo)
	other, _ := ownedConfigScopeAPIAgent(t, ctx, db, bob, "country-other", core.EngineMihomo)
	grantConfigScopeAPIAgent(t, ctx, db, owned, bob, 23450)
	for index, item := range []struct {
		agent string
		ips   []string
	}{{owned.ID, []string{"8.8.8.8", "8.8.8.8", "9.9.9.9", "1.1.1.1", "8.8.4.4", "10.0.0.1"}}, {other.ID, []string{"1.0.0.1"}}} {
		batch := core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", 0xcf000+index)}
		for i, ip := range item.ips {
			batch.Entries = append(batch.Entries, core.CoreLogEntry{Engine: core.EngineMihomo, Level: "info", Message: fmt.Sprintf("[TCP] %s --> example.invalid:443 using DIRECT", net.JoinHostPort(ip, strconv.Itoa(50000+i)))})
		}
		if err := db.StoreCoreLogs(ctx, item.agent, batch); err != nil {
			t.Fatal(err)
		}
	}
	var locations []store.ClientConnectionLocation
	for ip, code := range map[string]string{"8.8.8.8": "US", "9.9.9.9": "US", "1.1.1.1": "AU", "10.0.0.1": "US", "1.0.0.1": "US"} {
		locations = append(locations, store.ClientConnectionLocation{IP: ip, ClientIPLocation: core.ClientIPLocation{CountryCode: code}, RetryAfter: time.Now().Add(time.Hour)})
	}
	if err := db.SaveClientConnectionLocations(ctx, locations); err != nil {
		t.Fatal(err)
	}
	path := "/client-connections?country_code=us&include_non_public=true&locations=cached"
	for _, grouped := range []bool{false, true} {
		query := path
		if grouped {
			query += "&group_by=ip"
		}
		for _, test := range []struct {
			client configScopeAPIClient
			ips    int64
			flows  int64
		}{{alice, 2, 3}, {admin, 3, 4}, {bob, 1, 1}} {
			var result core.ClientConnectionHistory
			test.client.call("GET", query, nil, http.StatusOK, &result)
			if result.IPs != test.ips || result.Flows != test.flows || len(result.Timeline) != 1 || result.Timeline[0].Flows != test.flows {
				t.Fatalf("country summary/timeline grouped=%v: %+v", grouped, result)
			}
			for _, row := range result.Records {
				if row.Location.CountryCode != "US" || row.ClientIP == "10.0.0.1" || row.ClientIP == "8.8.4.4" || (test.client.userID == bob.userID && row.AgentID != other.ID) {
					t.Fatalf("country or shared-owner isolation failed: %+v", row)
				}
			}
		}
	}
	var first, second, empty core.ClientConnectionHistory
	query := path + "&group_by=ip&timeline=false&limit=1"
	alice.call("GET", query, nil, http.StatusOK, &first)
	alice.call("GET", query+"&cursor="+first.NextCursor, nil, http.StatusOK, &second)
	if len(first.Records) != 1 || len(second.Records) != 1 || first.Records[0].ID == second.Records[0].ID || first.NextCursor == "" || second.NextCursor != "" || first.Flows != second.Flows {
		t.Fatalf("country pagination: %+v / %+v", first, second)
	}
	alice.call("GET", "/client-connections?country_code=CN&locations=cached", nil, http.StatusOK, &empty)
	if empty.IPs != 0 || len(empty.Records) != 0 {
		t.Fatalf("unknown countries matched: %+v", empty)
	}
	bob.call("GET", query+"&agent_id="+owned.ID, nil, http.StatusOK, &empty)
	if empty.IPs != 0 || len(empty.Records) != 0 || len(empty.Sources) != 0 {
		t.Fatalf("country filter expanded shared host authority: %+v", empty)
	}
	if err := db.SetAgentAdminHidden(store.WithConfigScope(ctx, alice.userID, false), owned.ID, true); err != nil {
		t.Fatal(err)
	}
	admin.call("GET", path, nil, http.StatusOK, &empty)
	if empty.IPs != 1 || len(empty.Records) != 1 || empty.Records[0].AgentID != other.ID {
		t.Fatalf("country filter bypassed hidden node: %+v", empty)
	}
	permissions := []core.Permission{core.PermissionCoreLogsRead}
	admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
	alice, _ = managementLogin(t, alice.handler, "alice", "config scope password fixture", http.StatusOK)
	alice.call("GET", path, nil, http.StatusOK, &empty)
	if empty.IPs != 2 {
		t.Fatalf("logs-only country filter: %+v", empty)
	}
	alice.call("GET", "/regions", nil, http.StatusOK, nil)
	alice.call("GET", "/agents", nil, http.StatusForbidden, nil)
}

func TestClientConnectionCountrySnapshotDuringEnrichment(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "country-reclassified", core.EngineMihomo)
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, clientConnectionFixtures{Connections: []core.ClientConnection{{ClientIP: "8.8.8.8", ClientPort: 50000}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveClientConnectionLocations(ctx, []store.ClientConnectionLocation{{IP: "8.8.8.8", ClientIPLocation: core.ClientIPLocation{CountryCode: "CN"}, RetryAfter: time.Now().Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	handler := New(db, Config{AdminToken: admin.token, GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"country_code":"US"}`)), Header: make(http.Header)}, nil
	})}}).Handler()
	client, _ := managementLogin(t, handler, "alice", "config scope password fixture", http.StatusOK)
	var snapshot, latest core.ClientConnectionHistory
	client.call("GET", "/client-connections?country_code=CN&group_by=ip", nil, http.StatusOK, &snapshot)
	if snapshot.IPs != 1 || len(snapshot.Records) != 1 || snapshot.Records[0].Location.CountryCode != "CN" {
		t.Fatalf("enrichment changed membership without recomputing totals: %+v", snapshot)
	}
	client.call("GET", "/client-connections?country_code=CN&group_by=ip", nil, http.StatusOK, &latest)
	if latest.IPs != 0 || len(latest.Records) != 0 {
		t.Fatalf("next read ignored refreshed geography: %+v", latest)
	}
	client.call("GET", "/client-connections?country_code=US&group_by=ip", nil, http.StatusOK, &latest)
	if latest.IPs != 1 || len(latest.Records) != 1 || latest.Records[0].Location.CountryCode != "US" {
		t.Fatalf("refreshed country missing: %+v", latest)
	}
}

func TestClientConnectionAPIGrouping(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "group-owner", core.EngineMihomo)
	report := clientConnectionFixtures{Status: "ok", Connections: []core.ClientConnection{
		{ClientIP: "198.51.100.9", ClientPort: 50123},
		{ClientIP: "198.51.100.9", ClientPort: 50124},
	}}
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	path := "/client-connections?group_by=ip&include_non_public=true&agent_id=" + agent.ID
	for _, client := range []configScopeAPIClient{admin, alice} {
		var result, enriched core.ClientConnectionHistory
		client.call("GET", path+"&locations=cached", nil, http.StatusOK, &result)
		if result.Flows != 2 || result.IPs != 1 || len(result.Records) != 1 || len(result.Records[0].Endpoints) != 1 || result.Records[0].Endpoints[0].AgentID != agent.ID {
			t.Fatalf("grouped API result=%+v", result)
		}
		var withoutTimeline core.ClientConnectionHistory
		client.call("GET", path+"&locations=cached&timeline=false", nil, http.StatusOK, &withoutTimeline)
		if withoutTimeline.Flows != result.Flows || withoutTimeline.IPs != result.IPs || len(withoutTimeline.Records) != len(result.Records) || len(withoutTimeline.Timeline) != 0 {
			t.Fatalf("timeline-free API result=%+v", withoutTimeline)
		}
		client.call("GET", path+"&locations=only&cursor="+result.PageCursor, nil, http.StatusOK, &enriched)
		if len(enriched.Records) != 1 || enriched.Records[0].ID != result.Records[0].ID || !enriched.Records[0].Location.NonPublic || len(enriched.Records[0].Endpoints) != 0 {
			t.Fatalf("grouped location result=%+v", enriched)
		}
	}
	var denied core.ClientConnectionHistory
	bob.call("GET", path, nil, http.StatusOK, &denied)
	if len(denied.Records) != 0 || len(denied.Sources) != 0 {
		t.Fatalf("grouped API leaked data: %+v", denied)
	}
}

// Exercise the same panel log ingestion path used in production.
func storeAPIConnectionLogs(ctx context.Context, db *store.Store, agentID string, report clientConnectionFixtures) error {
	batch := core.CoreLogBatch{ID: "log_1123456789abcdef"}
	for _, c := range report.Connections {
		batch.Entries = append(batch.Entries, core.CoreLogEntry{Engine: core.EngineMihomo, Level: "info", Message: fmt.Sprintf("[TCP] %s --> example.invalid:443 using DIRECT", net.JoinHostPort(c.ClientIP, strconv.Itoa(c.ClientPort)))})
	}
	return db.StoreCoreLogs(ctx, agentID, batch)
}

type clientConnectionFixtures struct {
	Connections []core.ClientConnection `json:"connections"`
	Status      string                  `json:"status"`
}
