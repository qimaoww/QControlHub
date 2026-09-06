package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The generic task create/retry routes must not bypass node-management
// authorization for system-wide network changes.
func (s *Server) authorizeSystemBBR(w http.ResponseWriter, request *http.Request, agentID string) bool {
	role, _ := s.sessionRole(request)
	permissions, _ := s.sessionPermissions(request)
	if !role.Allows(core.PermissionAgentsManage) && !core.HasPermission(permissions, core.PermissionAgentsManage) {
		writeError(w, http.StatusForbidden, "system BBR requires agents.manage and tasks.execute")
		return false
	}
	agent, err := s.store.GetAgent(request.Context(), agentID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if agent.Status != "online" {
		writeError(w, http.StatusConflict, "Agent is offline; reconnect before changing system BBR")
		return false
	}
	return true
}
