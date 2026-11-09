// Execution and task visibility are independent grants. These reasons are
// shared by rendering, live refresh and the final submit guard.
export function coreInstallBlockReason(agent, engine, can, { automatic = false, pending = false } = {}) {
  if (!can("tasks.execute")) return "需要任务执行权限（tasks.execute）。";
  const shared = agent.can_manage === false;
  if (!automatic && !shared && !can("host.manage", agent)) return "需要此节点的管理授权。";
  if (!(agent.supported_capabilities || agent.capabilities || []).includes(engine)) return "节点不支持此内核。";
  if (!(agent.capabilities || []).includes(engine)) return "内核能力未启用，请由所有者在内核设置中开启。";
  if (agent.runtime?.[engine]?.existing_config_unsupported_reason) return agent.runtime[engine].existing_config_unsupported_reason;
  if (agent.status !== "online") return "节点离线，上线后可提交安装任务。";
  if (pending) return "安装任务正在执行，请等待结果刷新。";
  if (shared) {
    if (!["shared-traffic-v1", "shared-engines-v1", "independent-egress-v1", "shared-core-instances-v1", "shared-core-prepare-v1"].every(feature => (agent.features || []).includes(feature)))
      return "请联系节点所有者升级 Agent，以支持共享实例的受控安装准备。";
    if (!automatic && agent.runtime?.[engine]?.installed) return "内核已安装，直接复用当前版本；版本切换由所有者管理。";
  }
  if (automatic) {
    if (!(agent.features || []).includes("preset-auto-install-v2")) return "Agent 不支持自动安装，请在节点设置升级 Agent。";
    if (!(agent.features || []).includes("independent-egress-v1")) return "Agent 不支持独立出口校验，请联系所有者在节点设置升级 Agent。";
    if (!shared && !can("host.manage", agent)) return "自动安装需要此节点的管理授权。";
  }
  return "";
}

// Installation is part of configuration execution only when explicitly confirmed.
export function configDeployBlockReason(agent, engine, can) {
  if (!can("tasks.execute")) return "没有配置部署权限。";
  if (!(agent.supported_capabilities || agent.capabilities || []).includes(engine)) return "节点不支持此内核。";
  if (!(agent.capabilities || []).includes(engine)) return "内核能力未启用，请联系节点所有者开启。";
  if (agent.status !== "online") return "节点离线，草稿已保留，上线后可再次提交。";
  if (agent.runtime?.[engine]?.existing_config_unsupported_reason) return agent.runtime[engine].existing_config_unsupported_reason;
  if (!agent.features?.includes("independent-egress-v1")) return "请联系节点所有者升级 Agent 后部署配置。";
  return agent.runtime?.[engine]?.installed ? "" : coreInstallBlockReason(agent, engine, can, {automatic:true});
}

export function configDeployConfirmation(agent, engine, action = "deploy") {
  const missing = !agent.runtime?.[engine]?.installed;
  if (action === "validate") return {
    title: missing ? "安装并校验" : "校验配置",
    message: `校验 ${agent.name} · ${engine} · ${agent.shared_allocation ? "当前共享实例" : "当前主实例"}：${missing ? "缺失内核时安装稳定版，已安装则复用当前版本；" : ""}仅校验已保存配置，不部署或启动实例。`,
  };
  return {
    title: missing ? "安装并部署" : "部署并重启",
    message: `部署到 ${agent.name} · ${engine} · ${agent.shared_allocation ? "当前共享实例" : "当前主实例"}：${missing ? "缺失内核时安装稳定版，已安装则复用当前版本；校验通过后部署并启动此实例。" : "校验配置并重启此实例内核。"}`,
  };
}

export async function prepareConfigDeployment(ctx, base, agent, engine) {
  const fresh = await ctx.api(`${base}/workspace`);
  if (!ctx.current()) throw new DOMException("目标已变化", "AbortError");
  if (fresh.agent?.id !== agent.id || (fresh.agent.shared_allocation?.id || "") !== (agent.shared_allocation?.id || ""))
    throw new Error("目标实例已变化，草稿已保留，请重新选择节点。" );
  Object.assign(agent, fresh.agent);
  const reason = configDeployBlockReason(agent, engine, ctx.can);
  if (reason) throw Object.assign(new Error(reason), {deployPreflight:true});
  const confirmation = configDeployConfirmation(agent, engine);
  return await ctx.confirm(confirmation.message, confirmation.title) && ctx.current();
}
