import { createIPQualityBindings } from "./ip-quality-bindings.js";
import { ipQualityToday, ipQualityLatest, validIPQualityDate, ipQualityAvailableDates, ipQualityBlockReason, ipQualityFeature } from "./ip-quality-model.js";
import { createPoller } from "./refresh.js";

export function createIPQualityController(ctx, view) {
  const { state, api, can, notify, confirmAction,
    setTimer = (fn, delay) => (state.ipQualityPollTimer = setTimeout(fn, delay)),
    clearTimer = clearTimeout } = ctx;
  let accountData = state.data, serial = 0, snapshot = null, pendingAgents = null;
  let foregroundLoading = false, readFailed = false, readError = "";
  let availableDates = null;
  const daySnapshots = new Map();
  let submitting = new Set();
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const current = (data, epoch) => data === state.data && epoch === state.navigationEpoch && state.route === "ip-quality";
  const editable = (agent) => agent?.can_manage !== false && can("agents.manage", agent) && can("tasks.execute");
  const bind = createIPQualityBindings({ state, load, runCheck, setSchedule, select, showMonth });
  const poller = createPoller({
    run: () => load(undefined, { background: true }),
    isActive: () => state.route === "ip-quality",
    delay: () => 5000, setTimer, clearTimer,
  });
  function render(date, options = {}) {
    view({ date, timezone, agents: pendingAgents || [], ...snapshot,
      availableDates: availableDates || [], month: state.data.ipQualityCalendarMonth,
      loading: foregroundLoading, submitting, editable, readFailed, error: readError, ...options });
    bind();
  }
  async function load(requestedDate, { background = false } = {}) {
    const data = state.data, epoch = state.navigationEpoch;
    if (accountData !== data) {
      accountData = data;
      snapshot = null;
      pendingAgents = null;
      foregroundLoading = false;
      daySnapshots.clear();
      submitting = new Set();
      readFailed = false;
      readError = "";
      availableDates = null;
    }
    // The shell supplies route options as the first argument on page entry.
    const date = typeof requestedDate === "string" ? requestedDate : data.ipQualityDate || ipQualityLatest;
    if (date !== ipQualityLatest && (!validIPQualityDate(date) || date > ipQualityToday())) return false;
    if (typeof requestedDate === "string" && date !== ipQualityLatest && availableDates && !availableDates.includes(date)) return false;
    poller.stop();
    const request = ++serial;
    pendingAgents = null;
    foregroundLoading = !background;
    data.ipQualityDate = date;
    if (typeof requestedDate === "string") data.ipQualityCalendarMonth = date === ipQualityLatest ? "" : date.slice(0, 7);
    if (snapshot?.date !== date) {
      snapshot = daySnapshots.get(date) || null;
      readFailed = false;
      readError = "";
    }
    if (!background && current(data, epoch)) render(date);
    try {
      const agentsPromise = (background && snapshot?.agents ? Promise.resolve(snapshot.agents) : api("/agents"))
        .then((agents) => {
          if (!Array.isArray(agents)) throw new Error("IPQuality 接口返回了无效数据");
          if (!background && !snapshot?.history && current(data, epoch) && request === serial) {
            pendingAgents = agents.filter((agent) => agent.can_manage !== false);
            render(date);
          }
          return agents;
        });
      const [history, agents] = await Promise.all([
        api(`/ip-quality?${date === ipQualityLatest ? "" : `date=${encodeURIComponent(date)}&`}timezone=${encodeURIComponent(timezone)}`),
        agentsPromise,
      ]);
      if (!current(data, epoch) || request !== serial) return false;
      if (!Array.isArray(history?.records) || !Array.isArray(history?.schedules) || !Array.isArray(history?.dates) || !Array.isArray(agents))
        throw new Error("IPQuality 接口返回了无效数据");
      availableDates = ipQualityAvailableDates(history.dates);
      readFailed = false;
      readError = "";
      pendingAgents = null;
      foregroundLoading = false;
      snapshot = { date, history, agents: agents.filter((agent) => agent.can_manage !== false) };
      // Keep a few account-scoped views ready for instant return navigation.
      // Do not duplicate unusually large report responses in memory.
      if (JSON.stringify(history).length < 2 * 1024 * 1024) {
        daySnapshots.delete(date);
        daySnapshots.set(date, snapshot);
        while (daySnapshots.size > 3) daySnapshots.delete(daySnapshots.keys().next().value);
      }
      render(date);
      poller.start();
      return true;
    } catch (error) {
      if (!current(data, epoch) || request !== serial || error?.name === "AbortError") return false;
      readFailed = true;
      readError = error?.message || "检测记录读取失败";
      foregroundLoading = false;
      // A failed read is never replaced by demo data or an old date's report.
      render(date);
      poller.start();
      return false;
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
    if (accountData !== state.data || !availableDates?.length || readFailed || foregroundLoading || state.route !== "ip-quality") return false;
    if (!validIPQualityDate(`${month}-01`) || month < availableDates.at(-1).slice(0, 7) || month > availableDates[0].slice(0, 7)) return false;
    state.data.ipQualityCalendarMonth = month;
    render(state.data.ipQualityDate);
    return true;
  }
  // Switching the selected node repaints from the loaded snapshot: the sidebar
  // only ever offers nodes the current snapshot already returned, so no read is
  // needed, and the repaint is what moves the sidebar highlight.
  function select(agentID) {
    if (state.data.ipQualityAgent === agentID) return false;
    state.data.ipQualityAgent = agentID;
    render(state.data.ipQualityDate);
    return true;
  }
  return { load, runCheck, setSchedule, select };
}
