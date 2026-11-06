import { agentTimeZoneStatus, agentTimeZoneEffective, agentClockSyncPresentation } from "./agent-time-zone-model.js";
export { agentTimeZoneStatus, agentTimeZoneEffective, agentClockSyncPresentation } from "./agent-time-zone-model.js";

const timeZoneForms = new WeakMap();

// DOM reconciliation may replace attributes while retaining an edited form.
// Keep its original compare-and-swap version and in-flight write together.
export function agentTimeZoneFormState(form) {
  if (!form) return null;
  if (!timeZoneForms.has(form)) timeZoneForms.set(form, {
    mode: form.dataset.timezoneSavedMode,
    value: form.dataset.timezoneSavedValue,
    revision: Number(form.dataset.timezoneRevision) || 0,
    busy: false, error: "", pending: null,
  });
  const state = timeZoneForms.get(form);
  form.dataset.timezoneSavedMode = state.mode;
  form.dataset.timezoneSavedValue = state.value;
  form.dataset.timezoneRevision = String(state.revision);
  if (state.busy) form.dataset.busy = "1"; else delete form.dataset.busy;
  if (state.error) form.dataset.error = state.error; else delete form.dataset.error;
  return state;
}

export function updateAgentTimeZone(root, agent, { force = false } = {}) {
  const section = root?.querySelector?.(`[data-agent-timezone="${CSS.escape(String(agent?.id || ""))}"]`);
  if (!section) return;
  const setting = agent?.time_zone || {};
  const form = section.querySelector("[data-agent-timezone-form]");
  const local = agentTimeZoneFormState(form);
  const stale = Number(setting.revision || 0) < (local?.revision || 0);
  const applied = section.querySelector("[data-timezone-applied]");
  if (applied && !stale) {
    applied.textContent = agentTimeZoneEffective(setting);
    applied.hidden = !applied.textContent;
  }
  const status = section.querySelector("[data-timezone-status]");
  const input = form?.querySelector("[data-timezone-input]");
  const mode = section.querySelector("[data-timezone-mode]");
  const pristine = local && (mode?.value === local.mode && input?.value === local.value ||
    mode?.value === setting.mode && input?.value === (setting.time_zone || ""));
  if (!stale && input && mode && (force || (!local.busy && pristine))) {
    mode.value = setting.mode === "custom" ? "custom" : "auto";
    input.value = setting.time_zone || "";
    input.disabled = mode.value !== "custom" || mode.disabled;
    input.placeholder = mode.value === "auto" ? "等待检测" : "Asia/Shanghai";
    Object.assign(local, { mode: mode.value, value: input.value, revision: Number(setting.revision) || 0 });
    agentTimeZoneFormState(form);
    const submit = form.querySelector("[data-timezone-submit]");
    if (submit) submit.textContent = mode.value === "auto" && input.value ? "重新检测" : "应用";
  }
  if (!stale && status && (force || (!local?.busy && !local?.error))) {
    status.textContent = agentTimeZoneStatus(agent);
  }
  const clock = agentClockSyncPresentation(agent?.metrics?.clock_sync);
  const clockStatus = section.querySelector("[data-clock-sync-status]");
  if (clockStatus) clockStatus.textContent = clock.status;
  const clockOffset = section.querySelector("[data-clock-sync-offset]");
  if (clockOffset) clockOffset.textContent = clock.offset;
}
