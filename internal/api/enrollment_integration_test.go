package api

import (
	"bytes"
	"context"
	"crypto/rand"
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

	enroll := func(publicKey []byte) (int, string) {
		payload, _ := json.Marshal(core.EnrollRequest{
			Name: "api-reinstall-node", OS: "linux", Arch: "amd64",
			Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: authn.EncodePublicKey(publicKey),
		})
		request := httptest.NewRequest(http.MethodPost, "/agent/v1/enroll", bytes.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+created.Token)
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
	if status, id := enroll(firstKey); status != http.StatusCreated || id == "" {
		t.Fatalf("first enrollment status=%d id=%q", status, id)
	} else {
		agentID = id
	}
	secondStatus, secondID := enroll(randomEnrollmentKey(t))
	if secondStatus != http.StatusOK || secondID != agentID {
		t.Fatalf("repeat enrollment status=%d id=%q, want 200 and %q", secondStatus, secondID, agentID)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/enrollment-tokens/"+created.ID, nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete add-node command status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	installerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/agent-installer", nil)
	installerRequest.Header.Set("X-QControlHub-Enrollment", created.Token)
	installerResponse := httptest.NewRecorder()
	handler.ServeHTTP(installerResponse, installerRequest)
	if installerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("deleted add-node command installer status=%d, want 401", installerResponse.Code)
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
