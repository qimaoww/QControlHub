package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// sessionAllows reports whether the authenticated caller holds a capability.
// Role and explicit permission lookups are both deny-by-default, so an
// unresolvable session never grants access.
func (s *Server) sessionAllows(request *http.Request, permission core.Permission) bool {
	role, roleOK := s.sessionRole(request)
	permissions, permissionsOK := s.sessionPermissions(request)
	if !roleOK || !permissionsOK {
		return false
	}
	return role.Allows(permission) || core.HasPermission(permissions, permission)
}
