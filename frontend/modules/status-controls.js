// Unified status model, state mapping, action eligibility, and reusable node/status controls.

export const STATUS_MODEL_CATALOG = Object.freeze({
  connection: Object.freeze({
    online: Object.freeze({
      key: "online",
      label: "在线",
      nodeLabel: "节点在线",
      tone: "ok",
      dotStyle: "solid",
      source: "agent.status",
      trigger: "Agent WSS 会话活跃且心跳在有效窗口内",
      actions: Object.freeze(["status", "start", "restart", "stop", "install", "uninstall", "deploy", "validate", "upgrade-agent"]),
      recovery: "",
    }),
    offline: Object.freeze({
      key: "offline",
      label: "离线",
      nodeLabel: "节点离线",
      tone: "bad",
      dotStyle: "hollow",
      source: "agent.status",
      trigger: "Agent WSS 连接断开或心跳超时",
      actions: Object.freeze(["view-cached", "edit-draft", "toggle-capability"]),
      recovery: "检查节点 qagent 服务与网络连通性；恢复在线后自动同步",
    }),
    unknown: Object.freeze({
      key: "unknown",
      label: "未知",
      nodeLabel: "节点未知",
      tone: "muted",
      dotStyle: "dashed",
      source: "agent.status",
      trigger: "尚未收到首次心跳或节点状态不可用",
      actions: Object.freeze(["view-command"]),
      recovery: "确认部署命令已在目标主机执行并等待首次心跳",
    }),
  }),
  capability: Object.freeze({
    enabled: Object.freeze({
      key: "enabled",
      label: "已开启",
      defaultLabel: "默认开启",
      tone: "ok",
      source: "agent.capabilities / settings.default_agent_engines",
      trigger: "内核已启用",
      actions: Object.freeze(["disable"]),
      recovery: "",
    }),
    disabled: Object.freeze({
      key: "disabled",
      label: "已关闭",
      defaultLabel: "默认关闭",
      tone: "muted",
      source: "agent.capabilities / settings.default_agent_engines",
      trigger: "内核已关闭",
      actions: Object.freeze(["enable"]),
      recovery: "开启后可管理对应内核",
    }),
    unsupported: Object.freeze({
      key: "unsupported",
      label: "不支持",
      defaultLabel: "不支持",
      tone: "muted",
      source: "agent.supported_capabilities",
      trigger: "Agent 未声明支持该内核",
      actions: Object.freeze([]),
      recovery: "请调整 Agent 安装配置后重新注册",
    }),
    starting: Object.freeze({
      key: "starting",
      label: "等待启动",
      defaultLabel: "等待启动",
      tone: "warn",
      source: "agent.capability_transitions[engine]",
      trigger: "开启已安装内核，等待启动任务完成确认",
      actions: Object.freeze([]),
      recovery: "启动成功后开启；离线节点上线后执行",
    }),
    stopping: Object.freeze({
      key: "stopping",
      label: "等待停止",
      defaultLabel: "等待停止",
      tone: "warn",
      source: "agent.capability_transitions[engine]",
      trigger: "关闭已安装内核，等待停止任务完成确认",
      actions: Object.freeze([]),
      recovery: "停止成功后关闭；离线节点上线后执行",
    }),
    failed: Object.freeze({
      key: "failed",
      label: "未变更",
      defaultLabel: "未变更",
      tone: "warn",
      source: "agent.capability_transitions[engine]",
      trigger: "切换关联的启停任务失败或已取消",
      actions: Object.freeze(["retry", "view-tasks"]),
      recovery: "上次启停失败或取消；可重试，详情见任务页",
    }),
  }),
  kernel: Object.freeze({
    uninstalled: Object.freeze({
      key: "uninstalled",
      label: "未安装",
      liveLabel: "未安装",
      tone: "muted",
      liveTone: "uninstalled",
      source: "runtime.installed=false",
      trigger: "QAgent 专用目录尚未安装该内核",
      actions: Object.freeze(["install"]),
      recovery: "打开安装面板选择稳定版、开发版或指定版本安装",
    }),
    active: Object.freeze({
      key: "active",
      label: "运行中",
      liveLabel: "正在运行",
      tone: "ok",
      liveTone: "running",
      source: "runtime.service_status=active|running",
      trigger: "内核服务进程正在运行",
      actions: Object.freeze(["status", "restart", "stop", "version", "uninstall"]),
      recovery: "",
    }),
    inactive: Object.freeze({
      key: "inactive",
      label: "已停止",
      liveLabel: "已停止",
      tone: "bad",
      liveTone: "stopped",
      source: "runtime.service_status=inactive|stopped",
      trigger: "内核服务处于停止状态",
      actions: Object.freeze(["status", "start", "version", "uninstall"]),
      recovery: "点击启动服务恢复运行",
    }),
    activating: Object.freeze({
      key: "activating",
      label: "启动中",
      liveLabel: "启动中",
      tone: "warn",
      liveTone: "pending",
      source: "runtime.service_status=activating",
      trigger: "内核服务正在启动",
      actions: Object.freeze(["status", "stop"]),
      recovery: "等待服务启动完成并上报状态",
    }),
    deactivating: Object.freeze({
      key: "deactivating",
      label: "停止中",
      liveLabel: "停止中",
      tone: "warn",
      liveTone: "pending",
      source: "runtime.service_status=deactivating",
      trigger: "内核服务正在停止",
      actions: Object.freeze(["status"]),
      recovery: "等待服务停止完成并上报状态",
    }),
    failed: Object.freeze({
      key: "failed",
      label: "失败",
      liveLabel: "运行失败",
      tone: "bad",
      liveTone: "failed",
      source: "runtime.service_status=failed",
      trigger: "内核服务启动失败或异常退出",
      actions: Object.freeze(["status", "start", "restart", "version", "uninstall"]),
      recovery: "检查配置与内核日志后重新启动或部署",
    }),
    unknown: Object.freeze({
      key: "unknown",
      label: "未知",
      liveLabel: "状态未知",
      tone: "muted",
      liveTone: "unknown",
      source: "runtime.service_status=unknown|empty",
      trigger: "节点离线或尚未上报内核服务状态",
      actions: Object.freeze(["status"]),
      recovery: "节点在线时点击查询状态或等待心跳刷新",
    }),
    existing_pending: Object.freeze({
      key: "existing_pending",
      label: "待导入",
      chipLabel: "可导入",
      tone: "warn",
      liveTone: "uninstalled",
      source: "runtime.existing_config_available=true",
      trigger: "检测到尚未被 QAgent 接管的现有系统服务",
      actions: Object.freeze(["import-existing"]),
      recovery: "前往配置页手动导入并迁移现有服务",
    }),
    existing_blocked: Object.freeze({
      key: "existing_blocked",
      label: "检测到但不可迁移",
      chipLabel: "不可导入",
      tone: "warn",
      liveTone: "failed",
      source: "runtime.existing_config_unsupported_reason",
      trigger: "检测到现有系统服务但布局或配置不支持自动迁移",
      actions: Object.freeze(["view-reason"]),
      recovery: "按提示调整现有服务布局并重启 Agent 重新发现",
    }),
    capability_disabled: Object.freeze({
      key: "capability_disabled",
      label: "已关闭",
      liveLabel: "已关闭",
      tone: "muted",
      liveTone: "stopped",
      source: "agent.capabilities 不包含该内核",
      trigger: "节点已关闭该内核",
      actions: Object.freeze(["uninstall", "enable-capability"]),
      recovery: "在 Agent 标签页开启后恢复管理",
    }),
    uninstalling: Object.freeze({
      key: "uninstalling",
      label: "卸载中",
      liveLabel: "卸载中",
      tone: "warn",
      liveTone: "pending",
      source: "state.data.coreUninstallTasks",
      trigger: "已提交卸载任务，等待任务结果与 Agent 上报确认",
      actions: Object.freeze([]),
      recovery: "等待卸载完成并自动更新状态",
    }),
  }),
  task: Object.freeze({
    submitting: Object.freeze({
      key: "submitting",
      label: "提交中",
      tone: "warn",
      source: "client request in flight",
      trigger: "操作请求正在提交至控制面",
      actions: Object.freeze([]),
      recovery: "等待请求完成",
    }),
    pending: Object.freeze({
      key: "pending",
      label: "准备中",
      queueLabel: "正在排队",
      tone: "warn",
      source: "task.status=pending",
      trigger: "任务已入队，等待 Agent 认领执行",
      actions: Object.freeze(["cancel"]),
      recovery: "离线节点上线后自动认领，或取消排队任务",
    }),
    running: Object.freeze({
      key: "running",
      label: "执行中",
      queueLabel: "正在执行",
      tone: "warn",
      source: "task.status=running",
      trigger: "Agent 已认领并正在执行任务",
      actions: Object.freeze([]),
      recovery: "等待 Agent 完成执行并上报结果",
    }),
    awaiting_confirmation: Object.freeze({
      key: "awaiting_confirmation",
      label: "结果待确认",
      tone: "warn",
      source: "task terminal + agent runtime sync",
      trigger: "任务已提交或完成，等待 Agent 最新状态上报确认",
      actions: Object.freeze(["refresh"]),
      recovery: "等待下一轮心跳或手动刷新状态",
    }),
    succeeded: Object.freeze({
      key: "succeeded",
      label: "成功",
      tone: "ok",
      source: "task.status=succeeded",
      trigger: "任务执行成功并通过校验",
      actions: Object.freeze(["view-output"]),
      recovery: "",
    }),
    failed: Object.freeze({
      key: "failed",
      label: "失败",
      tone: "bad",
      source: "task.status=failed",
      trigger: "任务执行失败或触发自动回滚",
      actions: Object.freeze(["retry", "view-error"]),
      recovery: "查看诊断原因与节点输出后重试",
    }),
    canceled: Object.freeze({
      key: "canceled",
      label: "已取消",
      tone: "muted",
      source: "task.status=canceled",
      trigger: "排队中的任务被取消",
      actions: Object.freeze(["retry"]),
      recovery: "可按需重新提交任务",
    }),
  }),
  freshness: Object.freeze({
    loading: Object.freeze({
      key: "loading",
      label: "正在读取",
      metricLabel: "等待采集",
      tone: "muted",
      source: "initial load",
      trigger: "首次加载或尚未收到采样数据",
      actions: Object.freeze([]),
      recovery: "等待首次采样或读取返回",
    }),
    refreshing: Object.freeze({
      key: "refreshing",
      label: "正在刷新…",
      tone: "muted",
      source: "active poll / manual refresh",
      trigger: "后台或手动刷新进行中",
      actions: Object.freeze([]),
      recovery: "",
    }),
    fresh: Object.freeze({
      key: "fresh",
      label: "刚刚更新",
      tone: "ok",
      source: "latest sample",
      trigger: "数据在有效期内且读取成功",
      actions: Object.freeze(["refresh"]),
      recovery: "",
    }),
    cached: Object.freeze({
      key: "cached",
      label: "已显示缓存，正在更新…",
      tone: "muted",
      source: "session/snapshot cache",
      trigger: "优先展示已缓存数据并后台更新",
      actions: Object.freeze(["refresh"]),
      recovery: "手动刷新可跳过缓存",
    }),
    stale: Object.freeze({
      key: "stale",
      label: "数据已过期",
      offlineLabel: "离线保留",
      tone: "warn",
      source: "collected_at expired / agent offline",
      trigger: "节点离线或采样时间超过新鲜度窗口，保留最后已知有效数据",
      actions: Object.freeze(["refresh"]),
      recovery: "节点恢复在线或刷新成功后更新",
    }),
    failed: Object.freeze({
      key: "failed",
      label: "刷新失败，保留上次数据",
      initialFailedLabel: "暂时无法读取",
      tone: "warn",
      source: "API error",
      trigger: "读取失败，保留已有有效数据或提示错误",
      actions: Object.freeze(["retry"]),
      recovery: "点击刷新重试或等待自动重试",
    }),
    empty: Object.freeze({
      key: "empty",
      label: "暂无数据",
      tone: "muted",
      source: "empty collection",
      trigger: "读取成功但当前范围无记录",
      actions: Object.freeze(["create", "reset-filter"]),
      recovery: "",
    }),
    forbidden: Object.freeze({
      key: "forbidden",
      label: "无权限",
      tone: "muted",
      source: "permission check / 403",
      trigger: "当前账号无对应读取或操作权限",
      actions: Object.freeze([]),
      recovery: "请联系管理员授予对应权限",
    }),
  }),
  feature: Object.freeze({
    config_deployment: Object.freeze({
      saved: "已保存",
      deployed: "已部署",
      drift: "待部署",
      managed: "QAgent 托管",
      import_snapshot: "系统服务 · 只读快照",
      personal: "个人配置",
    }),
    core_log_source: Object.freeze({
      active: "采集正常",
      waiting: "等待日志来源",
      failed: "日志采集失败",
      unreported: "日志状态尚未上报",
      unsupported: "此节点不支持集中日志",
      status_unavailable: "日志状态能力不可用",
      uninstalled: "内核尚未安装",
      offline: "节点离线",
      unverified: "无法核验采集状态",
    }),
    ip_quality: Object.freeze({
      untested: "未检测",
      loading: "读取中",
      pending: "等待执行",
      running: "正在检测",
      succeeded: "已完成",
      failed: "检测失败",
      canceled: "已取消",
      read_failed: "读取失败",
    }),
    substore_sync: Object.freeze({
      unconfigured: "未配置",
      configured: "已配置",
      no_target: "尚未创建同步组",
      waiting_first: "等待首次同步",
      synced: "已同步",
      sync_failed: "上次同步失败",
      source_unavailable: "源配置已变更或不可用",
    }),
    traffic_policy: Object.freeze({
      metering: "持续统计",
      active: "已设置配额",
      blocked: "已封禁",
      waiting: "等待同步",
      error: "监控异常",
    }),
    system_bbr: Object.freeze({
      enabled: "BBR 已启用",
      other: "使用其他算法",
      waiting: "等待上报",
      waiting_qdisc: "等待队列参数",
      error: "参数读取异常",
      offline: "节点离线",
      legacy: "需升级 Agent",
    }),
  }),
});

export function normalizeConnectionState(status) {
  return status === "online" || status === "offline" ? status : "unknown";
}

export function connectionStateLabel(status, { nodeLabel = false } = {}) {
  const state = normalizeConnectionState(status);
  const entry = STATUS_MODEL_CATALOG.connection[state];
  return nodeLabel ? entry.nodeLabel : entry.label;
}

export function describeConnectionState(status, lastSeen, { ago, date, nodeLabel = false } = {}) {
  const state = normalizeConnectionState(status);
  const entry = STATUS_MODEL_CATALOG.connection[state];
  const hasHeartbeat = Boolean(lastSeen);
  const heartbeatText = hasHeartbeat
    ? typeof ago === "function" ? `心跳 ${ago(lastSeen)}` : `心跳 ${lastSeen}`
    : "尚未心跳";
  const heartbeatTitle = hasHeartbeat
    ? `上次心跳：${typeof date === "function" ? date(lastSeen) : lastSeen}${state === "offline" ? "（节点当前离线）" : ""}`
    : "尚未收到心跳";
  return {
    state,
    label: nodeLabel ? entry.nodeLabel : entry.label,
    tone: entry.tone,
    dotStyle: entry.dotStyle,
    offline: state !== "online",
    hasHeartbeat,
    heartbeatText,
    heartbeatTitle,
    recovery: entry.recovery,
  };
}

export function describeEngineCapability(engine, {
  selected = [],
  supported = [],
  writable = true,
  node = false,
  transitions = {},
  engineLabel = engine,
} = {}) {
  const available = supported.includes(engine);
  const checked = selected.includes(engine);
  const transition = transitions?.[engine];
  const pending = ["pending", "running"].includes(transition?.status);
  const failed = ["failed", "canceled"].includes(transition?.status);
  const state = pending
    ? transition.enabled ? "starting" : "stopping"
    : failed
      ? "failed"
      : !available
        ? "unsupported"
        : checked
          ? "enabled"
          : "disabled";
  const hint = pending
    ? `${transition.enabled ? "启动成功后开启" : "停止成功后关闭"}；离线节点上线后执行`
    : failed
      ? "上次启停失败或取消；可重试，详情见任务页"
      : !available
        ? "请调整 Agent 安装配置后重新注册"
        : "";
  const statusText = pending
    ? transition.enabled ? "等待启动" : "等待停止"
    : failed
      ? "未变更"
      : !available
        ? "不支持"
        : "";
  const disabled = Boolean(pending || !writable || (!available && !checked));
  const disabledReason = pending
    ? `${engineLabel} 正在${transition.enabled ? "等待启动" : "等待停止"}，任务确认后可再次切换`
    : !writable
      ? "当前账号无权修改内核开关"
      : !available && !checked
        ? `当前 Agent 未声明支持 ${engineLabel}，请调整安装配置后重新注册`
        : "";
  return {
    state,
    available,
    checked,
    pending,
    failed,
    hint,
    statusText,
    disabled,
    disabledReason,
    enabledLabel: node ? "已开启" : "默认开启",
    disabledLabel: node ? "已关闭" : "默认关闭",
  };
}

export function describeShareEngineRuntime(engine, runtimeInfo = null, { agent = null } = {}) {
  const hostRuntime = agent?.runtime?.[engine];
  if (!runtimeInfo && !hostRuntime) return null;
  const info = runtimeInfo || {};
  const installed = info.installed !== undefined
    ? Boolean(info.installed)
    : hostRuntime?.installed !== undefined
      ? Boolean(hostRuntime.installed)
      : true;
  const rawStatus = String(info.service_status || "");
  const configVersion = Number(info.config_version || 0);
  const deployed = Boolean(info.deployed || configVersion > 0);
  const uncertain = Boolean(info.uncertain || rawStatus === "unknown");
  const running = Boolean(info.running || ["active", "running"].includes(rawStatus));
  const state = !installed
    ? "uninstalled"
    : uncertain
      ? "unknown"
      : running
        ? "active"
        : rawStatus === "failed"
          ? "failed"
          : deployed
            ? "inactive"
            : "undeployed";
  const statusLabel = state === "uninstalled"
    ? "未安装"
    : state === "active"
      ? "运行中"
      : state === "unknown"
        ? "未知"
        : state === "failed"
          ? "失败"
          : state === "inactive"
            ? "已停止"
            : "未部署";
  const tone = state === "active"
    ? "ok"
    : state === "inactive" || state === "failed"
      ? "bad"
      : state === "unknown"
        ? "warn"
        : "muted";
  const username = String(info.username || info.share_id || "");
  const displayName = String(info.display_name || "");
  const userLabel = displayName && displayName !== username ? `${displayName} · ${username}` : username;
  return {
    engine,
    shareId: String(info.share_id || ""),
    userId: String(info.user_id || ""),
    username,
    displayName,
    userLabel,
    installed,
    version: String(info.version || hostRuntime?.version || ""),
    deployed,
    configVersion,
    running,
    uncertain,
    state,
    statusLabel,
    tone,
  };
}

export function describeSharedCoreInstances(engine, runtime = {}) {
  const rawList = Array.isArray(runtime?.shared_instances) ? runtime.shared_instances : [];
  const items = rawList
    .map((item) => describeShareEngineRuntime(engine, {
      ...item,
      installed: runtime?.installed !== undefined ? Boolean(runtime.installed) : item?.installed,
      version: item?.version || runtime?.version || "",
    }))
    .filter(Boolean);
  const totalCount = items.length;
  const runningCount = items.filter((item) => item.state === "active").length;
  const deployedCount = items.filter((item) => item.deployed).length;
  const stoppedCount = items.filter((item) => item.state === "inactive" || item.state === "failed").length;
  const undeployedCount = items.filter((item) => item.state === "undeployed").length;
  const uncertainCount = items.filter((item) => item.state === "unknown").length;
  const summaryLabel = totalCount > 0 ? `多用户 ${runningCount}/${totalCount} 运行` : "";
  const chipLabel = totalCount > 0 ? `共享 ${runningCount}/${totalCount}` : "";
  const detailParts = [];
  if (runningCount > 0) detailParts.push(`${runningCount} 运行中`);
  if (stoppedCount > 0) detailParts.push(`${stoppedCount} 已停止`);
  if (undeployedCount > 0) detailParts.push(`${undeployedCount} 未部署`);
  if (uncertainCount > 0) detailParts.push(`${uncertainCount} 待确认`);
  const summaryDetail = detailParts.join(" · ") || (totalCount > 0 ? `${runningCount}/${totalCount} 运行中` : "");
  const summaryTone = runningCount > 0
    ? "ok"
    : stoppedCount > 0 || uncertainCount > 0
      ? "warn"
      : "muted";
  return {
    hasShared: totalCount > 0,
    totalCount,
    runningCount,
    deployedCount,
    stoppedCount,
    undeployedCount,
    uncertainCount,
    summaryLabel,
    summaryDetail,
    chipLabel,
    summaryTone,
    items,
  };
}

export function describeCoreRuntime(engine, runtime = {}, {
  capabilityEnabled = true,
  online = true,
  isShared = false,
  deployed = null,
  uninstallPending = false,
  canUninstall = true,
  conciseVersion = (_e, v) => v || "版本未知",
  serviceStatusName = (s) => s || "未知",
  statusTone = () => "muted",
} = {}) {
  const installed = Boolean(runtime?.installed);
  const existingPending = Boolean(runtime?.existing_config_available);
  const existingUnsupportedReason = String(runtime?.existing_config_unsupported_reason || "");
  const existingBlocked = Boolean(existingUnsupportedReason);
  const rawStatus = String(runtime?.service_status || "");

  const installState = installed ? "installed" : "uninstalled";
  const existingState = existingBlocked ? "blocked" : existingPending ? "pending" : "none";
  const runtimeState = !installed
    ? "uninstalled"
    : ["active", "running"].includes(rawStatus)
      ? "active"
      : ["inactive", "stopped"].includes(rawStatus)
        ? "inactive"
        : rawStatus === "activating"
          ? "activating"
          : rawStatus === "deactivating"
            ? "deactivating"
            : rawStatus === "failed"
              ? "failed"
              : "unknown";

  const userConfigVersion = Number(deployed?.config_version || runtime?.config_version || 0);
  const explicitUndeployed = isShared && runtime?.deployed === false && !userConfigVersion && runtimeState === "inactive";
  const userDeployed = Boolean(deployed?.config_id || runtime?.deployed || userConfigVersion > 0 || (isShared && !explicitUndeployed && runtimeState === "active"));

  const serviceState = existingBlocked
    ? "检测到但不可迁移"
    : !capabilityEnabled
      ? "已关闭"
      : !installed
        ? "未安装"
        : explicitUndeployed
          ? "未部署"
          : serviceStatusName(rawStatus);

  const normalizedToneStatus =
    rawStatus === "running" && statusTone("active") === "ok"
      ? "active"
      : rawStatus === "stopped" && statusTone("inactive") === "bad"
        ? "inactive"
        : rawStatus;

  const serviceTone = existingBlocked
    ? "warn"
    : !capabilityEnabled || !installed || explicitUndeployed
      ? "muted"
      : statusTone(normalizedToneStatus);

  const actionHint = existingBlocked
    ? "现有服务不可自动迁移"
    : uninstallPending
      ? "正在卸载内核…"
      : !capabilityEnabled
        ? "已关闭 · 仅可卸载"
        : !online
          ? "节点当前离线 · 上线后可操作"
          : runtimeState === "activating"
            ? "服务启动中 · 请稍候"
            : runtimeState === "deactivating"
              ? "服务停止中 · 请稍候"
              : "";

  let liveLabel = "状态未知";
  let liveTone = "unknown";
  if (!installed) {
    liveLabel = existingPending ? "待导入" : "未安装";
    liveTone = "uninstalled";
  } else if (!online) {
    liveLabel = "状态未知";
    liveTone = "unknown";
  } else {
    switch (rawStatus) {
      case "active":
      case "running":
        liveLabel = "正在运行";
        liveTone = "running";
        break;
      case "inactive":
      case "stopped":
        liveLabel = explicitUndeployed ? "未部署" : "已停止";
        liveTone = "stopped";
        break;
      case "activating":
        liveLabel = "启动中";
        liveTone = "pending";
        break;
      case "deactivating":
        liveLabel = "停止中";
        liveTone = "pending";
        break;
      case "failed":
        liveLabel = "运行失败";
        liveTone = "failed";
        break;
      default:
        liveLabel = "状态未知";
        liveTone = "unknown";
        break;
    }
  }

  const versionLabel = installed ? conciseVersion(engine, runtime?.version) : "尚未安装";
  const versionTitle = installed ? (runtime?.version || "版本未知") : "尚未安装";
  const shared = describeSharedCoreInstances(engine, runtime);
  const instanceScope = String(runtime?.instance_scope || (isShared ? "shared-instance" : "host"));
  const hostInstallLabel = installed ? "宿主已安装" : "宿主未安装";
  const hostInstallTone = installed ? "ok" : "muted";
  const userInstanceLabel = !installed
    ? "未安装"
    : runtimeState === "active"
      ? "运行中"
      : runtimeState === "unknown"
        ? "状态未知"
        : runtimeState === "failed"
          ? "运行失败"
          : userDeployed
            ? "已停止"
            : "未部署";
  const userInstanceTone = !installed
    ? "muted"
    : runtimeState === "active"
      ? "ok"
      : runtimeState === "unknown"
        ? "warn"
        : runtimeState === "failed" || userDeployed
          ? "bad"
          : "muted";

  return {
    installed,
    installState,
    existingPending,
    existingBlocked,
    existingUnsupportedReason,
    existingState,
    runtimeState,
    uninstallPending: Boolean(uninstallPending),
    canUninstall: Boolean(canUninstall),
    serviceState,
    serviceTone,
    actionHint,
    liveLabel,
    liveTone,
    versionLabel,
    versionTitle,
    shared,
    instanceScope,
    hostInstallLabel,
    hostInstallTone,
    userConfigVersion,
    userDeployed,
    userInstanceLabel,
    userInstanceTone,
  };
}

export function describeServiceAction(action, {
  online = false,
  installed = false,
  capabilityEnabled = true,
  serviceStatus = "",
  agent = null,
  can = () => false,
  existingUnsupportedReason = "",
  canUninstall = true,
  uninstallPending = false,
  serviceActionDisabled = null,
  actionName = (a) => a,
  engineName = (e) => e,
  engine = "",
} = {}) {
  const baseLabel = actionName(action);
  const targetLabel = engine ? `${baseLabel} ${engineName(engine)}` : baseLabel;
  const isShared = agent?.can_manage === false;

  if (isShared && !["deploy", "validate", "status"].includes(action)) {
    return {
      disabled: true,
      reason: "共享节点的主机操作仅限所有者",
      title: "共享节点的主机操作仅限所有者",
      ariaLabel: targetLabel,
    };
  }
  if (existingUnsupportedReason) {
    return {
      disabled: true,
      reason: "检测到现有服务但不可自动迁移",
      title: `不可操作：${existingUnsupportedReason}`,
      ariaLabel: targetLabel,
    };
  }
  if (action === "uninstall") {
    if (uninstallPending) {
      return {
        disabled: true,
        reason: "卸载任务执行中，请等待状态刷新",
        title: "卸载任务执行中，请等待状态刷新",
        ariaLabel: `卸载 ${engineName(engine)} 内核`,
      };
    }
    if (!canUninstall) {
      return {
        disabled: true,
        reason: "请先升级 Agent 后卸载内核",
        title: "请先升级 Agent 后卸载内核",
        ariaLabel: `卸载 ${engineName(engine)} 内核`,
      };
    }
  }
  if (!capabilityEnabled && action !== "uninstall") {
    return {
      disabled: true,
      reason: "内核已关闭，请先在 Agent 页开启",
      title: `${baseLabel}不可用：内核已关闭`,
      ariaLabel: targetLabel,
    };
  }
  if (!online) {
    return {
      disabled: true,
      reason: "节点当前离线，上线后可执行此操作",
      title: action === "uninstall" ? `卸载 ${engineName(engine)} 内核（节点离线）` : `${baseLabel}（节点当前离线）`,
      ariaLabel: targetLabel,
    };
  }
  if (!installed && action !== "install") {
    return {
      disabled: true,
      reason: "尚未安装此内核",
      title: `${baseLabel}（尚未安装此内核）`,
      ariaLabel: targetLabel,
    };
  }
  if (!can("tasks.execute") || (action !== "status" && !can("host.manage", agent))) {
    return {
      disabled: true,
      reason: "当前账号缺少执行权限",
      title: action === "uninstall" ? `卸载 ${engineName(engine)} 内核` : `${baseLabel}（无操作权限）`,
      ariaLabel: targetLabel,
    };
  }
  if (action === "start" && ["active", "activating"].includes(serviceStatus)) {
    const why = serviceStatus === "activating" ? "服务正在启动中" : "服务已在运行中";
    return { disabled: true, reason: why, title: `${baseLabel}（${why}）`, ariaLabel: targetLabel };
  }
  if (action === "stop" && ["inactive", "deactivating"].includes(serviceStatus)) {
    const why = serviceStatus === "deactivating" ? "服务正在停止中" : "服务已停止";
    return { disabled: true, reason: why, title: `${baseLabel}（${why}）`, ariaLabel: targetLabel };
  }
  if (action === "restart" && ["inactive", "activating", "deactivating"].includes(serviceStatus)) {
    const why = serviceStatus === "activating"
      ? "服务正在启动中"
      : serviceStatus === "deactivating"
        ? "服务正在停止中"
        : "服务已停止，请直接启动";
    return { disabled: true, reason: why, title: `${baseLabel}（${why}）`, ariaLabel: targetLabel };
  }
  const guardDisabled = typeof serviceActionDisabled === "function"
    ? Boolean(serviceActionDisabled(action, online, installed, serviceStatus, agent))
    : false;
  if (guardDisabled) {
    return {
      disabled: true,
      reason: "当前状态不可执行此操作",
      title: baseLabel,
      ariaLabel: targetLabel,
    };
  }
  return {
    disabled: false,
    reason: "",
    title: action === "uninstall" ? `卸载 ${engineName(engine)} 内核` : baseLabel,
    ariaLabel: action === "uninstall" ? `卸载 ${engineName(engine)} 内核` : targetLabel,
  };
}

export function resolveRetainedNodeMetrics(agent = {}, lastKnownMap = null) {
  const incoming = agent?.metrics || {};
  if (incoming.collected_at) {
    if (lastKnownMap && agent?.id) {
      lastKnownMap[agent.id] = { ...incoming };
    }
    return { metrics: incoming, retained: false };
  }
  const cached = lastKnownMap && agent?.id ? lastKnownMap[agent.id] : null;
  if (cached?.collected_at) {
    return { metrics: cached, retained: true };
  }
  return { metrics: incoming, retained: false };
}

export function describeNodeMetrics(agent = {}, metrics = {}, {
  bytes = (v) => `${Number(v || 0)} B`,
  rate = (v) => `${Number(v || 0)} B/s`,
  percent = () => 0,
  ago = String,
  date = String,
  retained = false,
} = {}) {
  const hasSample = Boolean(metrics?.collected_at);
  const online = agent?.status === "online";
  const fallbackLabel = hasSample ? "不可用" : "等待采集";
  const levelFor = (available, val) =>
    !available ? "idle" : Number(val) >= 90 ? "danger" : Number(val) >= 75 ? "warn" : "normal";

  const cpuAvailable = Boolean(metrics?.cpu_available);
  const cpuValue = cpuAvailable ? Number(metrics?.cpu_percent || 0) : 0;
  const cpuText = cpuAvailable ? `${cpuValue.toFixed(1)}%` : fallbackLabel;
  const cpuLevel = levelFor(cpuAvailable, cpuValue);

  const memoryAvailable = Boolean(metrics?.memory_available);
  const memoryValue = memoryAvailable ? percent(metrics.memory_used_bytes, metrics.memory_total_bytes) : 0;
  const memoryText = memoryAvailable
    ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}`
    : fallbackLabel;
  const memoryLevel = levelFor(memoryAvailable, memoryValue);

  const diskAvailable = Boolean(metrics?.disk_available);
  const diskValue = diskAvailable ? percent(metrics.disk_used_bytes, metrics.disk_total_bytes) : 0;
  const diskText = diskAvailable
    ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}`
    : fallbackLabel;
  const diskLevel = levelFor(diskAvailable, diskValue);

  const networkAvailable = Boolean(metrics?.network_available);
  const downloadRateText = networkAvailable ? rate(metrics.network_rx_bps) : fallbackLabel;
  const uploadRateText = networkAvailable ? rate(metrics.network_tx_bps) : fallbackLabel;
  const downloadTotalText = networkAvailable ? bytes(metrics.network_rx_bytes) : "—";
  const uploadTotalText = networkAvailable ? bytes(metrics.network_tx_bytes) : "—";

  const freshnessState = !hasSample
    ? "waiting"
    : !online || retained
      ? "retained"
      : "fresh";
  const stampText = hasSample ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据";
  const stampTitle = hasSample
    ? `${!online || retained ? "节点离线 · 保留最后已知有效采样：" : "最近采集时间："}${date(metrics.collected_at)}`
    : !online
      ? "节点当前离线，尚未采集到资源数据"
      : "等待节点上报首次资源数据";

  return {
    hasSample,
    online,
    retained: Boolean(retained || (!online && hasSample)),
    freshnessState,
    fallbackLabel,
    cpu: { available: cpuAvailable, value: cpuValue, text: cpuText, level: cpuLevel },
    memory: { available: memoryAvailable, value: memoryValue, text: memoryText, level: memoryLevel },
    disk: { available: diskAvailable, value: diskValue, text: diskText, level: diskLevel },
    network: {
      available: networkAvailable,
      downloadRateText,
      uploadRateText,
      downloadTotalText,
      uploadTotalText,
    },
    stampText,
    stampTitle,
  };
}
