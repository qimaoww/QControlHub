package api

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenAdministratorAvailabilityUsesEffectiveAuthentication(t *testing.T) {
	const token = "administrator-identity-test-fixture"
	for _, test := range []struct {
		name      string
		config    Config
		available bool
	}{
		{"raw", Config{AdminToken: token}, true},
		{"digest", Config{AdminTokenDigest: sha256.Sum256([]byte(token))}, true},
		{"absent", Config{}, false},
		{"empty-digest", Config{AdminTokenDigest: sha256.Sum256(nil)}, false},
		{"operator-collision", Config{AdminToken: token, OperatorTokens: []string{token}}, false},
		{"auditor-collision", Config{AdminToken: token, AuditorTokens: []string{token}}, false},
		{"readonly-collision", Config{AdminTokenDigest: sha256.Sum256([]byte(token)), ReadonlyTokens: []string{token}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := New(nil, test.config)
			if server.tokenAdministratorAvailable() != test.available {
				t.Fatal("administrator availability disagrees with the effective token registry")
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","token":"`+token+`"}`))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if test.available {
				if response.Code != http.StatusOK {
					t.Fatalf("fresh token login: %d %s", response.Code, response.Body.String())
				}
				var session map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
					t.Fatal(err)
				}
				if session["user_id"] != "" || session["auth_type"] != "environment_token" || session["token_admin_available"] != true || session["role"] != "admin" {
					t.Fatalf("token identity: %+v", session)
				}
			} else if response.Code == http.StatusOK {
				var session map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
					t.Fatal(err)
				}
				if session["role"] == "admin" || session["token_admin_available"] != false {
					t.Fatalf("unavailable token acquired administrator identity: %+v", session)
				}
			}
			// Missing credentials must never synthesize an administrator entry.
			response = httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","token":""}`)))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("empty token login: %d", response.Code)
			}
		})
	}
}
