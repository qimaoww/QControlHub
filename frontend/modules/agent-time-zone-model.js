export function agentTimeZoneStatus(agent) {
  const setting = agent?.time_zone || {};
  if (setting.status === "applying") return "应用中";
  if (setting.status === "applied") return "已生效";
  if (setting.status === "failed") return setting.error || "应用失败，请重试";
  if (setting.status === "unsupported") return "请升级 Agent";
  if (agent?.status !== "online") return "离线，待生效";
  return setting.mode === "auto" && !setting.time_zone ? "待检测" : "待生效";
}

export function agentTimeZoneEffective(setting = {}) {
  if (!setting.applied_time_zone || setting.applied_time_zone === setting.time_zone) return "";
  return `当前生效：${setting.applied_time_zone}`;
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

