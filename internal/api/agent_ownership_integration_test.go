package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestTaskFailureWebhookUsesTaskOwnerNotSharedHostOwner(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	type delivery struct {
		path  string
		event notify.Event
	}
	deliveries := make(chan delivery, 4)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, "bad event", http.StatusBadRequest)
			return
		}
		deliveries <- delivery{r.URL.Path, event}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	for _, item := range []struct {
		client configScopeAPIClient
		path   string
	}{{admin, "/admin"}, {alice, "/alice"}, {bob, "/bob"}} {
		settings := core.DefaultPanelSettings()
		settings.NotifyTaskFailed, settings.WebhookURL = true, endpoint.URL+item.path
		item.client.call("PUT", "/settings", settings, http.StatusOK, nil)
	}
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "alice-owned-shared")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	config := configScopeAPIConfig(t, 21001)
	var saved core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", config, http.StatusOK, &saved)
	var task core.Task
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, ConfigID: saved.ID, Engine: saved.Engine, Action: core.ActionValidate}, http.StatusCreated, &task)
	server := New(db, Config{})
	server.notifyTaskFailure(agent.ID, task.ID, core.TaskResultRequest{Error: "Bob private validation diagnostic"})
	select {
	case got := <-deliveries:
		if got.path != "/bob" || got.event.TaskID != task.ID || got.event.Error != "Bob private validation diagnostic" {
			t.Fatalf("private task sent to wrong account: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no owner webhook received")
	}
}

func ownedConfigScopeAPIAgent(t *testing.T, ctx context.Context, db *store.Store, client configScopeAPIClient, name string, engines ...core.Engine) (core.Agent, core.EnrollmentTokenCreated) {
	t.Helper()
	if len(engines) == 0 {
		engines = []core.Engine{core.EngineMihomo}
	}
	var token core.EnrollmentTokenCreated
	client.call("POST", "/enrollment-tokens", map[string]string{"name": name}, http.StatusCreated, &token)
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: name, OS: "linux", Arch: "amd64",
		Capabilities: engines, Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureManagedConfigRead, core.AgentFeatureIndependentEgress},
		PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features,
		Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	return agent, token
}

func TestAccountsOwnAgentsSettingsAndSharing(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	own, token := ownedConfigScopeAPIAgent(t, ctx, db, alice, "same-node-name")
	other, otherToken := ownedConfigScopeAPIAgent(t, ctx, db, bob, "same-node-name")
	legacy := enrollConfigScopeAPIAgent(t, ctx, db)
	if own.ID == other.ID || own.OwnerID != alice.userID || other.OwnerID != bob.userID {
		t.Fatal("enrollment did not bind the credential's account")
	}
	var agents []core.Agent
	for _, client := range []configScopeAPIClient{alice, bob} {
		client.call("GET", "/agents", nil, http.StatusOK, &agents)
		wantID := own.ID
		if client.userID == bob.userID {
			wantID = other.ID
		}
		if len(agents) != 1 || agents[0].ID != wantID || !agents[0].CanManage {
			t.Fatalf("account did not see only its own manageable node: %+v", agents)
		}
		client.call("GET", "/agents/"+legacy.ID+"/configs", nil, http.StatusNotFound, nil)
		client.call("GET", "/users", nil, http.StatusForbidden, nil)
	}
	alice.call("POST", "/enrollment-tokens/"+otherToken.ID+"/command", nil, http.StatusNotFound, nil)
	alice.call("DELETE", "/enrollment-tokens/"+otherToken.ID, nil, http.StatusNotFound, nil)
	var tokens []core.EnrollmentToken
	alice.call("GET", "/enrollment-tokens", nil, http.StatusOK, &tokens)
	if len(tokens) != 1 || tokens[0].ID != token.ID {
		t.Fatal("another account's enrollment credentials leaked")
	}
	// The same protected credential may reinstall only its original node.
	reinstalled, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: own.Name, OS: "linux", Arch: "amd64",
		Capabilities: own.Capabilities, Features: own.Features,
		PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
	if err != nil || reinstalled.ID != own.ID || reinstalled.OwnerID != alice.userID {
		t.Fatalf("owned reinstall changed identity: %+v %v", reinstalled, err)
	}
	settings := core.DefaultPanelSettings()
	settings.PanelName, settings.KomariURL, settings.KomariAPIKey = "Alice panel", "https://komari.example", "alice-private-key"
	settings.AgentMetricsIntervalSeconds = 5
	alice.call("PUT", "/settings", settings, http.StatusOK, nil)
	for _, client := range []configScopeAPIClient{admin, bob} {
		body := client.call("GET", "/settings", nil, http.StatusOK, nil)
		if bytes.Contains(body, []byte("Alice panel")) || bytes.Contains(body, []byte("komari.example")) {
			t.Fatal("personal panel settings leaked to another account")
		}
	}
	alice.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: "https://substore.example/alice-secret"}, http.StatusOK, nil)
	var integration subStoreSyncResource
	bob.call("GET", "/substore-sync", nil, http.StatusOK, &integration)
	if integration.Settings.Configured || len(integration.Targets) != 0 {
		t.Fatal("another account inherited a Sub-Store backend")
	}
	var sharing core.AgentSharing
	alice.call("GET", "/agents/"+own.ID+"/sharing", nil, http.StatusOK, &sharing)
	request := core.AgentSharingRequest{Revision: sharing.Revision,
		Shares: []core.AgentSharingRecipient{{Username: "BoB", Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21002}, LimitBytes: 1000}}}
	alice.call("PUT", "/agents/"+own.ID+"/sharing", request, http.StatusOK, &sharing)
	alice.call("PUT", "/agents/"+own.ID+"/sharing", request, http.StatusConflict, nil)
	acceptConfigScopeAPIInvitations(bob)
	alice.call("GET", "/agents/"+own.ID+"/sharing", nil, http.StatusOK, &sharing)
	bob.call("GET", "/agents", nil, http.StatusOK, &agents)
	if len(agents) != 2 {
		t.Fatal("explicit sharing was not visible alongside owned nodes")
	}
	for _, agent := range agents {
		if agent.CanManage != (agent.ID == other.ID) {
			t.Fatal("a share conferred host administration")
		}
	}
	for _, operation := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/sharing", nil}, {"PUT", "/sharing", request},
		{"PUT", "/name", map[string]string{"name": "stolen"}}, {"DELETE", "", nil},
		{"POST", "/enrollment-command", nil}, {"POST", "/enrollment-token", nil},
		{"GET", "/komari", nil},
	} {
		bob.call(operation.method, "/agents/"+own.ID+operation.path, operation.body, http.StatusForbidden, nil)
	}
	bob.call("GET", "/core-logs?agent_id="+own.ID, nil, http.StatusForbidden, nil)
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: own.ID, Engine: core.EngineMihomo, Action: core.ActionStop}, http.StatusForbidden, nil)
	request.Revision, request.Shares = sharing.Revision, nil
	alice.call("PUT", "/agents/"+own.ID+"/sharing", request, http.StatusOK, nil)
	bob.call("GET", "/agents/"+own.ID+"/configs", nil, http.StatusNotFound, nil)
	bob.call("GET", "/agents", nil, http.StatusOK, &agents)
	if len(agents) != 1 || agents[0].ID != other.ID {
		t.Fatal("revocation hid an owned node or retained borrowed access")
	}
}

func TestSharedProfilePreferencesDoNotTransferWithPorts(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "profile-handoff")
	if err := db.SetAgentClientAddress(store.WithConfigScope(ctx, alice.userID, false), agent.ID, "edge.example.test"); err != nil {
		t.Fatal(err)
	}
	// Own-host reads are available before another account deploys.
	var read core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionReadManagedConfig}, http.StatusCreated, &read)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != read.ID {
		t.Fatalf("claim own read: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: "listeners: []\n"}); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusOK, nil)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	var config core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21002), http.StatusOK, &config)
	var task core.Task
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID}, http.StatusCreated, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	selector := map[string]any{"profile": clientProfileSelector{Engine: config.Engine, Tag: "same-tag", Port: 21002},
		"name": "Bob private label", "address": "bob.example.test", "address_mode": "ipv4"}
	bob.call("PUT", "/agents/"+agent.ID+"/client-address", selector, http.StatusOK, nil)
	body := bob.call("GET", "/client-access", nil, http.StatusOK, nil)
	if !bytes.Contains(body, []byte("Bob private label")) || !bytes.Contains(body, []byte("bob.example.test")) {
		t.Fatalf("configuration owner cannot customize its client presentation: %s", body)
	}
	for _, client := range []configScopeAPIClient{alice, bob} {
		body := client.call("GET", "/agents", nil, http.StatusOK, nil)
		if bytes.Contains(body, []byte("Bob private label")) || bytes.Contains(body, []byte("client_profile_")) {
			t.Fatal("profile preferences leaked through host labels")
		}
	}
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionReadManagedConfig}, http.StatusForbidden, nil)
	alice.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusForbidden, nil)
	// Stop, settle and release the reservation before reusing the same port.
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStop}, http.StatusCreated, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	var sharing core.AgentSharing
	alice.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	disabled := false
	alice.call("PUT", "/agents/"+agent.ID+"/sharing", core.AgentSharingRequest{Revision: sharing.Revision,
		Shares: []core.AgentSharingRecipient{{Username: "bob", Enabled: &disabled, Ports: []int{}}}}, http.StatusOK, nil)
	alice.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21002), http.StatusOK, &config)
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID}, http.StatusCreated, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	body = alice.call("GET", "/client-access", nil, http.StatusOK, nil)
	if bytes.Contains(body, []byte("Bob private label")) || bytes.Contains(body, []byte("bob.example.test")) {
		t.Fatal("reused port inherited another account's presentation")
	}
	bob.call("PUT", "/agents/"+agent.ID+"/client-address", selector, http.StatusNotFound, nil)
}

func TestAccountChangesInvalidateSessionsAcrossServers(t *testing.T) {
	for _, change := range []string{"disable", "password", "permissions"} {
		t.Run(change, func(t *testing.T) {
			db, _, _, alice, _ := newConfigScopeAPIFixture(t)
			const token = "second-server-breakglass-token"
			other := configScopeAPIClient{t: t, handler: New(db, Config{AdminToken: token}).Handler(), token: token}
			alice.call("GET", "/auth/session", nil, http.StatusOK, nil)
			switch change {
			case "disable":
				other.call("DELETE", "/users/"+alice.userID, nil, http.StatusNoContent, nil)
			case "password":
				other.call("PUT", "/users/"+alice.userID, map[string]string{"password": "replacement-password-fixture"}, http.StatusOK, nil)
			default:
				other.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": []string{"agents.read"}}, http.StatusOK, nil)
			}
			alice.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			alice.call("GET", "/configs", nil, http.StatusUnauthorized, nil)
		})
	}
}

func TestAsyncAuditPreservesAccountScope(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	var template core.ConfigTemplate
	alice.call("POST", "/templates", map[string]string{"name": "private audit", "engine": "mihomo", "content": "listeners: []"}, http.StatusCreated, &template)
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := db.ListAuditLogs(store.WithConfigScope(ctx, alice.userID, false), 200)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range entries {
			found = found || entry.Target == template.ID
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("asynchronous audit lost the authenticated account")
		}
		time.Sleep(10 * time.Millisecond)
	}
	body := bob.call("GET", "/audit", nil, http.StatusOK, nil)
	if bytes.Contains(body, []byte(template.ID)) {
		t.Fatal("private audit entry was visible to another account")
	}
}
