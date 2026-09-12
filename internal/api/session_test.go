package api

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAPISessionLoginCSRFAndLogout(t *testing.T) {
	token := strings.Repeat("a", 32)
	server := New(nil, Config{AdminTokenDigest: sha256.Sum256([]byte(token))})
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	request, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", response.StatusCode)
	}
	var session struct {
		Role      core.Role `json:"role"`
		CSRFToken string    `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if session.Role != core.RoleAdmin || session.CSRFToken == "" {
		t.Fatalf("unexpected session: %+v", session)
	}

	response, err = client.Get(testServer.URL + "/api/v1/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d", response.StatusCode)
	}
	response.Body.Close()

	request, _ = http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/auth/logout", nil)
	request.Header.Set("Authorization", "Bearer invalid-token-must-not-fall-back-to-cookie")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logout with invalid bearer and valid session status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	response.Body.Close()

	request, _ = http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/auth/logout", nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d", response.StatusCode)
	}
	response.Body.Close()

	request, _ = http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/auth/logout", nil)
	request.Header.Set(csrfHeader, session.CSRFToken)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestSameOriginRequiresExactSchemeAndHost(t *testing.T) {
	t.Parallel()
	server := New(nil, Config{AdminToken: strings.Repeat("a", 32), SecureTransport: true, AllowedOrigins: []string{"https://console.example"}})
	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "no origin", host: "qch.example", want: true},
		{name: "exact production origin", origin: "https://qch.example", host: "qch.example", want: true},
		{name: "explicit allowed origin", origin: "https://console.example", host: "qch.example", want: true},
		{name: "wrong scheme", origin: "http://qch.example", host: "qch.example", want: false},
		{name: "host suffix attack", origin: "https://evil-qch.example", host: "qch.example", want: false},
		{name: "userinfo confusion", origin: "https://qch.example@evil.example", host: "qch.example", want: false},
		{name: "path confusion", origin: "https://evil.example/://qch.example", host: "qch.example", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			if got := server.sameOrigin(request); got != test.want {
				t.Fatalf("sameOrigin(origin=%q, host=%q) = %t, want %t", test.origin, test.host, got, test.want)
			}
		})
	}
}

func TestLegacySessionsExposeDistinctStableWorkspaceIDs(t *testing.T) {
	server := New(nil, Config{OperatorTokens: []string{"legacy-alice-token", "legacy-bob-token"}})
	ids := map[string]string{}
	for _, token := range []string{"legacy-alice-token", "legacy-bob-token", "legacy-alice-token"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("login: %d %s", response.Code, response.Body.String())
		}
		var login struct {
			UserID      string `json:"user_id"`
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil {
			t.Fatal(err)
		}
		if login.UserID != "" || login.WorkspaceID != tokenConfigOwnerID(token) || strings.Contains(login.WorkspaceID, token) {
			t.Fatalf("invalid opaque legacy workspace: %+v", login)
		}
		if previous := ids[token]; previous != "" && previous != login.WorkspaceID {
			t.Fatal("workspace changed on relogin")
		}
		ids[token] = login.WorkspaceID
		request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		request.AddCookie(response.Result().Cookies()[0])
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		var session struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || session.WorkspaceID != login.WorkspaceID {
			t.Fatalf("session lost its namespace: %d %+v", response.Code, session)
		}
	}
	if ids["legacy-alice-token"] == ids["legacy-bob-token"] {
		t.Fatal("separate tokens share browser preferences")
	}
}
