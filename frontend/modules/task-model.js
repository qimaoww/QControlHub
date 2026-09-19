export function coreSourceName(source) {
  if (source === "mirror") return "vernesong/mihomo 镜像（第三方）";
  if (source === "official") return "MetaCubeX/mihomo 官方";
  return "";
}

export function coreSourceLabel(engine, version, source) {
  if (engine === "mihomo" && version === "development") {
    return coreSourceName(source || "official");
  }
  return coreSourceName(source);
}

export function taskRenderSignature(items, agents) {
    const agentNames = new Map(agents.map((agent) => [agent.id, agent.name]));
    return JSON.stringify(
      items.map((item) => [
        item.id,
        item.agent_id,
        agentNames.get(item.agent_id) || "",
        item.engine,
        item.action,
        item.status,
        item.attempt,
        item.config_id,
        item.config_version,
        item.core_version,
        item.core_source,
        item.install_if_missing,
        item.created_at,
        item.started_at,
        item.finished_at,
        item.error,
        item.output,
      ]),
    );
  }

export function diagnoseTask(task) {
    if (task.status !== "failed") return null;
    const error = String(task.error || "").toLowerCase();
    if (task.action === "ip-quality")
      return { title: "IP 质量检测未完成", advice: "查看节点依赖、网络或报告格式错误；缺失结果不会记为低风险。修复后可重新检测。" };
    if (task.install_if_missing && error.includes("stable core installation failed"))
      return {title:"稳定版安装失败，未继续执行配置", advice:"检查下载、校验或服务启动错误后重试；切换版本请到节点设置。"};
    if (error.includes("rolled back"))
      return {
        title: "变更失败，已自动回滚",
        advice: "旧配置或旧二进制已经恢复；先查询服务状态后再重试。",
      };
    if (error.includes("rejected the configuration"))
      return {
        title: "配置未通过真实内核校验",
        advice: "展开节点返回结果定位字段，修正后使用当前配置重试。",
      };
    if (task.action === "install")
      return {
        title: "内核安装或切换失败",
        advice: "展开结果确认下载、校验或重启阶段。",
      };
    return {
      title: "节点操作执行失败",
      advice: "展开节点返回结果并确认节点与服务状态后再重试。",
    };
  }
