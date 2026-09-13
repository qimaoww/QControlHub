package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPurgeUserRefreshesLivePoliciesAndRevokesRemoteSessions(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	owned, credential := ownedConfigScopeAPIAgent(t, ctx, db, alice, "purge-owned-hidden")
	borrowed, _ := ownedConfigScopeAPIAgent(t, ctx, db, bob, "purge-borrowed")
	unrelated := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, borrowed, alice, 21001)
	alice.call("PUT", "/agents/"+owned.ID+"/visibility", map[string]bool{"admin_hidden": true}, http.StatusOK, nil)
	publicKey, err := db.AgentPublicKey(ctx, owned.ID)
	if err != nil {
		t.Fatal(err)
	}

	server := New(db, Config{AdminToken: admin.token})
	admin.handler = server.Handler()
	var administrator core.User
	admin.call("POST", "/users", core.UserRequest{Username: "purge-named-admin", Role: core.RoleAdmin,
		Password: "config scope password fixture"}, http.StatusCreated, &administrator)
	namedAdmin := auditPR183Login(configScopeAPIClient{t: t, handler: admin.handler, userID: administrator.ID}, administrator.Username)
	// A different control-plane instance retains this session in memory.
	remote := auditPR183Login(configScopeAPIClient{t: t, handler: New(db, Config{}).Handler(), userID: alice.userID}, "alice")

	alice.call("POST", "/users/"+bob.userID+"/purge", nil, http.StatusForbidden, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+alice.userID+"/purge", nil)
	request.AddCookie(namedAdmin.cookie)
	response := httptest.NewRecorder()
	admin.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("purge without CSRF: %d %s", response.Code, response.Body.String())
	}
	namedAdmin.call("POST", "/users/"+administrator.ID+"/purge", nil, http.StatusConflict, nil)

	disconnected := false
	updates := make(map[string]<-chan struct{})
	for _, agent := range []core.Agent{owned, borrowed, unrelated} {
		updates[agent.ID] = server.registerConnection(agent.ID, "purge-live-test", func() { disconnected = true })
		defer server.unregisterConnection(agent.ID, "purge-live-test")
	}
	namedAdmin.call("POST", "/users/"+alice.userID+"/purge", nil, http.StatusNoContent, nil)
	if disconnected {
		t.Fatal("account purge disconnected an Agent")
	}
	for _, id := range []string{owned.ID, borrowed.ID} {
		select {
		case <-updates[id]:
		default:
			t.Errorf("affected Agent %s did not receive a policy refresh", id)
		}
	}
	select {
	case <-updates[unrelated.ID]:
		t.Error("purge refreshed an unrelated Agent")
	default:
	}
	remote.call("GET", "/agents", nil, http.StatusUnauthorized, nil)
	namedAdmin.call("POST", "/users/"+alice.userID+"/purge", nil, http.StatusNotFound, nil)
	var visible []core.Agent
	namedAdmin.call("GET", "/agents", nil, http.StatusOK, &visible)
	if !slices.ContainsFunc(visible, func(agent core.Agent) bool {
		return agent.ID == owned.ID && !agent.AdminHidden && agent.OwnerID == ""
	}) {
		t.Fatalf("named administrator cannot take over the hidden node: %+v", visible)
	}
	if key, err := db.AgentPublicKey(ctx, owned.ID); err != nil || !slices.Equal(key, publicKey) {
		t.Fatalf("purge changed the retained node's identity: %v", err)
	}
	if db.EnrollmentTokenUsable(ctx, credential.Token) {
		t.Fatal("deleted account can reinstall the retained node")
	}
	// recordAudit writes asynchronously after the response has completed.
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := db.ListAuditLogs(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(entries, func(entry core.AuditLogEntry) bool {
			return entry.Action == "user.purged" && entry.Target == alice.userID && strings.Contains(entry.Detail, "alice")
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("purge audit is not tied to the durable account ID and username")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
