package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getOwnAgentAccess(w http.ResponseWriter, request *http.Request) {
	access, err := s.store.UserAgentAccess(request.Context(), s.configOwnerID(request))
	if errors.Is(err, store.ErrNotFound) {
		access, err = core.AgentAccess{Shares: []core.AgentShare{}}, nil
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, access)
}

func (s *Server) getUserAgentAccess(w http.ResponseWriter, request *http.Request) {
	if role, _ := s.sessionRole(request); role != core.RoleAdmin {
		writeError(w, http.StatusForbidden, "only administrators may manage Agent sharing")
		return
	}
	access, err := s.store.UserAgentAccess(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, access)
}

func (s *Server) putUserAgentAccess(w http.ResponseWriter, request *http.Request) {
	if role, _ := s.sessionRole(request); role != core.RoleAdmin {
		writeError(w, http.StatusForbidden, "only administrators may manage Agent sharing")
		return
	}
	var input core.AgentAccessRequest
	if err := decodeJSON(w, request, &input, 128<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Revision < 1 {
		writeError(w, http.StatusBadRequest, "allocation revision is required; reload before saving")
		return
	}
	userID := request.PathValue("id")
	access, err := s.store.SetUserAgentAccess(request.Context(), userID, input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, share := range access.Shares {
		s.refreshAgentTrafficPolicies(share.AgentID)
	}
	s.recordAudit(request, "user.agent-access.updated", userID, "Agent isolation and cumulative traffic allocations updated")
	writeJSON(w, http.StatusOK, access)
}

func (s *Server) refreshUserAgentTrafficPolicies(request *http.Request, userID string) {
	access, err := s.store.UserAgentAccess(request.Context(), userID)
	if err != nil {
		return
	}
	for _, share := range access.Shares {
		s.refreshAgentTrafficPolicies(share.AgentID)
	}
}

func (s *Server) authorizeAgentResource(w http.ResponseWriter, request *http.Request) bool {
	if s.store == nil {
		return true // Unit tests may use an authentication-only server.
	}
	path := request.URL.Path
	if strings.HasPrefix(path, "/api/v1/users") {
		if role, _ := s.sessionRole(request); role != core.RoleAdmin {
			writeError(w, http.StatusForbidden, "only administrators may manage users")
			return false
		}
	}
	if strings.HasPrefix(path, "/api/v1/agents/") || strings.HasPrefix(path, "/api/v1/metrics/") {
		administration := request.Method != http.MethodGet && request.Method != http.MethodHead &&
			!strings.Contains(path, "/configs")
		if err := s.store.CheckAgentAccess(request.Context(), request.PathValue("id"), administration); err != nil {
			writeStoreError(w, err)
			return false
		}
	}
	// These capabilities expose or modify fleet-wide infrastructure. A shared
	// user cannot bypass an allowance by re-enrolling a node or editing global
	// credentials; per-target Sub-Store operations remain available.
	global := strings.HasPrefix(path, "/api/v1/enrollment-tokens") ||
		strings.HasPrefix(path, "/api/v1/users") || path == "/api/v1/audit" ||
		path == "/api/v1/core-logs" ||
		(request.Method != http.MethodGet && (path == "/api/v1/settings" || path == "/api/v1/substore-sync/settings"))
	if global {
		isolated, err := s.store.IsAgentIsolated(request.Context())
		if err != nil {
			writeStoreError(w, err)
			return false
		}
		if isolated {
			writeError(w, http.StatusForbidden, "shared users cannot manage fleet-wide infrastructure")
			return false
		}
	}
	return true
}
