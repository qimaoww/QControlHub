package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
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
