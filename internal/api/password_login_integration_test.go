package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func passwordLoginStore(t *testing.T) (*store.Store, context.Context, *pgx.Conn) {
	t.Helper()
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := schema.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	connection, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })
	return db, ctx, connection
}

func TestDatabaseLoginPreservesPasswordBytes(t *testing.T) {
	db, ctx, _ := passwordLoginStore(t)
	const token = "password-login-environment-fixture"
	handler := New(db, Config{AdminToken: token}).Handler()
	admin := configScopeAPIClient{t: t, handler: handler, token: token}
	for _, tc := range []struct{ name, password string }{
		{"leading", "  original password"},
		{"trailing", "original password  "},
		{"unicode-space", "\u3000\t中文密码 🔑\u00a0\n"},
		{"minimum-chinese", "中文密码"},
		{"combining", "e\u0301-password-🔑"},
		{"special", "quotes\"'\\\x00<&>!$%"},
		{"spaces-only", strings.Repeat(" ", 12)},
		{"maximum", strings.Repeat("界", 24)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var user core.User
			role := core.RoleUser
			if tc.name == "leading" {
				role = core.RoleAdmin
			}
			admin.call("POST", "/users", core.UserRequest{Username: strings.ToUpper(tc.name), Password: tc.password,
				Role: role, Permissions: []core.Permission{core.PermissionOverviewRead}}, http.StatusCreated, &user)
			_, originalHash, err := db.UserForLogin(ctx, user.Username)
			if err != nil {
				t.Fatal(err)
			}
			login, identity := managementLogin(t, handler, " "+strings.ToUpper(user.Username)+" ", tc.password, http.StatusOK)
			if identity["user_id"] != user.ID || identity["workspace_id"] != user.ID || identity["role"] != string(role) || identity["auth_type"] != "database_account" {
				t.Fatal("database identity or ownership changed")
			}
			login.call("GET", "/auth/session", nil, http.StatusOK, nil)
			// A fresh process must expire the old cookie and accept the original password.
			fresh := New(db, Config{AdminToken: token}).Handler()
			login.handler = fresh
			login.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			remote, _ := managementLogin(t, fresh, user.Username, tc.password, http.StatusOK)
			managementLogin(t, fresh, user.Username, tc.password+"x", http.StatusUnauthorized)
			if trimmed := strings.TrimSpace(tc.password); trimmed != tc.password {
				managementLogin(t, fresh, user.Username, trimmed, http.StatusUnauthorized)
			}
			// Omitted password on profile/permission updates must preserve the hash.
			admin.call("PUT", "/users/"+user.ID, map[string]any{"display_name": "Renamed", "permissions": []string{"overview.read", "configs.read"}}, http.StatusOK, nil)
			remote.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			_, afterHash, err := db.UserForLogin(ctx, user.Username)
			if err != nil || originalHash != afterHash {
				t.Fatal("profile update changed password hash")
			}
			remote, _ = managementLogin(t, fresh, user.Username, tc.password, http.StatusOK)
			reset := "\u3000reset password 🔑 "
			admin.call("PUT", "/users/"+user.ID, map[string]any{"password": reset}, http.StatusOK, nil)
			remote.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			managementLogin(t, fresh, user.Username, tc.password, http.StatusUnauthorized)
			active, _ := managementLogin(t, fresh, user.Username, reset, http.StatusOK)
			admin.call("PUT", "/users/"+user.ID, map[string]any{"disabled": true}, http.StatusOK, nil)
			active.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			managementLogin(t, fresh, user.Username, reset, http.StatusUnauthorized)
		})
	}
	for _, password := range []string{strings.Repeat("x", 11), strings.Repeat("界", 25)} {
		admin.call("POST", "/users", core.UserRequest{Username: "invalid", Password: password, Role: core.RoleUser}, http.StatusBadRequest, nil)
	}
	// Preserve the existing whitespace tolerance of environment tokens.
	_, identity := managementLogin(t, handler, "ADMIN", " \t"+token+"\n", http.StatusOK)
	if identity["user_id"] != "" || identity["auth_type"] != "environment_token" {
		t.Fatal("environment token gained a database identity")
	}
}

func TestLoginDatabaseFailureIsDiagnosedWithoutCredentialPenalty(t *testing.T) {
	db, ctx, connection := passwordLoginStore(t)
	if _, err := connection.Exec(ctx, `ALTER TABLE panel_users RENAME TO unavailable_panel_users`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	const token = "database-outage-environment-fixture"
	handler := New(db, Config{AdminToken: token}).Handler()
	for i := 0; i < 10; i++ {
		_, body := managementLogin(t, handler, "existing-user", "private password fixture", http.StatusInternalServerError)
		if strings.Contains(body["error"].(string), "panel_users") {
			t.Fatal("database internals exposed to browser")
		}
	}
	if !strings.Contains(logs.String(), "lookup database account for login") || !strings.Contains(logs.String(), "42P01") {
		t.Fatal("missing contextual database error diagnostic")
	}
	if strings.Contains(logs.String(), "private password fixture") || strings.Contains(logs.String(), token) {
		t.Fatal("login diagnostic exposed credentials")
	}
	managementLogin(t, handler, "admin", token, http.StatusOK)
	if _, err := connection.Exec(ctx, `ALTER TABLE unavailable_panel_users RENAME TO panel_users`); err != nil {
		t.Fatal(err)
	}
	var failure string
	for i := 0; i < 8; i++ {
		_, body := managementLogin(t, handler, "missing-user", "wrong password fixture", http.StatusUnauthorized)
		if i == 0 {
			failure = body["error"].(string)
		} else if body["error"] != failure {
			t.Fatal("authentication failures must remain uniform")
		}
	}
	managementLogin(t, handler, "admin", token, http.StatusTooManyRequests)
}
