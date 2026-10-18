package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestAccountNodeOrder(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	nodes := []core.Agent{}
	for i := 0; i < 4; i++ {
		nodes = append(nodes, enrollConfigScopeAPIAgent(t, ctx, db))
	}
	ids := []string{nodes[0].ID, nodes[1].ID, nodes[2].ID, nodes[3].ID}
	for _, node := range nodes {
		grantConfigScopeAPIAgent(t, ctx, db, node, alice, 21001)
		grantConfigScopeAPIAgent(t, ctx, db, node, bob, 21002)
	}
	var order store.NodeOrder
	read := func(client configScopeAPIClient) store.NodeOrder {
		var result store.NodeOrder
		client.call("GET", "/preferences/node-order", nil, 200, &result)
		return result
	}
	save := func(client configScopeAPIClient, subset []string, revision int64, status int, initialize bool) {
		client.call("PUT", "/preferences/node-order", map[string]any{"ids": subset, "revision": revision, "initialize": initialize}, status, &order)
	}
	if got := read(alice); got.Revision != 0 || !reflect.DeepEqual(got.IDs, ids) {
		t.Fatalf("initial: %+v", got)
	}
	save(alice, []string{ids[3], ids[1]}, 0, 200, true)
	want := []string{ids[3], ids[1], ids[0], ids[2]}
	if !reflect.DeepEqual(order.IDs, want) {
		t.Fatalf("partial merge: %+v", order)
	}
	save(alice, []string{ids[2], ids[0]}, 0, 200, true)
	if !reflect.DeepEqual(order.IDs, want) {
		t.Fatal("second migration overwrote saved order")
	}
	save(alice, []string{ids[2], ids[0]}, 0, 409, false)
	// A second login has a different cookie and reads the same durable account row.
	device, _ := managementLogin(t, alice.handler, "alice", "config scope password fixture", 200)
	if got := read(device); !reflect.DeepEqual(got.IDs, want) {
		t.Fatalf("second browser: %+v", got)
	}
	if got := read(bob); got.Revision != 0 || !reflect.DeepEqual(got.IDs, ids) {
		t.Fatal("accounts shared order")
	}
	save(bob, []string{ids[2], ids[0]}, 0, 200, false)
	if !reflect.DeepEqual(read(alice).IDs, want) {
		t.Fatal("bob changed alice")
	}
	save(admin, []string{ids[3], ids[2], ids[1], ids[0]}, 0, 200, false)
	var dbAdmin core.User
	admin.call("POST", "/users", core.UserRequest{Username: "order-admin", Password: "order administrator fixture password", Role: core.RoleAdmin}, 201, &dbAdmin)
	namedAdmin, _ := managementLogin(t, admin.handler, "order-admin", "order administrator fixture password", 200)
	save(namedAdmin, []string{ids[1], ids[0]}, 0, 200, false)
	if reflect.DeepEqual(read(namedAdmin).IDs, read(admin).IDs) {
		t.Fatal("database and environment administrators shared a preference")
	}
	// An account with only node-read access can save a preference for shared nodes.
	var reader core.User
	admin.call("POST", "/users", core.UserRequest{Username: "reader", Password: "reader fixture password", Role: core.RoleUser, Permissions: []core.Permission{core.PermissionAgentsRead}}, 201, &reader)
	readClient, _ := managementLogin(t, admin.handler, "reader", "reader fixture password", 200)
	readClient.userID = reader.ID
	grantConfigScopeAPIAgent(t, ctx, db, nodes[0], readClient, 21003)
	save(readClient, []string{ids[0]}, 0, 200, false)
	save(readClient, []string{ids[1]}, order.Revision, 404, false)
	for _, body := range []any{
		map[string]any{"ids": []string{ids[0], ids[0]}, "revision": 1},
		map[string]any{"ids": []string{"bad"}, "revision": 1},
		map[string]any{"ids": nil, "revision": 1},
		map[string]any{"ids": []string{ids[0]}},
		map[string]any{"ids": "bad", "revision": 1},
		map[string]any{"ids": []string{}, "revision": -1},
	} {
		alice.call("PUT", "/preferences/node-order", body, 400, nil)
	}
	// Offline/heartbeat state never enters the order query.
	if err := db.Heartbeat(ctx, nodes[0].ID, core.HeartbeatRequest{Features: nodes[0].Features}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(alice).IDs, want) {
		t.Fatal("offline reordered nodes")
	}
	added := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, added, alice, 21001)
	want = append(want, added.ID)
	if !reflect.DeepEqual(read(alice).IDs, want) {
		t.Fatal("new node did not append")
	}
	if err := db.DeleteAgent(ctx, nodes[2].ID); err != nil {
		t.Fatal(err)
	}
	filtered := []string{}
	for _, id := range want {
		if id != nodes[2].ID {
			filtered = append(filtered, id)
		}
	}
	if !reflect.DeepEqual(read(alice).IDs, filtered) {
		t.Fatal("deleted node remained visible")
	}
	// Revoke all grants: subsequent reads remove IDs even from an existing session.
	access, err := db.UserAgentAccess(ctx, alice.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SetUserAgentAccess(ctx, alice.userID, core.AgentAccessRequest{Isolated: true, Revision: access.Revision}); err != nil {
		t.Fatal(err)
	}
	if got := read(alice); len(got.IDs) != 0 {
		t.Fatalf("revoked nodes leaked: %+v", got)
	}
}

func TestAccountNodeOrderConcurrentInitialization(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	a := enrollConfigScopeAPIAgent(t, ctx, db)
	b := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, a, alice, 22001)
	grantConfigScopeAPIAgent(t, ctx, db, b, alice, 22001)
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	start := make(chan struct{})
	var group sync.WaitGroup
	results := make(chan store.NodeOrder, 2)
	errs := make(chan error, 2)
	for _, ids := range [][]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			order, err := db.SaveNodeOrder(scoped, ids, 0, true)
			results <- order
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *store.NodeOrder
	for result := range results {
		if result.Revision != 1 {
			t.Fatalf("migration raced: %+v", result)
		}
		if first == nil {
			copy := result
			first = &copy
		} else if !reflect.DeepEqual(first.IDs, result.IDs) {
			t.Fatal("initializers overwrote each other")
		}
	}
	// Competing normal saves require the last confirmed revision; one conflicts.
	nextErrors := make(chan error, 2)
	for _, ids := range [][]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		group.Add(1)
		go func() { defer group.Done(); _, err := db.SaveNodeOrder(scoped, ids, 1, false); nextErrors <- err }()
	}
	group.Wait()
	close(nextErrors)
	conflicts := 0
	for err := range nextErrors {
		if errors.Is(err, store.ErrNodeOrderConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("expected one conflict, got %d", conflicts)
	}
}
