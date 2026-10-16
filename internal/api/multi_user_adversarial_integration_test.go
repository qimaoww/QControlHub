package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Pause only after cookie, revision, permission and CSRF middleware passed.
// No sleeps or application hooks are needed to order the two requests.
type pausedAccountBody struct {
	io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (body *pausedAccountBody) Read(p []byte) (int, error) {
	body.once.Do(func() {
		close(body.entered)
		<-body.release
	})
	return body.Reader.Read(p)
}

func TestInFlightAccountMutationCannotKeepOldAuthority(t *testing.T) {
	for _, change := range []string{"demote", "disable", "purge", "revoke-regrant"} {
		for _, operation := range []string{"task", "archive", "node-config", "allocation", "metadata", "client-preferences", "template"} {
			t.Run(change+"/"+operation, func(t *testing.T) {
				db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
				const password = "inflight account password fixture"
				var actor core.User
				admin.call("POST", "/users", core.UserRequest{Username: "inflight-admin", Role: core.RoleAdmin, Password: password}, http.StatusCreated, &actor)
				client, _ := managementLogin(t, alice.handler, actor.Username, password, http.StatusOK)
				agent := enrollConfigScopeAPIAgent(t, ctx, db)
				path, method := "/tasks", "POST"
				var input any = core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}
				switch operation {
				case "archive":
					path = "/configs"
					input = core.Config{Name: "inflight", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"}
				case "node-config":
					path, method = "/agents/"+agent.ID+"/configs/mihomo", "PUT"
					input = map[string]any{"name": "inflight", "content": "mixed-port: 21001\n", "version": 0}
				case "metadata":
					path, method = "/agents/"+agent.ID+"/name", "PUT"
					input = map[string]any{"name": "unauthorized-name"}
				case "client-preferences":
					path, method = "/agents/"+agent.ID+"/client-address", "PUT"
					input = map[string]any{"address": "unauthorized.example.test"}
				case "template":
					path = "/templates"
					input = map[string]any{"name": "inflight", "engine": "mihomo", "content": "mixed-port: 21001\n"}
				case "allocation":
					path, method = "/users/"+alice.userID+"/agent-access", "PUT"
					var access core.AgentAccess
					admin.call("GET", path, nil, http.StatusOK, &access)
					input = core.AgentAccessRequest{Isolated: true, Revision: access.Revision, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}}}
				}
				encoded, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				body := &pausedAccountBody{Reader: bytes.NewReader(encoded), entered: make(chan struct{}), release: make(chan struct{})}
				var resume sync.Once
				defer resume.Do(func() { close(body.release) })
				request := httptest.NewRequest(method, "/api/v1"+path, body).WithContext(ctx)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set(csrfHeader, client.csrf)
				request.AddCookie(client.cookie)
				response := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { client.handler.ServeHTTP(response, request); close(done) }()
				select {
				case <-body.entered:
				case <-ctx.Done():
					t.Fatal("request never passed authentication")
				}
				switch change {
				case "demote":
					admin.call("PUT", "/users/"+actor.ID, map[string]any{"role": "user"}, http.StatusOK, nil)
				case "disable", "revoke-regrant":
					admin.call("DELETE", "/users/"+actor.ID, nil, http.StatusNoContent, nil)
					if change == "revoke-regrant" {
						admin.call("PUT", "/users/"+actor.ID, map[string]any{"disabled": false}, http.StatusOK, nil)
					}
				case "purge":
					admin.call("POST", "/users/"+actor.ID+"/purge", nil, http.StatusNoContent, nil)
				}
				resume.Do(func() { close(body.release) })
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("in-flight request did not finish")
				}
				if response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
					t.Fatalf("old authority survived %s: %d %s", change, response.Code, response.Body.String())
				}
				if current, err := db.GetAgent(ctx, agent.ID); err != nil || current.Name != agent.Name || current.Labels["client_address"] != "edge.example.test" {
					t.Fatalf("rejected request changed the node: %+v %v", current, err)
				}
				// Inspect from a fresh trusted scope, so the response alone cannot
				// hide a write or queued work left behind by the rejected request.
				configs, err := db.ListConfigs(ctx)
				if err != nil || len(configs) != 0 {
					t.Fatalf("rejected request saved an archive: %+v %v", configs, err)
				}
				templates, err := db.ListConfigTemplates(ctx)
				if err != nil || len(templates) != 0 {
					t.Fatalf("rejected request saved a template: %+v %v", templates, err)
				}
				workspaces, err := db.AgentConfigs(store.WithConfigScope(ctx, actor.ID, true), agent.ID)
				if err != nil || len(workspaces) != 0 {
					t.Fatalf("rejected request saved a workspace: %+v %v", workspaces, err)
				}
				tasks, err := db.ListTasks(ctx, agent.ID, 100)
				if err != nil || len(tasks) != 0 {
					t.Fatalf("rejected request queued work: %+v %v", tasks, err)
				}
				access, err := db.UserAgentAccess(ctx, alice.userID)
				if err != nil || len(access.Shares) != 0 {
					t.Fatalf("rejected request changed allocations: %+v %v", access, err)
				}
				client.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
				if change == "revoke-regrant" {
					fresh, _ := managementLogin(t, alice.handler, actor.Username, password, http.StatusOK)
					fresh.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}, http.StatusCreated, nil)
				}
			})
		}
	}
}

func TestOrdinaryUserInFlightSaveLosesRevokedPermission(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	token, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, alice.userID, false), core.EnrollmentTokenRequest{Name: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	agent := enrollTaskAPIAgent(t, ctx, db, token.Token, "owned")
	body := &pausedAccountBody{Reader: bytes.NewBufferString(`{"name":"owned","content":"mixed-port: 21001\n","version":0}`), entered: make(chan struct{}), release: make(chan struct{})}
	var resume sync.Once
	defer resume.Do(func() { close(body.release) })
	request := httptest.NewRequest("PUT", "/api/v1/agents/"+agent.ID+"/configs/mihomo", body).WithContext(ctx)
	request.AddCookie(alice.cookie)
	request.Header.Set(csrfHeader, alice.csrf)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { alice.handler.ServeHTTP(response, request); close(done) }()
	select {
	case <-body.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	admin.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": []core.Permission{core.PermissionAgentsRead}}, http.StatusOK, nil)
	resume.Do(func() { close(body.release) })
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf("revoked permission saved configuration: %d %s", response.Code, response.Body.String())
	}
	if configs, err := db.AgentConfigs(store.WithConfigScope(ctx, alice.userID, false), agent.ID); err != nil || len(configs) != 0 {
		t.Fatalf("rejected save changed the workspace: %+v %v", configs, err)
	}
}
