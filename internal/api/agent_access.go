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

func (s *Server) respondAgentShare(w http.ResponseWriter, request *http.Request) {
	var input core.AgentShareResponseRequest
	if err := decodeJSON(w, request, &input, 4<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := request.PathValue("id")
	access, err := s.store.RespondAgentShare(request.Context(), id, input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, share := range access.Shares {
		if share.ID == id {
			s.refreshAgentTrafficPolicies(share.AgentID)
			break
		}
	}
	s.recordAudit(request, "agent.sharing."+input.Decision, id, "Recipient responded to Agent sharing")
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

func (s *Server) getAgentSharing(w http.ResponseWriter, request *http.Request) {
	sharing, err := s.store.AgentSharing(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sharing)
}

func (s *Server) putAgentSharing(w http.ResponseWriter, request *http.Request) {
	var input core.AgentSharingRequest
	if err := decodeJSON(w, request, &input, 128<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := request.PathValue("id")
	sharing, err := s.store.SetAgentSharing(request.Context(), id, input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.refreshAgentTrafficPolicies(id)
	s.recordAudit(request, "agent.sharing.updated", id, "Agent recipients, ports and cumulative allowances updated")
	writeJSON(w, http.StatusOK, sharing)
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
			!strings.Contains(path, "/configs") && !strings.HasSuffix(path, "/client-address")
		administration = administration || strings.HasSuffix(path, "/sharing") || strings.HasSuffix(path, "/komari")
		if err := s.store.CheckAgentAccess(request.Context(), request.PathValue("id"), administration); err != nil {
			writeStoreError(w, err)
			return false
		}
		if value := request.PathValue("engine"); value != "" {
			engine, err := core.ParseEngine(value)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return false
			}
			if err := s.store.CheckAgentEngineAccess(request.Context(), request.PathValue("id"), engine); err != nil {
				writeStoreError(w, err)
				return false
			}
		}
	}
	// Enrollment credentials, settings, integrations and audit entries are
	// account-owned. Host-wide operations are checked against the specific
	// Agent; receiving a share never grants its enrollment or administration.
	return true
}
