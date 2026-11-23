import { panelDefaults, panelSettingOptions, taskFilterActions } from "../modules/runtime-defaults.js";
import { createPoller } from "../modules/poller.js";
import { bindPreferenceFields } from "./preferences.js";
import { readRegion, loadingTable, dialogContext, preparedDialog } from "./loading.js";
import { quotaInvitations } from "./invitations.js";
import { patch, poll } from "./live-dom.js";
import { formatDate } from "./appearance.js";
import { bindSourceEditorTools } from "./source-editor.js";
import { readableConfigSource } from '../modules/code-format.js';
import { editAgentSharing } from "./sharing.js";
import { dashboardTrafficPeriodMonths, isProbeRunning, mergePanelMetricsHistory, summarizeDashboardNodes, utcMonth } from "../modules/dashboard-model.js";
import { orderNodesBySavedOrder, readOrderedNodes } from "../modules/node-order.js";
import { diagnosticError, redactDiagnostic } from "../modules/errors.js";
import { coreSourceLabel, taskRetryState, taskTiming } from "../modules/task-model.js";
import { autoFilter, filterbar, node, button, actions, stack, grid, section, table, badge, field, code, muted, link, query, pathID, list, notice, copyText } from "./ui.js";

const byteFormatters = [0, 1].map(maximumFractionDigits => new Intl.NumberFormat("zh-CN", { maximumFractionDigits }));
export const bytes = value => {
  const amount = Math.max(0, Number(value) || 0);
  const exponent = amount ? Math.max(0, Math.min(4, Math.floor(Math.log(amount) / Math.log(1024)))) : 0;
  return `${byteFormatters[exponent ? 1 : 0].format(amount / 1024 ** exponent)}\u00a0${["B", "KB", "MB", "GB", "TB"][exponent]}`;
};
export const trafficDayColumns = ['日期（UTC）', '接收', '发送', '合计', '接收采样峰值', '发送采样峰值'];

export function trafficDayRows(days) {
  return days.flatMap(day => {
    const sampled = day.peak_sampled || day.peak_receive_bps > 0 || day.peak_send_bps > 0;
    const peak = value => sampled ? `${bytes(value)}/s` : '—';
    const rows = [[day.day, bytes(day.received_bytes), bytes(day.sent_bytes), bytes(day.used_bytes), peak(day.peak_receive_bps), peak(day.peak_send_bps)]];
    if (day.undated_since) {
      const rx = day.undated_received_bytes || 0, tx = day.undated_sent_bytes || 0;
      const start = day.undated_since === '0001-01-01' ? '起始未知' : day.undated_since;
      rows.push([`${start} 至 ${day.day}（日期待定）`, bytes(rx), bytes(tx), bytes(rx + tx), '—', '—']);
    }
    return rows;
  });
}

export const date = formatDate;
export const status = value => badge(({ online: "在线", offline: "离线", active: "运行中", inactive: "已停止", unknown: "未知", activating: "启动中", deactivating: "停止中", pending: "准备中", running: "执行中", succeeded: "成功", failed: "失败", canceled: "已取消", accepted: "已接受", rejected: "已拒绝" })[value] || value || "未知",
  ["online", "active", "succeeded", "accepted"].includes(value) ? "ok" : ["pending", "running"].includes(value) ? "warn" : ["offline", "failed"].includes(value) ? "bad" : "");
export const actionName = value => ({
  validate: "校验配置", deploy: "部署并重启", start: "启动服务", stop: "停止服务", restart: "重启服务", status: "查询状态",
  install: "安装内核", uninstall: "卸载内核", "read-config": "读取可导入配置", "read-managed-config": "读取托管配置",
  "panel-failure":"面板失败", "import-existing": "导入现有服务", "upgrade-agent": "升级 Agent", "enable-bbr": "启用 BBR",
  "disable-bbr": "关闭 BBR", "configure-tcp": "配置 TCP", "check-nginx":"检查 Nginx", "update-nginx":"更新 Nginx", "check-caddy":"检查 Caddy", "update-caddy":"更新 Caddy", "ip-quality": "IP 质量检测", "network-diagnostic":"网络诊断",
})[value] || value;

import { createLiveCells, quantityParts } from "./dashboard-console.js";
import { createPanelHostController } from "./dashboard-panel.js";
import { createDashboardProbeController } from "./dashboard-probes.js";
import { createDashboardTopologySection } from "./dashboard-topology.js";
import { createEdgeTelemetryController } from "./dashboard-telemetry.js";
import { createTrafficStage } from "./dashboard-traffic.js";

/** Shared telemetry resource loader/poller used by both the dashboard and standalone topology pages. */
function createTelemetryStore(ctx, { getMonth = utcMonth, onLoading, onSuccess, onSettled } = {}) {
  const readings = new Map();
  const states = new Map();
  const requests = new Map();
  const reading = new Map();

  async function readResource(key, path, force = false) {
    if (!ctx.current() || (requests.has(key) && !force)) return;
    requests.get(key)?.abort();
    const controller = new AbortController();
    requests.set(key, controller);
    if (!readings.has(key)) {
      states.set(key, "loading");
      onLoading?.(key);
    }
    const requestedMonth = getMonth();
    try {
      const value = await ctx.api(path, { signal: AbortSignal.any([ctx.signal, controller.signal]) });
      if (!ctx.current() || requests.get(key) !== controller) return;
      if (key === "traffic" && value.month !== requestedMonth) throw new Error("流量月份不匹配");
      if (key.startsWith("usage:") && value.month !== key.slice(6)) throw new Error("流量月份不匹配");
      readings.set(key, value);
      states.set(key, "ready");
      onSuccess?.(key, value);
    } catch (error) {
      if (!ctx.current() || requests.get(key) !== controller || error.name === "AbortError") return;
      if (error.status === 403) { readings.delete(key); states.set(key, "denied"); }
      else states.set(key, "failed");
    } finally {
      if (ctx.current() && requests.get(key) === controller) {
        requests.delete(key);
        await onSettled?.(key);
      }
    }
  }

  function read(key, path, force = false) {
    if (reading.has(key) && !force) return reading.get(key);
    const work = readResource(key, path, force);
    reading.set(key, work);
    void work.finally(() => { if (reading.get(key) === work) reading.delete(key); }).catch(() => {});
    return work;
  }

  const probePollInterval = () => {
    const pairs = readings.get("probes")?.pairs;
    return Array.isArray(pairs) && pairs.some((p) => isProbeRunning(p)) ? 2000 : 5000;
  };

  ctx.signal.addEventListener("abort", () => { requests.forEach(controller => controller.abort()); }, { once: true });

  return {
    readings,
    states,
    read,
    shouldPoll: (key, manual) => manual || states.get(key) !== "denied",
    probePollInterval,
  };
}

const BLANK_FIGURE = ["—", ""];
const NO_SUBS = Object.freeze([]);
const EDGE_KEYS = new Set(["connections", "certificates", "ipQuality"]);
const blankCell = (key, label) => ({ key, label, token: "", tone: "", pill: null, state: "ready", figure: BLANK_FIGURE, subs: NO_SUBS, viz: null });
const taskNumbers = (overview) => ({
  pending: Number(overview.tasks_pending || 0) + Number(overview.tasks_queued || 0),
  running: Number(overview.tasks_running || 0),
  failed: Number(overview.tasks_failed || 0),
});

export async function dashboardPage(ctx) {
  let month = ctx.state.data.dashboardMonth || utcMonth();
  const can = permission => ctx.can(permission);
  let panelHistory = [];
  let edgeLastFetch = 0;
  let stage = null;
  const store = createTelemetryStore(ctx, {
    getMonth: () => month,
    onLoading: (key) => { if (key === "traffic") stage?.paint(); },
    onSuccess: (key, value) => {
      if (key === "metrics") panelHistory = mergePanelMetricsHistory(value, panelHistory, 60);
      if (key === "agents") sampleRate(value);
    },
    onSettled: async (key) => {
      if (key === "metrics") paintMetrics();
      else if (key === "traffic" || key === "trafficPolicies" || key.startsWith("usage:")) stage?.paint();
      else if (key === "probes") topologySection.schedulePaint();
      else if (EDGE_KEYS.has(key)) edgeTelemetry.paint();
      else await queueBusiness();
    },
  });
  const { readings, states, read, shouldPoll, probePollInterval } = store;

  const liveCells = createLiveCells();
  const rateTrace = [];
  const usageKey = (monthKey) => `usage:${monthKey}`;
  const orderedAgents = () => orderNodesBySavedOrder(list(readings.get("agents")), readings.get("order")?.ids);
  // Fleet rate samples behind the live trace, oldest first. A refresh
  // without a valid sample leaves a gap instead of a zero.
  function sampleRate(agents) {
    const fleet = summarizeDashboardNodes(list(agents));
    rateTrace.push(fleet.sampled ? fleet.receiveBPS + fleet.sendBPS : null);
    if (rateTrace.length > 40) rateTrace.shift();
  }
  // Keeps the outgoing month for the natural periods when the selected
  // month moves away from a month they still need.
  function keepPeriodUsage() {
    const outgoing = readings.get("traffic");
    if (!outgoing || !dashboardTrafficPeriodMonths().includes(outgoing.month)) return;
    readings.set(usageKey(outgoing.month), outgoing);
    states.set(usageKey(outgoing.month), states.get("traffic") === "failed" ? "failed" : "ready");
  }
  // Persisted rows of one UTC month with their read state. The selected
  // month's reading doubles as a period source whenever it holds a month
  // the natural periods need.
  function trafficSource(monthKey) {
    const selected = readings.get("traffic");
    if (selected?.month === monthKey) return { value: selected, state: states.get("traffic") || "ready" };
    const key = usageKey(monthKey), value = readings.get(key);
    const state = states.get(key) || (month === monthKey ? states.get("traffic") : "") || "loading";
    return { value, state: !value && state === "ready" ? "loading" : state };
  }
  function selectMonth(value) {
    if (value === month) return;
    keepPeriodUsage();
    month = value;
    ctx.state.data.dashboardMonth = month;
    void read("traffic", query("/traffic-usage", { month }), true);
  }

  const hostController = can("panel-metrics.read") ? createPanelHostController(ctx, { bytes, date }) : null;
  function paintMetrics() {
    const m = readings.get("metrics") || {};
    const status = states.get("metrics");
    if (status === "denied") panelHistory = [];
    hostController?.paint({ m, history: panelHistory, status });
  }

  stage = can("traffic.read") ? createTrafficStage(ctx, {
    bytes,
    getMonth: () => month,
    onMonth: selectMonth,
    source: trafficSource,
    getPolicies: () => readings.get("trafficPolicies"),
    getAgents: orderedAgents,
    getOrderIDs: () => readings.get("order")?.ids || NO_SUBS,
  }) : null;

  let topologySection;
  const probeController = createDashboardProbeController(ctx, {
    getOrderIDs: () => readings.get("order")?.ids || [],
    getProbes: () => readings.get("probes"),
    reloadProbes: () => read("probes", "/dashboard/probes", true),
    onSaved: (saved, meta) => topologySection?.onProbeSaved?.(saved, meta),
    onDeleted: (pair) => topologySection?.onProbeDeleted?.(pair),
  });

  topologySection = createDashboardTopologySection(ctx, {
    date,
    getAgents: () => list(readings.get("agents")),
    getOrderIDs: () => readings.get("order")?.ids || [],
    getProbes: () => readings.get("probes"),
    getStates: () => states,
    probeController,
    isStandalonePage: false,
  });

  const edgeTelemetry = createEdgeTelemetryController(ctx, {
    readEdgeState: key => ({ data: readings.get(key), state: states.get(key) }),
  });

  let businessPaint;
  let businessReady, finishBusiness;
  const queueBusiness = () => {
    if (businessPaint != null) return businessReady;
    businessReady = new Promise(resolve => { finishBusiness = resolve; });
    businessPaint = requestAnimationFrame(() => {
      businessPaint = null;
      try {
        if (ctx.current()) {
          paintBusiness();
          stage?.paint();
          topologySection.schedulePaint();
          edgeTelemetry.paint();
        }
      } finally { finishBusiness(); }
    });
    return businessReady;
  };
  ctx.signal.addEventListener('abort', () => { cancelAnimationFrame(businessPaint); finishBusiness?.(); }, { once: true });

  const waitText = (key) => ["failed", "denied"].includes(states.get(key)) ? "读取失败" : "读取中";

  function nodeCell(overview, agents) {
    const cell = blankCell("节点", "在线节点");
    if (!readings.has("overview") && !readings.has("agents")) {
      cell.state = states.get("overview") === "failed" ? "failed" : "loading";
      cell.subs = [{ text: waitText("overview"), tone: cell.state === "failed" ? "bad" : "" }];
      return cell;
    }
    const online = Number(overview.agents_online ?? agents.filter(agent => agent.status === "online").length) || 0;
    const count = Number(overview.agents ?? agents.length) || 0;
    const offline = Math.max(0, count - online);
    cell.figure = [String(online), ` / ${count}`];
    if (offline > 0) cell.pill = { text: `离线 ${offline}`, tone: "bad" };
    else if (count > 0) cell.pill = { text: "全部在线", tone: "ok" };
    const pct = count > 0 ? (online / count) * 100 : 0;
    cell.viz = { type: "bar", pct, tone: online > 0 ? "ok" : "" };
    return cell;
  }

  function engineCell(fleet) {
    const cell = blankCell("运行内核", "运行内核");
    if (!readings.has("agents")) {
      cell.state = states.get("agents") === "failed" ? "failed" : "loading";
      cell.subs = [{ text: waitText("agents"), tone: cell.state === "failed" ? "bad" : "" }];
      return cell;
    }
    cell.figure = [String(fleet.running), ` / ${fleet.installed}`];
    const pct = fleet.installed > 0 ? (fleet.running / fleet.installed) * 100 : 0;
    if (fleet.installed > 0) {
      cell.token = `${Math.round(pct)}%`;
      cell.tone = pct >= 80 ? "ok" : pct > 0 ? "warn" : "";
    }
    cell.viz = { type: "bar", pct, tone: "" };
    return cell;
  }

  // Sum of the valid live samples of the authorized nodes. Offline nodes and
  // expired samples are left out; no valid sample shows no figure.
  function rateCell(fleet, count) {
    const cell = blankCell("节点速率", "节点速率");
    if (!fleet.sampled) {
      cell.state = readings.has("agents") ? "empty" : "loading";
      cell.subs = [{ text: readings.has("agents") ? "无有效采样" : waitText("agents"), tone: "" }];
      return cell;
    }
    cell.figure = quantityParts(bytes(fleet.receiveBPS + fleet.sendBPS), "/s");
    if (fleet.sampled < count) cell.token = `${fleet.sampled}/${count} 节点`;
    cell.subs = [{ text: `↓ ${bytes(fleet.receiveBPS)}/s`, tone: "rx" }, { text: `↑ ${bytes(fleet.sendBPS)}/s`, tone: "" }];
    if (rateTrace.filter(v => typeof v === "number" && Number.isFinite(v)).length >= 2) {
      cell.viz = { type: "trace", values: rateTrace };
    }
    return cell;
  }

  function taskCell(tasks, known) {
    const cell = blankCell("调度任务", "调度任务");
    if (!known) {
      cell.state = states.get("overview") === "failed" ? "failed" : "loading";
      cell.subs = [{ text: waitText("overview"), tone: cell.state === "failed" ? "bad" : "" }];
      return cell;
    }
    cell.figure = [String(tasks.running), "\u00a0运行中"];
    if (tasks.failed > 0) {
      cell.pill = {
        text: `失败 ${tasks.failed}`,
        tone: "bad",
        href: can("tasks.read") ? "#tasks?status=failed" : "",
      };
    }
    cell.subs = [{ text: `排队 ${tasks.pending}`, tone: "" }];
    return cell;
  }

  // 4 equal executive KPI cards (Nodes, Engines, Rate, Tasks).
  function paintConsole() {
    const overview = readings.get("overview") || {};
    const agents = orderedAgents();
    const fleet = summarizeDashboardNodes(agents);
    const known = readings.has("overview");
    const canAgents = can("agents.read");
    const cells = [nodeCell(overview, agents)];
    if (canAgents) cells.push(engineCell(fleet));
    if (canAgents && can("metrics.read")) cells.push(rateCell(fleet, agents.length));
    cells.push(taskCell(taskNumbers(overview), known));
    liveCells.paint(cells);
    return agents;
  }

  function paintBusiness() {
    paintConsole();
  }

  let historyLastFetch = 0;
  function refresh(manual = false) {
    const now = Date.now();
    // Persisted traffic history moves slowly, so it is read on a slower beat
    // than the live node and host samples.
    const historyDue = manual || !historyLastFetch || now - historyLastFetch >= 30000;
    const sources = [["overview", "/overview"]];
    if (can("agents.read")) sources.push(["agents", "/agents"], ["order", "/preferences/node-order"]);
    if (can("panel-metrics.read") && shouldPoll("metrics", manual)) sources.push(["metrics", "/panel-metrics"]);
    if (can("traffic.read")) {
      if (historyDue && shouldPoll("traffic", manual)) sources.push(["traffic", query("/traffic-usage", { month })]);
      if (shouldPoll("trafficPolicies", manual)) sources.push(["trafficPolicies", "/traffic-policies?view=page"]);
      const periodMonths = dashboardTrafficPeriodMonths();
      const selectedReady = readings.get("traffic")?.month === month;
      for (const key of [...states.keys()]) {
        if (!key.startsWith("usage:")) continue;
        const needed = periodMonths.includes(key.slice(6)) && !(selectedReady && key.slice(6) === month);
        if (!needed) { readings.delete(key); states.delete(key); }
      }
      for (const item of periodMonths.filter(value => value !== month)) {
        const key = usageKey(item);
        if ((historyDue || !states.has(key)) && shouldPoll(key, manual)) sources.push([key, query("/traffic-usage", { month: item })]);
      }
    }
    if (can("agents.read") && can("tasks.read") && shouldPoll("probes", manual)) sources.push(["probes", "/dashboard/probes"]);
    if (historyDue) historyLastFetch = now;
    if (manual || !edgeLastFetch || now - edgeLastFetch >= 30000) {
      edgeLastFetch = now;
      if (can("core-logs.read") && shouldPoll("connections", manual)) sources.push(["connections", "/client-connections?limit=1&timeline=true"]);
      if (can("certificates.read") && shouldPoll("certificates", manual)) sources.push(["certificates", "/certificates"]);
      if (can("agents.read") && shouldPoll("ipQuality", manual)) sources.push(["ipQuality", "/ip-quality"]);
    }
    return Promise.all(sources.map(([key, path]) => read(key, path)));
  }

  ctx.onRefresh(() => refresh(true));
  paintMetrics(); paintBusiness(); stage?.paint();
  topologySection.paint(); edgeTelemetry.paint();
  void refresh(true);
  poll(ctx, () => refresh(false), probePollInterval);

  const opsCards = [
    hostController?.element,
    edgeTelemetry.visible ? edgeTelemetry.element : null,
  ].filter(Boolean);
  const opsRow = opsCards.length
    ? node("div", { className: opsCards.length === 1 ? "a-dash-ops-row is-single" : "a-dash-ops-row" }, opsCards)
    : null;

  return node("div", { className: "a-dash-deck a-dash-console" },
    liveCells.element,
    opsRow,
    stage?.element,
    topologySection.visible && topologySection.element,
  );
}

export async function topologyPage(ctx) {
  const can = (permission) => ctx.can(permission);
  let topologySection;

  const store = createTelemetryStore(ctx, {
    onSettled: (key) => {
      if (key === "probes" || key === "agents" || key === "order") {
        topologySection?.schedulePaint();
      }
    },
  });
  const { readings, states, read, shouldPoll, probePollInterval } = store;

  const probeController = createDashboardProbeController(ctx, {
    getOrderIDs: () => readings.get("order")?.ids || [],
    getProbes: () => readings.get("probes"),
    reloadProbes: () => read("probes", "/dashboard/probes", true),
    onSaved: (saved, meta) => topologySection?.onProbeSaved?.(saved, meta),
    onDeleted: (pair) => topologySection?.onProbeDeleted?.(pair),
  });

  topologySection = createDashboardTopologySection(ctx, {
    date,
    getAgents: () => list(readings.get("agents")),
    getOrderIDs: () => readings.get("order")?.ids || [],
    getProbes: () => readings.get("probes"),
    getStates: () => states,
    probeController,
    isStandalonePage: true,
  });

  function refresh(manual = false) {
    const sources = [
      ...(can("agents.read") ? [["agents", "/agents"], ["order", "/preferences/node-order"]] : []),
      ...(can("agents.read") && can("tasks.read") && shouldPoll("probes", manual) ? [["probes", "/dashboard/probes"]] : []),
    ];
    return Promise.all(sources.map(([key, path]) => read(key, path)));
  }

  ctx.onRefresh(() => refresh(true));
  topologySection.paint();
  void refresh(true);
  poll(ctx, () => refresh(false), probePollInterval);

  return node("div", { className: "a-dash-deck a-console a-topo-console" },
    topologySection.visible ? topologySection.element : node("div", { className: "a-notice" }, "当前账号无权查看网络拓扑。"),
  );
}

function taskEngine(task) {
  if (task.action === 'network-diagnostic') return '节点网络';
  if (task.action === 'panel-failure') return '面板';
  if (task.action === 'upgrade-agent') return 'QAgent';
  if (task.action === 'ip-quality') return 'IPQuality';
  if (['enable-bbr', 'disable-bbr', 'configure-tcp'].includes(task.action)) return 'BBR / TCP';
  return task.engine;
}

function taskContext(ctx, task, targetAgent) {
  if (task.action === "network-diagnostic") return stack(muted(`节点网络测量 · ${date(task.created_at)}`),link("查看测量结果", `#network-tools?measurement=${pathID(task.id)}`));
  if (task.action === "panel-failure") return stack(muted(`面板操作 · ${date(task.created_at)}`),muted("未进入 Agent 执行"));
  const agent = targetAgent?.id === task.agent_id ? targetAgent : ctx.can('agents.read') && ctx.state.data.taskAgents?.find(agent => agent.id === task.agent_id);
  const source = coreSourceLabel(task.engine, task.core_version, task.core_source);
  return stack(muted(`节点 ${agent?.name || task.agent_id} · ${date(task.created_at)}`),
    muted(task.attempt ? `第 ${task.attempt} 次执行` : '尚未开始'),
    muted(`${task.engine || '来源未返回内核'} · ${task.shared_instance ? `共享实例 ${task.shared_traffic_id || '来源未返回实例标识'}` : '主实例'}`),
    !task.config_id && task.core_version && muted(`内核版本 ${task.core_version}${source ? ` · ${source}` : ''}`),
    task.install_if_missing && muted('缺少时安装稳定版；已有内核保持版本'));
}

export function taskDetail(ctx, taskID, {title = "任务详情", targetAgent} = {}) {
  if (!ctx.can("tasks.read")) return;
  let task = {id:taskID,status:"loading",error:"",output:""};
  let diagnosticSignature = "";
  if (!ctx.current()) return;
  const state = muted("正在读取任务…"), failure = notice(diagnosticError(task.error || ""));
  const output = code("");
  output.hidden = !output.textContent;
  const diagnosis = stack();
  const previousAttempts = actions();
  const historyDialogs = new Set();
  let diagnosisSignature;
  function paintDiagnosis() {
    const value = taskRetryState(task).diagnostic;
    const signature = JSON.stringify(value);
    if (signature === diagnosisSignature) return;
    diagnosisSignature = signature;
    patch(diagnosis, stack(value && ["title", "advice", "rollback"].map(key => value[key] && muted(redactDiagnostic(value[key])))));
    diagnosis.hidden = !value;
  }
  paintDiagnosis();
  const timing = muted(taskTiming(task));
  const context = stack(table(['项目', '内容'], [['节点', '正在读取…'], ['执行对象', '正在读取…'], ['创建时间', '正在读取…']], { label: '任务信息', paginate: 0 }));
  const engineLabel = badge(taskEngine(task)), actionLabel = badge(actionName(task.action));
  const heading = actions(state, engineLabel, actionLabel);
  const errorSummary = () => {
    const text=(task.action === "panel-failure" ? `面板操作失败：${redactDiagnostic(task.error || "来源未返回具体原因")}` : diagnosticError(task.error || "")).split("\n")[0];
    return text.slice(0,300)+(text.length>300 ? "…" : "");
  };
  const content = stack(heading,
    context, timing, failure, diagnosis, previousAttempts, output);
  const box = ctx.dialog(title, content, { wide: true });
  box.classList.add("a-task-dialog");
  let reading = false;
  const controller = new AbortController();
  const signal = AbortSignal.any([ctx.signal, controller.signal]);
  const stop = () => { detailPoller.stop(); controller.abort(); };
  const active = () => ctx.current() && box.open;
  const refresh = button("刷新结果", () => void initialRead.load());
  refresh.addEventListener('mutationend', () => { refresh.disabled = task.status === 'denied'; });
  content.insertBefore(actions(refresh,button("复制诊断",event=>void ctx.mutate(event.currentTarget,async()=>{
    await copyText([taskID, failure.textContent, output.textContent].filter(Boolean).join("\n\n"));
    ctx.announce("诊断已复制");
  }))), output);
  async function read({ automatic = false } = {}) {
    if (reading || !active()) return;
    reading = true;
    try {
      // Completed output is immutable until its task metadata changes. Keep
      // independently authorizing this resource without transferring its log.
      const terminal = ['succeeded', 'failed', 'canceled'].includes(task.status);
      if (automatic && terminal) {
        const state = await ctx.api(`/tasks/${pathID(taskID)}?view=status`, { signal });
        if (!active()) return;
        if (diagnosticSignature && ['status', 'attempt', 'finished_at', 'error'].every(key => state[key] === task[key])) {
          failure.textContent = errorSummary();
          return;
        }
      }
      const latest = await ctx.api(`/tasks/${pathID(taskID)}`, { signal });
      if (!active()) return;
      task = latest;
      engineLabel.textContent = taskEngine(task);
      actionLabel.textContent = actionName(task.action);
      timing.textContent = taskTiming(task);
      patch(context, taskContext(ctx, task, targetAgent));
      paintDiagnosis();
      const next = status(task.status);
      if (state.textContent !== next.textContent) { state.textContent = next.textContent; state.className = next.className; }
      const error = errorSummary();
      if (failure.textContent !== error) failure.textContent = error;
      let text = redactDiagnostic(task.output);
      const signature = JSON.stringify([task.status,task.attempt,task.finished_at]);
      if (["succeeded","failed","canceled"].includes(task.status) && diagnosticSignature !== signature) {
        const diagnostic = await ctx.api(`/tasks/${pathID(taskID)}/diagnostics`, {signal});
        if (!active()) return;
        const metadata = diagnostic.attempts || [];
        const attempts = [{...metadata.find(item=>item.attempt===task.attempt),attempt:task.attempt,error:diagnostic.error,output:diagnostic.output,source:diagnostic.source}];
        previousAttempts.replaceChildren(...metadata.filter(item=>item.attempt!==task.attempt).map(item=>button(`查看第 ${item.attempt} 次记录`, event=>void ctx.mutate(event.currentTarget,async()=>{
          const previous=await ctx.api(`/tasks/${pathID(taskID)}/diagnostics?attempt=${item.attempt}`,{signal});
          if (!active()) return;
          const detail=[`领取次数：${item.attempt}`,`来源：${previous.source}`,previous.error,previous.output].filter(Boolean).join("\n\n");
          const history=ctx.dialog(`任务记录 · 第 ${item.attempt} 次`,stack(code(detail),actions(button("复制诊断",event=>void ctx.mutate(event.currentTarget,()=>copyText(detail))))),{wide:true});
          history.classList.add('a-task-dialog');
          historyDialogs.add(history);
          history.addEventListener('close',()=>historyDialogs.delete(history),{once:true});
          box.addEventListener('close',()=>history.close(),{once:true});
        }))));
        text = [diagnostic.occurrences>1 && `发生次数：${diagnostic.occurrences} · 最近发生：${date(diagnostic.last_failure_at)}`,
          diagnostic.retry_of && `重试来源：${diagnostic.retry_of}`,
          ...attempts.map(item => [task.action!=="panel-failure" && `执行尝试：${item.attempt}`,
            item.executed_attempt>0 && item.executed_attempt!==item.attempt && `Agent 返回此前第 ${item.executed_attempt} 次领取时的执行结果；本次为重传核对。`,
            item.source==="legacy-agent" && "历史或旧 Agent 记录：来源未提供完整性信息；已丢失的内容无法恢复。",
            item.error && `错误详情\n${redactDiagnostic(item.error)}`,item.output && `相关输出\n${redactDiagnostic(item.output)}`].filter(Boolean).join("\n\n"))].filter(Boolean).join("\n\n");
        diagnosticSignature = signature;
      } else if (diagnosticSignature === signature) text = output.textContent;
      if (output.textContent !== text) {
        const follow = output.scrollHeight-output.scrollTop-output.clientHeight<24;
        const selection = getSelection(), previous = output.firstChild;
        const selected = previous?.nodeType === Node.TEXT_NODE && selection?.anchorNode === previous && selection.focusNode === previous
          ? [selection.anchorOffset, selection.focusOffset] : null;
        // A completed receipt adds diagnostic context before the live output.
        // Keep the selected output text selected when that prefix is inserted.
        const selectionShift = selected && previous.data ? Math.max(0, text.indexOf(previous.data)) : 0;
        if (previous?.nodeType === Node.TEXT_NODE) previous.data = text;
        else output.textContent = text;
        if (selected && output.firstChild) selection.setBaseAndExtent(output.firstChild, Math.min(selected[0] + selectionShift, text.length), output.firstChild, Math.min(selected[1] + selectionShift, text.length));
        output.hidden = !text;
        if(follow)output.scrollTop=output.scrollHeight;
      }
    } catch (error) {
      if (active() && [403, 404].includes(error.status)) {
        for (const history of historyDialogs) history.close();
        previousAttempts.replaceChildren();
        output.textContent = ''; output.hidden = true; state.textContent = '无权读取';
        timing.textContent = '';
        context.replaceChildren();
        heading.replaceChildren(state);
        failure.textContent = '无权读取此任务。';
        task = { status: 'denied' }; refresh.disabled = true; stop();
        paintDiagnosis();
      }
      throw error;
    } finally { reading = false; }
  }
  // Terminal receipts still reauthorize access; this detail cadence is independent
  // of the account's task-list refresh preference.
  const detailPoller = createPoller({
    isActive: () => active() && task.status !== 'denied', coalesce: false, delay: () => 2000,
    async run() {
      try { if (!document.hidden) await read({ automatic: true }); }
      catch (error) { if (active() && error.name !== 'AbortError') failure.textContent = error.message; }
    },
  });
  box.addEventListener("close", () => { stop(); ctx.signal.removeEventListener("abort", stop); }, {once:true});
  ctx.signal.addEventListener("abort", stop, {once:true});
  const initialRead = readRegion(dialogContext(ctx, box), '任务详情', () => read(), () => {});
  content.prepend(initialRead.element);
  void initialRead.load().finally(() => detailPoller.start());
}

export async function tasksPage(ctx) {
  const defaultPageSize = () => {
    const value = ctx.state.data.settings?.task_page_size;
    return panelSettingOptions.task_page_size.includes(value) ? value : panelDefaults.task_page_size;
  };
  const filters = ctx.state.data.taskFilters || { limit: String(defaultPageSize()) };
  let settingsLoadedAt = ctx.state.data.settings ? Date.now() : -Infinity;
  let preferencesAdopted = Boolean(ctx.state.data.settings || ctx.state.data.taskFilters), queryTouched = false;
  const pollDelay = () => {
    const value = ctx.state.data.settings?.task_poll_interval_ms;
    return panelSettingOptions.task_poll_interval_ms.includes(value) ? value : panelDefaults.task_poll_interval_ms;
  };
  let readSequence = 0, filtering = false, tasksLoaded = false;
  let agents = [], agentsLoadedAt = -Infinity, lastTasks = [], agentsPending, agentsVerified = false;
  const agentField = field('节点', 'agent_id', filters.agent_id || '', { options: [['', '全部节点'], ...(filters.agent_id ? [[filters.agent_id, filters.agent_id]] : [])] });
  const agentPicker = agentField.querySelector('select');
  let agentSignature;
  function syncAgents() {
    let selected = agentPicker.value;
    const choices = ctx.can('agents.read') ? agents : [...new Set(lastTasks.map(task => task.agent_id))].map(id => ({ id, name: id }));
    if (selected && agentsVerified && !choices.some(agent => agent.id === selected)) {
      selected = ''; agentPicker.value = ''; preferenceFields.save({ agent_id: '' });
      queueMicrotask(() => { if (ctx.current()) filter.requestSubmit(); });
    }
    const options = [['', '全部节点'], ...choices.map(agent => [agent.id, agent.name])];
    if (selected && !choices.some(agent => agent.id === selected)) options.push([selected, selected]);
    const signature = JSON.stringify(options);
    if (signature === agentSignature || agentPicker.matches(':open') && ctx.can('agents.read') && agents.length) return;
    agentPicker.replaceChildren(agentPicker.querySelector('button'), ...options.map(([value, label]) => node('option', { value }, label)));
    agentPicker.value = selected;
    agentSignature = signature;
  }
  const filter = autoFilter(ctx, [
    node("div", { className: "a-form-grid" },
      field("状态", "status", filters.status || "", { options: [["", "全部"], ["pending", "准备中"], ["running", "执行中"], ["succeeded", "成功"], ["failed", "失败"], ["canceled", "已取消"]] }),
      agentField,
      field("操作", "action", filters.action || "", { options: [["", "全部操作"], ...taskFilterActions.map(action => [action, actionName(action)])] }),
      field("条数", "limit", filters.limit ?? String(panelDefaults.task_page_size), { type: "number", min: 1, max: 500 })),
  ], async (values, _, request) => {
    const sequence = ++readSequence; filtering = true;
    try {
      const tasks = await request.api(query("/tasks", values));
      if (!request.current() || sequence !== readSequence) return;
      ctx.state.data.taskFilters = values; draw(tasks);
    } finally { if (sequence === readSequence) filtering = false; }
  }, () => ({ status: "", agent_id: "", action: "", limit: String(defaultPageSize()) }));
  const preferenceFields = bindPreferenceFields(ctx, 'tasks', [filter], { restore: () => {
    preferencesAdopted = true; queryTouched = true;
    ctx.state.data.taskFilters = Object.fromEntries(new FormData(filter));
    filter.requestSubmit();
  } });
  filter.addEventListener('reset', () => queueMicrotask(() => preferenceFields.save(Object.fromEntries(new FormData(filter)))));
  filter.addEventListener('input', () => { queryTouched = true; });
  filter.addEventListener('change', () => { queryTouched = true; });
  const results = node("div");
  const taskTable = table(["操作", "节点", "执行对象", "状态", "创建时间", "操作"], [], { label: "任务", rowKey: row => row.taskID });
  results.append(taskTable);
  const refreshState = muted('正在实时更新'); refreshState.setAttribute('role', 'status');
  function editRetry(task) {
    return preparedDialog(ctx, '编辑后重试', ['当前配置'], local => editRetryLoaded(local, task, ctx), { submit: '保存并重试' });
  }
  async function editRetryLoaded(ctx, task, parentContext) {
    const resource = await ctx.api(`/tasks/${pathID(task.id)}/retry-editor`);
    if (!ctx.current()) return;
    const { config } = resource;
    config.content = readableConfigSource(config.content, config.engine);
    const form = ctx.form([
      muted(`${config.name} · ${date(config.updated_at)}`),
      task.error && notice(diagnosticError(task.error)),
      config.agent_id && actions(link("在实时配置中编辑", `#live-config?agent=${pathID(config.agent_id)}&engine=${pathID(config.engine)}`)),
      node("div", { className: "a-editor" }, field("当前配置", "content", config.content, { multiline: 18, required: true, spellcheck: false })),
    ], `保存并重试${task.action === "deploy" ? "部署" : "校验"}`, async values => {
      if (!await ctx.confirm("保存当前修改并重新执行任务？部署会重启对应内核。", "保存并重试") || !ctx.current()) return;
      const result = await ctx.api(`/tasks/${pathID(task.id)}/edit-retry`, { method: "POST", body: { content: values.content, expected_version: config.version } });
      if (!ctx.current()) return;
      ctx.setDirty(false); box.close(); ctx.announce("配置已保存，重试任务已提交");
      await taskDetail(parentContext, result.task.id);
    });
    const syncDraft = () => ctx.setDirty(form.elements.content.value !== config.content);
    form.addEventListener("input", syncDraft);
    bindSourceEditorTools(ctx, form, () => config.engine, () => config.content, syncDraft, true);
    const box = ctx.dialog("编辑后重试", form, { wide: true });
    box.classList.add("a-task-dialog");
    box.onRequestClose = async () => {
      if (form.elements.content.value !== config.content && !await ctx.confirm("放弃未保存的配置修改？", "放弃更改") || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
  }
  function draw(tasks) {
    lastTasks = list(tasks); syncAgents();
    taskTable.updateRows(lastTasks.map(task => Object.assign(() => {
      const retryState = taskRetryState(task);
      return [
        actionName(task.action), agents.find(agent => agent.id === task.agent_id)?.name || task.agent_id || (task.action === "network-diagnostic" ? "节点网络" : "面板"), taskEngine(task), stack(actions(status(task.status)), muted(taskTiming(task)), retryState.missingConfigVersion && muted("缺少配置历史关联")), date(task.created_at),
        node('div', { className: 'a-actions', 'data-task-id': task.id }, button("详情", () => void ctx.mutate(null, () => taskDetail(ctx, task.id))),
          ctx.can("tasks.execute") && retryState.canRetry && button(task.config_id ? "重试配置" : "重试任务", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm(task.config_id ? `确定重试原任务关联配置？${task.install_if_missing ? "仅缺少内核时安装稳定版，已安装的内核不会切换版本。" : ""}配置已有更新时会拒绝本次重试，请选择编辑后重试或重新部署。` : "重新执行此任务？请先核对原任务结果。", "重试任务") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              const retry = await ctx.api(`/tasks/${pathID(task.id)}/retry`, { method: "POST", body: task.config_id ? { expected_config_version: task.config_version } : {} });
              ctx.announce("已提交重试任务");
              await taskDetail(ctx, retry.id);
            });
          }, { "data-retry": task.id }),
          ctx.can("tasks.execute") && retryState.canEditRetry &&
            (ctx.can("agent-config.write") || ctx.can("configs.write")) && button("编辑后重试", () => editRetry(task)),
          ctx.can("tasks.execute") && ["pending", "running"].includes(task.status) && button("取消", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm("取消此任务？已在节点执行的操作可能仍需核实结果。", "取消任务") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              await ctx.api(`/tasks/${pathID(task.id)}`, { method: "DELETE" });
              ctx.announce("已提交取消请求");
              draw(await ctx.api(query("/tasks", ctx.state.data.taskFilters || filters)));
            });
          }, { tone: "danger" })),
      ];
    }, { taskID: task.id })));
  }
  async function refresh() {
    if (filtering) return;
    const sequence = ++readSequence;
    try {
      const updateAgents = async () => {
        if (!ctx.can('agents.read')) { agents = []; agentsVerified = false; delete ctx.state.data.taskAgents; return; }
        if (Date.now() - agentsLoadedAt < 30000) return;
        if (agentsPending) return agentsPending;
        agentsPending = (async () => {
        try {
          const latest = await readOrderedNodes(ctx.api);
          if (!ctx.current()) return;
          agents = latest; agentsVerified = true; ctx.state.data.taskAgents = agents; syncAgents(); if (lastTasks.length) draw(lastTasks);
        } catch (error) {
          if (error.name === 'AbortError') throw error;
          if (!ctx.current()) return;
          if (error.status === 403) { agents = []; agentsVerified = false; delete ctx.state.data.taskAgents; }
        }
        agentsLoadedAt = Date.now();
        })().finally(() => { agentsPending = null; });
        return agentsPending;
      };
      const updateSettings = async () => {
        if (!ctx.can('settings.read') || Date.now() - settingsLoadedAt < 30000) return;
        try {
          const settings = await ctx.api('/settings');
          if (!ctx.current()) return;
          if (!preferencesAdopted) {
            preferencesAdopted = true;
            if (!queryTouched && !ctx.state.data.taskFilters && panelSettingOptions.task_page_size.includes(settings.task_page_size)) {
              filters.limit = filter.elements.limit.value = String(settings.task_page_size);
              filter.requestSubmit();
            }
          }
        }
        catch (error) { if (error.name === 'AbortError') throw error; }
        if (ctx.current()) settingsLoadedAt = Date.now();
      };
      void updateAgents().catch(() => {});
      void updateSettings().catch(() => {});
      const tasks = await ctx.api(query('/tasks', ctx.state.data.taskFilters || filters));
      if (!ctx.current() || sequence !== readSequence) return;
      draw(tasks); tasksLoaded = true; refreshState.textContent = '正在实时更新';
    } catch (error) {
      if (ctx.current() && sequence === readSequence) {
        if (error.status === 403) { draw([]); refreshState.textContent = '任务访问权限已变化'; }
        else refreshState.textContent = tasksLoaded ? '更新失败，保留上次数据' : '任务读取失败，请重试';
      }
      throw error;
    }
  }
  taskTable.setLoading(true);
  const initialRead = readRegion(ctx, '任务', refresh, () => {}, {view:taskTable});
  void initialRead.load();
  ctx.onRefresh(initialRead.load);
  poll(ctx, refresh, pollDelay);
  return stack(filterbar(filter), initialRead.element, refreshState, section(null, results));
}

export async function quotaPage(ctx) {
  const invitations = quotaInvitations(ctx);
  const managedTable = table(["节点", "状态", "操作"], [], { label: "可管理的共享 Agent", rowKey: row => row[2].dataset.sharingAgent });
  const management = section("共享 Agent 设置", managedTable);
  let sequence = 0;
  async function readManaged() {
    const expected = ++sequence;
    const managed = ctx.can("agents.read") ? (await readOrderedNodes(ctx.api)).filter(agent => agent.can_manage !== false && ctx.can("agents.manage", agent)) : [];
    if (!ctx.current() || sequence !== expected) return;
    managedTable.updateRows(managed.map(agent => [node("strong", {}, agent.name), status(agent.status),
      button("共享设置", () => editAgentSharing(ctx, agent), { "data-sharing-agent": agent.id })]));
    management.hidden = !managed.length;
  }
  managedTable.setLoading(true);
  const managedRead = readRegion(ctx, '可管理的共享节点', readManaged, () => {}, {view:managedTable});
  void managedRead.load();
  ctx.onRefresh(() => Promise.all([invitations.refresh(), managedRead.load()]));
  return stack(managedRead.element, management, invitations);
}
