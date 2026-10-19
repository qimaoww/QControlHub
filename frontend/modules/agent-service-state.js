import { describeCoreRuntime, describeServiceAction } from "./status-controls.js";

export { describeCoreRuntime, describeServiceAction };

export const installedEngineCount = (agent) =>
  (agent?.capabilities || []).filter(
    (engine) => agent.runtime?.[engine]?.installed,
  ).length;

export function createServiceActionGuard(can) {
  const serviceActionDisabled = (action, online, installed, serviceStatus, agent, engine) => {
    if (!online || !installed || !can("tasks.execute")) return true;
    if (agent?.can_manage === false) {
      const instance = agent.runtime?.[engine]?.instance;
      if (!can("instance.manage", agent) || !instance || ["pending", "running"].includes(instance.operation?.status)) return true;
      if (action !== "status" && !instance.created) return true;
      if (["start", "restart"].includes(action) && (!instance.deployed_version || instance.config_uncertain || instance.restriction)) return true;
    } else if (action !== "status" && !can("host.manage", agent)) return true;
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
