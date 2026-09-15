package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestTaskAPIWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "task API integration", TTLMinutes: 5, MaxUses: 2,
	})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	var agentIDs []string
	t.Cleanup(func() {
		cleanupTaskAPIFixture(t, databaseURL, enrollment.ID, agentIDs)
	})
	primaryAgent := enrollTaskAPIAgent(t, ctx, dataStore, enrollment.Token, "task-api-primary")
	agentIDs = append(agentIDs, primaryAgent.ID)
	secondaryAgent := enrollTaskAPIAgent(t, ctx, dataStore, enrollment.Token, "task-api-secondary")
	agentIDs = append(agentIDs, secondaryAgent.ID)

	firstContent := "listeners: [{name: first, type: http, port: 7890}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n"
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: primaryAgent.ID, Name: "task API retry v1", Engine: core.EngineMihomo, Content: firstContent,
	}, 0)
	if err != nil {
		t.Fatalf("save initial task API config: %v", err)
	}
	canceledTask := createTaskAPIFixture(t, ctx, dataStore, core.TaskRequest{
		AgentID: primaryAgent.ID, Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigID: config.ID,
	})
	stopTask := createTaskAPIFixture(t, ctx, dataStore, core.TaskRequest{
		AgentID: primaryAgent.ID, Action: core.ActionStop, Engine: core.EngineMihomo,
	})
	statusTask := createTaskAPIFixture(t, ctx, dataStore, core.TaskRequest{
		AgentID: primaryAgent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo,
	})
	readConfigTask := createTaskAPIFixture(t, ctx, dataStore, core.TaskRequest{
		AgentID: primaryAgent.ID, Action: core.ActionReadConfig, Engine: core.EngineMihomo,
	})
	secondaryTask := createTaskAPIFixture(t, ctx, dataStore, core.TaskRequest{
		AgentID: secondaryAgent.ID, Action: core.ActionStop, Engine: core.EngineMihomo,
	})

	adminToken := strings.Repeat("t", 48)
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	reusePayload, err := json.Marshal(core.TaskRequest{
		AgentID: primaryAgent.ID, Action: core.ActionReadConfig, Engine: core.EngineMihomo,
	})
	if err != nil {
		t.Fatalf("encode reused task request: %v", err)
	}
	reuseRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(reusePayload))
	reuseRequest.Header.Set("Authorization", "Bearer "+adminToken)
	reuseRequest.Header.Set("Content-Type", "application/json")
	reuseResponse := httptest.NewRecorder()
	handler.ServeHTTP(reuseResponse, reuseRequest)
	if reuseResponse.Code != http.StatusOK {
		t.Fatalf("reuse active task status=%d body=%s", reuseResponse.Code, reuseResponse.Body.String())
	}
	var reusedTask core.Task
	if err := json.Unmarshal(reuseResponse.Body.Bytes(), &reusedTask); err != nil || !reusedTask.Reused || reusedTask.ID != readConfigTask.ID {
		t.Fatalf("reused API task = %+v, %v", reusedTask, err)
	}

	cancelResponse := taskAPIRequest(t, handler, adminToken, http.MethodDelete, "/api/v1/tasks/"+canceledTask.ID)
	if cancelResponse.Code != http.StatusNoContent || cancelResponse.Body.Len() != 0 {
		t.Fatalf("cancel pending task status=%d body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}
	canceled, err := dataStore.GetTask(ctx, canceledTask.ID)
	if err != nil || canceled.Status != core.TaskCanceled {
		t.Fatalf("canceled task = %+v, %v", canceled, err)
	}

	assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+primaryAgent.ID,
		[]string{canceledTask.ID, stopTask.ID, statusTask.ID, readConfigTask.ID}, nil)
	assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+secondaryAgent.ID,
		[]string{secondaryTask.ID}, nil)
	assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+primaryAgent.ID+"&status=canceled",
		[]string{canceledTask.ID}, func(task core.Task) bool { return task.Status == core.TaskCanceled })
	assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+primaryAgent.ID+"&status=pending",
		[]string{stopTask.ID, statusTask.ID, readConfigTask.ID}, func(task core.Task) bool { return task.Status == core.TaskPending })
	assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+primaryAgent.ID+"&action=stop",
		[]string{stopTask.ID}, func(task core.Task) bool { return task.Action == core.ActionStop })
	assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+primaryAgent.ID+"&action=read-config",
		[]string{readConfigTask.ID}, func(task core.Task) bool { return task.Action == core.ActionReadConfig })
	limited := assertTaskAPIList(t, handler, adminToken,
		"/api/v1/tasks?agent_id="+primaryAgent.ID+"&status=pending&limit=1",
		nil, func(task core.Task) bool { return task.Status == core.TaskPending })
	if len(limited) != 1 {
		t.Fatalf("limited task count = %d, want 1", len(limited))
	}

	for _, target := range []string{
		"/api/v1/tasks?status=queued",
		"/api/v1/tasks?action=upgrade",
	} {
		response := taskAPIRequest(t, handler, adminToken, http.MethodGet, target)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status=%d body=%s", target, response.Code, response.Body.String())
		}
		var failure map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure["error"] == "" {
			t.Fatalf("GET %s invalid error response %q: %v", target, response.Body.String(), err)
		}
	}

	secondContent := "listeners: [{name: second, type: http, port: 7891}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n"
	updated, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: primaryAgent.ID, Name: "task API retry v2", Engine: core.EngineMihomo, Content: secondContent,
	}, config.Version)
	if err != nil || updated.Version != config.Version+1 {
		t.Fatalf("save current task API config = %+v, %v", updated, err)
	}
	for _, pendingID := range []string{stopTask.ID, statusTask.ID, readConfigTask.ID} {
		if err := dataStore.CancelTask(ctx, pendingID); err != nil {
			t.Fatalf("cancel task %s before retry claim: %v", pendingID, err)
		}
	}

	retryResponse := taskAPIRequest(t, handler, adminToken, http.MethodPost, "/api/v1/tasks/"+canceledTask.ID+"/retry")
	if retryResponse.Code != http.StatusCreated {
		t.Fatalf("retry canceled task status=%d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	if strings.Contains(retryResponse.Body.String(), "config_content") || strings.Contains(retryResponse.Body.String(), secondContent) {
		t.Fatalf("retry response exposed snapshotted configuration: %s", retryResponse.Body.String())
	}
	var retried core.Task
	if err := json.Unmarshal(retryResponse.Body.Bytes(), &retried); err != nil {
		t.Fatalf("decode retried task: %v", err)
	}
	if retried.ID == "" || retried.ID == canceledTask.ID || retried.AgentID != primaryAgent.ID ||
		retried.Action != canceledTask.Action || retried.Engine != canceledTask.Engine || retried.Status != core.TaskPending || retried.ConfigContent != "" {
		t.Fatalf("retried task = %+v", retried)
	}
	claimed, err := dataStore.ClaimTask(ctx, primaryAgent.ID)
	if err != nil || claimed == nil || claimed.ID != retried.ID || claimed.ConfigContent != secondContent || claimed.ConfigVersion != updated.Version {
		t.Fatalf("claimed retried task = %+v, %v", claimed, err)
	}
	original, err := dataStore.GetTask(ctx, canceledTask.ID)
	if err != nil || original.Status != core.TaskCanceled {
		t.Fatalf("original task after retry = %+v, %v", original, err)
	}
}
