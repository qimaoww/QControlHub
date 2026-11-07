import { quotaInvitations } from "./invitations.js";
import { listen, patch, poll } from "./live-dom.js";
import { currentTheme } from '../modules/theme-lifecycle.js';
import { formatDate } from "./appearance.js";
import { bindSourceEditorTools } from "./source-editor.js";
import { editAgentSharing } from "./sharing.js";
import { aggregateDashboardTrafficDays, summarizeDashboardTraffic, summarizeDashboardNodes, rankDashboardTraffic, utcMonth, panelMetricUsage } from "../modules/dashboard-model.js";
import { metricTrack } from './metric-track.js';
import { orderNodesBySavedOrder, readOrderedNodes } from "../modules/node-order.js";
import { diagnosticError, redactDiagnostic } from "../modules/errors.js";
import { taskRetryState } from "../modules/task-model.js";
import { autoFilter, filterbar, node, button, actions, stack, grid, section, table, badge, field, code, muted, link, query, pathID, list, notice } from "./ui.js";

export const bytes = value => {
  const amount = Math.max(0, Number(value) || 0);
  const exponent = amount ? Math.max(0, Math.min(4, Math.floor(Math.log(amount) / Math.log(1024)))) : 0;
  return `${(amount / 1024 ** exponent).toLocaleString("zh-CN", { maximumFractionDigits: exponent ? 1 : 0 })}\u00a0${["B", "KB", "MB", "GB", "TB"][exponent]}`;
};
export const date = formatDate;
export const status = value => badge(({ online: "在线", offline: "离线", active: "运行中", inactive: "已停止", unknown: "未知", activating: "启动中", deactivating: "停止中", pending: "准备中", running: "执行中", succeeded: "成功", failed: "失败", canceled: "已取消", accepted: "已接受", rejected: "已拒绝" })[value] || value || "未知",
  ["online", "active", "succeeded", "accepted"].includes(value) ? "ok" : ["pending", "running"].includes(value) ? "warn" : ["offline", "failed"].includes(value) ? "bad" : "");
export const actionName = value => ({
  validate: "校验配置", deploy: "部署并重启", start: "启动服务", stop: "停止服务", restart: "重启服务", status: "查询状态",
  install: "安装内核", uninstall: "卸载内核", "read-config": "读取可导入配置", "read-managed-config": "读取托管配置",
  "import-existing": "导入现有服务", "upgrade-agent": "升级 Agent", "enable-bbr": "启用 BBR",
  "disable-bbr": "关闭 BBR", "configure-tcp": "配置 TCP", "ip-quality": "IP 质量检测",
})[value] || value;

function responsiveChart(days, signal) {
  const container = node("div", { className: "a-chart-container" });
  let currentDays = days, selectedDay = '', previous = "", width = Math.max(280, innerWidth - (innerWidth > 896 ? 256 : 0) - 96);
  const render = () => {
    const signature = JSON.stringify([Math.round(width), currentDays]);
    if (signature === previous) return;
    previous = signature;
    if (!currentDays.some(day => day.day === selectedDay)) selectedDay = '';
    const fresh = chart(currentDays, Math.max(280, width), selectedDay, day => { selectedDay = day; });
    if (container.firstChild) patch(container.firstChild, fresh); else container.append(fresh);
  };
  render();
  container.updateDays = value => { currentDays = value; render(); };
  const observer = new ResizeObserver(entries => { width = entries[0].contentRect.width; render(); });
  observer.observe(container);
  signal.addEventListener("abort", () => observer.disconnect(), { once: true });
  return container;
}

function chart(days, width, selectedDay, selectDay) {
  const height = 235, left = 62, right = 18, top = 16, bottom = 27;
  const max = Math.max(3, ...days.flatMap(day => [day.received_bytes, day.sent_bytes]));
  const points = key => days.map((day, index) => [left + index / Math.max(1, days.length - 1) * (width - left - right),
    height - bottom - day[key] / max * (height - top - bottom)]);
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.setAttribute("class", "a-chart");
  svg.setAttribute("role", "group");
  svg.setAttribute("aria-label", "所选月份每日流量：实线为接收，虚线为发送");
  const add = (tag, attrs, text) => {
    const element = document.createElementNS(svg.namespaceURI, tag);
    Object.entries(attrs).forEach(([key, value]) => element.setAttribute(key, value));
    if (text) element.textContent = text;
    svg.append(element);
    return element;
  };
  for (let i = 0; i < 4; i++) {
    const y = top + i / 3 * (height - top - bottom);
    add("path", { d: `M${left} ${y}H${width - right}`, stroke: "#e4e4e7", "stroke-dasharray": "3 4" });
    add("text", { x: left - 8, y: y + 4, "text-anchor": "end" }, bytes(max * (1 - i / 3)));
  }
  if (days.length) {
    for (const [key, label, stroke, dash] of [['received_bytes', '接收', '#3f3f46', ''], ['sent_bytes', '发送', '#62626b', '5 4']]) {
      const series = points(key);
      const line = series.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
      if (!dash) add("path", { d: `${line}L${series.at(-1)[0]} ${height - bottom}H${left}Z`, fill: "#e4e4e7", opacity: ".55" });
      add("path", { d: line, stroke, "stroke-width": "2", 'stroke-dasharray': dash, fill: "none", 'data-series': key, 'aria-label': label });
      if (days.length === 1) add('circle', { cx: series[0][0], cy: series[0][1], r: 3, fill: stroke });
    }
    days.forEach((day, index) => {
      if (index % 5 === 0 || index === days.length - 1)
        add("text", { x: points('received_bytes')[index][0], y: height - 6, "text-anchor": "middle" }, day.day.slice(5));
    });
    const xValues = points('received_bytes').map(([x]) => x);
    days.forEach((day, index) => {
      const start = index ? (xValues[index - 1] + xValues[index]) / 2 : left;
      const end = index === days.length - 1 ? width - right : (xValues[index] + xValues[index + 1]) / 2;
      const label = `${day.day} · UTC · 接收 ${bytes(day.received_bytes)} · 发送 ${bytes(day.sent_bytes)} · 合计 ${bytes(day.received_bytes + day.sent_bytes)}`;
      const band = add('rect', { x: start, y: top, width: end - start, height: height - top - bottom,
        fill: 'currentColor', 'fill-opacity': selectedDay === day.day ? '.045' : '0', 'pointer-events': 'all',
        'data-chart-day': day.day, 'data-refresh-key': `chart-day-${day.day}`, title: label, role: 'button',
        'aria-label': label, tabindex: selectedDay === day.day || !selectedDay && index === 0 ? '0' : '-1' });
      const select = owner => {
        selectDay(owner.dataset.chartDay);
        owner.closest('svg').querySelectorAll('[data-chart-day]').forEach(item => {
          item.setAttribute('fill-opacity', item === owner ? '.045' : '0'); item.setAttribute('tabindex', item === owner ? '0' : '-1');
        });
      };
      listen(band, 'pointerenter', event => select(event.currentTarget));
      listen(band, 'focus', event => select(event.currentTarget));
      listen(band, 'pointerleave', event => {
        if (document.activeElement === event.currentTarget) return;
        selectDay(''); event.currentTarget.setAttribute('fill-opacity', '0');
      });
      listen(band, 'pointerup', event => {
        event.currentTarget.focus({ preventScroll: true });
        currentTheme('analytics')?.motion?.tooltip?.(event.currentTarget);
      });
      listen(band, 'keydown', event => {
        const items = [...event.currentTarget.closest('svg').querySelectorAll('[data-chart-day]')], current = items.indexOf(event.currentTarget);
        const next = ({ ArrowLeft: Math.max(0, current - 1), ArrowRight: Math.min(items.length - 1, current + 1), Home: 0, End: items.length - 1 })[event.key];
        if (next != null) {
          event.preventDefault(); items[next].focus({ preventScroll: true });
          currentTheme('analytics')?.motion?.tooltip?.(items[next]);
        } else if (['Enter', ' '].includes(event.key)) { event.preventDefault(); currentTheme('analytics')?.motion?.tooltip?.(event.currentTarget); }
      });
    });
  }
  return svg;
}

export async function dashboardPage(ctx) {
  let month = ctx.state.data.dashboardMonth || utcMonth();
  const can = permission => ctx.can(permission);
  const readings = new Map();
  const states = new Map();
  const requests = new Map();
  const finite = value => typeof value === "number" && Number.isFinite(value) && value >= 0;
  const sampling = muted("正在读取资源采样");
  sampling.setAttribute("role", "status");
  const resourceGrid = node("div", { className: "a-stack" });
  const stat = (title, value, subtitle) => node("div", { className: "a-stat", "data-refresh-key": title },
    node("div", { className: "a-stat-heading" }, muted(title)), node("strong", {}, value), muted(subtitle));
  const overviewGrid = node('div', { className: can('agents.read') && can('metrics.read') ? 'a-stat-grid' : 'a-form-grid' });
  function paintMetrics() {
    const m = readings.get("metrics") || {};
    const valid = kind => panelMetricUsage(m, kind) !== null;
    const percent = kind => valid(kind) ? `${panelMetricUsage(m, kind).toFixed(1)}%` : "—";
    const capacity = kind => valid(kind) ? `已用 ${bytes(m[`${kind}_used_bytes`])} / ${bytes(m[`${kind}_total_bytes`])}` : "本次采样不可用";
    const network = m.network_available && finite(m.network_rx_bps) && finite(m.network_tx_bps);
    const meter = (title, kind, detail) => node('div', { className: 'a-stat', 'data-refresh-key': title },
      node('div', { className: 'a-stat-heading' }, muted(title)), node('strong', {}, percent(kind)),
      metricTrack(parseFloat(percent(kind)), valid(kind)), muted(detail));
    patch(resourceGrid, stack(node('div', { className: 'a-form-grid' },
      meter("CPU 使用率", 'cpu', valid("cpu") ? "全部逻辑核心平均值" : "本次采样不可用"),
      meter("内存使用率", 'memory', capacity("memory")),
      meter("根目录磁盘", 'disk', capacity("disk"))),
      node('div', { className: 'a-probe-footer' }, node('div', { className: 'a-probe-network' },
        node('div', { className: 'a-stack', 'data-refresh-key': '网络接收速率' }, muted('网络接收速率'), node('strong', {}, network ? `${bytes(m.network_rx_bps)}/s` : '—'),
          muted(`累计 ${m.network_available && finite(m.network_rx_bytes) ? bytes(m.network_rx_bytes) : '—'}`)),
        stack(muted('网络发送速率'), node('strong', {}, network ? `${bytes(m.network_tx_bps)}/s` : '—'),
          muted(`累计 ${m.network_available && finite(m.network_tx_bytes) ? bytes(m.network_tx_bytes) : '—'}`))))));
    const time = Date.parse(m.collected_at);
    const collected = Number.isFinite(time) && time > 0;
    const stale = collected && Date.now() - time > 15000;
    sampling.textContent = states.get("metrics") === "denied" ? "资源查看权限已变化" : states.get("metrics") === "failed" ? (collected ? "刷新失败 · 保留上次采样" : "资源读取失败 · 自动重试中") : !collected ? "尚无有效采样" : stale ? `采样已过期 · ${date(m.collected_at)}` : `最近采样 ${date(m.collected_at)}`;
  }
  const host = can("panel-metrics.read") && section("面板运行资源",
    resourceGrid, sampling,
    "当前控制面运行环境；容器部署仅反映容器可见的资源范围。");
  const businessState = muted("正在读取业务状态");
  businessState.setAttribute("role", "status");
  const overviewCard = section('运行概况', overviewGrid, businessState);
  const nodeSummary = actions();
  const nodeTable = table(["节点", "运行内核"], [], { label: "节点运行状态", columnWidths: ['72%', '28%'], emptyText: "尚未添加可访问的节点", paginate: 4, rowKey: row => row[0].querySelector("a").getAttribute("href") });
  const nodes = can("agents.read") && section("节点运行状态", stack(node("div", { className: "a-card-body" }, nodeSummary), nodeTable), link("管理节点", "#node-settings"));
  const plot = responsiveChart([], ctx.signal);
  const trafficSummary = (receive, sent) => node('div', { className: 'a-stat', 'data-refresh-key': '月累计流量' },
    node('div', { className: 'a-stat-heading' }, muted('月累计流量')), node('strong', {}, receive == null ? '—' : bytes(receive + sent)), muted('接收与发送合计'),
    node('div', { className: 'a-probe-network' }, stack(muted('累计接收'), node('b', {}, receive == null ? '—' : bytes(receive))), stack(muted('累计发送'), node('b', {}, sent == null ? '—' : bytes(sent)))));
  const trafficTotals = trafficSummary();
  const trafficState = muted("正在读取流量记录");
  const trafficPeriod = muted(`${month} · UTC`);
  const trafficTrends = actions();
  const trafficRanking = node('div', { className: 'a-form-grid', 'aria-label': '节点流量排行' });
  const monthField = field("月份", "traffic_month", month, { type: "month", max: utcMonth(), required: true });
  const monthInput = monthField.querySelector("input");
  monthInput.addEventListener("change", () => {
    if (!monthInput.checkValidity()) return;
    month = monthInput.value;
    ctx.state.data.dashboardMonth = month;
    void read("traffic", query("/traffic-usage", { month }), true);
  });
  const detailTable = table(["日期", "接收", "发送", "合计", "接收峰值", "发送峰值"], [], { label: "每日流量明细", paginate: 10, rowKey: row => row[0] });
  const detailPeriod = muted("");
  let trafficDialog;
  const detailsButton = button("流量明细", () => {
    if (trafficDialog?.open) return;
    trafficDialog = ctx.dialog("流量明细", stack(detailPeriod, detailTable, actions(link("流量管理", "#traffic"))), { wide: true });
  });
  const trafficCard = can("traffic.read") && section("流量概况", stack(
    node('div', { className: 'a-card-body' }, node('div', { className: 'a-ipq-filter' },
      node('div', { className: 'a-form-fields' }, monthField), detailsButton)),
    node('div', { className: 'a-ipq-layout' }, trafficTotals, node('div', { className: 'a-card-body' }, stack(plot,
      node("div", { className: "a-chart-meta" }, muted('实线 接收 · 虚线 发送'), trafficPeriod)))),
    node('div', { className: 'a-card-body' }, stack(trafficTrends,
      node('div', { className: 'a-filterbar' }, node('h3', {}, '用量最高的节点'), trafficState), trafficRanking))));
  function paintBusiness() {
    const overview = readings.get("overview") || {};
    const agents = orderNodesBySavedOrder(list(readings.get("agents")), readings.get("order")?.ids);
    const failed = [...states].filter(([key, state]) => key !== "metrics" && key !== "traffic" && ["failed", "denied"].includes(state));
    const waiting = ["overview", ...(can("agents.read") ? ["agents"] : [])].some(key => !readings.has(key) && (!states.has(key) || states.get(key) === "loading"));
    businessState.textContent = waiting ? "正在读取业务状态" : failed.length ? "部分业务状态不可用 · 自动重试中" : "业务状态每 5 秒更新";
    const knownNodes = readings.has("overview") || readings.has("agents");
    const online = knownNodes ? overview.agents_online ?? agents.filter(agent => agent.status === "online").length : "—";
    const count = knownNodes ? overview.agents ?? agents.length : "—";
    const fleet = summarizeDashboardNodes(agents);
    const knownRuntime = readings.has('agents');
    patch(overviewGrid, node('div', { className: overviewGrid.className },
      stat('在线节点', online, `共 ${count} 个节点${knownNodes ? ` · 离线 ${Math.max(0, count - online)}` : ''}`),
      can('agents.read') ? stat('运行内核', knownRuntime ? fleet.running : '—', knownRuntime ? `共 ${fleet.installed} 个已安装实例` : '正在读取实例状态') : stat('节点配置', overview.node_configs ?? '—', '当前获授权的节点配置'),
      can('agents.read') && can('metrics.read') && stat('节点速率合计', fleet.sampled ? `${bytes(fleet.receiveBPS + fleet.sendBPS)}/s` : '—', fleet.sampled ? `接收 ${bytes(fleet.receiveBPS)}/s · 发送 ${bytes(fleet.sendBPS)}/s · ${fleet.sampled} 个节点` : '暂无有效的在线节点采样'),
      stat('配置档案', overview.configs ?? '—', `节点配置 ${overview.node_configs ?? '—'}`)));
    patch(nodeSummary, actions(...fleet.engines.map(item => badge(`${({mihomo:'Mihomo',xray:'Xray','sing-box':'sing-box','ss-rust':'SS Rust'})[item.engine] || item.engine} ${item.running}/${item.installed}`)),
      !fleet.engines.length && muted('暂无内核运行记录')));
    if (readings.has("agents") || states.get("agents") === "denied") nodeTable.updateRows(agents.map(agent => [
      stack(actions(node("a", { href: `#node-settings?agent=${pathID(agent.id)}` }, agent.name), status(agent.status)), muted(`最近心跳 ${date(agent.last_seen)}`)),
      (() => { const current = summarizeDashboardNodes([agent]); return current.installed ? `${current.running} / ${current.installed}` : '—'; })()]));
  }
  function paintTraffic() {
    const traffic = readings.get("traffic");
    if (traffic) {
      const days = aggregateDashboardTrafficDays(traffic.days, traffic.month).filter(day => day.day <= new Date().toISOString().slice(0, 10));
      const summary = summarizeDashboardTraffic(days, traffic.month);
      trafficPeriod.textContent = `${traffic.month} · UTC`;
      detailPeriod.textContent = `${traffic.month} · UTC`;
      const receive = days.reduce((sum, day) => sum + day.received_bytes, 0), sent = days.reduce((sum, day) => sum + day.sent_bytes, 0);
      patch(trafficTotals, trafficSummary(receive, sent));
      patch(trafficTrends, actions(muted(summary.isCurrentMonth ? `UTC 今日 ${bytes(summary.todayBytes)}` : "历史月份"),
        muted(`日均 ${summary.averageBytes == null ? "—" : bytes(summary.averageBytes)} · ${summary.elapsedDays} 天`),
        muted(`峰值日 ${summary.peakDay || "—"} · ${bytes(summary.peakBytes)}`)));
      detailTable.updateRows(days.map(day => [day.day, bytes(day.received_bytes), bytes(day.sent_bytes), bytes(day.received_bytes + day.sent_bytes), `${bytes(day.peak_receive_bps)}/s`, `${bytes(day.peak_send_bps)}/s`]));
      plot.updateDays(days);
      paintRanking();
    } else if (states.get("traffic") === "denied") {
      trafficTotals.replaceChildren(); trafficTrends.replaceChildren(); trafficRanking.replaceChildren(); plot.updateDays([]); detailTable.updateRows([]);
    }
    detailsButton.disabled = !traffic;
    trafficState.textContent = states.get("traffic") === "denied" ? "流量查看权限已变化" : states.get("traffic") === "loading" ? "正在读取" : states.get("traffic") === "failed" ? (traffic ? "刷新失败 · 保留已有记录" : "流量读取失败 · 自动重试中") : traffic ? (list(traffic.days).length ? "每日流量" : "所选月份暂无流量记录") : "尚无流量记录";
  }
  function paintRanking() {
    const traffic = readings.get('traffic');
    if (!traffic) return;
    const agents = list(readings.get('agents'));
    const ranked = rankDashboardTraffic(traffic.days, traffic.month, undefined, agents.map(agent => agent.id)), total = ranked.reduce((sum, entry) => sum + entry.used, 0);
    patch(trafficRanking, node('div', { className: 'a-form-grid', 'aria-label': '节点流量排行' }, ranked.length ? ranked.slice(0, 3).map(entry =>
      node('div', { className: 'a-probe-meter', 'data-refresh-key': entry.agentID },
        node('div', { className: 'a-probe-meter-label' }, node('a', { href: `#traffic?agent=${pathID(entry.agentID)}` }, agents.find(agent => agent.id === entry.agentID)?.name || '关联节点'), node('strong', {}, bytes(entry.used))),
        metricTrack(total ? entry.used / total * 100 : 0, true, false))) : muted('尚无按节点归集的流量记录')));
  }
  async function read(key, path, force = false) {
    if (!ctx.current() || requests.has(key) && !force) return;
    requests.get(key)?.abort();
    const controller = new AbortController();
    requests.set(key, controller);
    if (!readings.has(key) || force) {
      states.set(key, "loading");
      if (key === "traffic") paintTraffic();
    }
    const requestedMonth = month;
    try {
      const value = await ctx.api(path, { signal: AbortSignal.any([ctx.signal, controller.signal]) });
      if (!ctx.current() || requests.get(key) !== controller) return;
      if (key === "traffic" && value.month !== requestedMonth) throw new Error("流量月份不匹配");
      readings.set(key, value); states.set(key, "ready");
    } catch (error) {
      if (!ctx.current() || requests.get(key) !== controller || error.name === "AbortError") return;
      if (error.status === 403) { readings.delete(key); states.set(key, "denied"); }
      else states.set(key, "failed");
    } finally {
      if (ctx.current() && requests.get(key) === controller) {
        requests.delete(key);
        if (key === "metrics") paintMetrics();
        else if (key === "traffic") paintTraffic();
        else { paintBusiness(); if (key === 'agents') paintRanking(); }
      }
    }
  }
  function refresh() {
    const sources = [["overview", "/overview"],
      ...(can("agents.read") ? [["agents", "/agents"]] : []),
      ...(can("agents.read") ? [["order", "/preferences/node-order"]] : []),
      ...(can("panel-metrics.read") ? [["metrics", "/panel-metrics"]] : []),
      ...(can("traffic.read") ? [["traffic", query("/traffic-usage", { month })]] : [])];
    return Promise.all(sources.map(([key, path]) => read(key, path)));
  }
  ctx.onRefresh(refresh);
  ctx.signal.addEventListener("abort", () => { requests.forEach(controller => controller.abort()); trafficDialog?.close(); }, { once: true });
  paintMetrics(); paintTraffic(); paintBusiness();
  void refresh();
  poll(ctx, () => { void refresh(); }, 5000);
  return stack(overviewCard, host, trafficCard, nodes);
}

export async function taskDetail(ctx, taskID) {
  if (!ctx.can("tasks.read")) return;
  let task = await ctx.api(`/tasks/${pathID(taskID)}`);
  if (!ctx.current()) return;
  const state = status(task.status), failure = notice(diagnosticError(task.error || ""));
  const output = code(redactDiagnostic(task.output).trim());
  output.hidden = !output.textContent;
  const diagnosis = stack();
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
  const content = stack(actions(state, badge(task.engine), badge(actionName(task.action))),
    muted(`节点 ${task.agent_id} · ${date(task.created_at)}`), failure, diagnosis, output);
  const box = ctx.dialog("任务详情", content, { wide: true });
  box.classList.add("a-task-dialog");
  let timer, reading = false;
  const stop = () => clearTimeout(timer);
  const active = () => ctx.current() && box.open;
  const refresh = button("刷新结果", event => void ctx.mutate(event.currentTarget, read));
  content.append(actions(refresh));
  async function read() {
    if (reading || !active()) return;
    reading = true;
    try {
      const latest = await ctx.api(`/tasks/${pathID(taskID)}`);
      if (!active()) return;
      task = latest;
      paintDiagnosis();
      const next = status(task.status);
      if (state.textContent !== next.textContent) { state.textContent = next.textContent; state.className = next.className; }
      const error = diagnosticError(task.error || "");
      if (failure.textContent !== error) failure.textContent = error;
      const text = redactDiagnostic(task.output).trim();
      if (output.textContent !== text) {
        const follow = output.scrollHeight-output.scrollTop-output.clientHeight<24;
        const selection = getSelection(), previous = output.firstChild;
        const selected = previous?.nodeType === Node.TEXT_NODE && selection?.anchorNode === previous && selection.focusNode === previous
          ? [selection.anchorOffset, selection.focusOffset] : null;
        if (previous?.nodeType === Node.TEXT_NODE) previous.data = text;
        else output.textContent = text;
        if (selected && output.firstChild) selection.setBaseAndExtent(output.firstChild, Math.min(selected[0], text.length), output.firstChild, Math.min(selected[1], text.length));
        output.hidden = !text;
        if(follow)output.scrollTop=output.scrollHeight;
      }
    } catch (error) {
      if (active() && error.status === 403) {
        output.textContent = ''; output.hidden = true; state.textContent = '无权读取';
        failure.textContent = '无权读取此任务。';
        task = { ...task, status: 'denied' }; stop();
        paintDiagnosis();
      }
      throw error;
    } finally { reading = false; }
  }
  async function poll() {
    try { if (!document.hidden && active()) await read(); }
    catch (error) { if (active() && error.name !== "AbortError") failure.textContent = error.message; }
    finally { if (active() && ["pending","running"].includes(task.status)) timer=setTimeout(poll,2000); }
  }
  box.addEventListener("close", stop, {once:true});
  ctx.signal.addEventListener("abort", stop, {once:true});
  if(["pending","running"].includes(task.status))timer=setTimeout(poll,2000);
}

export async function tasksPage(ctx) {
  const filters = ctx.state.data.taskFilters || { limit: "100" };
  let readSequence = 0, filtering = false;
  const filter = autoFilter(ctx, [
    node("div", { className: "a-form-grid" },
      field("状态", "status", filters.status || "", { options: [["", "全部"], ["pending", "准备中"], ["running", "执行中"], ["succeeded", "成功"], ["failed", "失败"], ["canceled", "已取消"]] }),
      field("节点 ID", "agent_id", filters.agent_id || ""),
      field("操作", "action", filters.action || "", { options: [["", "全部操作"], ...["validate", "deploy", "install", "uninstall", "start", "stop", "restart", "status", "upgrade-agent", "ip-quality", "enable-bbr", "disable-bbr", "configure-tcp"].map(action => [action, actionName(action)])] }),
      field("条数", "limit", filters.limit || "100", { type: "number", min: 1, max: 500 })),
  ], async (values, _, request) => {
    const sequence = ++readSequence; filtering = true;
    try {
      const tasks = await request.api(query("/tasks", values));
      if (!request.current() || sequence !== readSequence) return;
      ctx.state.data.taskFilters = values; draw(tasks);
    } finally { if (sequence === readSequence) filtering = false; }
  }, { status: "", agent_id: "", action: "", limit: "100" });
  const results = node("div");
  const taskTable = table(["操作", "节点", "内核", "状态", "创建时间", "操作"], [], { label: "任务", rowKey: row => row[5].dataset.taskId });
  results.append(taskTable);
  const refreshState = muted('正在实时更新'); refreshState.setAttribute('role', 'status');
  async function editRetry(task) {
    const resource = await ctx.api(`/tasks/${pathID(task.id)}/retry-editor`);
    if (!ctx.current()) return;
    const { config } = resource;
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
      await taskDetail(ctx, result.task.id);
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
    taskTable.updateRows(list(tasks).map(task => {
      const retryState = taskRetryState(task);
      return [
        actionName(task.action), task.agent_id, task.engine, stack(status(task.status), retryState.missingConfigVersion && muted("缺少配置历史关联")), date(task.created_at),
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
            (ctx.can("agent-config.write") || ctx.can("configs.write")) && button("编辑后重试", event => void ctx.mutate(event.currentTarget, () => editRetry(task))),
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
    }));
  }
  async function refresh() {
    if (filtering) return;
    const sequence = ++readSequence;
    try {
      const tasks = await ctx.api(query('/tasks', ctx.state.data.taskFilters || filters));
      if (!ctx.current() || sequence !== readSequence) return;
      draw(tasks); refreshState.textContent = '正在实时更新';
    } catch (error) {
      if (ctx.current() && sequence === readSequence) {
        if (error.status === 403) { draw([]); refreshState.textContent = '任务访问权限已变化'; }
        else refreshState.textContent = '更新失败，保留上次数据';
      }
      throw error;
    }
  }
  await refresh();
  ctx.onRefresh(refresh);
  poll(ctx, refresh);
  return stack(filterbar(filter), refreshState, section(null, results));
}

export async function quotaPage(ctx) {
  const invitations = await quotaInvitations(ctx);
  if (!ctx.current()) return invitations;
  const managedTable = table(["节点", "状态", "操作"], [], { label: "可管理的共享 Agent", rowKey: row => row[2].dataset.sharingAgent });
  const management = section("共享 Agent 设置", managedTable);
  let sequence = 0;
  async function readManaged() {
    const expected = ++sequence;
    const managed = ctx.can("agents.read") ? (await readOrderedNodes(ctx.api)).filter(agent => agent.can_manage !== false && ctx.can("agents.manage", agent)) : [];
    if (!ctx.current() || sequence !== expected) return;
    managedTable.updateRows(managed.map(agent => [node("strong", {}, agent.name), status(agent.status),
      button("共享设置", event => void ctx.mutate(event.currentTarget, () => editAgentSharing(ctx, agent)), { "data-sharing-agent": agent.id })]));
    management.hidden = !managed.length;
  }
  await readManaged();
  ctx.onRefresh(() => Promise.all([invitations.refresh(), readManaged()]));
  return stack(management, invitations);
}
