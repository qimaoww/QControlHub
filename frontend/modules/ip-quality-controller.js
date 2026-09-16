import { createIPQualityBindings } from "./ip-quality-bindings.js";
import { ipQualityToday, validIPQualityDate, ipQualityBlockReason, ipQualityFeature } from "./ip-quality-model.js";
import { createPoller } from "./refresh.js";

export function createIPQualityController(ctx, view) {
  const { state, api, can, notify, confirmAction,
    setTimer = (fn, delay) => (state.ipQualityPollTimer = setTimeout(fn, delay)),
    clearTimer = clearTimeout } = ctx;
  let accountData = state.data, serial = 0, snapshot = null, readFailed = false, readError = "";
  let submitting = new Set();
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const current = (data, epoch) => data === state.data && epoch === state.navigationEpoch && state.route === "ip-quality";
  const editable = (agent) => agent?.can_manage !== false && can("agents.manage", agent) && can("tasks.execute");
  const bind = createIPQualityBindings({ state, load, runCheck, setSchedule });
  const poller = createPoller({
    run: () => load(undefined, { background: true }),
    isActive: () => state.route === "ip-quality",
    delay: () => 5000, setTimer, clearTimer,
  });
  function render(date, options = {}) {
    view({ date, timezone, ...snapshot, submitting, editable, readFailed, error: readError, ...options });
    bind();
  }
  async function load(requestedDate, { background = false } = {}) {
    poller.stop();
    const data = state.data, epoch = state.navigationEpoch;
    if (accountData !== data) {
      accountData = data;
      snapshot = null;
      submitting = new Set();
      readFailed = false;
      readError = "";
    }
    // The shell supplies route options as the first argument on page entry.
    const date = typeof requestedDate === "string" ? requestedDate : data.ipQualityDate || ipQualityToday();
    if (!validIPQualityDate(date) || date > ipQualityToday()) return false;
    const request = ++serial;
    data.ipQualityDate = date;
    if (snapshot?.date !== date) snapshot = null;
    if (!background && current(data, epoch)) render(date, { loading: true });
    try {
      const [history, agents] = await Promise.all([
        api(`/ip-quality?date=${encodeURIComponent(date)}&timezone=${encodeURIComponent(timezone)}`),
        api("/agents"),
      ]);
      if (!current(data, epoch) || request !== serial) return false;
      if (!Array.isArray(history?.records) || !Array.isArray(history?.schedules) || !Array.isArray(agents))
        throw new Error("IPQuality 接口返回了无效数据");
      readFailed = false;
      readError = "";
      snapshot = { date, history, agents: agents.filter((agent) => agent.can_manage !== false) };
      render(date);
      poller.start();
      return true;
    } catch (error) {
      if (!current(data, epoch) || request !== serial || error?.name === "AbortError") return false;
      readFailed = true;
      readError = error?.message || "检测记录读取失败";
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
    if (accountData !== data || !current(data, epoch) || !agent || !editable(agent) || readFailed || pending.has(agentID)) return false;
    if (!scheduling && ipQualityBlockReason(agent, record)) return false;
    if (scheduling && enabled && !agent.features?.includes(ipQualityFeature)) return false;
    pending.add(agentID);
    render(data.ipQualityDate);
    try {
      const message = scheduling && !enabled
        ? `关闭 ${agent.name} 的每日 IPQuality 检测？已提交的任务不会因此中止。`
        : `${scheduling ? "启用每日检测" : "检测"} ${agent.name} 的出口 IP？Agent 将运行固定版本的 IPQuality，联系第三方 IP 数据库、媒体和邮件服务，通常需要数分钟；不上传在线报告，也不自动安装依赖。`;
      if (!(await confirmAction(message, scheduling ? "每日 IP 检测" : "开始 IP 检测")) ||
          !current(data, epoch) || selectedDate !== data.ipQualityDate) return false;
      await api(scheduling ? `/ip-quality/schedules/${encodeURIComponent(agentID)}` : "/ip-quality", {
        method: scheduling ? "PUT" : "POST",
        body: JSON.stringify(scheduling ? { enabled } : { agent_id: agentID }),
      });
      if (!current(data, epoch)) return false;
      notify(scheduling ? (enabled ? "每日检测已启用，将在节点在线时执行" : "每日检测已关闭") : "检测任务已提交");
      pending.delete(agentID);
      await load(scheduling || data.ipQualityDate !== selectedDate ? data.ipQualityDate : ipQualityToday());
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
  return { load, runCheck, setSchedule };
}
