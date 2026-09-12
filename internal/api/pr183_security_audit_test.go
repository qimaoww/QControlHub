package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Isolated-schema security regressions. No production or live-fixture
// accounts are used.
func auditPR183Request(client configScopeAPIClient, method, path string, body any) *httptest.ResponseRecorder {
	client.t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		client.t.Fatal(err)
	}
	request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	} else {
		request.AddCookie(client.cookie)
		request.Header.Set(csrfHeader, client.csrf)
	}
	response := httptest.NewRecorder()
	client.handler.ServeHTTP(response, request)
	return response
}

func auditPR183Login(client configScopeAPIClient, username string) configScopeAPIClient {
	client.t.Helper()
	response := auditPR183Request(client, "POST", "/auth/login", map[string]string{
		"username": username,
		"token":    "config scope password fixture",
	})
	if response.Code != http.StatusOK {
		client.t.Fatalf("audit login: %d %s", response.Code, response.Body.String())
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		client.t.Fatal(err)
	}
	client.cookie, client.csrf = response.Result().Cookies()[0], session.CSRF
	return client
}

func TestPR183AuditQueuedHostReadCannotExposeRecipientConfig(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "queued-read-audit")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	bobInput := configScopeAPIConfig(t, 21001)
	bobInput.Content = strings.ReplaceAll(bobInput.Content, "same-tag", "bob-private-audit-sentinel")
	var bobConfig core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", bobInput, http.StatusOK, &bobConfig)
	var deploy core.Task
	bob.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: bobConfig.ID,
	}, http.StatusCreated, &deploy)

	// The recipient's deployment is pending, so the owner still passes the
	// creation-time check against the previous engine ownership.
	response := auditPR183Request(alice, "POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionReadManagedConfig,
	})
	if response.Code == http.StatusForbidden || response.Code == http.StatusConflict {
		return
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("queue host read: %d %s", response.Code, response.Body.String())
	}
	var read core.Task
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	completeConfigScopeAPITask(t, ctx, db, deploy)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		return // Dispatch-time reauthorization rejected the obsolete read.
	}
	if claimed.ID != read.ID {
		t.Fatalf("claimed an unexpected task: %s", claimed.ID)
	}
	// The real Agent reads its current on-disk configuration at execution
	// time. At this point it is the successfully deployed recipient config.
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, Output: bobConfig.Content,
	}); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, http.StatusForbidden, nil)

	var stop core.Task
	alice.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop,
	}, http.StatusCreated, &stop)
	completeConfigScopeAPITask(t, ctx, db, stop)
	var own core.Config
	alice.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21002), http.StatusOK, &own)
	alice.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: own.ID,
	}, http.StatusCreated, &deploy)
	completeConfigScopeAPITask(t, ctx, db, deploy)

	response = auditPR183Request(alice, "GET", "/tasks/"+read.ID+"/config-snapshot", nil)
	leaked := bytes.Contains(response.Body.Bytes(), []byte("bob-private-audit-sentinel"))
	t.Logf("old owner read after recipient deploy and owner takeover: HTTP %d, recipient content=%t", response.Code, leaked)
	if response.Code == http.StatusOK && leaked {
		t.Fatal("node owner retrieved the recipient's private configuration through an obsolete queued read")
	}
}

func TestPR183AuditRevokedTaskCapabilityStopsQueuedExecution(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "removed-capability-audit")
	var task core.Task
	alice.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop,
	}, http.StatusCreated, &task)
	permissions := []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead}
	admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
	alice = auditPR183Login(alice, "alice")
	alice.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop,
	}, http.StatusForbidden, nil)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Fatalf("tasks.execute was removed, but queued %s task was dispatched: %s", claimed.Action, claimed.ID)
	}
}

func TestPR183AuditDisabledThenEnabledUserCannotResumeOldLease(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "disabled-lease-audit")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	var config core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusOK, &config)
	var task core.Task
	bob.call("POST", "/tasks", core.TaskRequest{
		AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: config.ID,
	}, http.StatusCreated, &task)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("initial dispatch: %+v %v", claimed, err)
	}
	// The Agent is disconnected while the administrator disables and later
	// reenables the user. Neither transition should preserve the old lease.
	disabled := true
	admin.call("PUT", "/users/"+bob.userID, core.UserUpdate{Disabled: &disabled}, http.StatusOK, nil)
	disabled = false
	admin.call("PUT", "/users/"+bob.userID, core.UserUpdate{Disabled: &disabled}, http.StatusOK, nil)
	resumed, err := db.RunningTask(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed != nil && resumed.ID == task.ID && resumed.LeaseID == claimed.LeaseID {
		t.Fatal("reenabling a disabled recipient restored the pre-disable deployment lease")
	}
}

func TestPR183AuditWorkspaceCannotBypassMetricCapability(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "metric-capability-audit")
	if err := db.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{
		CollectedAt: time.Now().UTC(), CPUAvailable: true, CPUPercent: 47.3,
	}); err != nil {
		t.Fatal(err)
	}
	permissions := []core.Permission{core.PermissionAgentsRead, core.PermissionAgentConfigRead}
	admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
	alice = auditPR183Login(alice, "alice")
	alice.call("GET", "/metrics/"+agent.ID, nil, http.StatusForbidden, nil)
	var agents []core.Agent
	alice.call("GET", "/agents", nil, http.StatusOK, &agents)
	if len(agents) != 1 || agents[0].Metrics.CPUAvailable {
		t.Fatal("baseline list did not redact metrics")
	}
	var workspace struct {
		Agent core.Agent `json:"agent"`
	}
	alice.call("GET", "/agents/"+agent.ID+"/configs/mihomo/workspace", nil, http.StatusOK, &workspace)
	if workspace.Agent.Metrics.CPUAvailable || workspace.Agent.Metrics.CPUPercent != 0 {
		t.Fatalf("workspace disclosed metrics without metrics.read: cpu_available=%t cpu_percent=%.1f",
			workspace.Agent.Metrics.CPUAvailable, workspace.Agent.Metrics.CPUPercent)
	}
}

// A template that expands {{lan_ip}} copies host-metric data into a returned
// and saved configuration. A principal with templates.write and
// agent-config.write but no metrics.read must be rejected before rendering.
func TestPR183AuditTemplateCannotBypassMetricCapability(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "template-capability-audit")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	const privateAddress = "10.83.42.77"
	if err := db.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{
		CollectedAt:       time.Now().UTC(),
		NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{privateAddress}}},
	}); err != nil {
		t.Fatal(err)
	}
	permissions := []core.Permission{
		core.PermissionAgentsRead, core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite,
		core.PermissionConfigsRead, core.PermissionTemplatesRead, core.PermissionTemplatesWrite, core.PermissionTasksRead,
	}
	admin.call("PUT", "/users/"+bob.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
	bob = auditPR183Login(bob, "bob")
	bob.call("GET", "/metrics/"+agent.ID, nil, http.StatusForbidden, nil)
	list := bob.call("GET", "/agents", nil, http.StatusOK, nil)
	if bytes.Contains(list, []byte(privateAddress)) {
		t.Fatal("baseline agent list did not redact metrics")
	}
	input := configScopeAPIConfig(t, 21001)
	input.Content += "\n# template-lan-ip: {{lan_ip}}\n"
	var template core.ConfigTemplate
	bob.call("POST", "/templates", map[string]string{
		"name": "metric-capability-template", "engine": "mihomo", "content": input.Content,
	}, http.StatusCreated, &template)
	response := auditPR183Request(bob, "POST", "/templates/"+template.ID+"/apply",
		map[string]string{"agent_id": agent.ID})
	if response.Code != http.StatusForbidden {
		t.Fatalf("template apply without metrics.read: %d %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(privateAddress)) {
		t.Fatal("rejected template apply still disclosed the host LAN address")
	}
	var configs []core.Config
	bob.call("GET", "/agents/"+agent.ID+"/configs", nil, http.StatusOK, &configs)
	if len(configs) != 0 {
		t.Fatalf("rejected template apply persisted %d configurations", len(configs))
	}
	// A caller holding metrics.read keeps the documented placeholder.
	var allowed core.ConfigTemplate
	alice.call("POST", "/templates", map[string]string{
		"name": "metric-capability-allowed", "engine": "mihomo", "content": input.Content,
	}, http.StatusCreated, &allowed)
	var saved core.Config
	alice.call("POST", "/templates/"+allowed.ID+"/apply", map[string]string{"agent_id": agent.ID}, http.StatusOK, &saved)
	if !strings.Contains(saved.Content, privateAddress) {
		t.Fatal("metrics.read caller did not receive the rendered LAN address")
	}
}

// Holding agents.manage or enrollment.manage does not confer host authority
// over a node that was merely shared. Every host-administration path must keep
// failing for the recipient after the grant is accepted.
func TestPR183AuditSharedRecipientCannotAdministerHost(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "recipient-admin-audit")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	var sharing core.AgentSharing
	alice.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	if len(sharing.Shares) != 1 || sharing.Shares[0].UserID != bob.userID || sharing.Shares[0].Status != core.AgentShareAccepted {
		t.Fatalf("sharing fixture mismatch: %+v", sharing)
	}
	for _, operation := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/agents/" + agent.ID + "/capabilities/mihomo", map[string]any{"enabled": false}},
		{"PUT", "/agents/" + agent.ID + "/region", map[string]any{"country_code": "US"}},
		{"PUT", "/agents/" + agent.ID + "/komari", map[string]any{"uuid": "shared-node-uuid"}},
		{"GET", "/agents/" + agent.ID + "/sharing", nil},
		{"PUT", "/agents/" + agent.ID + "/sharing", core.AgentSharingRequest{Revision: sharing.Revision}},
		{"DELETE", "/agents/" + agent.ID, nil},
		{"POST", "/agents/" + agent.ID + "/enrollment-token", map[string]any{}},
		{"POST", "/agents/" + agent.ID + "/enrollment-command", map[string]any{}},
	} {
		bob.call(operation.method, operation.path, operation.body, http.StatusForbidden, nil)
	}
	// Account administration is unavailable regardless of the users.manage
	// capability the recipient may also hold.
	bob.call("GET", "/users/"+alice.userID+"/agent-access", nil, http.StatusForbidden, nil)
	bob.call("PUT", "/users/"+alice.userID+"/agent-access",
		core.AgentAccessRequest{Revision: 1, Isolated: true}, http.StatusForbidden, nil)
	// The owner still sees the accepted grant untouched by the denied calls.
	alice.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	if len(sharing.Shares) != 1 || sharing.Shares[0].Status != core.AgentShareAccepted || !sharing.Shares[0].Enabled {
		t.Fatalf("denied recipient administration altered the grant: %+v", sharing)
	}
}

// Automatic region detection and the interface list are both derived from host
// metrics. A recipient must not learn a host address without metrics.read, and
// even with the capability a borrowed node must expose only globally routable
// addresses.
func TestPR183AuditSharedNodeAddressDisclosure(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "address-disclosure-audit")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	const privateAddress, publicAddress = "10.83.42.77", "8.8.4.4"
	if err := db.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{
		CollectedAt:       time.Now().UTC(),
		CPUAvailable:      true,
		CPUPercent:        12.5,
		PublicIPv4:        publicAddress,
		PublicIPv4Source:  core.PublicIPProbeSourceAgent,
		NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{privateAddress, publicAddress}}},
	}); err != nil {
		t.Fatal(err)
	}
	permissions := []core.Permission{core.PermissionAgentsRead, core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite}
	admin.call("PUT", "/users/"+bob.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
	bob = auditPR183Login(bob, "bob")
	region := bob.call("GET", "/agents/"+agent.ID+"/region", nil, http.StatusOK, nil)
	if bytes.Contains(region, []byte(publicAddress)) || bytes.Contains(region, []byte(privateAddress)) || bytes.Contains(region, []byte(`"country"`)) {
		t.Fatalf("region endpoint disclosed a host address without metrics.read: %s", region)
	}
	permissions = append(permissions, core.PermissionMetricsRead)
	admin.call("PUT", "/users/"+bob.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
	bob = auditPR183Login(bob, "bob")
	region = bob.call("GET", "/agents/"+agent.ID+"/region", nil, http.StatusOK, nil)
	if !bytes.Contains(region, []byte(publicAddress)) {
		t.Fatalf("metrics.read caller lost automatic region detection: %s", region)
	}
	list := bob.call("GET", "/agents", nil, http.StatusOK, nil)
	if bytes.Contains(list, []byte(privateAddress)) {
		t.Fatalf("borrowed node exposed private interface addresses: %s", list)
	}
	if !bytes.Contains(list, []byte(publicAddress)) {
		t.Fatalf("borrowed node lost its routable address candidate: %s", list)
	}
}

// An owner-hidden node stays invisible and unmanageable for every
// administrator credential, remains usable by its owner and explicit share
// recipients, appears read-only in the cross-account directory, and stays
// deletable so a stale node can always be cleaned up.
func TestPR183AuditOwnerHiddenNodeVisibility(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	var created core.EnrollmentTokenCreated
	alice.call("POST", "/enrollment-tokens", map[string]any{
		"name": "hidden-node-audit", "admin_hidden": true,
	}, http.StatusCreated, &created)
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{
		Name: "hidden-node-audit", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features: []string{core.AgentFeatureManagedConfigRead, core.AgentFeatureSharedTraffic,
			core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress},
		PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)),
	}, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !agent.AdminHidden {
		t.Fatal("enrollment did not carry the owner-hidden choice")
	}
	var adminAgents, ownerAgents []core.Agent
	admin.call("GET", "/agents", nil, http.StatusOK, &adminAgents)
	for _, item := range adminAgents {
		if item.ID == agent.ID {
			t.Fatal("administrator list exposed an owner-hidden node")
		}
	}
	alice.call("GET", "/agents", nil, http.StatusOK, &ownerAgents)
	ownerVisible := false
	for _, item := range ownerAgents {
		if item.ID == agent.ID {
			ownerVisible = true
			if !item.AdminHidden {
				t.Fatal("owner list lost the hidden marker")
			}
		}
	}
	if !ownerVisible {
		t.Fatal("owner lost access to the hidden node")
	}
	// Fleet maintenance runs without a request principal and must still see
	// owner-hidden nodes so accounting and monitoring keep working.
	systemAgents, err := db.ListAgents(store.WithSystemScope(ctx))
	if err != nil {
		t.Fatal(err)
	}
	systemVisible := false
	for _, item := range systemAgents {
		if item.ID == agent.ID {
			systemVisible = true
		}
	}
	if !systemVisible {
		t.Fatal("system maintenance lost access to the owner-hidden node")
	}
	for _, path := range []string{
		"/agents/" + agent.ID + "/configs",
		"/agents/" + agent.ID + "/sharing",
		"/agents/" + agent.ID + "/komari",
		"/agents/" + agent.ID + "/region",
		"/metrics/" + agent.ID,
		"/core-logs?agent_id=" + agent.ID,
	} {
		admin.call("GET", path, nil, http.StatusNotFound, nil)
	}
	admin.call("PUT", "/agents/"+agent.ID+"/name", map[string]string{"name": "stolen"}, http.StatusNotFound, nil)
	admin.call("PUT", "/agents/"+agent.ID+"/visibility", map[string]bool{"admin_hidden": false}, http.StatusNotFound, nil)
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop}, http.StatusNotFound, nil)
	var directory []core.AgentDirectoryEntry
	admin.call("GET", "/agent-directory", nil, http.StatusOK, &directory)
	directoryFound := false
	for _, entry := range directory {
		if entry.ID == agent.ID {
			directoryFound = true
			if !entry.AdminHidden || entry.OwnerUsername != "alice" {
				t.Fatalf("directory entry mismatch: %+v", entry)
			}
		}
	}
	if !directoryFound {
		t.Fatal("directory did not report the hidden node")
	}
	// The owner keeps working with the node; no administrator list may leak
	// its identity or its queued work.
	var hiddenTask core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}, http.StatusCreated, &hiddenTask)
	for _, path := range []string{"/tasks", "/deployments", "/traffic-policies", "/traffic-usage", "/overview"} {
		body := admin.call("GET", path, nil, http.StatusOK, nil)
		if bytes.Contains(body, []byte(agent.ID)) || bytes.Contains(body, []byte(hiddenTask.ID)) {
			t.Fatalf("%s leaked the owner-hidden node or its task", path)
		}
	}
	bob.call("GET", "/agent-directory", nil, http.StatusForbidden, nil)
	alice.call("PUT", "/agents/"+agent.ID+"/visibility", map[string]bool{"admin_hidden": false}, http.StatusOK, nil)
	admin.call("GET", "/agents/"+agent.ID+"/configs", nil, http.StatusOK, nil)
	// Restoring visibility does not hand the toggle to administrators.
	admin.call("PUT", "/agents/"+agent.ID+"/visibility", map[string]bool{"admin_hidden": true}, http.StatusNotFound, nil)
	alice.call("PUT", "/agents/"+agent.ID+"/visibility", map[string]bool{"admin_hidden": true}, http.StatusOK, nil)
	admin.call("DELETE", "/agents/"+agent.ID, nil, http.StatusNoContent, nil)
}
