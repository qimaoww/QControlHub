package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

	firstContent := "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n"
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

	secondContent := "mixed-port: 7891\nmode: global\nrules:\n  - MATCH,DIRECT\n"
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

func TestTaskAPIRejectsEveryCoreActionForUnsupportedExistingService(t *testing.T) {
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
		Name: "unsupported existing service task gate", TTLMinutes: 5, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "unsupported-existing-service", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineSingBox},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollment.Token)
	if err != nil {
		t.Fatalf("enroll agent: %v", err)
	}
	t.Cleanup(func() { cleanupTaskAPIFixture(t, databaseURL, enrollment.ID, []string{agent.ID}) })
	reason := "unsupported executable wrapper"
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{
		core.EngineSingBox: {ServiceStatus: "active", ExistingConfigUnsupportedReason: reason},
	}}); err != nil {
		t.Fatalf("record unsupported discovery: %v", err)
	}
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "existing sing-box snapshot", Engine: core.EngineSingBox, Content: `{"inbounds":[]}`,
	}, 0)
	if err != nil {
		t.Fatalf("save node snapshot: %v", err)
	}

	adminToken := strings.Repeat("u", 48)
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	for _, action := range []core.Action{
		core.ActionValidate, core.ActionDeploy, core.ActionStart, core.ActionStop,
		core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionReadConfig,
		core.ActionImportExisting,
	} {
		t.Run(string(action), func(t *testing.T) {
			input := core.TaskRequest{AgentID: agent.ID, Action: action, Engine: core.EngineSingBox}
			if action == core.ActionValidate || action == core.ActionDeploy || action == core.ActionImportExisting {
				input.ConfigID = config.ID
			}
			if action == core.ActionInstall {
				input.CoreVersion = "stable"
			}
			payload, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(payload))
			request.Header.Set("Authorization", "Bearer "+adminToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "core tasks are disabled") || !strings.Contains(response.Body.String(), reason) {
				t.Fatalf("POST %s status=%d body=%s", action, response.Code, response.Body.String())
			}
		})
	}
}

func enrollTaskAPIAgent(t *testing.T, ctx context.Context, dataStore *store.Store, enrollmentToken, name string) core.Agent {
	t.Helper()
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatalf("generate public key: %v", err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollmentToken)
	if err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	return agent
}

func enrollTaskAPIAgentWithSource(t *testing.T, ctx context.Context, dataStore *store.Store, enrollmentToken, name string) core.Agent {
	t.Helper()
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatalf("generate public key: %v", err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureMihomoDevelopmentSource},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollmentToken)
	if err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	return agent
}

func TestTaskAPICoreSourceWithPostgreSQL(t *testing.T) {
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
	legacyEnrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "task API source legacy", TTLMinutes: 5, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create legacy enrollment token: %v", err)
	}
	legacy := enrollTaskAPIAgent(t, ctx, dataStore, legacyEnrollment.Token, "task-api-legacy")
	t.Cleanup(func() { cleanupTaskAPIFixture(t, databaseURL, legacyEnrollment.ID, []string{legacy.ID}) })
	sourceEnrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "task API source", TTLMinutes: 5, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create source enrollment token: %v", err)
	}
	source := enrollTaskAPIAgentWithSource(t, ctx, dataStore, sourceEnrollment.Token, "task-api-source")
	t.Cleanup(func() { cleanupTaskAPIFixture(t, databaseURL, sourceEnrollment.ID, []string{source.ID}) })

	adminToken := strings.Repeat("t", 48)
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	post := func(payload core.TaskRequest) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode task request: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(encoded))
		request.Header.Set("Authorization", "Bearer "+adminToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if response := post(core.TaskRequest{
		AgentID: legacy.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	}); response.Code != http.StatusConflict {
		t.Fatalf("legacy mirror status=%d body=%s", response.Code, response.Body.String())
	}
	legacyOfficial := post(core.TaskRequest{
		AgentID: legacy.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if legacyOfficial.Code != http.StatusCreated {
		t.Fatalf("legacy official status=%d body=%s", legacyOfficial.Code, legacyOfficial.Body.String())
	}
	mirrorResponse := post(core.TaskRequest{
		AgentID: source.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if mirrorResponse.Code != http.StatusCreated {
		t.Fatalf("mirror install status=%d body=%s", mirrorResponse.Code, mirrorResponse.Body.String())
	}
	var mirrorTask core.Task
	if err := json.Unmarshal(mirrorResponse.Body.Bytes(), &mirrorTask); err != nil || mirrorTask.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("mirror task = %+v, %v", mirrorTask, err)
	}

	for name, payload := range map[string]core.TaskRequest{
		"stable rejects mirror": {AgentID: source.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable, CoreSource: string(core.CoreSourceMirror)},
		"unknown source":        {AgentID: source.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionDevelopment, CoreSource: "private"},
		"status rejects source": {AgentID: legacy.ID, Action: core.ActionStatus, Engine: core.EngineMihomo, CoreSource: string(core.CoreSourceMirror)},
	} {
		if response := post(payload); response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
}

func createTaskAPIFixture(t *testing.T, ctx context.Context, dataStore *store.Store, request core.TaskRequest) core.Task {
	t.Helper()
	task, err := dataStore.CreateTask(ctx, request)
	if err != nil {
		t.Fatalf("create %s task: %v", request.Action, err)
	}
	return task
}

func taskAPIRequest(t *testing.T, handler http.Handler, token, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertTaskAPIList(t *testing.T, handler http.Handler, token, target string, wantIDs []string, predicate func(core.Task) bool) []core.Task {
	t.Helper()
	response := taskAPIRequest(t, handler, token, http.MethodGet, target)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", target, response.Code, response.Body.String())
	}
	var tasks []core.Task
	if err := json.Unmarshal(response.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode GET %s: %v", target, err)
	}
	if wantIDs != nil {
		got := make(map[string]struct{}, len(tasks))
		for _, task := range tasks {
			got[task.ID] = struct{}{}
		}
		if len(got) != len(wantIDs) {
			t.Fatalf("GET %s task IDs=%v, want %v", target, got, wantIDs)
		}
		for _, id := range wantIDs {
			if _, ok := got[id]; !ok {
				t.Fatalf("GET %s task IDs=%v, missing %s", target, got, id)
			}
		}
	}
	if predicate != nil {
		for _, task := range tasks {
			if !predicate(task) {
				t.Fatalf("GET %s returned task outside filter: %+v", target, task)
			}
		}
	}
	return tasks
}

func cleanupTaskAPIFixture(t *testing.T, databaseURL, enrollmentID string, agentIDs []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Errorf("connect for task API cleanup: %v", err)
		return
	}
	defer connection.Close(ctx)
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Errorf("begin task API cleanup: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	for _, agentID := range agentIDs {
		for _, statement := range []string{
			`DELETE FROM tasks WHERE agent_id=$1`,
			`DELETE FROM config_revisions WHERE agent_id=$1`,
			`DELETE FROM configs WHERE agent_id=$1`,
			`DELETE FROM agent_nonces WHERE agent_id=$1`,
			`DELETE FROM agents WHERE id=$1`,
		} {
			if _, err := tx.Exec(ctx, statement, agentID); err != nil {
				t.Errorf("clean task API fixture agent %s: %v", agentID, err)
				return
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM enrollment_tokens WHERE id=$1`, enrollmentID); err != nil {
		t.Errorf("clean task API enrollment token: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit task API cleanup: %v", err)
	}
}
