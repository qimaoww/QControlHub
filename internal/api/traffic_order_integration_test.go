package api

import (
	"reflect"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestAccountTrafficOrder(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	first := enrollConfigScopeAPIAgent(t, ctx, db)
	second := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, first, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, first, bob, 21002)
	a, b, c := first.ID+":21001", first.ID+":21002", first.ID+":21003"
	read := func(client configScopeAPIClient) store.NodeOrder {
		var got store.NodeOrder
		client.call("GET", "/preferences/traffic-order", nil, 200, &got)
		return got
	}
	save := func(client configScopeAPIClient, ids any, revision int64, status int, initialize bool) store.NodeOrder {
		var got store.NodeOrder
		client.call("PUT", "/preferences/traffic-order", map[string]any{"ids": ids, "revision": revision, "initialize": initialize}, status, &got)
		return got
	}
	if got := read(alice); got.Revision != 0 || len(got.IDs) != 0 {
		t.Fatal(got)
	}
	saved := save(alice, []string{a, b, c}, 0, 200, false)
	if saved.Revision != 1 {
		t.Fatal(saved)
	}
	// A second session of the same viewer reads the durable preference.
	device, _ := managementLogin(t, alice.handler, "alice", "config scope password fixture", 200)
	if got := read(device); !reflect.DeepEqual(got, saved) {
		t.Fatal("second device lost account order", got)
	}
	if got := read(bob); got.Revision != 0 || len(got.IDs) != 0 {
		t.Fatal("shared viewers leaked preferences", got)
	}
	save(bob, []string{b, a}, 0, 200, false)
	if got := read(alice); !reflect.DeepEqual(got, saved) {
		t.Fatal("another viewer changed order")
	}
	// Partial updates keep filtered-out slots and append newly discovered ports.
	saved = save(alice, []string{c, a}, saved.Revision, 200, false)
	if !reflect.DeepEqual(saved.IDs, []string{c, b, a}) {
		t.Fatal("partial update lost hidden slot", saved)
	}
	save(device, []string{a, c}, 1, 409, false)
	if got := read(alice); !reflect.DeepEqual(got, saved) {
		t.Fatal("conflict overwrote preference")
	}
	save(alice, []string{a, b}, 0, 200, true)
	if got := read(alice); !reflect.DeepEqual(got, saved) {
		t.Fatal("migration replaced server order")
	}
	save(alice, []string{second.ID + ":21001"}, saved.Revision, 404, false)
	for _, invalid := range []any{nil, []string{a, a}, []string{first.ID + ":0"}, []string{first.ID + ":65536"}, []string{first.ID + ":021001"}, []string{"untrusted:21001"}} {
		save(alice, invalid, saved.Revision, 400, false)
	}
	if got := read(alice); !reflect.DeepEqual(got, saved) {
		t.Fatal("invalid request changed preference")
	}
	adminOrder := save(admin, []string{second.ID + ":21001", a}, 0, 200, false)
	if len(adminOrder.IDs) != 2 {
		t.Fatal("administrator order missing", adminOrder)
	}
	// Revoked sharing removes both the visible key and the ability to save it.
	access, err := db.UserAgentAccess(ctx, alice.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SetUserAgentAccess(ctx, alice.userID, core.AgentAccessRequest{Isolated: true, Revision: access.Revision}); err != nil {
		t.Fatal(err)
	}
	if got := read(alice); len(got.IDs) != 0 {
		t.Fatal("revoked node remained visible", got)
	}
	save(alice, []string{a}, read(alice).Revision, 404, false)
	if got := read(bob); len(got.IDs) != 2 {
		t.Fatal("revoking another viewer removed preference", got)
	}
}
