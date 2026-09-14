const rolePermissions = {
  admin: new Set(["overview.read", "agents.read", "agents.manage", "client-access.read", "deployments.read", "catalogs.read", "agent-config.read", "agent-config.write", "configs.read", "configs.write", "configs.delete", "configs.restore", "tasks.read", "tasks.execute", "enrollment.manage", "settings.read", "settings.manage", "audit.read", "metrics.read", "traffic.read", "traffic.manage", "users.manage", "core-logs.read", "templates.read", "templates.write", "templates.delete"]),
  operator: new Set(["overview.read", "agents.read", "deployments.read", "client-access.read", "catalogs.read", "agent-config.read", "agent-config.write", "configs.read", "configs.write", "tasks.read", "tasks.execute", "settings.read", "audit.read", "metrics.read", "traffic.read", "traffic.manage", "templates.read", "templates.write", "core-logs.read"]),
  auditor: new Set(["overview.read", "agents.read", "deployments.read", "tasks.read", "settings.read", "audit.read", "metrics.read", "traffic.read", "core-logs.read"]),
  readonly: new Set(["overview.read", "agents.read", "deployments.read", "client-access.read", "catalogs.read", "agent-config.read", "configs.read", "tasks.read", "settings.read", "audit.read", "metrics.read", "traffic.read", "templates.read", "core-logs.read"]),
};
const roleRanks = { readonly: 1, auditor: 1, operator: 2, admin: 3 };

export function createPermissionChecker(state) {
  const can = (capability, agent) => {
    const role = state.session?.role;
    if (role === "admin") return true;
    if (capability === "agent-access.read") return Boolean(state.session);
    if (capability === "users.manage") return false;
    if (agent && ["host.manage", "operator", "agents.manage", "traffic.manage", "enrollment.manage"].includes(capability) && (agent.can_manage === false || (role === "user" && agent.can_manage !== true))) return false;
    if (capability === "host.manage") return can("tasks.execute");
    if (capability === "system-bbr.read") return can("agents.read");
    if (capability === "operator") capability = "tasks.execute";
    if (role === "user") return (state.session?.permissions || []).includes(capability);
    if (capability in roleRanks) return (roleRanks[role] || 0) >= (roleRanks[capability] || 0);
    return Boolean(rolePermissions[role]?.has(capability));
  };
  return can;
}
