package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestManagementAPIRouteAuthorizationMatrix(t *testing.T) {
	t.Parallel()
	const (
		adminToken    = "admin-route-policy-token"
		operatorToken = "operator-route-policy-token"
		auditorToken  = "auditor-route-policy-token"
		readonlyToken = "readonly-route-policy-token"
	)
	type route struct{ method, path string }
	managementRoutes := []route{
		{http.MethodGet, "/api/v1/overview"},
		{http.MethodGet, "/api/v1/agents"},
		{http.MethodGet, "/api/v1/deployments"},
		{http.MethodGet, "/api/v1/client-access"},
		{http.MethodPut, "/api/v1/agents/agt_0123456789abcdef/client-address"},
		{http.MethodGet, "/api/v1/config-catalogs/mihomo"},
		{http.MethodDelete, "/api/v1/agents/agt_0123456789abcdef"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/enrollment-token"},
		{http.MethodGet, "/api/v1/agents/agt_0123456789abcdef/configs"},
		{http.MethodGet, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo"},
		{http.MethodPut, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo"},
		{http.MethodGet, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/workspace"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/plans"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/server-inbounds"},
		{http.MethodGet, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/fields/dns"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/fields/dns"},
		{http.MethodGet, "/api/v1/configs"},
		{http.MethodPost, "/api/v1/configs"},
		{http.MethodPut, "/api/v1/configs/cfg_test"},
		{http.MethodDelete, "/api/v1/configs/cfg_test"},
		{http.MethodGet, "/api/v1/configs/cfg_test/revisions"},
		{http.MethodGet, "/api/v1/configs/cfg_test/revisions/1"},
		{http.MethodPost, "/api/v1/configs/cfg_test/revisions/1/restore"},
		{http.MethodGet, "/api/v1/tasks"},
		{http.MethodPost, "/api/v1/tasks"},
		{http.MethodGet, "/api/v1/tasks/tsk_test"},
		{http.MethodGet, "/api/v1/tasks/tsk_test/config-snapshot"},
		{http.MethodDelete, "/api/v1/tasks/tsk_test"},
		{http.MethodPost, "/api/v1/tasks/tsk_test/retry"},
		{http.MethodGet, "/api/v1/enrollment-tokens"},
		{http.MethodPost, "/api/v1/enrollment-tokens"},
		{http.MethodDelete, "/api/v1/enrollment-tokens/tok_test"},
		{http.MethodGet, "/api/v1/settings"},
		{http.MethodPut, "/api/v1/settings"},
		{http.MethodGet, "/api/v1/audit"},
		{http.MethodGet, "/api/v1/metrics/agt_0123456789abcdef"},
		{http.MethodGet, "/api/v1/templates"},
		{http.MethodPost, "/api/v1/templates"},
		{http.MethodDelete, "/api/v1/templates/tpl_test"},
		{http.MethodPost, "/api/v1/templates/tpl_test/apply"},
	}
	status := func(item route, token string) int {
		handler := New(nil, Config{AdminToken: adminToken, OperatorTokens: []string{operatorToken}, AuditorTokens: []string{auditorToken}, ReadonlyTokens: []string{readonlyToken}}).Handler()
		request := httptest.NewRequest(item.method, item.path, nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	for _, item := range managementRoutes {
		if got := status(item, ""); got != http.StatusUnauthorized {
			t.Errorf("%s %s without credentials = %d, want %d", item.method, item.path, got, http.StatusUnauthorized)
		}
	}
	readonlyDenied := []route{
		{http.MethodDelete, "/api/v1/agents/agt_0123456789abcdef"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/enrollment-token"},
		{http.MethodPut, "/api/v1/agents/agt_0123456789abcdef/client-address"},
		{http.MethodPut, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/plans"}, {http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/server-inbounds"}, {http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/configs/mihomo/fields/dns"},
		{http.MethodPost, "/api/v1/configs"}, {http.MethodPut, "/api/v1/configs/cfg_test"}, {http.MethodDelete, "/api/v1/configs/cfg_test"},
		{http.MethodPost, "/api/v1/configs/cfg_test/revisions/1/restore"},
		{http.MethodPost, "/api/v1/tasks"}, {http.MethodDelete, "/api/v1/tasks/tsk_test"}, {http.MethodPost, "/api/v1/tasks/tsk_test/retry"},
		{http.MethodGet, "/api/v1/enrollment-tokens"}, {http.MethodPost, "/api/v1/enrollment-tokens"}, {http.MethodDelete, "/api/v1/enrollment-tokens/tok_test"},
		{http.MethodPut, "/api/v1/settings"},
		{http.MethodPost, "/api/v1/templates"}, {http.MethodDelete, "/api/v1/templates/tpl_test"}, {http.MethodPost, "/api/v1/templates/tpl_test/apply"},
	}
	for _, item := range readonlyDenied {
		if got := status(item, readonlyToken); got != http.StatusForbidden {
			t.Errorf("%s %s with readonly token = %d, want %d", item.method, item.path, got, http.StatusForbidden)
		}
	}
	operatorDenied := []route{
		{http.MethodDelete, "/api/v1/agents/agt_0123456789abcdef"},
		{http.MethodPost, "/api/v1/agents/agt_0123456789abcdef/enrollment-token"},
		{http.MethodPut, "/api/v1/agents/agt_0123456789abcdef/client-address"},
		{http.MethodDelete, "/api/v1/configs/cfg_test"},
		{http.MethodPost, "/api/v1/configs/cfg_test/revisions/1/restore"},
		{http.MethodGet, "/api/v1/enrollment-tokens"}, {http.MethodPost, "/api/v1/enrollment-tokens"}, {http.MethodDelete, "/api/v1/enrollment-tokens/tok_test"},
		{http.MethodPut, "/api/v1/settings"},
		{http.MethodDelete, "/api/v1/templates/tpl_test"},
	}
	for _, item := range operatorDenied {
		if got := status(item, operatorToken); got != http.StatusForbidden {
			t.Errorf("%s %s with operator token = %d, want %d", item.method, item.path, got, http.StatusForbidden)
		}
	}
	auditorDenied := []route{
		{http.MethodPost, "/api/v1/tasks"},
		{http.MethodPost, "/api/v1/configs"},
		{http.MethodGet, "/api/v1/settings"},
	}
	for _, item := range auditorDenied {
		if got := status(item, auditorToken); got != http.StatusForbidden {
			t.Errorf("%s %s with auditor token = %d, want %d", item.method, item.path, got, http.StatusForbidden)
		}
	}
}

// TestManagementAPIRolesEnforceTokenPrivileges verifies that operator and
// readonly tokens can read but not administer, and that admin keeps every
// capability.
func TestManagementAPIRolesEnforceTokenPrivileges(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	const (
		adminToken    = "admin-role-token"
		operatorToken = "operator-role-token"
		readonlyToken = "readonly-role-token"
	)
	server := New(dataStore, Config{
		AdminToken:     adminToken,
		OperatorTokens: []string{operatorToken},
		ReadonlyTokens: []string{readonlyToken},
	})
	handler := server.Handler()

	request := func(method, path, token string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}
	statusFor := func(method, path, token string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request(method, path, token))
		return recorder.Code
	}

	// Read endpoints are open to every role.
	for _, token := range []string{adminToken, operatorToken, readonlyToken} {
		if status := statusFor(http.MethodGet, "/api/v1/agents", token); status != http.StatusOK {
			t.Fatalf("GET agents with %s = %d, want 200", token, status)
		}
	}

	// Operator may submit tasks but not administer enrollment tokens.
	if status := statusFor(http.MethodPost, "/api/v1/tasks", operatorToken); status != http.StatusBadRequest {
		t.Fatalf("POST tasks with operator = %d, want 400 (reaches handler)", status)
	}
	if status := statusFor(http.MethodPost, "/api/v1/enrollment-tokens", operatorToken); status != http.StatusForbidden {
		t.Fatalf("POST enrollment-tokens with operator = %d, want 403", status)
	}
	if status := statusFor(http.MethodDelete, "/api/v1/agents/agt_0123456789abcdef", operatorToken); status != http.StatusForbidden {
		t.Fatalf("DELETE agents with operator = %d, want 403", status)
	}

	// Readonly cannot submit tasks either.
	if status := statusFor(http.MethodPost, "/api/v1/tasks", readonlyToken); status != http.StatusForbidden {
		t.Fatalf("POST tasks with readonly = %d, want 403", status)
	}

	// Admin keeps everything; a bogus token is rejected everywhere.
	if status := statusFor(http.MethodPost, "/api/v1/enrollment-tokens", adminToken); status != http.StatusBadRequest {
		t.Fatalf("POST enrollment-tokens with admin = %d, want 400 (reaches handler)", status)
	}
	if status := statusFor(http.MethodGet, "/api/v1/agents", "bogus-token"); status != http.StatusUnauthorized {
		t.Fatalf("GET agents with bogus token = %d, want 401", status)
	}
}
