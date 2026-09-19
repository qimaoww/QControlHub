package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientConnectionsRejectInvalidQueries(t *testing.T) {
	token := strings.Repeat("a", 48)
	handler := New(nil, Config{AdminToken: token}).Handler()
	for _, query := range []string{"limit=201", "port=65536", "port=0", "before=-1", "engine=unknown", "transport=quic", "client_ip=bad", "since=bad", "bucket=week", "since=2026-01-01T00:00:00Z&until=2026-02-01T00:00:00Z"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/client-connections?"+query, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/client-connections", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", response.Code)
	}
}
