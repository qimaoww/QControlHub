package api

import (
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
	server := New(nil, Config{AdminToken: token})
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
