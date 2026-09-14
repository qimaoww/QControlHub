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

// These sets are built once during package initialization and never exposed
// for mutation. Login/session reads can share them without rebuilding policy.
var (
	rolePermissions        = map[Role]map[Permission]struct{}{}
	grantablePermissions   []Permission
	grantablePermissionSet map[Permission]struct{}
)

func AllPermissions() []Permission { return append([]Permission(nil), allPermissions...) }

// PermissionGrantsAdministration identifies administrator-only capabilities.
// Explicit user grants must agree with the store's existing administrator
// scope instead of implying authority that the stored role does not have.
func PermissionGrantsAdministration(permission Permission) bool {
	return permission == PermissionUsersManage
}

// GrantablePermissions returns the capabilities an administrator may assign
// to a non-administrator. Store writes reject administrator-only requests;
// reads use this allowlist to filter impossible grants in older backups.
func GrantablePermissions() []Permission {
	return append([]Permission(nil), grantablePermissions...)
}

// NormalizePermissions keeps known capabilities in their original order,
// without duplicates. Account writes must also validate the resulting role.
func NormalizePermissions(values []Permission) []Permission {
	return normalizePermissions(values, rolePermissions[RoleAdmin])
}

// NormalizeGrantablePermissions also removes administrator-only capabilities
// when resolving an existing non-administrator account.
func NormalizeGrantablePermissions(values []Permission) []Permission {
	return normalizePermissions(values, grantablePermissionSet)
}

func normalizePermissions(values []Permission, allowed map[Permission]struct{}) []Permission {
	capacity := min(len(values), len(allowed))
	seen := make(map[Permission]struct{}, capacity)
	result := make([]Permission, 0, capacity)
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
	for _, permission := range allPermissions {
		if !PermissionGrantsAdministration(permission) {
			grantablePermissions = append(grantablePermissions, permission)
		}
	}
	grantablePermissionSet = permissionSet(grantablePermissions...)
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
