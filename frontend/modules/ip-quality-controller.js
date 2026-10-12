import { createIPQualityBindings } from "./ip-quality-bindings.js";
import { ipQualityToday, ipQualityLatest, validIPQualityDate, ipQualityAvailableDates, ipQualityBlockReason, ipQualityFeature } from "./ip-quality-model.js";
import { createPoller } from "./refresh.js";

export function createIPQualityController(ctx, view) {
  const { state, api, can, notify, confirmAction,
    setTimer = (fn, delay) => (state.ipQualityPollTimer = setTimeout(fn, delay)),
    clearTimer = clearTimeout } = ctx;
  let accountData = state.data, serial = 0, snapshot = null, pendingAgents = null;
  let foregroundLoading = false, readFailed = false, readError = "";
  let recordsRemoved = false;
  let availableDates = null;
  let scopeEpoch = -1, requestController = null, releaseLifecycle = () => {};
  const daySnapshots = new Map();
  let submitting = new Set();
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const current = (data, epoch) => data === state.data && epoch === state.navigationEpoch && state.route === "ip-quality";
  const editable = (agent) => agent?.can_manage !== false && can("agents.manage", agent) && can("tasks.execute");
  const bind = createIPQualityBindings({ state, load, runCheck, setSchedule, select, showMonth });
  const poller = createPoller({
    run: () => load(undefined, { background: true }),
    isActive: () => current(accountData, scopeEpoch) && !document.hidden && !requestController,
    delay: () => 5000, setTimer, clearTimer,
  });
  function dispose() {
    poller.stop();
    serial += 1;
    requestController?.abort();
    requestController = null;
    scopeEpoch = -1;
    releaseLifecycle();
    releaseLifecycle = () => {};
  }
  function watchLifecycle(epoch) {
    if (scopeEpoch === epoch) return;
    releaseLifecycle();
    scopeEpoch = epoch;
    const signal = state.routeSignal;
    const visibility = () => {
      if (!current(accountData, epoch)) return;
      if (document.hidden) poller.stop();
      else void load(undefined, { background: true });
    };
    document.addEventListener?.("visibilitychange", visibility);
    signal?.addEventListener("abort", dispose, { once: true });
    releaseLifecycle = () => {
      document.removeEventListener?.("visibilitychange", visibility);
      signal?.removeEventListener("abort", dispose);
    };
  }
  function render(date, options = {}) {
    view({ date, timezone, agents: pendingAgents || [], ...snapshot,
      availableDates: availableDates || [], month: state.data.ipQualityCalendarMonth,
      loading: foregroundLoading, submitting, editable, readFailed, error: readError, recordsRemoved, ...options });
    bind();
  }
  function restrictNodes(agents) {
    const allowed = new Set(agents.filter(agent => agent.can_manage !== false).map(agent => agent.id));
    const restrict = cached => {
      cached.agents = cached.agents.filter(agent => allowed.has(agent.id));
      cached.history.records = cached.history.records.filter(record => allowed.has(record.agent_id));
    };
    const removed = snapshot?.agents.some(agent => !allowed.has(agent.id));
    if (snapshot) restrict(snapshot);
    for (const cached of daySnapshots.values()) restrict(cached);
    return removed;
  }
  async function load(requestedDate, { background = false } = {}) {
    const data = state.data, epoch = state.navigationEpoch;
    if (!current(data, epoch)) return false;
    if (accountData !== data) {
      dispose();
      accountData = data;
      snapshot = null;
      pendingAgents = null;
      foregroundLoading = false;
      daySnapshots.clear();
      submitting = new Set();
      readFailed = false;
      readError = "";
      availableDates = null;
      recordsRemoved = false;
    }
    // The shell supplies route options as the first argument on page entry.
    const date = typeof requestedDate === "string" ? requestedDate : data.ipQualityDate || ipQualityLatest;
    if (date !== ipQualityLatest && (!validIPQualityDate(date) || date > ipQualityToday())) return false;
    if (typeof requestedDate === "string" && date !== ipQualityLatest && availableDates && !availableDates.includes(date)) return false;
    watchLifecycle(epoch);
    poller.stop();
    const request = ++serial;
    requestController?.abort();
    const controller = new AbortController();
    requestController = controller;
    const options = { signal: controller.signal };
    pendingAgents = null;
    foregroundLoading = !background;
    data.ipQualityDate = date;
    if (typeof requestedDate === "string") data.ipQualityCalendarMonth = date === ipQualityLatest ? "" : date.slice(0, 7);
    if (snapshot?.date !== date) {
      snapshot = daySnapshots.get(date) || null;
      readFailed = false;
      readError = "";
      recordsRemoved = false;
    }
    if (!background && current(data, epoch)) render(date);
    try {
      const agentsPromise = api("/agents", options)
        .then((agents) => {
          if (!Array.isArray(agents)) throw new Error("IPQuality 接口返回了无效数据");
          if (current(data, epoch) && request === serial && !controller.signal.aborted && restrictNodes(agents)) render(date);
          if (!background && !snapshot?.history && current(data, epoch) && request === serial) {
            pendingAgents = agents.filter((agent) => agent.can_manage !== false);
            render(date);
          }
          return agents;
        });
      const [history, agents] = await Promise.all([
        api(`/ip-quality?${date === ipQualityLatest ? "" : `date=${encodeURIComponent(date)}&`}timezone=${encodeURIComponent(timezone)}`, options),
        agentsPromise,
      ]);
      if (!current(data, epoch) || request !== serial) return false;
      if (!Array.isArray(history?.records) || !Array.isArray(history?.schedules) || !Array.isArray(history?.dates) || !Array.isArray(agents))
        throw new Error("IPQuality 接口返回了无效数据");
      if ((history.date || ipQualityLatest) !== date || history.timezone !== timezone ||
          history.schedules.some(schedule => !schedule?.agent_id) ||
          history.records.some(record => !record || !record.task_id || !record.agent_id ||
            !["pending", "running", "succeeded", "failed", "canceled"].includes(record.status) ||
            record.result && (!Array.isArray(record.result.reports) || record.result.reports.some(report => !report || typeof report !== "object")) ||
            record.archives && (!Array.isArray(record.archives) || record.archives.some(archive => !archive || typeof archive !== "object")) ||
            record.last_successful && (record.last_successful.agent_id !== record.agent_id || record.last_successful.status !== "succeeded" ||
              !Array.isArray(record.last_successful.result?.reports) || record.last_successful.result.reports.some(report => !report || typeof report !== "object") ||
              record.last_successful.archives && (!Array.isArray(record.last_successful.archives) || record.last_successful.archives.some(archive => !archive || typeof archive !== "object")))))
        throw new Error("IPQuality 接口返回了无效数据或不匹配的检测日期");
      availableDates = ipQualityAvailableDates(history.dates);
      readFailed = false;
      readError = "";
      pendingAgents = null;
      foregroundLoading = false;
      if (history.records.length) recordsRemoved = false;
      else if (snapshot?.history.records.length) recordsRemoved = true;
      snapshot = { date, history, agents: agents.filter((agent) => agent.can_manage !== false) };
      // Cached dates must not resurrect reports after a successful read removes
      // their records or their node's ownership/permissions.
      restrictNodes(agents);
      for (const day of daySnapshots.keys()) {
        if (day !== ipQualityLatest && !availableDates.includes(day)) { daySnapshots.delete(day); continue; }
      }
      // Keep a few account-scoped views ready for instant return navigation.
      // Do not duplicate unusually large report responses in memory.
      if (JSON.stringify(history).length < 2 * 1024 * 1024) {
        daySnapshots.delete(date);
        daySnapshots.set(date, snapshot);
        while (daySnapshots.size > 3) daySnapshots.delete(daySnapshots.keys().next().value);
      }
      render(date);
      return true;
    } catch (error) {
      if (!current(data, epoch) || request !== serial || error?.name === "AbortError") return false;
      readFailed = true;
      readError = error?.message || "检测记录读取失败";
      if (error?.status === 403) {
        snapshot = null;
        pendingAgents = [];
        daySnapshots.clear();
        availableDates = [];
        data.ipQualityNodes = [];
        readError = "查看权限已变化，无法读取检测记录。请确认账号权限后重试。";
      }
      foregroundLoading = false;
      // A failed read is never replaced by demo data or an old date's report.
      render(date);
      return false;
    } finally {
      controller.abort();
      if (request === serial && current(data, epoch)) {
        requestController = null;
        poller.start();
      }
    }
  }
  async function mutate(agentID, enabled) {
    const data = state.data, epoch = state.navigationEpoch, pending = submitting, selectedDate = data.ipQualityDate;
    const agent = snapshot?.agents.find((item) => item.id === agentID);
    const record = snapshot?.history.records.find((item) => item.agent_id === agentID);
    const scheduling = typeof enabled === "boolean";
    const canOperate = () => selectedDate === ipQualityLatest || selectedDate === ipQualityToday();
    if (!canOperate() || snapshot?.date !== selectedDate || foregroundLoading) return false;
    if (accountData !== data || !current(data, epoch) || !agent || !editable(agent) || readFailed || pending.has(agentID)) return false;
    if (!scheduling && ipQualityBlockReason(agent, record)) return false;
    if (scheduling && enabled && !agent.features?.includes(ipQualityFeature)) return false;
    pending.add(agentID);
    render(data.ipQualityDate);
    try {
      const message = scheduling && !enabled
        ? `关闭 ${agent.name} 的每日检测？`
        : `${scheduling ? "为" : "检测"} ${agent.name}${scheduling ? "启用每日检测" : "的出口 IP"}？检测会访问第三方服务，耗时数分钟；缺少依赖时自动安装，不上传报告。`;
      if (!(await confirmAction(message, scheduling ? "每日 IP 检测" : "开始 IP 检测")) ||
          !current(data, epoch) || selectedDate !== data.ipQualityDate || !canOperate() || readFailed) return false;
      await api(scheduling ? `/ip-quality/schedules/${encodeURIComponent(agentID)}` : "/ip-quality", {
        method: scheduling ? "PUT" : "POST",
        body: JSON.stringify(scheduling ? { enabled } : { agent_id: agentID }),
      });
      if (!current(data, epoch)) return false;
      notify(scheduling ? (enabled ? "每日检测已启用，将在节点在线时执行" : "每日检测已关闭") : "检测任务已提交");
      pending.delete(agentID);
      await load(data.ipQualityDate);
      return true;
    } catch (error) {
      if (current(data, epoch) && error?.name !== "AbortError") notify(error.message, "error");
      return false;
    } finally {
      pending.delete(agentID);
      if (current(data, epoch)) render(data.ipQualityDate);
    }
  }
  function runCheck(agentID) { return mutate(agentID); }
  function setSchedule(agentID, enabled) { return mutate(agentID, enabled); }
  function showMonth(month) {
    if (accountData !== state.data || !availableDates?.length || readFailed || state.route !== "ip-quality") return false;
    if (!validIPQualityDate(`${month}-01`) || month < availableDates.at(-1).slice(0, 7) || month > availableDates[0].slice(0, 7)) return false;
    state.data.ipQualityCalendarMonth = month;
    render(state.data.ipQualityDate);
    return true;
  }
  // Switching the selected node repaints from the loaded snapshot: the sidebar
  // only ever offers nodes the current snapshot already returned, so no read is
  // needed, and the repaint is what moves the sidebar highlight.
  function select(agentID) {
    if (!current(accountData, scopeEpoch)) return false;
    if (state.data.ipQualityAgent === agentID) return false;
    state.data.ipQualityAgent = agentID;
    render(state.data.ipQualityDate);
    return true;
  }
  return { load, runCheck, setSchedule, select, dispose };
}
