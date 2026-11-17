package api

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExpiredSessionResponseContract(t *testing.T) {
	token := strings.Repeat("a", 32)
	server := New(nil, Config{AdminTokenDigest: sha256.Sum256([]byte(token))})
	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"token":"`+token+`"}`)))
	if login.Code != http.StatusOK {
		t.Fatal("fixture login failed")
	}
	server.sessionsMu.Lock()
	for key, session := range server.sessions {
		session.ExpiresAt = time.Now().Add(-time.Second)
		server.sessions[key] = session
	}
	server.sessionsMu.Unlock()
	request := httptest.NewRequest("GET", "/api/v1/auth/session", nil)
	request.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnauthorized || body["error"] != "登录已过期，请重新登录。" || body["detail"] != "session expired" {
		t.Fatalf("unexpected expiration response: status=%d error=%q detail=%q", response.Code, body["error"], body["detail"])
	}
}

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

func TestCookieSessionMissesDoNotTripLoginRateLimit(t *testing.T) {
	token := strings.Repeat("a", 32)
	server := New(nil, Config{AdminTokenDigest: sha256.Sum256([]byte(token))})
	staleCookie := &http.Cookie{Name: server.cookieName(), Value: "stale-restart-cookie"}
	remote := "198.51.100.25:43210"

	for i := 0; i < 12; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
		request.RemoteAddr = remote
		request.AddCookie(staleCookie)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("stale cookie request %d status = %d, want %d (%s)", i, response.Code, http.StatusUnauthorized, response.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["detail"] != "session expired" {
			t.Fatalf("stale cookie detail = %q, want session expired", body["detail"])
		}
	}

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	unauthenticated.RemoteAddr = remote
	unauthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthRec, unauthenticated)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}
	var unauthBody map[string]string
	if err := json.Unmarshal(unauthRec.Body.Bytes(), &unauthBody); err != nil {
		t.Fatal(err)
	}
	if unauthBody["detail"] != "authentication required" {
		t.Fatalf("unauthenticated detail = %q, want authentication required", unauthBody["detail"])
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	loginReq.RemoteAddr = remote
	loginRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login after stale cookies status = %d, want %d (%s)", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}

	bearerRemote := "198.51.100.26:43210"
	for i := 0; i < 8; i++ {
		badBearer := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
		badBearer.RemoteAddr = bearerRemote
		badBearer.Header.Set("Authorization", "Bearer wrong-legacy-token")
		badRec := httptest.NewRecorder()
		server.Handler().ServeHTTP(badRec, badBearer)
		if badRec.Code != http.StatusUnauthorized {
			t.Fatalf("bad bearer attempt %d status = %d, want %d", i, badRec.Code, http.StatusUnauthorized)
		}
	}
	blocked := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	blocked.RemoteAddr = bearerRemote
	blocked.Header.Set("Authorization", "Bearer wrong-legacy-token")
	blockedRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(blockedRec, blocked)
	if blockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("9th bad bearer status = %d, want %d", blockedRec.Code, http.StatusTooManyRequests)
	}
}
