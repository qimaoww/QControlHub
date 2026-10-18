// All recipient surfaces consume the same permission-scoped server snapshot.
// The configuration selector describes saved/applied files. Runtime status and
// lifecycle actions remain in node management.
export function sharedConfigPresentation(agent, engine) {
  if (agent?.can_manage !== false) return null;
  const runtime = agent.runtime?.[engine] || {};
  const instance = runtime.instance;
  if (typeof runtime.installed !== "boolean") return { label: "配置待核实", tone: "unknown" };
  if (!runtime.installed) return { label: "未安装", tone: "uninstalled" };
  if (!instance || instance.config_uncertain) return { label: "配置待核实", tone: "unknown" };
  const saved = instance.saved_version;
  const deployed = instance.deployed_version;
  if (saved && (saved !== deployed || instance.saved_config_id !== instance.deployed_config_id))
    return { label: `v${saved} 待部署`, tone: "pending" };
  if (deployed) return { label: `已部署 v${deployed}`, tone: "running" };
  return { label: "尚未配置", tone: "uninstalled" };
}

export function sharedInstancePresentation(agent, engine) {
  const runtime = agent?.runtime?.[engine] || {};
  const instance = runtime.instance;
  if (agent?.can_manage !== false) return null;
  let label = "状态未知", tone = "muted";
  const operation = instance?.operation;
  const busy = ["pending", "running"].includes(operation?.status);
  const running = agent.status === "online" && ["active", "running", "reloading"].includes(runtime.service_status);
  if (agent.status === "offline") { label = "离线 · 状态未知"; tone = "warn"; }
  else if (agent.status !== "online") label = "状态未知";
  else if (runtime.installed === false) label = "未安装";
  else if (runtime.installed !== true || !instance || instance.config_uncertain) label = "状态未知";
  else if (!instance?.saved_version && !instance?.deployed_version) label = "已安装 · 尚未配置";
  else if (!instance?.deployed_version && !instance?.created) { label = "待部署"; tone = "warn"; }
  else if (running) { label = "正在运行"; tone = "ok"; }
  else if (runtime.service_status === "inactive") label = "已停止";
  else if (runtime.service_status === "failed") { label = "启动失败"; tone = "bad"; }
  else if (["activating", "deactivating"].includes(runtime.service_status)) { label = runtime.service_status === "activating" ? "启动中" : "停止中"; tone = "warn"; }
  if (busy) { label = `${{deploy:"部署", start:"启动", stop:"停止", restart:"重启", install:"安装"}[operation.action] || "操作"}${operation.status === "pending" ? "排队中" : "执行中"}`; tone = "warn"; }
  const deployed = instance?.deployed_version;
  const saved = instance?.saved_version;
  const drift = saved && (instance.saved_config_id !== instance.deployed_config_id || saved !== deployed);
  const version = deployed ? `${instance.config_uncertain ? "运行版本待核实 · 上次部署" : running ? "运行" : "已部署"} v${deployed}${drift ? ` · v${saved} 待部署` : ""}` : saved ? `v${saved} 已保存，尚未部署` : !instance || instance.config_uncertain ? "配置待核实" : "尚未保存配置";
  const reason = [instance?.upgrade_required ? "请联系所有者升级 Agent 后操作本人实例" : "",
    instance?.restriction, instance?.config_uncertain ? "实际配置版本尚未核实，请重新部署" : "",
    operation?.status === "failed" ? `上次${operation.action === "deploy" ? "部署" : "操作"}失败：${operation.error || "请查看日志"}` : instance?.reason].filter(Boolean).join("；");
  return { label, tone, version, reason, busy, instance };
}

export function updateSharedInstanceMarkup(root, agent, engine, format) {
  const status = sharedInstancePresentation(agent, engine);
  if (!status) return;
  const version = root.querySelector(`[data-instance-version="${engine}"]`);
  if (version) {
    version.textContent = status.version;
    version.title = status.instance?.observed_at ? `实例状态更新于 ${format(status.instance.observed_at)}` : "等待实例状态上报";
  }
  const reason = root.querySelector(`[data-instance-reason="${engine}"]`);
  if (reason) {
    reason.textContent = status.reason;
    reason.hidden = !status.reason;
  }
}

export function sharedAllocationText(agent, bytes) {
  const allocation = agent?.shared_allocation;
  if (!allocation) return "";
  const ports = allocation.ports || [];
  const range = ports.length > 3 && ports.every((port, i) => !i || port === ports[i-1]+1) ? `${ports[0]}–${ports.at(-1)}` : ports.join("、");
  return `本人端口 ${ports[0] === 0 ? "按需分配" : range || "未分配"} · 额度 ${bytes(allocation.used_bytes || 0)} / ${allocation.limit_bytes ? bytes(allocation.limit_bytes) : "不限"}`;
}
