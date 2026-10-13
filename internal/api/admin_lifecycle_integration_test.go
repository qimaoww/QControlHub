package api

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func managementLogin(t *testing.T, handler http.Handler, username, secret string, status int) (configScopeAPIClient, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "token": secret})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body))))
	if response.Code != status {
		t.Fatalf("fresh login for %s: %d %s, want %d", username, response.Code, response.Body.String(), status)
	}
	var session map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	client := configScopeAPIClient{t: t, handler: handler}
	if status == http.StatusOK {
		client.cookie, client.csrf = response.Result().Cookies()[0], session["csrf_token"].(string)
	}
	return client, session
}

func TestEnvironmentAdminManagesOnlyDatabaseAdministrator(t *testing.T) {
	const token = "admin-lifecycle-environment-fixture"
	const password = "administrator account password fixture"
	for _, digestOnly := range []bool{false, true} {
		for _, action := range []string{"disable-put", "disable-delete", "demote", "purge"} {
			name := "raw/" + action
			config := Config{AdminToken: token}
			if digestOnly {
				name = "digest/" + action
				config = Config{AdminTokenDigest: sha256.Sum256([]byte(token))}
			}
			t.Run(name, func(t *testing.T) {
				db, _, _, alice, _ := newConfigScopeAPIFixture(t)
				handler := New(db, config).Handler()
				admin, identity := managementLogin(t, handler, "admin", token, http.StatusOK)
				if identity["user_id"] != "" || identity["auth_type"] != "environment_token" {
					t.Fatalf("default admin was confused with a database account: %+v", identity)
				}
				var user core.User
				admin.call("POST", "/users", core.UserRequest{Username: "test", Password: password, Role: core.RoleAdmin}, http.StatusCreated, &user)
				target, _ := managementLogin(t, handler, user.Username, password, http.StatusOK)
				// Revalidation must also invalidate a session on another process.
				remote, _ := managementLogin(t, New(db, config).Handler(), user.Username, password, http.StatusOK)
				var items []core.User
				admin.call("GET", "/users", nil, http.StatusOK, &items)
				adminCount := 0
				for _, item := range items {
					if item.Role == core.RoleAdmin {
						adminCount++
						if item.ID != user.ID || item.AccountActions == nil || *item.AccountActions != (core.UserAccountActions{}) {
							t.Fatalf("token administrator could not manage test: %+v", item)
						}
					}
				}
				if adminCount != 1 {
					t.Fatalf("database administrator count = %d", adminCount)
				}
				// A role-bearing session cannot delete or disable its own row.
				target.call("POST", "/users/"+user.ID+"/purge", nil, http.StatusConflict, nil)
				target.call("DELETE", "/users/"+user.ID, nil, http.StatusConflict, nil)
				target.call("PUT", "/users/"+user.ID, map[string]any{"disabled": true}, http.StatusConflict, nil)
				alice.call("POST", "/users/"+user.ID+"/purge", nil, http.StatusForbidden, nil)
				alice.call("DELETE", "/users/"+user.ID, nil, http.StatusForbidden, nil)
				alice.call("PUT", "/users/"+user.ID, map[string]any{"disabled": true}, http.StatusForbidden, nil)
				alice.call("PUT", "/users/"+user.ID, map[string]any{"role": "user"}, http.StatusForbidden, nil)
				switch action {
				case "disable-put":
					admin.call("PUT", "/users/"+user.ID, map[string]any{"disabled": true}, http.StatusOK, nil)
				case "disable-delete":
					admin.call("DELETE", "/users/"+user.ID, nil, http.StatusNoContent, nil)
				case "demote":
					admin.call("PUT", "/users/"+user.ID, map[string]any{"role": "user"}, http.StatusOK, nil)
				case "purge":
					admin.call("POST", "/users/"+user.ID+"/purge", nil, http.StatusNoContent, nil)
				}
				target.call("GET", "/users", nil, http.StatusUnauthorized, nil)
				remote.call("GET", "/users", nil, http.StatusUnauthorized, nil)
				if action == "demote" {
					demoted, _ := managementLogin(t, handler, user.Username, password, http.StatusOK)
					demoted.call("GET", "/users", nil, http.StatusForbidden, nil)
				} else {
					managementLogin(t, handler, user.Username, password, http.StatusUnauthorized)
				}
				// Use a new server and cookie, proving that a cached role is not
				// the remaining management entry counted by the protection.
				fresh, _ := managementLogin(t, New(db, config).Handler(), "admin", token, http.StatusOK)
				fresh.call("GET", "/users", nil, http.StatusOK, nil)
				fresh.call("GET", "/auth/session", nil, http.StatusOK, nil)
			})
		}
	}
}

func TestDatabaseAdminIdentityAndUnavailableTokenProtection(t *testing.T) {
	const token = "disabled-admin-entry-fixture"
	const password = "database administrator password fixture"
	for _, config := range []Config{{}, {AdminToken: token, ReadonlyTokens: []string{token}}} {
		t.Run("unavailable-token", func(t *testing.T) {
			db, ctx, _, _, _ := newConfigScopeAPIFixture(t)
			hash, err := authn.HashPassword(password)
			if err != nil {
				t.Fatal(err)
			}
			user, err := db.CreateUser(ctx, core.UserRequest{Username: "admin", Role: core.RoleAdmin}, hash)
			if err != nil {
				t.Fatal(err)
			}
			handler := New(db, config).Handler()
			admin, identity := managementLogin(t, handler, "admin", password, http.StatusOK)
			if identity["user_id"] != user.ID || identity["auth_type"] != "database_account" || identity["token_admin_available"] != false {
				t.Fatalf("database account named admin was treated as a token administrator: %+v", identity)
			}
			var users []core.User
			admin.call("GET", "/users", nil, http.StatusOK, &users)
			for _, item := range users {
				if item.ID == user.ID && (item.AccountActions.DeleteRestriction != "current_account" || item.AccountActions.DisableRestriction != "current_account" || item.AccountActions.DemoteRestriction != "last_administrator") {
					t.Fatalf("wrong account protection: %+v", item.AccountActions)
				}
			}
			body := admin.call("PUT", "/users/"+user.ID, map[string]any{"role": "user"}, http.StatusConflict, nil)
			if !strings.Contains(string(body), "最后一个可用的管理员登录入口") {
				t.Fatalf("last administrator error = %s", body)
			}
			admin.call("POST", "/users/"+user.ID+"/purge", nil, http.StatusConflict, nil)
			admin.call("PUT", "/users/"+user.ID, map[string]any{"disabled": true}, http.StatusConflict, nil)
			// Untrusted JSON cannot supply the backend authentication state.
			admin.call("PUT", "/users/"+user.ID, map[string]any{"role": "user", "token_admin_available": true}, http.StatusBadRequest, nil)
			fresh, _ := managementLogin(t, New(db, config).Handler(), "admin", password, http.StatusOK)
			fresh.call("GET", "/users", nil, http.StatusOK, nil)
			var other, disabled core.User
			for _, target := range []*core.User{&other, &disabled} {
				name := "other-admin"
				if target == &disabled {
					name = "disabled-admin"
				}
				fresh.call("POST", "/users", core.UserRequest{Username: name, Password: password, Role: core.RoleAdmin}, http.StatusCreated, target)
			}
			fresh.call("DELETE", "/users/"+disabled.ID, nil, http.StatusNoContent, nil)
			fresh.call("POST", "/users/"+other.ID+"/purge", nil, http.StatusNoContent, nil)
			remaining, _ := managementLogin(t, New(db, config).Handler(), "admin", password, http.StatusOK)
			remaining.call("GET", "/users", nil, http.StatusOK, nil)
			remaining.call("PUT", "/users/"+user.ID, map[string]any{"role": "user"}, http.StatusConflict, nil)
		})
	}
}
