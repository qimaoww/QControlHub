package core

// Permission is an explicit capability granted by a panel role. Keeping the
// policy in the core package makes the API and other adapters share the same
// authorization vocabulary instead of relying on route-specific rank checks.
type Permission string

const (
	PermissionOverviewRead     Permission = "overview.read"
	PermissionAgentsRead       Permission = "agents.read"
	PermissionAgentsManage     Permission = "agents.manage"
	PermissionClientAccessRead Permission = "client-access.read"
	PermissionDeploymentsRead  Permission = "deployments.read"
	PermissionCatalogsRead     Permission = "catalogs.read"
	PermissionAgentConfigRead  Permission = "agent-config.read"
	PermissionAgentConfigWrite Permission = "agent-config.write"
	PermissionConfigsRead      Permission = "configs.read"
	PermissionConfigsWrite     Permission = "configs.write"
	PermissionConfigsDelete    Permission = "configs.delete"
	PermissionConfigsRestore   Permission = "configs.restore"
	PermissionTasksRead        Permission = "tasks.read"
	PermissionTasksExecute     Permission = "tasks.execute"
	PermissionEnrollmentManage Permission = "enrollment.manage"
	PermissionSettingsRead     Permission = "settings.read"
	PermissionSettingsManage   Permission = "settings.manage"
	PermissionAuditRead        Permission = "audit.read"
	PermissionMetricsRead      Permission = "metrics.read"
	PermissionPanelMetricsRead Permission = "panel-metrics.read"
	PermissionCoreLogsRead     Permission = "core-logs.read"
	PermissionTrafficRead      Permission = "traffic.read"
	PermissionTrafficManage    Permission = "traffic.manage"
	PermissionUsersManage      Permission = "users.manage"
	PermissionTemplatesRead    Permission = "templates.read"
	PermissionTemplatesWrite   Permission = "templates.write"
	PermissionTemplatesDelete  Permission = "templates.delete"
)

var allPermissions = []Permission{
	PermissionOverviewRead, PermissionAgentsRead, PermissionAgentsManage,
	PermissionClientAccessRead, PermissionDeploymentsRead, PermissionCatalogsRead,
	PermissionAgentConfigRead, PermissionAgentConfigWrite, PermissionConfigsRead,
	PermissionConfigsWrite, PermissionConfigsDelete, PermissionConfigsRestore,
	PermissionTasksRead, PermissionTasksExecute, PermissionEnrollmentManage,
	PermissionSettingsRead, PermissionSettingsManage, PermissionAuditRead,
	PermissionMetricsRead, PermissionPanelMetricsRead, PermissionCoreLogsRead, PermissionTrafficRead, PermissionTrafficManage, PermissionUsersManage, PermissionTemplatesRead,
	PermissionTemplatesWrite, PermissionTemplatesDelete,
}

var rolePermissions = map[Role]map[Permission]struct{}{}

func AllPermissions() []Permission { return append([]Permission(nil), allPermissions...) }

// PermissionGrantsAdministration reports whether holding this capability is
// equivalent to being an administrator.
//
// users.manage is admin-equivalent by construction rather than merely strong:
// UpdateUser authorizes on scope.Admin, scope.Admin is derived from the stored
// role, and an admin resolves every agent, config and task without an owner
// filter. An account that can edit users can therefore set its own role to
// admin and take over the whole fleet.
//
// The console never offers this capability for a user account, so the
// invariant enforced by GrantablePermissions is defence in depth: it also
// covers a direct API call, a future console change, and a database restored
// from an older schema.
func PermissionGrantsAdministration(permission Permission) bool {
	return permission == PermissionUsersManage
}

// GrantablePermissions returns the capabilities an administrator may assign
// explicitly. It is AllPermissions minus the admin-equivalent ones, so
// NormalizePermissions rejects an attempt to store an administrator capability
// on a user row instead of quietly dropping it.
func GrantablePermissions() []Permission {
	result := make([]Permission, 0, len(allPermissions))
	for _, permission := range allPermissions {
		if PermissionGrantsAdministration(permission) {
			continue
		}
		result = append(result, permission)
	}
	return result
}

// NormalizePermissions keeps only known capabilities. assignable is used for
// requests that attach capabilities to an account, so an admin-equivalent
// capability cannot be stored on a user row; pass AllPermissions to normalize
// a role that already carries full authority.
func NormalizePermissions(values []Permission, assignable []Permission) []Permission {
	allowed := make(map[Permission]struct{}, len(assignable))
	for _, value := range assignable {
		allowed[value] = struct{}{}
	}
	seen := make(map[Permission]struct{}, len(values))
	result := make([]Permission, 0, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// Admin is intentionally built from the union of every declared capability.
// New capabilities must be listed here so the administrator remains the
// complete break-glass role while lower roles stay deny-by-default.
func init() {
	rolePermissions[RoleAdmin] = permissionSet(allPermissions...)
}

func permissionSet(values ...Permission) map[Permission]struct{} {
	result := make(map[Permission]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// Allows reports whether this role has the named capability. Unknown roles
// and capabilities are denied by default.
func (role Role) Allows(permission Permission) bool {
	_, ok := rolePermissions[role][permission]
	return ok
}

func HasPermission(values []Permission, wanted Permission) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
