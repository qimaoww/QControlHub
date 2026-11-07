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

func TestAccountNodeViewPreference(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	read := func(client configScopeAPIClient) store.NodeViewPreference {
		var p store.NodeViewPreference
		client.call("GET", "/preferences/node-view", nil, 200, &p)
		return p
	}
	save := func(client configScopeAPIClient, id string, revision int64, status int) store.NodeViewPreference {
		var p store.NodeViewPreference
		var target any
		if status == 200 {
			target = &p
		}
		client.call("PUT", "/preferences/node-view", map[string]any{"view": id, "revision": revision}, status, target)
		return p
	}
	for _, client := range []configScopeAPIClient{admin, alice, bob} {
		if p := read(client); p.View != "cards" || p.Revision != 0 {
			t.Fatalf("default: %+v", p)
		}
	}
	p := save(alice, "list", 0, 200)
	if p.View != "list" || p.Revision != 1 {
		t.Fatalf("save: %+v", p)
	}
	if p := read(bob); p.View != "cards" || p.Revision != 0 {
		t.Fatal("cross-account preference leak")
	}
	save(bob, "cards", 0, 200)
	if read(alice).View != "list" {
		t.Fatal("bob changed alice")
	}
	device, _ := managementLogin(t, alice.handler, "alice", "config scope password fixture", 200)
	if read(device) != read(alice) {
		t.Fatal("preference did not survive another login")
	}
	save(device, "cards", 0, 409)
	if read(alice).View != "list" {
		t.Fatal("stale write changed preference")
	}
	for _, body := range []any{
		map[string]any{"view": "unknown", "revision": 1},
		map[string]any{"view": "", "revision": 1},
		map[string]any{"view": "List", "revision": 1},
		map[string]any{"view": "cards", "revision": -1},
		map[string]any{"view": "cards"},
		map[string]any{"view": "cards", "revision": nil},
		map[string]any{"view": "cards", "revision": 1, "account_id": bob.userID},
	} {
		alice.call("PUT", "/preferences/node-view", body, 400, nil)
	}
	alice.call("GET", "/preferences/node-view/"+bob.userID, nil, 404, nil)
	var reader core.User
	admin.call("POST", "/users", core.UserRequest{Username: "view-reader", Password: "theme reader fixture password", Role: core.RoleUser}, 201, &reader)
	restricted, _ := managementLogin(t, admin.handler, "view-reader", "theme reader fixture password", 200)
	save(restricted, "list", 0, 200)
	if read(restricted).View != "list" {
		t.Fatal("permission-free account cannot select node view")
	}
	save(admin, "list", 0, 200)
	var named core.User
	admin.call("POST", "/users", core.UserRequest{Username: "view-admin", Password: "theme named administrator fixture", Role: core.RoleAdmin}, 201, &named)
	namedClient, _ := managementLogin(t, admin.handler, "view-admin", "theme named administrator fixture", 200)
	if p := read(namedClient); p.View != "cards" || p.Revision != 0 {
		t.Fatal("environment admin shares named admin preference")
	}
	for _, method := range []string{"GET", "PUT"} {
		req := httptest.NewRequest(method, "/api/v1/preferences/node-view", nil)
		res := httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", method, res.Code)
		}
		req = httptest.NewRequest(method, "/api/v1/preferences/node-view", nil)
		req.AddCookie(alice.cookie)
		req.Header.Set(csrfHeader, bob.csrf)
		res = httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized && res.Code != http.StatusForbidden {
			t.Fatalf("session binding %s: %d", method, res.Code)
		}
	}
}

func TestAccountNodeViewConcurrentSaves(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, id := range []string{"cards", "list"} {
		group.Add(1)
		go func() { defer group.Done(); <-start; _, err := db.SaveNodeView(scoped, id, 0); results <- err }()
	}
	close(start)
	group.Wait()
	close(results)
	conflicts := 0
	for err := range results {
		if errors.Is(err, store.ErrNodeViewConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflicts=%d", conflicts)
	}
	p, err := db.ReadNodeView(scoped)
	if err != nil || p.Revision != 1 {
		t.Fatalf("concurrent save: %+v %v", p, err)
	}
	if _, err := db.ReadNodeView(ctx); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("unscoped read: %v", err)
	}
}
