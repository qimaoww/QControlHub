package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

type configScopeAPIClient struct {
	t       *testing.T
	handler http.Handler
	cookie  *http.Cookie
	csrf    string
	token   string
	userID  string
}

func (client configScopeAPIClient) call(method, path string, body any, status int, result any) []byte {
	client.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		client.t.Fatal(err)
	}
	request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	} else {
		request.AddCookie(client.cookie)
		request.Header.Set(csrfHeader, client.csrf)
	}
	response := httptest.NewRecorder()
	client.handler.ServeHTTP(response, request)
	if response.Code != status {
		client.t.Fatalf("%s %s: %d %s, want %d", method, path, response.Code, response.Body.String(), status)
	}
	if result != nil {
		if err := json.Unmarshal(response.Body.Bytes(), result); err != nil {
			client.t.Fatal(err)
		}
	}
	return response.Body.Bytes()
}

func newConfigScopeAPIFixture(t *testing.T) (*store.Store, context.Context, configScopeAPIClient, configScopeAPIClient, configScopeAPIClient) {
	t.Helper()
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	const adminToken = "config-scope-administrator-fixture"
	handler := New(db, Config{AdminToken: adminToken, OperatorTokens: []string{"legacy-alice-fixture", "legacy-bob-fixture"}}).Handler()
	admin := configScopeAPIClient{t: t, handler: handler, token: adminToken}
	login := func(name string) configScopeAPIClient {
		t.Helper()
		const password = "config scope password fixture"
		var user core.User
		admin.call("POST", "/users", core.UserRequest{Username: name, Password: password, Role: core.RoleUser,
			Permissions: []core.Permission{
				core.PermissionOverviewRead, core.PermissionAgentsRead, core.PermissionAgentsManage,
				core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite, core.PermissionConfigsRead,
				core.PermissionConfigsWrite, core.PermissionConfigsDelete, core.PermissionConfigsRestore,
				core.PermissionTemplatesRead, core.PermissionTemplatesWrite, core.PermissionTemplatesDelete,
				core.PermissionTasksRead, core.PermissionTasksExecute, core.PermissionClientAccessRead,
				core.PermissionDeploymentsRead, core.PermissionSettingsManage,
				core.PermissionSettingsRead, core.PermissionEnrollmentManage, core.PermissionAuditRead,
				core.PermissionCoreLogsRead, core.PermissionMetricsRead, core.PermissionTrafficRead, core.PermissionTrafficManage,
			}}, http.StatusCreated, &user)
		payload, _ := json.Marshal(map[string]string{"username": name, "token": password})
		request := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("login %s: %d %s", name, response.Code, response.Body.String())
		}
		var session struct {
			CSRF string `json:"csrf_token"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return configScopeAPIClient{t: t, handler: handler, userID: user.ID, cookie: response.Result().Cookies()[0], csrf: session.CSRF}
	}
	return db, ctx, admin, login("alice"), login("bob")
}

func enrollConfigScopeAPIAgent(t *testing.T, ctx context.Context, db *store.Store) core.Agent {
	t.Helper()
	token, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "shared-host"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "shared-host", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, Features: []string{core.AgentFeatureManagedConfigRead, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress},
		PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentClientAddress(ctx, agent.ID, "edge.example.test"); err != nil {
		t.Fatal(err)
	}
	return agent
}

func grantConfigScopeAPIAgent(t *testing.T, ctx context.Context, db *store.Store, agent core.Agent, client configScopeAPIClient, ports ...int) {
	t.Helper()
	access, err := db.UserAgentAccess(ctx, client.userID)
	if err != nil {
		t.Fatal(err)
	}
	request := core.AgentAccessRequest{Isolated: true, Revision: access.Revision}
	for _, share := range access.Shares {
		enabled := share.Enabled
		request.Shares = append(request.Shares, core.AgentShareRequest{AgentID: share.AgentID,
			Engines: share.Engines, Ports: share.Ports, LimitBytes: share.LimitBytes, Enabled: &enabled})
	}
	request.Shares = append(request.Shares, core.AgentShareRequest{AgentID: agent.ID, Engines: agent.Capabilities, Ports: ports})
	if _, err := db.SetUserAgentAccess(ctx, client.userID, request); err != nil {
		t.Fatal(err)
	}
	acceptConfigScopeAPIInvitations(client)
}

func acceptConfigScopeAPIInvitations(client configScopeAPIClient) core.AgentAccess {
	client.t.Helper()
	var access core.AgentAccess
	client.call("GET", "/agent-access", nil, http.StatusOK, &access)
	for _, share := range access.Shares {
		if share.Enabled && share.Status == core.AgentSharePending {
			client.call("POST", "/agent-access/"+share.ID+"/response",
				core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}, http.StatusOK, &access)
		}
	}
	return access
}

func completeConfigScopeAPITask(t *testing.T, ctx context.Context, db *store.Store, task core.Task) {
	t.Helper()
	claimed, err := db.ClaimTask(ctx, task.AgentID)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim: %+v %v, want %s", claimed, err, task.ID)
	}
	if err := db.CompleteTask(ctx, task.AgentID, task.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func configScopeAPIConfig(t *testing.T, port int) core.Config {
	t.Helper()
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolSS2022)
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Tag, input.Port = "same-tag", port
	content, err := serverconfig.Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	return core.Config{Name: "personal config", Engine: core.EngineMihomo, Content: content}
}

func TestAuthenticatedUsersCannotSelectForeignConfigurations(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	aliceInput, bobInput := configScopeAPIConfig(t, 21001), configScopeAPIConfig(t, 21002)
	aliceInput.OwnerID = bob.userID
	var own, other, archive, second core.Config
	alice.call("PUT", base, aliceInput, http.StatusOK, &own)
	bob.call("PUT", base, bobInput, http.StatusOK, &other)
	if own.ID == other.ID || own.OwnerID != alice.userID || other.OwnerID != bob.userID {
		t.Fatal("authenticated ownership was not enforced")
	}
	alice.call("POST", "/configs", aliceInput, http.StatusCreated, &archive)
	alice.call("POST", "/configs", aliceInput, http.StatusCreated, &second)
	var configs []core.Config
	alice.call("GET", "/configs", nil, http.StatusOK, &configs)
	if len(configs) != 2 {
		t.Fatalf("multiple personal archives were not saved: %+v", configs)
	}
	bob.call("GET", "/configs", nil, http.StatusOK, &configs)
	if len(configs) != 0 {
		t.Fatal("foreign archives appeared in the configuration selector")
	}
	bob.call("GET", "/agents/"+agent.ID+"/configs", nil, http.StatusOK, &configs)
	if len(configs) != 1 || configs[0].ID != other.ID {
		t.Fatal("foreign workspace appeared in node configurations")
	}
	for _, path := range []string{base, base + "/workspace", base + "/files"} {
		body := bob.call("GET", path, nil, http.StatusOK, nil)
		if bytes.Contains(body, []byte(own.ID)) || bytes.Contains(body, []byte(aliceInput.Content)) {
			t.Fatalf("foreign workspace leaked through %s", path)
		}
	}
	aliceInput.Version = 1
	bob.call("PUT", "/configs/"+archive.ID, aliceInput, http.StatusNotFound, nil)
	bob.call("DELETE", "/configs/"+archive.ID, nil, http.StatusNotFound, nil)
	for _, id := range []string{archive.ID, own.ID} {
		bob.call("GET", "/configs/"+id+"/revisions", nil, http.StatusNotFound, nil)
		bob.call("GET", "/configs/"+id+"/revisions/1", nil, http.StatusNotFound, nil)
		bob.call("POST", "/configs/"+id+"/revisions/1/restore", map[string]int{"expected_version": 1}, http.StatusNotFound, nil)
		bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: own.Engine, Action: core.ActionDeploy, ConfigID: id}, http.StatusNotFound, nil)
	}
	aliceInput.ID = own.ID
	bob.call("PUT", base, aliceInput, http.StatusNotFound, nil)
	var template core.ConfigTemplate
	alice.call("POST", "/templates", map[string]any{"name": "Alice template", "engine": "mihomo", "content": aliceInput.Content}, http.StatusCreated, &template)
	bob.call("POST", "/templates/"+template.ID+"/apply", map[string]string{"agent_id": agent.ID}, http.StatusNotFound, nil)
	bob.call("DELETE", "/templates/"+template.ID, nil, http.StatusNotFound, nil)
	var templates []core.ConfigTemplate
	bob.call("GET", "/templates", nil, http.StatusOK, &templates)
	if len(templates) != 0 {
		t.Fatal("foreign template content leaked")
	}
	var task core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: own.Engine, Action: core.ActionValidate, ConfigID: own.ID}, http.StatusCreated, &task)
	for _, path := range []string{"/tasks/" + task.ID, "/tasks/" + task.ID + "?view=status"} {
		bob.call("GET", path, nil, http.StatusNotFound, nil)
	}
	bob.call("DELETE", "/tasks/"+task.ID, nil, http.StatusNotFound, nil)
	bob.call("POST", "/tasks/"+task.ID+"/retry", nil, http.StatusNotFound, nil)
	for _, action := range []core.Action{core.ActionReadConfig, core.ActionReadManagedConfig, core.ActionImportExisting} {
		alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: own.Engine, Action: action}, http.StatusForbidden, nil)
	}
	alice.call("GET", "/tasks/"+task.ID+"/config-snapshot", nil, http.StatusForbidden, nil)
	admin.call("GET", "/configs/"+archive.ID+"/revisions/1", nil, http.StatusOK, nil)
	var overview core.Overview
	bob.call("GET", "/overview", nil, http.StatusOK, &overview)
	if overview.Configs != 0 || overview.NodeConfigs != 1 || overview.TasksPending != 0 {
		t.Fatalf("foreign counts leaked: %+v", overview)
	}

	// Legacy bearer tokens and login cookies must share one durable scope,
	// while two distinct tokens never share a workspace.
	legacy := configScopeAPIClient{t: t, handler: admin.handler, token: "legacy-alice-fixture"}
	legacy.call("POST", "/configs", bobInput, http.StatusCreated, &archive)
	configScopeAPIClient{t: t, handler: admin.handler, token: "legacy-bob-fixture"}.call("GET", "/configs", nil, http.StatusOK, &configs)
	if len(configs) != 0 || archive.OwnerID != tokenConfigOwnerID(legacy.token) {
		t.Fatal("legacy token workspaces were not separated")
	}
	loginBody := bytes.NewBufferString(`{"token":"legacy-alice-fixture"}`)
	response := httptest.NewRecorder()
	admin.handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/v1/auth/login", loginBody))
	if response.Code != http.StatusOK {
		t.Fatalf("legacy cookie login: %d %s", response.Code, response.Body.String())
	}
	legacy.token, legacy.cookie = "", response.Result().Cookies()[0]
	legacy.call("GET", "/configs", nil, http.StatusOK, &configs)
	if len(configs) != 1 || configs[0].ID != archive.ID {
		t.Fatal("legacy token cookie opened a different workspace")
	}
}

func TestSubStoreSyncFormatsAndReplacementDeploymentIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	var mu sync.Mutex
	var remoteGroup map[string]any
	mutations := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "GET":
			groups := []any{}
			if remoteGroup != nil {
				groups = append(groups, remoteGroup)
			}
			writeSubStoreTestEnvelope(t, w, http.StatusOK, groups, "")
		case "POST", "PATCH":
			remoteGroup = decodeSubStoreTestPayload(t, r.Body)
			mutations++
			writeSubStoreTestEnvelope(t, w, http.StatusOK, remoteGroup, "")
		default:
			t.Errorf("unexpected remote mutation %s", r.Method)
		}
	}))
	defer remote.Close()
	admin.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: remote.URL + "/fixture-secret"}, http.StatusOK, nil)
	// Sharing an Agent never inherits the owner's integration credentials.
	alice.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: remote.URL + "/fixture-secret"}, http.StatusOK, nil)
	bob.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: remote.URL + "/fixture-secret"}, http.StatusOK, nil)
	var own, other core.Config
	base := "/agents/" + agent.ID + "/configs/mihomo"
	alice.call("PUT", base, configScopeAPIConfig(t, 21001), http.StatusOK, &own)
	bob.call("PUT", base, configScopeAPIConfig(t, 21002), http.StatusOK, &other)
	deploy := func(client configScopeAPIClient, config core.Config) {
		t.Helper()
		var task core.Task
		client.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID}, http.StatusCreated, &task)
		completeConfigScopeAPITask(t, ctx, db, task)
	}
	deploy(alice, own)
	var target core.SubStoreSyncTarget
	alice.call("POST", "/substore-sync/targets", subStoreTargetRequest{DisplayName: "Alice", SyncFormat: "mihomo"}, http.StatusCreated, &target)
	var resource subStoreSyncResource
	alice.call("GET", "/substore-sync", nil, http.StatusOK, &resource)
	if len(resource.Profiles) != 1 || resource.Profiles[0].ConfigID != own.ID {
		t.Fatalf("missing private deployment: %+v", resource.Profiles)
	}
	selections := []core.SubStoreSyncSelection{{AgentID: agent.ID, Engine: own.Engine, ProfileTag: "same-tag", CustomName: "Private node"}}
	// Old clients omit config_id; the server must pin it, never retain a wildcard.
	var stored []core.SubStoreSyncSelection
	alice.call("PUT", "/substore-sync/selections", subStoreSelectionsRequest{TargetID: target.ID, Selections: selections}, http.StatusOK, &stored)
	if len(stored) != 1 || stored[0].ConfigID != own.ID {
		t.Fatal("legacy selection was not pinned to its owner's deployed configuration")
	}
	for _, format := range []string{"mihomo", "url", "mihomo"} {
		alice.call("PUT", "/substore-sync/targets/"+target.ID, subStoreTargetRequest{DisplayName: "Alice", SyncFormat: format}, http.StatusOK, nil)
		alice.call("POST", "/substore-sync/run", subStoreRunRequest{TargetID: target.ID}, http.StatusOK, nil)
		mu.Lock()
		content, _ := remoteGroup["content"].(string)
		mu.Unlock()
		_, _, yamlNode := subStoreMihomoNode(content)
		if (format == "mihomo") != yamlNode || (format == "url" && !strings.HasPrefix(content, "ss://")) || subStoreNodeName(content) != "Private node" {
			t.Fatalf("wrong %s sync payload: %q", format, content)
		}
	}
	bob.call("GET", "/substore-sync", nil, http.StatusOK, &resource)
	if len(resource.Targets) != 0 || len(resource.Profiles) != 0 {
		t.Fatal("another user's Sub-Store group or deployment was exposed")
	}
	for _, method := range []string{"PUT", "DELETE"} {
		bob.call(method, "/substore-sync/targets/"+target.ID, subStoreTargetRequest{DisplayName: "stolen"}, http.StatusNotFound, nil)
	}
	bob.call("GET", "/substore-sync?target_id="+target.ID, nil, http.StatusNotFound, nil)
	bob.call("POST", "/substore-sync/run", subStoreRunRequest{TargetID: target.ID}, http.StatusNotFound, nil)
	var remotes []subStoreRemoteTarget
	bob.call("GET", "/substore-sync/remote-targets", nil, http.StatusOK, &remotes)
	if len(remotes) != 0 {
		t.Fatal("another user's remote group appeared as importable")
	}
	bob.call("POST", "/substore-sync/targets/import", subStoreImportTargetRequest{SubscriptionName: target.SubscriptionName}, http.StatusConflict, nil)
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: other.Engine, Action: core.ActionDeploy, ConfigID: other.ID}, http.StatusConflict, nil)
	var stop core.Task
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: other.Engine, Action: core.ActionStop}, http.StatusCreated, &stop)
	completeConfigScopeAPITask(t, ctx, db, stop)
	deploy(bob, other)
	alice.call("GET", "/substore-sync", nil, http.StatusOK, &resource)
	if len(resource.Profiles) != 1 || resource.Profiles[0].Available || resource.Profiles[0].ConfigID != own.ID {
		t.Fatal("stale selection rebound to another user's same-named inbound")
	}
	alice.call("POST", "/substore-sync/run", subStoreRunRequest{TargetID: target.ID}, http.StatusConflict, nil)
	var access []clientAccessEntry
	alice.call("GET", "/client-access", nil, http.StatusOK, &access)
	if len(access) != 0 {
		t.Fatal("replaced deployment exposed the new owner's credentials")
	}
	// Even the administrator must not silently rebind a target's pinned config.
	admin.call("POST", "/substore-sync/run", subStoreRunRequest{TargetID: target.ID}, http.StatusNotFound, nil)
	mu.Lock()
	defer mu.Unlock()
	if mutations != 3 {
		t.Fatalf("blocked requests mutated the remote subscription: %d writes", mutations)
	}
}

func TestApplyingPrivateTemplatePreservesOtherUsersTraffic(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21003)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	var bobConfig core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21002), http.StatusOK, &bobConfig)
	var task core.Task
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: bobConfig.Engine, Action: core.ActionDeploy, ConfigID: bobConfig.ID}, http.StatusCreated, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	unrelated := enrollConfigScopeAPIAgent(t, ctx, db)
	endpoint := core.PortTrafficEndpoint{AgentID: unrelated.ID, Engine: core.EngineMihomo, Name: "unrelated", Port: 21004, Protocol: core.TrafficProtocolBoth}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, []core.PortTrafficEndpoint{endpoint}, false); err != nil {
		t.Fatal(err)
	}
	var template core.ConfigTemplate
	input := configScopeAPIConfig(t, 21003)
	alice.call("POST", "/templates", map[string]string{"name": "private template", "engine": "mihomo", "content": input.Content}, http.StatusCreated, &template)
	alice.call("POST", "/templates/"+template.ID+"/apply", map[string]string{"agent_id": agent.ID}, http.StatusOK, nil)
	for _, item := range []struct {
		agentID string
		port    int
	}{{agent.ID, 21002}, {unrelated.ID, 21004}} {
		policies, err := db.AgentPortTrafficPolicies(ctx, item.agentID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, policy := range policies {
			if policy.Port == 21003 {
				t.Fatal("an undeployed shared draft created a host firewall monitor")
			}
			found = found || policy.Port == item.port
		}
		if !found {
			t.Fatalf("applying template removed monitored port %d on %s", item.port, item.agentID)
		}
	}
}
