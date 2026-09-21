package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type connectionGeoTransport func(*http.Request) (*http.Response, error)

func (f connectionGeoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientConnectionGeographyPersistsAcrossServerRestart(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "connection-geo", core.EngineMihomo)
	report := clientConnectionFixtures{Status: "ok"}
	for i, ip := range []string{"8.8.8.8", "8.8.8.8", "1.1.1.1", "10.0.0.1", "9.9.9.9"} {
		report.Connections = append(report.Connections, core.ClientConnection{Engine: core.EngineMihomo, Protocol: "trojan", Inbound: "entry", Transport: "tcp", ClientIP: ip, ClientPort: 50123 + i, LocalIP: "192.0.2.1", LocalPort: 443})
	}
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	client := &http.Client{Transport: connectionGeoTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		body := `{"country_code":"US","country":"United States","region":"California"}`
		status := 200
		switch r.URL.Path {
		case "/v1/ip/geo/8.8.8.8.json":
			body = `{"country_code":"CN","country":"China","region":"Guangdong"}`
		case "/v1/ip/geo/1.1.1.1.json":
		case "/v1/ip/geo/9.9.9.9.json":
			status = 503
			body = "unavailable"
		default:
			t.Errorf("unexpected external lookup: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	for range 2 {
		// Recreate the GeoIP client too, so only the database can avoid new requests.
		admin.handler = New(db, Config{AdminToken: admin.token, GeoIPHTTPClient: client}).Handler()
		var history core.ClientConnectionHistory
		admin.call("GET", "/client-connections?include_non_public=true&agent_id="+agent.ID, nil, http.StatusOK, &history)
		if len(history.Records) != 5 {
			t.Fatalf("history unavailable: %+v", history)
		}
		for _, record := range history.Records {
			switch record.ClientIP {
			case "8.8.8.8":
				if record.Location.CountryCode != "CN" || record.Location.Province != "广东" {
					t.Fatalf("missing province: %+v", record.Location)
				}
			case "1.1.1.1":
				if record.Location.CountryCode != "US" || record.Location.Province != "" {
					t.Fatalf("foreign location: %+v", record.Location)
				}
			case "10.0.0.1":
				if !record.Location.NonPublic {
					t.Fatal("private address was not marked")
				}
			case "9.9.9.9":
				if record.Location.CountryCode != "" {
					t.Fatal("failed lookup invented a location")
				}
			}
		}
	}
	if len(calls) != 3 {
		t.Fatalf("provider calls=%v", calls)
	}
	for path, count := range calls {
		if count != 1 {
			t.Fatalf("cache/dedup failed for %s: %d", path, count)
		}
	}
	var hidden core.ClientConnectionHistory
	bob.call("GET", "/client-connections?include_non_public=true&agent_id="+agent.ID, nil, http.StatusOK, &hidden)
	if len(hidden.Records) != 0 {
		t.Fatalf("geography leaked across owners: %+v", hidden)
	}
}

func TestClientConnectionGeographyStopsOnCancellation(t *testing.T) {
	db, ctx, admin, _, _ := newConfigScopeAPIFixture(t)
	var calls atomic.Int32
	server := New(db, Config{AdminToken: admin.token, GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}})
	records := make([]core.ClientConnectionRecord, 32)
	for i := range records {
		records[i].ClientIP = fmt.Sprintf("8.8.4.%d", i+1)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	server.resolveClientConnectionLocations(requestCtx, records, false)
	if time.Since(started) > time.Second {
		t.Fatal("cancelled lookup delayed connection history")
	}
	if got := calls.Load(); got < 1 || got > 8 {
		t.Fatalf("lookup concurrency/cancellation: %d requests", got)
	}
}

func TestClientConnectionDeferredLocations(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "deferred-geo", core.EngineXray)
	report := clientConnectionFixtures{Status: "ok", Connections: []core.ClientConnection{{Engine: core.EngineXray, Protocol: "vless", Inbound: "entry", Transport: "tcp", ClientIP: "8.8.8.8", ClientPort: 50123, LocalIP: "192.0.2.1", LocalPort: 443}}}
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	handler := New(db, Config{AdminToken: admin.token, GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"country_code":"CN","country":"China","region":"Guangdong"}`)), Header: make(http.Header)}, nil
	})}}).Handler()
	admin.handler = handler
	var fast core.ClientConnectionHistory
	admin.call("GET", "/client-connections?locations=cached&agent_id="+agent.ID, nil, http.StatusOK, &fast)
	if calls.Load() != 0 || len(fast.Records) != 1 || fast.Flows != 1 || len(fast.Sources) != 1 || len(fast.Timeline) != 1 {
		t.Fatalf("initial read blocked on lookup or lost history: %+v calls=%d", fast, calls.Load())
	}
	var hidden core.ClientConnectionHistory
	bob.call("GET", "/client-connections?locations=only&agent_id="+agent.ID, nil, http.StatusOK, &hidden)
	if len(hidden.Records) != 0 || calls.Load() != 0 {
		t.Fatal("deferred enrichment bypassed authorization")
	}
	var enriched core.ClientConnectionHistory
	admin.call("GET", "/client-connections?locations=only&agent_id="+agent.ID, nil, http.StatusOK, &enriched)
	if len(enriched.Records) != 1 || enriched.Records[0].Location.Province != "广东" || calls.Load() != 1 {
		t.Fatalf("enrichment missing: %+v", enriched)
	}
	if enriched.Flows != 0 || len(enriched.Timeline) != 0 || len(enriched.Sources) != 0 {
		t.Fatal("enrichment repeated history aggregates")
	}
	admin.call("GET", "/client-connections?locations=cached&agent_id="+agent.ID, nil, http.StatusOK, &fast)
	if fast.Records[0].Location.Province != "广东" || calls.Load() != 1 {
		t.Fatal("cached location not reused")
	}
}
