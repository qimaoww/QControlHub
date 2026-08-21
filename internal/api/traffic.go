package api

import (
	"net/http"
	"strconv"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
