package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficPoliciesForAgentKeepsMonitorOnlySafeForLegacyAgents(t *testing.T) {
	t.Parallel()
	policies := []core.PortTrafficPolicy{
		{ID: "trf_0123456789abcdef", LimitBytes: 1024, AutoBlock: false},
		{ID: "trf_fedcba9876543210", LimitBytes: 2048, AutoBlock: true},
	}
	prepared := trafficPoliciesForAgent(policies)
	if prepared[0].LimitBytes != math.MaxInt64 || prepared[1].LimitBytes != 2048 {
		t.Fatalf("prepared policies = %+v", prepared)
	}
	if policies[0].LimitBytes != 1024 {
		t.Fatal("preparing Agent policies mutated the management API value")
	}
}

func TestTrafficUsageQueryValidation(t *testing.T) {
	t.Parallel()
	const token = "traffic-usage-query-validation-token"
	handler := New(nil, Config{AdminToken: token}).Handler()
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "month", path: "/api/v1/traffic-usage?month=2026-13", want: "month must use YYYY-MM"},
		{name: "policy", path: "/api/v1/traffic-usage?month=2026-08&policy_id=invalid", want: "policy_id is invalid"},
		{name: "agent", path: "/api/v1/traffic-usage?month=2026-08&agent_id=" + strings.Repeat("a", 101), want: "agent_id is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("status=%d body=%q, want 400 containing %q", response.Code, response.Body.String(), test.want)
			}
		})
	}
}
