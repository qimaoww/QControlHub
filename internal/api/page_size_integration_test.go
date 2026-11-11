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

func TestAccountPageSizePreference(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	read := func(client configScopeAPIClient) store.PageSizePreference {
		var p store.PageSizePreference
		client.call("GET", "/preferences/page-size/nodes", nil, 200, &p)
		return p
	}
	save := func(client configScopeAPIClient, id int, revision int64, status int) store.PageSizePreference {
		var p store.PageSizePreference
		var target any
		if status == 200 {
			target = &p
		}
		client.call("PUT", "/preferences/page-size/nodes", map[string]any{"page_size": id, "revision": revision}, status, target)
		return p
	}
	for _, client := range []configScopeAPIClient{admin, alice, bob} {
		if p := read(client); p.PageSize != 30 || p.Revision != 0 {
			t.Fatalf("default: %+v", p)
		}
	}
	p := save(alice, 50, 0, 200)
	if p.PageSize != 50 || p.Revision != 1 {
		t.Fatalf("save: %+v", p)
	}
	if p := read(bob); p.PageSize != 30 || p.Revision != 0 {
		t.Fatal("cross-account preference leak")
	}
	var traffic store.PageSizePreference
	alice.call("GET", "/preferences/page-size/traffic", nil, 200, &traffic)
	if traffic.PageSize != 30 || traffic.Revision != 0 {
		t.Fatal("page preference leak")
	}
	alice.call("PUT", "/preferences/page-size/traffic", map[string]any{"page_size": 200, "revision": 0}, 200, &traffic)
	if read(alice).PageSize != 50 {
		t.Fatal("traffic changed node preference")
	}
	var tcp store.PageSizePreference
	alice.call("PUT", "/preferences/page-size/tcp", map[string]any{"page_size": 100, "revision": 0}, 200, &tcp)
	if tcp.PageSize != 100 || read(alice).PageSize != 50 {
		t.Fatal("TCP preference was not independent")
	}
	alice.call("GET", "/preferences/page-size/other", nil, 404, nil)
	save(bob, 30, 0, 200)
	if read(alice).PageSize != 50 {
		t.Fatal("bob changed alice")
	}
	device, _ := managementLogin(t, alice.handler, "alice", "config scope password fixture", 200)
	if read(device) != read(alice) {
		t.Fatal("preference did not survive another login")
	}
	save(device, 30, 0, 409)
	if read(alice).PageSize != 50 {
		t.Fatal("stale write changed preference")
	}
	for _, body := range []any{
		map[string]any{"page_size": 201, "revision": 1},
		map[string]any{"page_size": 0, "revision": 1},
		map[string]any{"page_size": 12, "revision": 1},
		map[string]any{"page_size": 30, "revision": -1},
		map[string]any{"page_size": 30},
		map[string]any{"page_size": 30, "revision": nil},
		map[string]any{"page_size": 30, "revision": 1, "account_id": bob.userID},
	} {
		alice.call("PUT", "/preferences/page-size/nodes", body, 400, nil)
	}
	alice.call("GET", "/preferences/page-size/nodes/"+bob.userID, nil, 404, nil)
	var reader core.User
	admin.call("POST", "/users", core.UserRequest{Username: "view-reader", Password: "theme reader fixture password", Role: core.RoleUser}, 201, &reader)
	restricted, _ := managementLogin(t, admin.handler, "view-reader", "theme reader fixture password", 200)
	save(restricted, 50, 0, 200)
	if read(restricted).PageSize != 50 {
		t.Fatal("permission-free account cannot select page size")
	}
	save(admin, 50, 0, 200)
	var named core.User
	admin.call("POST", "/users", core.UserRequest{Username: "view-admin", Password: "theme named administrator fixture", Role: core.RoleAdmin}, 201, &named)
	namedClient, _ := managementLogin(t, admin.handler, "view-admin", "theme named administrator fixture", 200)
	if p := read(namedClient); p.PageSize != 30 || p.Revision != 0 {
		t.Fatal("environment admin shares named admin preference")
	}
	for _, method := range []string{"GET", "PUT"} {
		req := httptest.NewRequest(method, "/api/v1/preferences/page-size/nodes", nil)
		res := httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", method, res.Code)
		}
		req = httptest.NewRequest(method, "/api/v1/preferences/page-size/nodes", nil)
		req.AddCookie(alice.cookie)
		req.Header.Set(csrfHeader, bob.csrf)
		res = httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized && res.Code != http.StatusForbidden {
			t.Fatalf("session binding %s: %d", method, res.Code)
		}
	}
}

func TestAccountPageSizeConcurrentSaves(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, id := range []int{30, 50} {
		group.Add(1)
		go func() { defer group.Done(); <-start; _, err := db.SavePageSize(scoped, "nodes", id, 0); results <- err }()
	}
	close(start)
	group.Wait()
	close(results)
	conflicts := 0
	for err := range results {
		if errors.Is(err, store.ErrPageSizeConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflicts=%d", conflicts)
	}
	p, err := db.ReadPageSize(scoped, "nodes")
	if err != nil || p.Revision != 1 {
		t.Fatalf("concurrent save: %+v %v", p, err)
	}
	if _, err := db.ReadPageSize(ctx, "nodes"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("unscoped read: %v", err)
	}
}

func TestAccountPageSizeAPIKeyPolicy(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	permissions := []core.Permission{core.PermissionPreferencesRead, core.PermissionPreferencesWrite}
	key, _ := testCreateBusinessKey(t, alice, permissions)
	var current store.PageSizePreference
	key.call("PUT", "/preferences/page-size/nodes", map[string]any{"page_size": 100, "revision": 0}, 200, &current)
	alice.call("GET", "/preferences/page-size/nodes", nil, 200, &current)
	if current.PageSize != 100 {
		t.Fatal("key preference was not attached to its account")
	}
	bob.call("GET", "/preferences/page-size/nodes", nil, 200, &current)
	if current.PageSize != 30 {
		t.Fatal("key changed another account")
	}
	var readonly apiKeyCreation
	alice.call("POST", "/settings/api-keys", core.APIKeyInput{Name: "page reader", Mode: core.APIKeyReadOnly, Permissions: []core.Permission{core.PermissionPreferencesRead}}, 201, &readonly)
	reader := configScopeAPIClient{t: t, handler: alice.handler, token: readonly.Secret}
	reader.call("GET", "/preferences/page-size/nodes", nil, 200, nil)
	reader.call("PUT", "/preferences/page-size/nodes", map[string]any{"page_size": 30, "revision": 1}, 403, nil)
	weak, _ := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionAgentsRead})
	weak.call("GET", "/preferences/page-size/nodes", nil, 403, nil)
	weak.call("PUT", "/preferences/page-size/nodes", map[string]any{"page_size": 30, "revision": 0}, 403, nil)
}
