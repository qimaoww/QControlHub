export const installedEngineCount = (agent) =>
  (agent?.capabilities || []).filter(
    (engine) => agent.runtime?.[engine]?.installed,
  ).length;

export function createServiceActionGuard(can) {
const serviceActionDisabled = (action, online, installed, serviceStatus, agent) => {
  if (!online || !installed || !can("tasks.execute")) return true;
  if (action !== "status" && !can("host.manage", agent)) return true;
  if (action === "start")
    return ["active", "activating"].includes(serviceStatus);
  if (action === "stop")
    return ["inactive", "deactivating"].includes(serviceStatus);
  if (action === "restart")
    return ["inactive", "activating", "deactivating"].includes(serviceStatus);
  return false;
};

  return serviceActionDisabled;
}
