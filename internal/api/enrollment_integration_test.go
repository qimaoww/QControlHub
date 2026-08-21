package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestAddNodeAPICreatesReusableCredentialAndSupportsReinstall(t *testing.T) {
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

	adminToken := strings.Repeat("n", 48)
	server := New(dataStore, Config{AdminToken: adminToken, AgentInstaller: []byte("#!/bin/sh\n")})
	handler := server.Handler()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", bytes.NewBufferString(`{"name":"api-reinstall-node"}`))
	createRequest.Header.Set("Authorization", "Bearer "+adminToken)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create add-node command status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created core.EnrollmentTokenCreated
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created add-node command: %v", err)
	}
	if !created.Reusable || created.ExpiresAt != nil || created.MaxUses != 0 {
		t.Fatalf("created add-node command metadata = %+v", created.EnrollmentToken)
	}

	var agentID string
	t.Cleanup(func() {
		if agentID != "" {
			_ = dataStore.DeleteAgent(context.Background(), agentID)
		}
		_ = dataStore.DeleteEnrollmentToken(context.Background(), created.ID)
	})

	enroll := func(token string, publicKey []byte) (int, string) {
		payload, _ := json.Marshal(core.EnrollRequest{
			Name: "api-reinstall-node", OS: "linux", Arch: "amd64",
			Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: authn.EncodePublicKey(publicKey),
		})
		request := httptest.NewRequest(http.MethodPost, "/agent/v1/enroll", bytes.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var result core.EnrollResponse
		if response.Code == http.StatusCreated || response.Code == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				t.Fatalf("decode enrollment response: %v", err)
			}
		}
		return response.Code, result.AgentID
	}

	firstKey := randomEnrollmentKey(t)
	if status, id := enroll(created.Token, firstKey); status != http.StatusCreated || id == "" {
		t.Fatalf("first enrollment status=%d id=%q", status, id)
	} else {
		agentID = id
	}
	if err := dataStore.Heartbeat(ctx, agentID, core.HeartbeatRequest{
		Metrics: &core.HostMetrics{CPUAvailable: true, CPUPercent: 12.5},
	}); err != nil {
		t.Fatalf("store heartbeat metrics: %v", err)
	}
	agentsOnlyToken := strings.Repeat("r", 48)
	server.roleTokens[sha256.Sum256([]byte(agentsOnlyToken))] = tokenPrincipal{
		Role: core.RoleUser, Permissions: []core.Permission{core.PermissionAgentsRead},
	}
	agentsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	agentsRequest.Header.Set("Authorization", "Bearer "+agentsOnlyToken)
	agentsResponse := httptest.NewRecorder()
	handler.ServeHTTP(agentsResponse, agentsRequest)
	if agentsResponse.Code != http.StatusOK {
		t.Fatalf("agents-only list status=%d body=%s", agentsResponse.Code, agentsResponse.Body.String())
	}
	var visibleAgents []core.Agent
	if err := json.NewDecoder(agentsResponse.Body).Decode(&visibleAgents); err != nil {
		t.Fatalf("decode agents-only list: %v", err)
	}
	var visibleAgent *core.Agent
	for index := range visibleAgents {
		if visibleAgents[index].ID == agentID {
			visibleAgent = &visibleAgents[index]
			break
		}
	}
	if visibleAgent == nil {
		t.Fatalf("agents-only response omitted enrolled agent %s", agentID)
	}
	if visibleAgent.Metrics.CPUAvailable || visibleAgent.Metrics.CPUPercent != 0 {
		t.Fatalf("agents-only response exposed metrics: %+v", visibleAgent.Metrics)
	}
	adminAgentsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	adminAgentsRequest.Header.Set("Authorization", "Bearer "+adminToken)
	adminAgentsResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminAgentsResponse, adminAgentsRequest)
	if adminAgentsResponse.Code != http.StatusOK {
		t.Fatalf("admin list agents status=%d body=%s", adminAgentsResponse.Code, adminAgentsResponse.Body.String())
	}
	var adminAgents []core.Agent
	if err := json.NewDecoder(adminAgentsResponse.Body).Decode(&adminAgents); err != nil {
		t.Fatalf("decode admin agents list: %v", err)
	}
	metricsVisible := false
	for _, item := range adminAgents {
		if item.ID == agentID && item.Metrics.CPUAvailable && item.Metrics.CPUPercent == 12.5 {
			metricsVisible = true
			break
		}
	}
	if !metricsVisible {
		t.Fatal("admin agents response omitted authorized metrics")
	}
	additionalRequest := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/enrollment-token", nil)
	additionalRequest.Header.Set("Authorization", "Bearer "+adminToken)
	additionalResponse := httptest.NewRecorder()
	handler.ServeHTTP(additionalResponse, additionalRequest)
	if additionalResponse.Code != http.StatusCreated {
		t.Fatalf("create additional Agent install command status=%d body=%s", additionalResponse.Code, additionalResponse.Body.String())
	}
	var additional core.EnrollmentTokenCreated
	if err := json.NewDecoder(additionalResponse.Body).Decode(&additional); err != nil {
		t.Fatalf("decode additional Agent install command: %v", err)
	}
	if additional.ID == created.ID || additional.AgentID != agentID || additional.Name != created.Name || additional.Token == "" {
		t.Fatalf("additional Agent install command = %+v", additional)
	}
	if !dataStore.EnrollmentTokenUsable(ctx, created.Token) || !dataStore.EnrollmentTokenUsable(ctx, additional.Token) {
		t.Fatal("creating another Agent install command invalidated a credential")
	}
	secondStatus, secondID := enroll(additional.Token, randomEnrollmentKey(t))
	if secondStatus != http.StatusOK || secondID != agentID {
		t.Fatalf("repeat enrollment status=%d id=%q, want 200 and %q", secondStatus, secondID, agentID)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/enrollment-tokens/"+additional.ID, nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete add-node command status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	installerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/agent-installer", nil)
	installerRequest.Header.Set("X-QControlHub-Enrollment", additional.Token)
	installerResponse := httptest.NewRecorder()
	handler.ServeHTTP(installerResponse, installerRequest)
	if installerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("deleted add-node command installer status=%d, want 401", installerResponse.Code)
	}
	previousInstallerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/agent-installer", nil)
	previousInstallerRequest.Header.Set("X-QControlHub-Enrollment", created.Token)
	previousInstallerResponse := httptest.NewRecorder()
	handler.ServeHTTP(previousInstallerResponse, previousInstallerRequest)
	if previousInstallerResponse.Code != http.StatusOK {
		t.Fatalf("previous add-node command installer status=%d, want 200", previousInstallerResponse.Code)
	}
	if _, err := dataStore.AgentPublicKey(ctx, agentID); err != nil {
		t.Fatalf("deleting one add-node command removed the Agent: %v", err)
	}
}

func randomEnrollmentKey(t *testing.T) []byte {
	t.Helper()
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}
