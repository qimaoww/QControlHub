package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
