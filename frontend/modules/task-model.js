export function coreSourceName(source) {
  if (source === "mirror") return "vernesong/mihomo 镜像（第三方）";
  if (source === "official") return "MetaCubeX/mihomo 官方";
  return "";
}

export function taskTiming(task, now = Date.now()) {
  if (task.status === "pending") return "准备执行";
  const started = Date.parse(task.started_at), finished = Date.parse(task.finished_at);
  const valid = value => Number.isFinite(value) && value > 0;
  const duration = end => {
    const seconds = Math.max(0, Math.round((end - started) / 1000));
    return seconds < 60 ? `${seconds}\u00a0秒` : `${Math.floor(seconds / 60)}\u00a0分 ${seconds % 60}\u00a0秒`;
  };
  if (task.status === "running") return valid(started) ? `已运行 ${duration(now)}` : "正在启动执行";
  if (valid(started) && valid(finished)) return `执行 ${duration(finished)}`;
  return valid(finished) && !task.started_at ? "未开始执行" : "耗时未知";
}

export function coreSourceLabel(engine, version, source) {
  if (engine === "mihomo" && version === "development") {
    return coreSourceName(source || "official");
  }
  return coreSourceName(source);
}

export function taskRetryState(task) {
  const missingConfigVersion = Boolean(task.config_id) && !(Number.isInteger(task.config_version) && task.config_version > 0);
  const finished = ["failed", "canceled"].includes(task.status);
  let diagnostic = diagnoseTask(task);
  if (missingConfigVersion && finished) {
    const advice = "历史任务缺少配置历史关联，请核对配置后，选择编辑后重试或重新部署。";
    diagnostic = diagnostic
      ? { ...diagnostic, advice: `${diagnostic.advice} ${advice}` }
      : { title: "配置历史关联缺失", advice };
  }
  return {
    missingConfigVersion,
    canRetry: finished && !missingConfigVersion,
    canEditRetry: finished && Boolean(task.config_id) && ["validate", "deploy"].includes(task.action),
    diagnostic,
  };
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
    if (error.includes("[阶段:") || error.includes("托管 tls") || error.includes("caddy")) {
      const raw = String(task.error || "");
      const phase = raw.match(/\[阶段:([^\]]+)\]/)?.[1] || "TLS 与协议就绪检查";
      const reason = raw.match(/\[原因:([^\]]+)\]\s*([^；\n]+)/);
      const rollbackFailed = /rollback failed|反代恢复失败|回滚失败/i.test(raw);
      const restored = /已恢复原反代状态|previous configuration was restored|newly created configuration was removed/i.test(raw);
      const advice = phase === "配置生成及校验" ? "展开节点结果定位配置字段，可使用“编辑后重试”修正后提交。" : phase === "服务应用" ? "展开节点结果检查服务启动错误及最终状态，修复后重试。" : "检查域名 A/AAAA、节点到 ACME 的网络、公网 TCP 80 和受管目录权限；可在高级设置选择已有证书或外部反代。";
      return {title:`部署失败 · ${phase}`, advice:reason ? `${reason[1]}：${reason[2]}` : advice,
        rollback:rollbackFailed ? "回滚失败：需检查节点最终服务与路由状态" : restored ? "已恢复此前反代路由；业务配置和服务状态见节点结果" : "当前配置未提交；请查看节点结果确认最终服务状态"};
    }
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
        advice: "展开节点返回结果定位字段，可选择“编辑后重试”修正配置并重新提交。",
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
