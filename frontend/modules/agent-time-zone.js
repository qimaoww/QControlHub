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

export function updateAgentTimeZone(root, agent, { force = false } = {}) {
  const section = root?.querySelector?.(`[data-agent-timezone="${CSS.escape(String(agent?.id || ""))}"]`);
  if (!section) return;
  const setting = agent?.time_zone || {};
  const form = section.querySelector("[data-agent-timezone-form]");
  const stale = Number(setting.revision || 0) < Number(form?.dataset.timezoneRevision || 0);
  const applied = section.querySelector("[data-timezone-applied]");
  if (applied && !stale) {
    applied.textContent = agentTimeZoneEffective(setting);
    applied.hidden = !applied.textContent;
  }
  const status = section.querySelector("[data-timezone-status]");
  const input = form?.querySelector("[data-timezone-input]");
  const mode = section.querySelector("[data-timezone-mode]");
  const pristine = form && mode?.value === form.dataset.timezoneSavedMode && input?.value === form.dataset.timezoneSavedValue;
  if (!stale && input && mode && (force || (!form.dataset.busy && pristine))) {
    mode.value = setting.mode === "custom" ? "custom" : "auto";
    input.value = setting.time_zone || "";
    input.disabled = mode.value !== "custom" || mode.disabled;
    input.placeholder = mode.value === "auto" ? "等待检测" : "Asia/Shanghai";
    form.dataset.timezoneSavedMode = mode.value;
    form.dataset.timezoneSavedValue = input.value;
    form.dataset.timezoneRevision = String(setting.revision || 0);
    const submit = form.querySelector("[data-timezone-submit]");
    if (submit) submit.textContent = mode.value === "auto" && input.value ? "重新检测" : "应用";
  }
  if (!stale && status && (force || (!form?.dataset.busy && !form?.dataset.error))) {
    status.textContent = agentTimeZoneStatus(agent);
  }
  const clock = agentClockSyncPresentation(agent?.metrics?.clock_sync);
  const clockStatus = section.querySelector("[data-clock-sync-status]");
  if (clockStatus) clockStatus.textContent = clock.status;
  const clockOffset = section.querySelector("[data-clock-sync-offset]");
  if (clockOffset) clockOffset.textContent = clock.offset;
}
