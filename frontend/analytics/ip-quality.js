import { patch } from "./live-dom.js";
import { nodeName } from "./node-region.js";
import { createIPQualityController } from "../modules/ip-quality-controller.js";
import { ipQualityLatest, ipQualityToday, ipQualityReportRecord, ipQualityBlockReason, ipQualityFeature, ipQualityNodeList, ipQualityTiming, ipQualityStatusName } from "../modules/ip-quality-model.js";
import { orderNodesBySavedOrder, savedNodeOrder } from "../modules/node-order.js";
import { node, button, actions, stack, grid, section, badge, field, muted, list, pathID, notice } from "./ui.js";
import { date, status, taskDetail } from "./operations.js";

export function ipQualityPage(ctx) {
  let nodeOrder = savedNodeOrder(), lastSnapshot;
  const orderReady = ctx.api('/preferences/node-order').then(order => {
    if (ctx.current() && Array.isArray(order?.ids)) { nodeOrder = order.ids; if (lastSnapshot) draw(lastSnapshot); }
  }).catch(() => {});
  const navigation = node("nav", { className: "a-ipq-nodes", "aria-label": "检测节点" });
  const pendingReport = () => section('检测报告', node('div', {className:'a-card-body a-stack'},
    node('dl', {className:'a-traffic-facts'}, ['节点', '检测时间', '状态'].map(label => node('div', {}, node('dt', {}, label), node('dd', {}, '正在读取…')))),
    grid(section('IPv4 报告', node('div', {className:'a-card-body'}, muted('正在读取检测记录…'))), section('IPv6 报告', node('div', {className:'a-card-body'}, muted('正在读取检测记录…'))))),
    actions(button('立即检测', () => {}, {disabled:true}), button('启用定时检测', () => {}, {disabled:true})));
  const results = node("div", { className: "a-ipq-detail a-stack" }, pendingReport());
  const archiveStates = new Map();
  const loading = muted("正在读取检测记录…");
  loading.setAttribute("role", "status");
  const dateField = field("检测日期", "date", "", { options: [["", "最新检测"]] });
  const select = dateField.querySelector("select");
  const reset = button("重置", () => { select.value = ""; void controller.load(ipQualityLatest); });
  const filter = node("form", { className: "a-stack a-filter-form a-auto-filter a-ipq-filter", onsubmit(event) {
    event.preventDefault(); void controller.load(select.value || ipQualityLatest);
  } }, node("fieldset", { className: "a-form-fields a-stack" }, dateField), actions(reset));
  select.addEventListener("change", () => void controller.load(select.value || ipQualityLatest));
  const state = {
    get data() { return ctx.state.data; },
    get route() { return ctx.current() ? "ip-quality" : ""; },
    navigationEpoch: 0, routeSignal: ctx.signal,
  };
  state.data.ipQualityAgent ||= state.data.ipQualityNode;
  const controller = createIPQualityController({ state, can: ctx.can, notify: ctx.announce, confirmAction: ctx.confirm,
    api: async (path, options = {}) => {
      const read = ctx.api(path, { ...options, body: typeof options.body === "string" ? JSON.parse(options.body) : options.body });
      return read;
    },
  }, draw);
  function draw(snapshot) {
    if (!ctx.current()) return;
    lastSnapshot = snapshot;
    const { history, date: selectedDate, timezone, loading: reading, readFailed, submitting, creating, creationErrors, editable } = snapshot;
    loading.textContent = readFailed ? history ? "读取失败，保留已有记录" : "检测记录读取失败，请刷新重试" : reading ? "正在读取检测记录…" : "正在实时更新";
    loading.title = snapshot.error || "";
    const available = snapshot.availableDates || [];
    const dates = selectedDate === ipQualityLatest || available.includes(selectedDate) ? available : [selectedDate, ...available];
    const signature = JSON.stringify(dates);
    if (select.dataset.dates !== signature && document.activeElement !== select) {
      select.replaceChildren(node("button", { type: "button", tabindex: "-1" }, node("selectedcontent")),
        node("option", { value: "" }, "最新检测"), ...dates.map(day => node("option", { value: day }, day)));
      select.dataset.dates = signature;
    }
    if (document.activeElement !== select) select.value = selectedDate === ipQualityLatest ? "" : selectedDate;
    select.disabled = !history && !dates.length; reset.disabled = !history && !dates.length;
    filter.toggleAttribute("aria-busy", Boolean(reading));
    const canOperate = selectedDate === ipQualityLatest || selectedDate === ipQualityToday();
    const records = list(history?.records), schedules = new Map(list(history?.schedules).map(schedule => [schedule.agent_id, schedule]));
    const byID = new Map(list(snapshot.agents).map(agent => [agent.id, agent]));
    const nodes = ipQualityNodeList(orderNodesBySavedOrder(list(snapshot.agents), nodeOrder), records, canOperate, state.data.ipQualityAgent,
      history ? "" : readFailed ? "读取失败" : "正在读取记录", selectedDate === ipQualityLatest);
    if (nodes.selected) state.data.ipQualityAgent = nodes.selected.id;
    patch(navigation, node("nav", { className: "a-ipq-nodes", "aria-label": "检测节点" }, nodes.items.map(item =>
      button(stack(nodeName(ctx, byID.get(item.id)), muted(item.note)), () => controller.select(item.id), {
        className: "a-ipq-node", "data-refresh-key": item.id, "aria-pressed": String(item.id === nodes.selected?.id),
      }))));
    const agent = nodes.selected;
    if (!agent) {
      if (!history && reading && !readFailed) { if (!results.querySelector('.a-card')) results.replaceChildren(pendingReport()); return; }
      results.replaceChildren(node("p", { className: "a-empty" }, !history ? readFailed ? "检测记录读取失败，可刷新重试" : "正在读取检测记录…" : selectedDate === ipQualityLatest ? "暂无可检测的节点" : "所选日期暂无检测记录"));
      return;
    }
    const record = records.find(record => record.agent_id === agent.id);
    const report = ipQualityReportRecord(record, selectedDate, timezone);
    const reason = !history ? "正在读取检测记录" : !canOperate ? "历史记录不能提交检测" : ipQualityBlockReason(agent, record);
    const blocked = readFailed ? "请先刷新检测记录" : reason;
    const pending = submitting?.has(agent.id);
    const tools = actions(
      canOperate && editable(agent) && button(creating?.has(agent.id) ? "任务创建中…" : "立即检测", () => void controller.runCheck(agent.id), { disabled: Boolean(blocked || pending), title: blocked, "data-ip-quality-run": agent.id }),
      canOperate && editable(agent) && button(schedules.get(agent.id)?.enabled ? "关闭定时检测" : "启用定时检测", () => void controller.setSchedule(agent.id, !schedules.get(agent.id)?.enabled), { "data-ip-quality-schedule": agent.id, "aria-pressed": String(Boolean(schedules.get(agent.id)?.enabled)), disabled: Boolean(!history || readFailed || pending || !schedules.get(agent.id)?.enabled && !agent.features?.includes(ipQualityFeature)) }),
      record?.task_id && ctx.can("tasks.read") && button("任务详情", () => void taskDetail(ctx, record.task_id)));
    const archives = list(report?.archives).map(archive => {
      const key = `${report.task_id}:${archive.family}`;
      if (!archiveStates.has(key)) archiveStates.set(key, { failed: false, reload: 0 });
      const state = archiveStates.get(key);
      const link = `/api/v1/ip-quality/${pathID(report.task_id)}/archives/${archive.family}`;
      const preview = `${link}?layout=aligned`;
      const update = (image, failed) => {
        if (failed && !state.failed) void ctx.recordFailure?.(Object.assign(new Error(`报告图片加载失败：${agent.name} · IPv${archive.family} · ${report.task_id}；浏览器未提供具体网络或图片解码原因。`),{agentID:agent.id,resource:preview}), "IP 质量报告图片", "image_loading");
        state.failed = failed;
        if (!failed && image.naturalWidth && image.naturalHeight) {
          state.width = image.naturalWidth; state.height = image.naturalHeight;
          image.setAttribute("width", state.width); image.setAttribute("height", state.height);
        }
        const view = image.closest("section");
        if (!view) return;
        view.querySelector("[role=alert]").textContent = failed ? "报告图片加载失败，请重试。" : "";
        view.querySelector("[data-report-retry]").hidden = !failed;
      };
      const image = node("img", { src: state.reload ? `${preview}&reload=${state.reload}` : preview,
        width: state.width, height: state.height,
        alt: `${agent.name} IPv${archive.family} 检测报告`, loading: "lazy", className: "a-report-image",
        onerror: event => update(event.currentTarget, true), onload: event => update(event.currentTarget, false) });
      const retry = button("重新加载图片", event => {
        state.reload++;
        const image = event.currentTarget.closest("section").querySelector("img");
        update(image, false); image.src = `${preview}&reload=${state.reload}`;
      }, { "data-report-retry": "", hidden: !state.failed });
      const view = section(`IPv${archive.family} 报告`, node("div", { className: "a-card-body" }, stack(
        node("a", { href: preview, target: "_blank", rel: "noopener noreferrer", "aria-label": `查看 IPv${archive.family} 报告原图`, title: "查看原图" }, image),
        notice(state.failed ? "报告图片加载失败，请重试。" : "", "error"), retry)),
        node("a", { className: "a-button", href: link, download: "" }, "下载报告"));
      view.dataset.refreshKey = key;
      return view;
    });
    const timing = ipQualityTiming(record);
    const lifecycle = record && `${record.status === "pending" ? "排队中，等待 Agent 领取" : record.status === "running" ? "执行中" : ipQualityStatusName(record.status)}${timing ? ` · ${timing}` : ""}`;
    const summary = section(agent.name,
      node("div", { className: "a-card-body" }, stack(
        actions(canOperate && status(agent.status), record ? status(record.status) : badge(!history ? "正在读取" : "尚未检测"), canOperate && badge(schedules.get(agent.id)?.enabled ? "定时检测已启用" : "定时检测已关闭")),
        record && muted(lifecycle),
        ["pending", "running"].includes(record?.status) && muted("检测可能需要数分钟，耗时受节点网络和探测源影响。"),
        (report || record) && actions(muted(report && report !== record ? "上次成功检测" : "检测时间"), node("time", { datetime: (report || record).finished_at || (report || record).created_at }, date((report || record).finished_at || (report || record).created_at))),
        reason && muted(reason), record?.error && notice(record.error), creationErrors?.get(agent.id) && notice(creationErrors.get(agent.id)),
        report && report !== record && muted(["pending", "running"].includes(record.status) ? "新检测进行中，保留上次成功报告" : "最新检测未成功，保留上次成功报告"),
        !archives.length && node("p", { className: "a-empty" }, report ? "该记录没有报告图片" : !history ? reading ? "正在读取检测记录…" : "检测记录读取失败，可刷新重试" : snapshot.recordsRemoved?.has(agent.id) ? "检测记录已移除" : record?.status === "succeeded" ? "检测已完成，暂无可用报告" : ["pending", "running"].includes(record?.status) ? "检测进行中，可在任务详情查看状态" : "暂无报告，可运行检测或切换日期查看记录"),
      )), tools);
    summary.dataset.ipQualityPanel = agent.id;
    patch(results, node("div", { className: "a-ipq-detail a-stack" }, summary,
      archives.length > 1 ? grid(archives) : archives));
  }
  ctx.signal.addEventListener("abort", () => { controller.dispose(); archiveStates.clear(); }, { once: true });
  ctx.onRefresh(() => controller.load());
  void controller.load();
  return stack(filter, loading, node("div", { className: "a-ipq-layout" }, navigation, results),
    node("p", { className: "a-muted" }, node("a", { href: "https://github.com/xykt/IPQuality", target: "_blank", rel: "noopener noreferrer" }, "xykt/IPQuality"), " · AGPL-3.0"));
}
