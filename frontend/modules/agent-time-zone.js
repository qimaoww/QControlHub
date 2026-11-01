export function agentTimeZoneStatus(agent) {
  const setting = agent?.time_zone || {};
  if (setting.status === "applying") return "正在同步时区到 Agent 进程";
  if (setting.status === "applied") return "时区已同步到 Agent 进程";
  if (setting.status === "failed") return setting.error || "时区同步失败，请重新检测或重试";
  if (setting.status === "unsupported") return setting.error || "当前 Agent 不支持，请升级 Agent";
  return agent?.status === "online" ? "等待同步" : "节点离线，时区待生效";
}

export function agentTimeZoneEffective(setting = {}) {
  if (!setting.applied_time_zone) return "尚未确认";
  return setting.applied_time_zone === setting.time_zone ? "与当前配置一致" : setting.applied_time_zone;
}

export function agentClockSyncPresentation(clockSync) {
  if (!clockSync || clockSync.status === "unavailable") return { status: "不可用", offset: "" };
  const status = clockSync.status === "synchronized" ? "已同步" : "未同步";
  const rawOffset = clockSync.offset_milliseconds;
  const offset = Number(rawOffset);
  return {
    status,
    offset: rawOffset == null || rawOffset === "" || !Number.isFinite(offset)
      ? "偏差不可用"
      : `${offset >= 0 ? "+" : ""}${offset.toFixed(3)} ms`,
  };
}

export function updateAgentTimeZone(root, agent, { force = false } = {}) {
  const section = root?.querySelector?.(`[data-agent-timezone="${CSS.escape(String(agent?.id || ""))}"]`);
  if (!section) return;
  const setting = agent?.time_zone || {};
  const configured = section.querySelector("[data-timezone-configured]");
  if (configured) configured.textContent = setting.time_zone || "等待检测";
  const applied = section.querySelector("[data-timezone-applied]");
  if (applied) applied.textContent = agentTimeZoneEffective(setting);
  const status = section.querySelector("[data-timezone-status]");
  const form = section.querySelector("[data-agent-timezone-form]");
  if (status && (force || (!form?.dataset.busy && !form?.dataset.error))) {
    status.textContent = agentTimeZoneStatus(agent);
  }
  const clock = agentClockSyncPresentation(agent?.metrics?.clock_sync);
  const clockStatus = section.querySelector("[data-clock-sync-status]");
  if (clockStatus) clockStatus.textContent = clock.status;
  const clockOffset = section.querySelector("[data-clock-sync-offset]");
  if (clockOffset) clockOffset.textContent = clock.offset;
}
