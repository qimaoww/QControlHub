// A running service is healthy; a running task is still in progress.
export function serviceStatusInfo(status) {
  switch (status) {
    case "active":
    case "running":
      return { label: "运行中", tone: "ok", state: "running" };
    case "inactive":
    case "stopped":
      return { label: "已停止", tone: "muted", state: "stopped" };
    case "activating":
      return { label: "启动中", tone: "warn", state: "pending" };
    case "deactivating":
      return { label: "停止中", tone: "warn", state: "pending" };
    case "pending":
      return { label: "准备中", tone: "warn", state: "pending" };
    case "failed":
      return { label: "运行失败", tone: "bad", state: "failed" };
    default:
      return { label: "状态未知", tone: "muted", state: "unknown" };
  }
}

export function serviceRuntimeStatus(runtime = {}, online = true) {
  if (!runtime.installed) {
    return {
      label: runtime.existing_config_available ? "待导入" : "未安装",
      tone: "muted",
      state: "uninstalled",
    };
  }
  return serviceStatusInfo(online ? runtime.service_status : "unknown");
}
