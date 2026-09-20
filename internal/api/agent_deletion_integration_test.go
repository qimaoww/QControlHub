package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
			stored, err := db.GetTaskState(ctx, task.ID)
			if err != nil || stored.Status != core.TaskFailed {
				t.Fatalf("pending deployment was not failed: %+v, %v", stored, err)
			}
		})
	}
}
