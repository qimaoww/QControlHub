package api

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func trafficPoliciesForAgent(policies []core.PortTrafficPolicy) []core.PortTrafficPolicy {
	prepared := append([]core.PortTrafficPolicy(nil), policies...)
	for index := range prepared {
		if !prepared[index].QuotaEnabled || !prepared[index].AutoBlock {
			// Older Agents ignore the auto_block JSON field and always enforce
			// LimitBytes. Sending an unreachable limit keeps monitor-only
			// policies fail-safe until those Agents are upgraded.
			prepared[index].LimitBytes = math.MaxInt64
		}
	}
	return prepared
}

func (s *Server) listPortTrafficUsage(w http.ResponseWriter, request *http.Request) {
	monthValue := strings.TrimSpace(request.URL.Query().Get("month"))
	if monthValue == "" {
		monthValue = time.Now().UTC().Format("2006-01")
	}
	month, err := time.Parse("2006-01", monthValue)
	if err != nil {
		writeError(w, http.StatusBadRequest, "month must use YYYY-MM")
		return
	}
	agentID := strings.TrimSpace(request.URL.Query().Get("agent_id"))
	policyID := strings.TrimSpace(request.URL.Query().Get("policy_id"))
	if len(agentID) > 100 {
		writeError(w, http.StatusBadRequest, "agent_id is invalid")
		return
	}
	if policyID != "" && !core.ValidPortTrafficPolicyID(policyID) {
		writeError(w, http.StatusBadRequest, "policy_id is invalid")
		return
	}
	usage, err := s.store.ListPortTrafficDailyUsage(request.Context(), agentID, policyID, month)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"month":    monthValue,
		"timezone": "UTC",
		"days":     usage,
	})
}

func (s *Server) listPortTrafficPolicies(w http.ResponseWriter, request *http.Request) {
	policies, err := s.store.ListPortTrafficPolicies(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, policies)
}

func (s *Server) reconcilePortTrafficEndpoints(ctx context.Context) ([]core.PortTrafficEndpoint, []string, error) {
	configs, err := s.store.ListAgentConfigs(ctx)
	if err != nil {
		return nil, nil, err
	}
	endpoints := trafficEndpointsFromConfigs(configs)
	changedAgents, err := s.store.ReconcilePortTrafficEndpoints(ctx, endpoints)
	if err != nil {
		return endpoints, nil, err
	}
	return endpoints, changedAgents, nil
}

func (s *Server) refreshPortTrafficMonitoring(ctx context.Context, connectedAgentID string) {
	_, changedAgents, err := s.reconcilePortTrafficEndpoints(ctx)
	if err != nil {
		// Listener accounting is best-effort and must never make configuration
		// management or an authenticated Agent session unavailable.
		slog.Warn("reconcile discovered traffic endpoints", "error", err)
		return
	}
	for _, agentID := range changedAgents {
		if agentID != connectedAgentID {
			s.DisconnectAgent(agentID)
		}
	}
}

func (s *Server) listPortTrafficEndpoints(w http.ResponseWriter, request *http.Request) {
	configs, err := s.store.ListAgentConfigs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, trafficEndpointsFromConfigs(configs))
}

func trafficEndpointsFromConfigs(configs []core.Config) []core.PortTrafficEndpoint {
	result := make([]core.PortTrafficEndpoint, 0)
	seen := make(map[string]struct{})
	for _, config := range configs {
		if config.AgentID == "" || !config.Engine.Valid() {
			continue
		}
		for _, endpoint := range serverconfig.DiscoverTrafficPorts(config.Engine, config.Content) {
			endpoint.AgentID = config.AgentID
			endpoint.ConfigVersion = config.Version
			endpoint.ConfigUpdatedAt = config.UpdatedAt
			key := endpoint.AgentID + "\x00" + string(endpoint.Engine) + "\x00" + strconv.Itoa(endpoint.Port) + "\x00" + string(endpoint.Protocol) + "\x00" + endpoint.Name
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, endpoint)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].AgentID != result[right].AgentID {
			return result[left].AgentID < result[right].AgentID
		}
		if result[left].Port != result[right].Port {
			return result[left].Port < result[right].Port
		}
		if result[left].Engine != result[right].Engine {
			return result[left].Engine < result[right].Engine
		}
		return result[left].Name < result[right].Name
	})
	return result
}

func (s *Server) createPortTrafficPolicy(w http.ResponseWriter, request *http.Request) {
	var input core.PortTrafficPolicyRequest
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	policy, err := s.store.CreatePortTrafficPolicy(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.DisconnectAgent(policy.AgentID)
	s.recordAudit(request, "traffic_policy.created", policy.ID, trafficPolicyAuditDetail(policy))
	writeJSON(w, http.StatusCreated, policy)
}

func (s *Server) updatePortTrafficPolicy(w http.ResponseWriter, request *http.Request) {
	var input core.PortTrafficPolicyRequest
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	policy, err := s.store.UpdatePortTrafficPolicy(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.DisconnectAgent(policy.AgentID)
	s.recordAudit(request, "traffic_policy.updated", policy.ID, trafficPolicyAuditDetail(policy))
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) resetPortTrafficPolicy(w http.ResponseWriter, request *http.Request) {
	policy, err := s.store.ResetPortTrafficPolicy(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.DisconnectAgent(policy.AgentID)
	s.recordAudit(request, "traffic_policy.reset", policy.ID, trafficPolicyAuditDetail(policy))
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) deletePortTrafficPolicy(w http.ResponseWriter, request *http.Request) {
	agentID, err := s.store.DeletePortTrafficPolicy(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.DisconnectAgent(agentID)
	s.recordAudit(request, "traffic_policy.quota_removed", request.PathValue("id"), "monitoring continues for discovered ports")
	w.WriteHeader(http.StatusNoContent)
}

func trafficPolicyAuditDetail(policy core.PortTrafficPolicy) string {
	return string(policy.Engine) + " port " + strconv.Itoa(policy.Port) + "/" + string(policy.Protocol)
}
