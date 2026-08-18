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
