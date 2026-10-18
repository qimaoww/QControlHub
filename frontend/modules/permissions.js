export function createPermissionChecker(state) {
  const can = (capability, agent) => {
    const role = state.session?.role;
    if (!["admin", "user"].includes(role)) return false;
    if (capability === "certificates.read" || capability === "agent-access.read") return true;
    if (capability === "instance.manage") return Boolean(agent?.can_manage === false &&
      agent.shared_allocation?.enabled && agent.shared_allocation.status === "accepted" &&
      agent.features?.includes("shared-core-lifecycle-v1") && can("tasks.execute"));
    if (agent && ["host.manage", "operator", "agents.manage", "traffic.manage", "enrollment.manage"].includes(capability) && (agent.can_manage === false || (role === "user" && agent.can_manage !== true))) return false;
    if (capability === "host.manage") return can("tasks.execute");
    if (capability === "system-bbr.read") return can("agents.read");
    if (capability === "operator") capability = "tasks.execute";
    if (role === "admin") return true;
    if (capability === "users.manage") return false;
    return (state.session.permissions || []).includes(capability);
  };
  return can;
}
