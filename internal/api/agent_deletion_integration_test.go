package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeleteAgentDeadlineReturnsGatewayTimeout(t *testing.T) {
	db, _, _, _, _ := newConfigScopeAPIFixture(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/expired", nil).WithContext(ctx)
	request.SetPathValue("id", "expired")
	response := httptest.NewRecorder()
	New(db, Config{}).deleteAgent(response, request)
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), "删除节点超时") {
		t.Fatalf("expired deletion: %d %s", response.Code, response.Body.String())
	}
}

func TestDeleteOfflineAgentWithPendingDeployment(t *testing.T) {
	for _, actor := range []string{"owner", "admin"} {
		t.Run(actor, func(t *testing.T) {
			db, ctx, admin, owner, _ := newConfigScopeAPIFixture(t)
			var token core.EnrollmentTokenCreated
			owner.call("POST", "/enrollment-tokens", map[string]string{"name": "offline-node"}, http.StatusCreated, &token)
			agent := enrollTaskAPIAgent(t, ctx, db, token.Token, "offline-node")
			var agents []core.Agent
			owner.call("GET", "/agents", nil, http.StatusOK, &agents)
			if len(agents) != 1 || agents[0].Status != "offline" {
				t.Fatalf("expected one offline agent, got %+v", agents)
			}
			var config core.Config
			owner.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001), http.StatusOK, &config)
			var task core.Task
			owner.call("POST", "/tasks", core.TaskRequest{
				AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: config.ID,
			}, http.StatusCreated, &task)
			client := owner
			if actor == "admin" {
				client = admin
			}
			client.call("DELETE", "/agents/"+agent.ID, nil, http.StatusNoContent, nil)
			owner.call("GET", "/agents", nil, http.StatusOK, &agents)
			if len(agents) != 0 {
				t.Fatalf("deleted offline agent remains visible: %+v", agents)
			}
			if db.EnrollmentTokenUsable(ctx, token.Token) {
				t.Fatal("deleted offline node can still re-enroll with its old credential")
			}
			// Physical cleanup runs separately; the API has already returned success.
			if err := db.CleanupDeletedAgents(ctx); err != nil {
				t.Fatal(err)
			}
			stored, err := db.GetTaskState(ctx, task.ID)
			if err != nil || stored.Status != core.TaskFailed {
				t.Fatalf("pending deployment was not failed: %+v, %v", stored, err)
			}
		})
	}
}
