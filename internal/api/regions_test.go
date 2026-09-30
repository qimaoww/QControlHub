package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestRegionAPIValidationAndPermissions(t *testing.T) {
	admin, reader := strings.Repeat("a", 48), strings.Repeat("r", 48)
	handler := New(nil, Config{AdminToken: admin, ReadonlyTokens: []string{reader}}).Handler()
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"GET", "/api/v1/regions", "", "", 401},
		{"GET", "/api/v1/regions", reader, "", 200},
		{"PUT", "/api/v1/agents/node/region", "", `{"country_code":"US"}`, 401},
		{"PUT", "/api/v1/agents/node/region", reader, `{"country_code":"US"}`, 403},
		{"PUT", "/api/v1/agents/node/region", admin, `{}`, 400},
		{"PUT", "/api/v1/agents/node/region", admin, `{"country_code":null}`, 400},
		{"PUT", "/api/v1/agents/node/region", admin, `{"country_code":1}`, 400},
		{"PUT", "/api/v1/agents/node/region", admin, `{"country_code":"ZZ"}`, 400},
		{"PUT", "/api/v1/agents/node/region", admin, `{"country_code":"../cn"}`, 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s %s: %d %s", tc.method, tc.path, tc.body, w.Code, w.Body.String())
		}
		if tc.method == "GET" && w.Code == 200 {
			var codes []string
			if err := json.Unmarshal(w.Body.Bytes(), &codes); err != nil || len(codes) != len(geoip.RegionCodes()) {
				t.Fatalf("region catalog: %s, %v", w.Body.String(), err)
			}
		}
	}
}

func TestAgentRegionPreferenceLifecycle(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	credential, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "region"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "region", OS: "linux", Arch: "amd64", PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)), Labels: map[string]string{"custom": "keep"}}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentClientAddress(ctx, agent.ID, "edge.example.test"); err != nil {
		t.Fatal(err)
	}
	admin := strings.Repeat("a", 48)
	handler := New(db, Config{AdminToken: admin}).Handler()
	endpoint := "/api/v1/agents/" + agent.ID + "/region"
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+admin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	// No public IP is required for a manually selected flag, even offline.
	for _, code := range []string{" sg ", "JP", "TW", ""} {
		request("PUT", endpoint, `{"country_code":"`+code+`"}`, 200)
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
			t.Fatal(err)
		}
		loaded, err := db.GetAgent(ctx, agent.ID)
		if err != nil || loaded.Labels["custom"] != "keep" || loaded.Labels["client_address"] != "edge.example.test" || store.AgentRegionCode(loaded) != strings.ToUpper(strings.TrimSpace(code)) {
			t.Fatalf("persisted labels: %v, %v", loaded.Labels, err)
		}
		w := request("GET", endpoint, "", 200)
		var region map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &region); err != nil {
			t.Fatal(err)
		}
		if region["country_code"] != store.AgentRegionCode(loaded) || (code != "" && region["source"] != "manual") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("resolved region: %v", region)
		}
		if code == "" {
			if _, exists := loaded.Labels["region_code"]; exists {
				t.Fatal("automatic mode must remove the label")
			}
		}
	}
	// Preference writes retain the existing browser CSRF protection.
	login := request("POST", "/api/v1/auth/login", `{"token":"`+admin+`"}`, 200)
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	for _, csrf := range []string{"", session.CSRFToken} {
		r := httptest.NewRequest("PUT", endpoint, strings.NewReader(`{"country_code":"US"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(login.Result().Cookies()[0])
		r.Header.Set(csrfHeader, csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := http.StatusOK
		if csrf == "" {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Fatalf("CSRF: %d %s", w.Code, w.Body.String())
		}
	}
	request("PUT", "/api/v1/agents/missing/region", `{"country_code":"US"}`, 404)
	request("DELETE", "/api/v1/agents/"+agent.ID, "", 204)
	request("PUT", endpoint, `{"country_code":"US"}`, 404)
}

func regionTestAgents() []core.Agent {
	return []core.Agent{
		{ID: "alpha", Metrics: core.HostMetrics{PublicIPv4: "8.8.8.8"}},
		{ID: "bravo", Metrics: core.HostMetrics{PublicIPv4: "1.1.1.1"}},
		{ID: "charlie", Metrics: core.HostMetrics{PublicIPv4: "9.9.9.9"}},
	}
}

func regionTestRequest(t *testing.T, token string) *http.Request {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/v1/agents", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func regionTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// Every panel page loads the node list, so automatic regions must resolve in
// one provider round trip instead of one round trip per node.
func TestAgentListRegionsResolveConcurrently(t *testing.T) {
	const lookupDelay = 150 * time.Millisecond
	admin := strings.Repeat("a", 48)
	handler := New(nil, Config{
		AdminToken: admin,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			time.Sleep(lookupDelay)
			return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
		})},
	})
	agents := regionTestAgents()
	started := time.Now()
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	elapsed := time.Since(started)
	if elapsed >= time.Duration(len(agents))*lookupDelay {
		t.Fatalf("region resolution took %s for %d nodes; lookups must run concurrently", elapsed, len(agents))
	}
	for _, agent := range agents {
		if agent.RegionCode != "US" {
			t.Fatalf("agent %s region = %q, want US", agent.ID, agent.RegionCode)
		}
	}
}

// A provider that never answers must not hold the node list open.
func TestAgentListRegionsBoundSlowProvider(t *testing.T) {
	admin := strings.Repeat("a", 48)
	release := make(chan struct{})
	defer close(release)
	handler := New(nil, Config{
		AdminToken: admin,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			select {
			case <-release:
				return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		})},
	})
	agents := regionTestAgents()
	started := time.Now()
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	elapsed := time.Since(started)
	if elapsed > regionResolveBudget+time.Second {
		t.Fatalf("region resolution blocked the node list for %s, budget is %s", elapsed, regionResolveBudget)
	}
	for _, agent := range agents {
		if agent.RegionCode != "" {
			t.Fatalf("slow provider resolved %s after the budget expired", agent.ID)
		}
	}
}

func TestAgentListRegionsKeepCompletedLookupsWhenQueueTimesOut(t *testing.T) {
	admin := strings.Repeat("a", 48)
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int64
	handler := New(nil, Config{
		AdminToken: admin,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			if strings.HasSuffix(request.URL.Path, "/8.8.8.1.json") {
				return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
			}
			select {
			case <-release:
				return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		})},
	})
	// One fast lookup frees a slot, then eight slow lookups fill all workers
	// while the final address remains queued beyond the budget.
	agents := make([]core.Agent, regionResolveConcurrency+2)
	for index := range agents {
		agents[index] = core.Agent{ID: fmt.Sprint(index), Metrics: core.HostMetrics{PublicIPv4: fmt.Sprintf("8.8.8.%d", index+1)}}
	}
	started := time.Now()
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	if elapsed := time.Since(started); elapsed > regionResolveBudget+time.Second {
		t.Fatalf("queued lookups blocked the list for %s", elapsed)
	}
	if agents[0].RegionCode != "US" {
		t.Fatalf("completed lookup region = %q, want US despite the queued timeout", agents[0].RegionCode)
	}
	for _, agent := range agents[1:] {
		if agent.RegionCode != "" {
			t.Fatalf("blocked lookup resolved agent %s inside the budget", agent.ID)
		}
	}
	if got := calls.Load(); got != regionResolveConcurrency+1 {
		t.Fatalf("provider calls = %d, want one completed lookup and %d in flight", got, regionResolveConcurrency)
	}
}

func TestAgentListRegionsShareLookupForPublicAddress(t *testing.T) {
	admin := strings.Repeat("a", 48)
	var calls atomic.Int64
	handler := New(nil, Config{
		AdminToken: admin,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			// Keep the cache cold long enough for concurrent duplicate
			// lookups to reach the provider if addresses are not grouped.
			time.Sleep(100 * time.Millisecond)
			return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
		})},
	})
	agents := []core.Agent{
		{ID: "alpha", Metrics: core.HostMetrics{PublicIPv4: "8.8.8.8"}},
		{ID: "bravo", Metrics: core.HostMetrics{PublicIPv4: "::ffff:8.8.8.8"}},
		{ID: "charlie", Metrics: core.HostMetrics{PublicIPv4: "8.8.8.8"}},
		{ID: "delta", Labels: map[string]string{"region_code": "JP"}, Metrics: core.HostMetrics{PublicIPv4: "8.8.8.8"}},
	}
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	for _, agent := range agents[:3] {
		if agent.RegionCode != "US" {
			t.Fatalf("agent %s region = %q, want shared US result", agent.ID, agent.RegionCode)
		}
	}
	if agents[3].RegionCode != "JP" {
		t.Fatalf("manual region = %q, want JP", agents[3].RegionCode)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want one for the shared public address", got)
	}
}

// The node list is re-read on every navigation. A provider that fails must not
// send the panel through the same failing round trip each time.
func TestAgentListRegionsRememberProviderFailures(t *testing.T) {
	admin := strings.Repeat("a", 48)
	var calls atomic.Int64
	handler := New(nil, Config{
		AdminToken: admin,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return regionTestResponse(http.StatusServiceUnavailable, "unavailable"), nil
		})},
	})
	agents := regionTestAgents()
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	if first := calls.Load(); first != int64(len(agents)) {
		t.Fatalf("provider calls = %d, want one per unresolved node", first)
	}
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	if calls.Load() != int64(len(agents)) {
		t.Fatalf("provider calls = %d, want the failure window to skip the provider", calls.Load())
	}
}

func TestAgentListRegionsPreferManualPreference(t *testing.T) {
	admin := strings.Repeat("a", 48)
	var calls atomic.Int64
	handler := New(nil, Config{
		AdminToken: admin,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
		})},
	})
	agents := []core.Agent{
		{ID: "alpha", Labels: map[string]string{"region_code": "jp"}, Metrics: core.HostMetrics{PublicIPv4: "8.8.8.8"}},
		{ID: "bravo", Metrics: core.HostMetrics{PublicIPv4: "1.1.1.1"}},
	}
	handler.resolveAgentRegions(regionTestRequest(t, admin), agents)
	if agents[0].RegionCode != "JP" || agents[1].RegionCode != "US" {
		t.Fatalf("regions = %q/%q, want JP/US", agents[0].RegionCode, agents[1].RegionCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want only the node without a preference", calls.Load())
	}
}

func TestAgentListRegionsIncludeEmptyCodeAfterBudget(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	automatic, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "automatic-region")
	manual, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "manual-region")
	metrics := core.HostMetrics{PublicIPv4: "8.8.8.8"}
	if err := db.Heartbeat(ctx, automatic.ID, core.HeartbeatRequest{Metrics: &metrics}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentRegionCode(ctx, manual.ID, "JP"); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int64
	admin.handler = New(db, Config{
		AdminToken: admin.token,
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			select {
			case <-release:
				return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		})},
	}).Handler()
	var response []map[string]json.RawMessage
	admin.call("GET", "/agents", nil, http.StatusOK, &response)
	if len(response) != 2 {
		t.Fatalf("list contains %d agents, want 2", len(response))
	}
	want := map[string]string{automatic.ID: "", manual.ID: "JP"}
	for _, item := range response {
		var id, code string
		if err := json.Unmarshal(item["id"], &id); err != nil {
			t.Fatal(err)
		}
		raw, exists := item["region_code"]
		if !exists {
			t.Fatalf("agent %s omitted region_code, causing per-card fallback after the list budget", id)
		}
		if err := json.Unmarshal(raw, &code); err != nil {
			t.Fatal(err)
		}
		if expected, ok := want[id]; !ok || code != expected {
			t.Fatalf("agent %s region = %q, want %q", id, code, expected)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want only the automatic node", got)
	}
}
