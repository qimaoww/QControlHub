package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Exercise real authentication and PostgreSQL readers with lifecycle cancellation
// after authorization. The same failure wrapper also serves direct API clients.
func TestPanelReadFailurePolicy(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	server := New(db, configScopeFixtureConfig())
	loginRequest := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"alice","token":"`+configScopeFixturePassword+`"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	server.login(loginResponse, loginRequest)
	if loginResponse.Code != 200 {
		t.Fatal(loginResponse.Body.String())
	}
	session := configScopeAPIClient{t: t, handler: server.Handler(), cookie: loginResponse.Result().Cookies()[0]}
	key, _ := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionAgentsRead, core.PermissionCoreLogsRead, core.PermissionTasksRead})
	for _, client := range []configScopeAPIClient{admin, session, key} {
		countClient := admin
		if client.token != configScopeFixtureAdminToken {
			countClient = alice
		}

		for _, scenario := range []struct {
			name, path string
			read       http.HandlerFunc
		}{
			{"connections", "/client-connections", server.listClientConnections},
			{"logs", "/core-logs", server.listCoreLogs},
			{"region", "/agents/" + agent.ID + "/region", server.getAgentRegion},
		} {
			for _, timeout := range []bool{false, true} {
				var before, after []core.Task
				countClient.call("GET", "/tasks?action=panel-failure&limit=500", nil, 200, &before)
				var stop context.CancelFunc
				requestContext, cancel := context.WithCancel(ctx)
				stop = cancel
				mux := http.NewServeMux()
				pattern := "GET /api/v1" + scenario.path
				if scenario.name == "region" {
					pattern = "GET /api/v1/agents/{id}/region"
				}
				mux.Handle(pattern, server.requirePermission(core.PermissionAgentsRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if timeout {
						expired, done := context.WithDeadline(r.Context(), time.Now().Add(-time.Second))
						defer done()
						r = r.WithContext(expired)
					} else {
						stop()
					}
					scenario.read(w, r)
				})))
				request := httptest.NewRequest("GET", "/api/v1"+scenario.path, nil).WithContext(requestContext)
				if client.cookie != nil {
					request.AddCookie(client.cookie)
				} else {
					request.Header.Set("Authorization", "Bearer "+client.token)
				}
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				cancel()
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				countClient.call("GET", "/tasks?action=panel-failure&limit=500", nil, 200, &after)
				t.Logf("%s timeout=%t HTTP=%d new_tasks=%d body=%s", scenario.name, timeout, response.Code, len(after)-len(before), response.Body.String())
				if len(after) != len(before) {
					t.Errorf("%s read created an operation failure", scenario.name)
				}
				if body["failure_retention"] != "not_required" || body["retention_error"] != nil || body["failure_task_id"] != nil {
					t.Errorf("read retention policy missing: %v", body)
				}
				expected := 499
				if timeout {
					expected = 504
				}
				if response.Code != expected || body["request_id"] == nil {
					t.Errorf("lost read outcome/identifier: %d %v", response.Code, body)
				}
			}
		}
	}
	// Validation and permission rejections remain real errors, without read tasks.
	alice.call("GET", "/core-logs?limit=0", nil, 400, nil)
	admin.call("GET", "/client-connections?scope_only=invalid", nil, 400, nil)
	// Older clients cannot turn a classified GET into a task by submitting it.
	var skipped map[string]any
	alice.call("POST", "/tasks/failures", core.PanelFailure{Operation: "GET /api/v1/core-logs", Resource: "/api/v1/core-logs", Stage: "api_request", Summary: "读取失败", Detail: "deadline exceeded"}, 200, &skipped)
	if skipped["failure_retention"] != "not_required" || skipped["id"] != nil {
		t.Fatalf("read backfill was retained: %v", skipped)
	}
	// The diagnostic endpoint itself cannot recursively produce another task.
	var response map[string]any
	alice.call("POST", "/tasks/failures", core.PanelFailure{}, 400, &response)
	if response["failure_retention"] != "not_required" || strings.Contains(response["error"].(string), "保存") {
		t.Fatalf("recursive retention: %v", response)
	}
}

func TestConcurrentReadErrorsDoNotBackfillTasks(t *testing.T) {
	_, _, admin, _, _ := newConfigScopeAPIFixture(t)
	var before, after []core.Task
	admin.call("GET", "/tasks?action=panel-failure&limit=500", nil, 200, &before)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() { defer group.Done(); admin.call("GET", "/core-logs?limit=0", nil, 400, nil) }()
	}
	group.Wait()
	admin.call("GET", "/tasks?action=panel-failure&limit=500", nil, 200, &after)
	if len(after) != len(before) {
		t.Fatalf("concurrent reads created %d tasks", len(after)-len(before))
	}
}
