// Execution and task visibility are independent grants. These reasons are
// shared by rendering, live refresh and the final submit guard.
export function coreInstallBlockReason(agent, engine, can, { automatic = false, pending = false } = {}) {
  if (!can("tasks.execute")) return "需要任务执行权限（tasks.execute）。";
  if (!automatic && !can("host.manage", agent)) return "宿主机内核版本由节点所有者管理；共享用户请在增加入站时初始化独立实例。";
  if (!(agent.supported_capabilities || agent.capabilities || []).includes(engine)) return "节点不支持此内核。";
  if (!(agent.capabilities || []).includes(engine)) return "内核能力未启用，请由所有者在节点设置的 Agent 页开启。";
  if (agent.runtime?.[engine]?.existing_config_unsupported_reason) return agent.runtime[engine].existing_config_unsupported_reason;
  if (agent.status !== "online") return "节点离线，上线后可提交安装任务。";
  if (pending) return "安装任务正在执行，请等待结果刷新。";
  if (automatic) {
    if (!(agent.features || []).includes("preset-auto-install-v1")) return "Agent 不支持自动安装，请在节点设置升级 Agent。";
    if (agent.can_manage === false) {
      if (!["shared-core-instances-v1", "shared-core-prepare-v1"].every(feature => (agent.features || []).includes(feature)))
        return "请联系节点所有者升级 Agent，以支持共享实例的受控安装准备。";
    } else if (!can("host.manage", agent)) return "自动安装需要此节点的管理授权。";
  }
  return "";
}
