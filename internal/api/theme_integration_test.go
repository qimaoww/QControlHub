package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestAccountThemePreference(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	read := func(client configScopeAPIClient) store.ThemePreference {
		var p store.ThemePreference
		client.call("GET", "/preferences/theme", nil, 200, &p)
		return p
	}
	save := func(client configScopeAPIClient, id string, revision int64, status int) store.ThemePreference {
		var p store.ThemePreference
		var target any
		if status == 200 {
			target = &p
		}
		client.call("PUT", "/preferences/theme", map[string]any{"theme_id": id, "revision": revision}, status, target)
		return p
	}
	for _, client := range []configScopeAPIClient{admin, alice, bob} {
		if p := read(client); p.ThemeID != "analytics" || p.Revision != 0 {
			t.Fatalf("default: %+v", p)
		}
	}
	p := save(alice, "analytics", 0, 200)
	if p.ThemeID != "analytics" || p.Revision != 1 {
		t.Fatalf("save: %+v", p)
	}
	if p := read(bob); p.ThemeID != "analytics" || p.Revision != 0 {
		t.Fatal("cross-account preference leak")
	}
	save(bob, "classic", 0, 400)
	save(bob, "analytics", 0, 200)
	bobDevice, _ := managementLogin(t, bob.handler, "bob", "config scope password fixture", 200)
	if p := read(bobDevice); p.ThemeID != "analytics" || p.Revision != 1 {
		t.Fatalf("theme selection did not survive login: %+v", p)
	}
	if read(alice).ThemeID != "analytics" {
		t.Fatal("bob changed alice")
	}
	device, _ := managementLogin(t, alice.handler, "alice", "config scope password fixture", 200)
	if read(device) != read(alice) {
		t.Fatal("preference did not survive another login")
	}
	save(device, "analytics", 0, 409)
	if read(alice).ThemeID != "analytics" {
		t.Fatal("stale write changed preference")
	}
	for _, body := range []any{
		map[string]any{"theme_id": "unknown", "revision": 1},
		map[string]any{"theme_id": "", "revision": 1},
		map[string]any{"theme_id": "Analytics", "revision": 1},
		map[string]any{"theme_id": "classic", "revision": -1},
		map[string]any{"theme_id": "classic"},
		map[string]any{"theme_id": "classic", "revision": nil},
		map[string]any{"theme_id": "classic", "revision": 1, "account_id": bob.userID},
	} {
		alice.call("PUT", "/preferences/theme", body, 400, nil)
	}
	alice.call("GET", "/preferences/theme/"+bob.userID, nil, 404, nil)
	var reader core.User
	admin.call("POST", "/users", core.UserRequest{Username: "theme-reader", Password: "theme reader fixture password", Role: core.RoleUser}, 201, &reader)
	restricted, _ := managementLogin(t, admin.handler, "theme-reader", "theme reader fixture password", 200)
	save(restricted, "analytics", 0, 200)
	if read(restricted).ThemeID != "analytics" {
		t.Fatal("permission-free account cannot select theme")
	}
	save(admin, "analytics", 0, 200)
	var named core.User
	admin.call("POST", "/users", core.UserRequest{Username: "theme-admin", Password: "theme named administrator fixture", Role: core.RoleAdmin}, 201, &named)
	namedClient, _ := managementLogin(t, admin.handler, "theme-admin", "theme named administrator fixture", 200)
	if p := read(namedClient); p.ThemeID != "analytics" || p.Revision != 0 {
		t.Fatal("environment admin shares named admin preference")
	}
	for _, method := range []string{"GET", "PUT"} {
		req := httptest.NewRequest(method, "/api/v1/preferences/theme", nil)
		res := httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", method, res.Code)
		}
		req = httptest.NewRequest(method, "/api/v1/preferences/theme", nil)
		req.AddCookie(alice.cookie)
		req.Header.Set(csrfHeader, bob.csrf)
		res = httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized && res.Code != http.StatusForbidden {
			t.Fatalf("session binding %s: %d", method, res.Code)
		}
	}
}

func TestAccountThemeConcurrentSaves(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, id := range []string{"analytics", "analytics"} {
		group.Add(1)
		go func() { defer group.Done(); <-start; _, err := db.SaveTheme(scoped, id, 0); results <- err }()
	}
	close(start)
	group.Wait()
	close(results)
	conflicts := 0
	for err := range results {
		if errors.Is(err, store.ErrThemeConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflicts=%d", conflicts)
	}
	p, err := db.ReadTheme(scoped)
	if err != nil || p.Revision != 1 {
		t.Fatalf("concurrent save: %+v %v", p, err)
	}
	if _, err := db.ReadTheme(ctx); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("unscoped read: %v", err)
	}
}
