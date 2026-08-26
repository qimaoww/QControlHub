package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func trafficPoliciesForAgent(policies []core.PortTrafficPolicy) []core.PortTrafficPolicy {
	prepared := append([]core.PortTrafficPolicy(nil), policies...)
	for index := range prepared {
		if !prepared[index].AutoBlock {
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
	s.recordAudit(request, "traffic_policy.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func trafficPolicyAuditDetail(policy core.PortTrafficPolicy) string {
	return string(policy.Engine) + " port " + strconv.Itoa(policy.Port) + "/" + string(policy.Protocol)
}
