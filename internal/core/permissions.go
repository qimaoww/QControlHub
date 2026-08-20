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
	PermissionUsersManage      Permission = "users.manage"
	PermissionTemplatesRead    Permission = "templates.read"
	PermissionTemplatesWrite   Permission = "templates.write"
	PermissionTemplatesDelete  Permission = "templates.delete"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	RoleAuditor: permissionSet(
		PermissionOverviewRead,
		PermissionAgentsRead,
		PermissionDeploymentsRead,
		PermissionTasksRead,
		PermissionAuditRead,
		PermissionMetricsRead,
	),
	RoleReadonly: permissionSet(
		PermissionOverviewRead,
		PermissionAgentsRead,
		PermissionDeploymentsRead,
		PermissionClientAccessRead,
		PermissionCatalogsRead,
		PermissionAgentConfigRead,
		PermissionConfigsRead,
		PermissionTasksRead,
		PermissionSettingsRead,
		PermissionAuditRead,
		PermissionMetricsRead,
		PermissionTemplatesRead,
	),
	RoleOperator: permissionSet(
		PermissionOverviewRead,
		PermissionAgentsRead,
		PermissionDeploymentsRead,
		PermissionClientAccessRead,
		PermissionCatalogsRead,
		PermissionAgentConfigRead,
		PermissionAgentConfigWrite,
		PermissionConfigsRead,
		PermissionConfigsWrite,
		PermissionTasksRead,
		PermissionTasksExecute,
		PermissionSettingsRead,
		PermissionAuditRead,
		PermissionMetricsRead,
		PermissionTemplatesRead,
		PermissionTemplatesWrite,
	),
}

// Admin is intentionally built from the union of every declared capability.
// New capabilities must be listed here so the administrator remains the
// complete break-glass role while lower roles stay deny-by-default.
func init() {
	rolePermissions[RoleAdmin] = permissionSet(
		PermissionOverviewRead,
		PermissionAgentsRead,
		PermissionAgentsManage,
		PermissionClientAccessRead,
		PermissionDeploymentsRead,
		PermissionCatalogsRead,
		PermissionAgentConfigRead,
		PermissionAgentConfigWrite,
		PermissionConfigsRead,
		PermissionConfigsWrite,
		PermissionConfigsDelete,
		PermissionConfigsRestore,
		PermissionTasksRead,
		PermissionTasksExecute,
		PermissionEnrollmentManage,
		PermissionSettingsRead,
		PermissionSettingsManage,
		PermissionAuditRead,
		PermissionMetricsRead,
		PermissionUsersManage,
		PermissionTemplatesRead,
		PermissionTemplatesWrite,
		PermissionTemplatesDelete,
	)
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
