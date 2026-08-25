package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
	dataStore, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("e", 32))
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
		Metrics: &core.HostMetrics{CPUAvailable: true, CPUPercent: 12.5, ObservedPublicIP: "93.184.216.34"},
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
	if strings.Contains(agentsResponse.Body.String(), created.Token) {
		t.Fatal("ordinary Agent list leaked an enrollment credential")
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
	if visibleAgent.EnrollmentCommandAvailable {
		t.Fatal("agents-only response exposed enrollment command availability")
	}
	if visibleAgent.Metrics.CPUAvailable || visibleAgent.Metrics.CPUPercent != 0 || visibleAgent.Metrics.ObservedPublicIP != "" {
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
	commandAvailable := false
	for _, item := range adminAgents {
		if item.ID == agentID && item.Metrics.CPUAvailable && item.Metrics.CPUPercent == 12.5 && item.Metrics.ObservedPublicIP == "93.184.216.34" {
			metricsVisible = true
		}
		if item.ID == agentID && item.EnrollmentCommandAvailable {
			commandAvailable = true
		}
	}
	if !metricsVisible {
		t.Fatal("admin agents response omitted authorized metrics")
	}
	if !commandAvailable {
		t.Fatal("admin agents response omitted recoverable command availability")
	}
	readCommand := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/enrollment-command", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	firstRead := readCommand(adminToken)
	secondRead := readCommand(adminToken)
	if firstRead.Code != http.StatusOK || secondRead.Code != http.StatusOK {
		t.Fatalf("repeat command reads status=%d/%d body=%s/%s", firstRead.Code, secondRead.Code, firstRead.Body.String(), secondRead.Body.String())
	}
	var firstCommand, secondCommand core.EnrollmentTokenCreated
	if err := json.NewDecoder(firstRead.Body).Decode(&firstCommand); err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(secondRead.Body).Decode(&secondCommand); err != nil {
		t.Fatal(err)
	}
	if firstCommand.ID != created.ID || secondCommand.ID != created.ID || firstCommand.Token != created.Token || secondCommand.Token != created.Token {
		t.Fatalf("repeat reads changed command: first=%+v second=%+v", firstCommand, secondCommand)
	}
	if firstRead.Header().Get("Cache-Control") != "no-store" || secondRead.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential read response is cacheable")
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"`+adminToken+`"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)
	var browserSession struct {
		CSRFToken string `json:"csrf_token"`
	}
	if loginResponse.Code != http.StatusOK || json.NewDecoder(loginResponse.Body).Decode(&browserSession) != nil || browserSession.CSRFToken == "" {
		t.Fatalf("browser login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("browser login cookies=%d, want 1", len(cookies))
	}
	csrfRead := func(csrf string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/enrollment-command", nil)
		request.AddCookie(cookies[0])
		if csrf != "" {
			request.Header.Set(csrfHeader, csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := csrfRead(""); response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), created.Token) {
		t.Fatalf("command read without CSRF status=%d body=%s", response.Code, response.Body.String())
	}
	if response := csrfRead(browserSession.CSRFToken); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), created.Token) {
		t.Fatalf("command read with CSRF status=%d body=%s", response.Code, response.Body.String())
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/enrollment-tokens", nil)
	listRequest.Header.Set("Authorization", "Bearer "+adminToken)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), created.Token) {
		t.Fatalf("ordinary enrollment list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	auditRequest := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	auditRequest.Header.Set("Authorization", "Bearer "+adminToken)
	auditResponse := httptest.NewRecorder()
	handler.ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK || strings.Contains(auditResponse.Body.String(), created.Token) {
		t.Fatalf("audit log leaked enrollment credential: status=%d body=%s", auditResponse.Code, auditResponse.Body.String())
	}
	deniedRead := readCommand(agentsOnlyToken)
	if deniedRead.Code != http.StatusForbidden || strings.Contains(deniedRead.Body.String(), created.Token) {
		t.Fatalf("unauthorized command read status=%d body=%s", deniedRead.Code, deniedRead.Body.String())
	}
	crossNodeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agt_0000000000000000/enrollment-command", nil)
	crossNodeRequest.Header.Set("Authorization", "Bearer "+adminToken)
	crossNodeResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossNodeResponse, crossNodeRequest)
	if crossNodeResponse.Code != http.StatusNotFound || strings.Contains(crossNodeResponse.Body.String(), created.Token) {
		t.Fatalf("cross-node command read status=%d body=%s", crossNodeResponse.Code, crossNodeResponse.Body.String())
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

func TestEnrollmentSecretDisclosureRequiresSynchronousAudit(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	adminToken := strings.Repeat("a", 48)
	server := New(dataStore, Config{AdminToken: adminToken, AgentInstaller: []byte("#!/bin/sh\n")})
	server.auditWriter = func(context.Context, core.AuditLogEntry) error {
		return errors.New("forced audit failure")
	}
	handler := server.Handler()
	create := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"name":"audit-gated-create"}`))
	create.Header.Set("Authorization", "Bearer "+adminToken)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusServiceUnavailable || strings.Contains(createResponse.Body.String(), "token") {
		t.Fatalf("audit-failed create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	createdRows, err := dataStore.ListEnrollmentTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range createdRows {
		if row.Name == "audit-gated-create" {
			t.Fatal("audit-failed create left a disclosed enrollment record behind")
		}
	}

	protected, err := dataStore.CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "audit-gated-read", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.DeleteEnrollmentToken(context.Background(), protected.ID)
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: protected.Name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)),
	}, protected.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.DeleteAgent(context.Background(), agent.ID)
	agentCreate := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/enrollment-token", nil)
	agentCreate.Header.Set("Authorization", "Bearer "+adminToken)
	agentCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(agentCreateResponse, agentCreate)
	if agentCreateResponse.Code != http.StatusServiceUnavailable || strings.Contains(agentCreateResponse.Body.String(), "token") {
		t.Fatalf("audit-failed Agent create status=%d body=%s", agentCreateResponse.Code, agentCreateResponse.Body.String())
	}
	agentRead := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/enrollment-command", nil)
	agentRead.Header.Set("Authorization", "Bearer "+adminToken)
	agentReadResponse := httptest.NewRecorder()
	handler.ServeHTTP(agentReadResponse, agentRead)
	if agentReadResponse.Code != http.StatusServiceUnavailable || strings.Contains(agentReadResponse.Body.String(), protected.Token) {
		t.Fatalf("audit-failed Agent read status=%d body=%s", agentReadResponse.Code, agentReadResponse.Body.String())
	}
	read := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens/"+protected.ID+"/command", nil)
	read.Header.Set("Authorization", "Bearer "+adminToken)
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusServiceUnavailable || strings.Contains(readResponse.Body.String(), protected.Token) {
		t.Fatalf("audit-failed read status=%d body=%s", readResponse.Code, readResponse.Body.String())
	}

	db, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	_, err = db.Exec(ctx, `
		CREATE OR REPLACE FUNCTION qch_test_reject_audit() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced audit insert failure'; END; $$;
		DROP TRIGGER IF EXISTS qch_test_reject_audit_trigger ON audit_logs;
		CREATE TRIGGER qch_test_reject_audit_trigger BEFORE INSERT ON audit_logs
		FOR EACH ROW EXECUTE FUNCTION qch_test_reject_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(context.Background(), `DROP TRIGGER IF EXISTS qch_test_reject_audit_trigger ON audit_logs; DROP FUNCTION IF EXISTS qch_test_reject_audit()`)
	}()
	server.auditWriter = nil
	databaseFailureCreate := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"name":"audit-db-failure-create"}`))
	databaseFailureCreate.Header.Set("Authorization", "Bearer "+adminToken)
	databaseFailureCreate.Header.Set("Content-Type", "application/json")
	databaseFailureResponse := httptest.NewRecorder()
	handler.ServeHTTP(databaseFailureResponse, databaseFailureCreate)
	if databaseFailureResponse.Code != http.StatusServiceUnavailable || strings.Contains(databaseFailureResponse.Body.String(), "token") {
		t.Fatalf("database audit-failed create status=%d body=%s", databaseFailureResponse.Code, databaseFailureResponse.Body.String())
	}
	var remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM enrollment_tokens WHERE name=$1`, "audit-db-failure-create").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("database audit-failed create left %d enrollment rows", remaining)
	}
	databaseFailureAgentCreate := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/enrollment-token", nil)
	databaseFailureAgentCreate.Header.Set("Authorization", "Bearer "+adminToken)
	databaseFailureAgentResponse := httptest.NewRecorder()
	handler.ServeHTTP(databaseFailureAgentResponse, databaseFailureAgentCreate)
	if databaseFailureAgentResponse.Code != http.StatusServiceUnavailable || strings.Contains(databaseFailureAgentResponse.Body.String(), "token") {
		t.Fatalf("database audit-failed Agent create status=%d body=%s", databaseFailureAgentResponse.Code, databaseFailureAgentResponse.Body.String())
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM enrollment_tokens WHERE agent_id=$1`, agent.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("database audit-failed Agent create left %d rows, want original bound row only", remaining)
	}
	_, _ = db.Exec(ctx, `DROP TRIGGER IF EXISTS qch_test_reject_audit_trigger ON audit_logs; DROP FUNCTION IF EXISTS qch_test_reject_audit()`)
	server.auditWriter = nil
	successAgentCreate := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/enrollment-token", nil)
	successAgentCreate.Header.Set("Authorization", "Bearer "+adminToken)
	successAgentResponse := httptest.NewRecorder()
	handler.ServeHTTP(successAgentResponse, successAgentCreate)
	if successAgentResponse.Code != http.StatusCreated {
		t.Fatalf("audit-success Agent create status=%d body=%s", successAgentResponse.Code, successAgentResponse.Body.String())
	}
	var successAgent core.EnrollmentTokenCreated
	if err := json.NewDecoder(successAgentResponse.Body).Decode(&successAgent); err != nil {
		t.Fatalf("decode audit-success Agent create: %v", err)
	}
	if successAgent.ID == "" || successAgent.Token == "" || successAgent.AgentID != agent.ID {
		t.Fatalf("audit-success Agent create = %+v", successAgent)
	}
	defer dataStore.DeleteEnrollmentToken(context.Background(), successAgent.ID)
	successRead := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens/"+protected.ID+"/command", nil)
	successRead.Header.Set("Authorization", "Bearer "+adminToken)
	successResponse := httptest.NewRecorder()
	handler.ServeHTTP(successResponse, successRead)
	if successResponse.Code != http.StatusOK || !strings.Contains(successResponse.Body.String(), protected.Token) {
		t.Fatalf("audit-success read status=%d body=%s", successResponse.Code, successResponse.Body.String())
	}
	auditEntries, err := dataStore.ListAuditLogs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	var auditCount int
	for _, entry := range auditEntries {
		if entry.Target == protected.ID && entry.Action == "enrollment_token.command_viewed" {
			auditCount++
			if strings.Contains(entry.Detail, protected.Token) {
				t.Fatalf("successful audit entry leaked token: %+v", entry)
			}
		}
	}
	if auditCount != 1 {
		t.Fatalf("successful command read persisted audit count=%d, want 1", auditCount)
	}
	var agentCreateAuditCount int
	for _, entry := range auditEntries {
		if entry.Action == "agent.enrollment_token.created" && entry.Target == agent.ID && entry.Detail == successAgent.ID {
			agentCreateAuditCount++
			if strings.Contains(entry.Detail, successAgent.Token) {
				t.Fatalf("successful Agent create audit entry leaked token: %+v", entry)
			}
		}
	}
	if agentCreateAuditCount != 1 {
		t.Fatalf("successful Agent create audit count=%d, want 1 for enrollment %s", agentCreateAuditCount, successAgent.ID)
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
