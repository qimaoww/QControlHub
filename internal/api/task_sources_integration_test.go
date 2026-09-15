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

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

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
		core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionReadConfig, core.ActionReadManagedConfig,
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
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "已禁用该内核的任务") || !strings.Contains(response.Body.String(), reason) {
				t.Fatalf("POST %s status=%d body=%s", action, response.Code, response.Body.String())
			}
		})
	}
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
