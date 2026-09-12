package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentInvitationAPIConsentAndIdentityBoundaries(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "invite-only-node")
	var sharing core.AgentSharing
	alice.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	alice.call("PUT", "/agents/"+agent.ID+"/sharing", core.AgentSharingRequest{Revision: sharing.Revision,
		Shares: []core.AgentSharingRecipient{{Username: "bob", Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 1000, Ports: []int{21001}}}}, http.StatusOK, &sharing)
	share := sharing.Shares[0]
	path := "/agent-access/" + share.ID + "/response"
	input := core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}
	alice.call("POST", path, input, http.StatusNotFound, nil)
	admin.call("POST", path, input, http.StatusNotFound, nil)
	noCSRF := bob
	noCSRF.csrf = ""
	noCSRF.call("POST", path, input, http.StatusForbidden, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1"+path, bytes.NewBufferString(`{"revision":1,"decision":"accept"}`))
	response := httptest.NewRecorder()
	bob.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous invitation response: %d %s", response.Code, response.Body.String())
	}
	bob.call("POST", path, map[string]any{"decision": "accept"}, http.StatusBadRequest, nil)
	bob.call("POST", path, map[string]any{"revision": input.Revision, "decision": "accepted"}, http.StatusBadRequest, nil)
	var access core.AgentAccess
	bob.call("GET", "/agent-access", nil, http.StatusOK, &access)
	if len(access.Shares) != 1 || access.Shares[0].Status != core.AgentSharePending || access.Shares[0].OwnerUsername != "alice" {
		t.Fatalf("recipient cannot identify pending invitation: %+v", access)
	}
	assertDenied := func() {
		t.Helper()
		var agents []core.Agent
		bob.call("GET", "/agents", nil, http.StatusOK, &agents)
		if len(agents) != 0 {
			t.Fatalf("unaccepted invitation exposed an Agent: %+v", agents)
		}
		for _, path := range []string{"/agents/" + agent.ID + "/configs", "/agents/" + agent.ID + "/configs/mihomo/workspace", "/metrics/" + agent.ID} {
			bob.call("GET", path, nil, http.StatusNotFound, nil)
		}
		bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusNotFound, nil)
		bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}, http.StatusNotFound, nil)
	}
	assertDenied()
	input.Decision = "reject"
	bob.call("POST", path, input, http.StatusOK, &access)
	if access.Shares[0].Status != core.AgentShareRejected {
		t.Fatal("reject did not persist")
	}
	assertDenied()
	input.Decision = "accept"
	bob.call("POST", path, input, http.StatusConflict, nil)
	alice.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	if sharing.Shares[0].Status != core.AgentShareRejected {
		t.Fatal("owner cannot see rejection")
	}
	alice.call("PUT", "/agents/"+agent.ID+"/sharing", core.AgentSharingRequest{Revision: sharing.Revision,
		Shares: []core.AgentSharingRecipient{{Username: "bob", Engines: []core.Engine{core.EngineMihomo}, Reinvite: true, LimitBytes: 1000, Ports: []int{21001}}}}, http.StatusOK, &sharing)
	input.Revision = sharing.Shares[0].InvitationRevision
	bob.call("POST", path, input, http.StatusOK, &access)
	if access.Shares[0].Status != core.AgentShareAccepted {
		t.Fatal("accept did not persist")
	}
	var agents []core.Agent
	bob.call("GET", "/agents", nil, http.StatusOK, &agents)
	if len(agents) != 1 || agents[0].ID != agent.ID || agents[0].CanManage {
		t.Fatalf("acceptance gave wrong access or host control: %+v", agents)
	}
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusOK, nil)
	bob.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusForbidden, nil)
	// Accepted recipients can leave without owner or administrator approval.
	input.Revision, input.Decision = access.Shares[0].InvitationRevision, "reject"
	bob.call("POST", path, input, http.StatusOK, &access)
	assertDenied()
	bob.call("POST", path, input, http.StatusConflict, nil)
	// Personal settings and integrations remain independent and available.
	bob.call("PUT", "/substore-sync/settings", map[string]string{"endpoint_url": "http://127.0.0.1/bob-private"}, http.StatusOK, nil)
	body := alice.call("GET", "/substore-sync", nil, http.StatusOK, nil)
	if bytes.Contains(body, []byte("bob-private")) {
		t.Fatal("sharing response leaked personal integration")
	}
}

func TestAgentInvitationAPINeedsNoHostPermissionsAndCannotRestoreDisabledUser(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "permissionless-invite")
	const password = "invitation-only-test-password"
	var user core.User
	admin.call("POST", "/users", core.UserRequest{Username: "invite-only", Password: password, Role: core.RoleUser,
		Permissions: []core.Permission{}}, http.StatusCreated, &user)
	payload, _ := json.Marshal(map[string]string{"username": user.Username, "token": password})
	request := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	alice.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	client := configScopeAPIClient{t: t, handler: alice.handler, userID: user.ID, cookie: response.Result().Cookies()[0], csrf: session.CSRF}
	var access core.AgentAccess
	admin.call("GET", "/users/"+user.ID+"/agent-access", nil, http.StatusOK, &access)
	admin.call("PUT", "/users/"+user.ID+"/agent-access", core.AgentAccessRequest{Revision: access.Revision, Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}}}, http.StatusOK, &access)
	share := access.Shares[0]
	client.call("GET", "/agent-access", nil, http.StatusOK, nil)
	client.call("POST", "/agent-access/"+share.ID+"/response",
		core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}, http.StatusOK, &access)
	client.call("GET", "/agents", nil, http.StatusForbidden, nil)
	admin.call("DELETE", "/users/"+user.ID, nil, http.StatusNoContent, nil)
	client.call("POST", "/agent-access/"+share.ID+"/response",
		core.AgentShareResponseRequest{Revision: access.Shares[0].InvitationRevision, Decision: "reject"}, http.StatusUnauthorized, nil)
}
