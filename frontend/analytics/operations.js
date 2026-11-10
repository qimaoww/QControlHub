import { readRegion, loadingTable, dialogContext, preparedDialog } from "./loading.js";
import { quotaInvitations } from "./invitations.js";
import { listen, patch, poll } from "./live-dom.js";
import { formatDate } from "./appearance.js";
import { bindSourceEditorTools } from "./source-editor.js";
import { editAgentSharing } from "./sharing.js";
import { readOrderedNodes } from "../modules/node-order.js";
import { diagnosticError, redactDiagnostic } from "../modules/errors.js";
import { coreSourceLabel, taskRetryState, taskTiming } from "../modules/task-model.js";
import { autoFilter, filterbar, node, button, actions, stack, grid, section, table, badge, field, code, muted, link, query, pathID, list, notice, copyText } from "./ui.js";

const byteFormatters = [0, 1].map(maximumFractionDigits => new Intl.NumberFormat("zh-CN", { maximumFractionDigits }));
export const bytes = value => {
  const amount = Math.max(0, Number(value) || 0);
  const exponent = amount ? Math.max(0, Math.min(4, Math.floor(Math.log(amount) / Math.log(1024)))) : 0;
  return `${byteFormatters[exponent ? 1 : 0].format(amount / 1024 ** exponent)}\u00a0${["B", "KB", "MB", "GB", "TB"][exponent]}`;
};
export const date = formatDate;
export const status = value => badge(({ online: "在线", offline: "离线", active: "运行中", inactive: "已停止", unknown: "未知", activating: "启动中", deactivating: "停止中", pending: "准备中", running: "执行中", succeeded: "成功", failed: "失败", canceled: "已取消", accepted: "已接受", rejected: "已拒绝" })[value] || value || "未知",
  ["online", "active", "succeeded", "accepted"].includes(value) ? "ok" : ["pending", "running"].includes(value) ? "warn" : ["offline", "failed"].includes(value) ? "bad" : "");
export const actionName = value => ({
  validate: "校验配置", deploy: "部署并重启", start: "启动服务", stop: "停止服务", restart: "重启服务", status: "查询状态",
  install: "安装内核", uninstall: "卸载内核", "read-config": "读取可导入配置", "read-managed-config": "读取托管配置",
  "panel-failure":"面板失败", "import-existing": "导入现有服务", "upgrade-agent": "升级 Agent", "enable-bbr": "启用 BBR",
  "disable-bbr": "关闭 BBR", "configure-tcp": "配置 TCP", "ip-quality": "IP 质量检测",
})[value] || value;

function taskEngine(task) {
  if (task.action === 'panel-failure') return '面板';
  if (task.action === 'upgrade-agent') return 'QAgent';
  if (task.action === 'ip-quality') return 'IPQuality';
  if (['enable-bbr', 'disable-bbr', 'configure-tcp'].includes(task.action)) return 'BBR / TCP';
  return task.engine;
}

function taskContext(ctx, task, targetAgent) {
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
  let timer, reading = false;
  const controller = new AbortController();
  const signal = AbortSignal.any([ctx.signal, controller.signal]);
  const stop = () => { clearTimeout(timer); controller.abort(); };
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
  async function poll() {
    try { if (!document.hidden && active()) await read({ automatic: true }); }
    catch (error) { if (active() && error.name !== "AbortError") failure.textContent = error.message; }
    finally { if (active() && task.status !== 'denied') timer=setTimeout(poll,2000); }
  }
  box.addEventListener("close", () => { stop(); ctx.signal.removeEventListener("abort", stop); }, {once:true});
  ctx.signal.addEventListener("abort", stop, {once:true});
  const initialRead = readRegion(dialogContext(ctx, box), '任务详情', () => read(), () => {});
  content.prepend(initialRead.element);
  void initialRead.load().finally(() => { if (active() && task.status !== 'denied') timer=setTimeout(poll,2000); });
}

export async function tasksPage(ctx) {
  const pageSize = ctx.state.data.settings?.task_page_size;
  const filters = ctx.state.data.taskFilters || { limit: String([50, 100, 500].includes(pageSize) ? pageSize : 100) };
  let settingsLoadedAt = ctx.state.data.settings ? Date.now() : -Infinity;
  let preferencesAdopted = Boolean(ctx.state.data.settings || ctx.state.data.taskFilters), queryTouched = false;
  const pollDelay = () => {
    const value = ctx.state.data.settings?.task_poll_interval_ms;
    return [600, 1000, 2000, 5000].includes(value) ? value : 1000;
  };
  let readSequence = 0, filtering = false, tasksLoaded = false;
  let agents = [], agentsLoadedAt = -Infinity, lastTasks = [], agentsPending;
  const agentField = field('节点', 'agent_id', filters.agent_id || '', { options: [['', '全部节点'], ...(filters.agent_id ? [[filters.agent_id, filters.agent_id]] : [])] });
  const agentPicker = agentField.querySelector('select');
  let agentSignature;
  function syncAgents() {
    const selected = agentPicker.value;
    const choices = ctx.can('agents.read') ? agents : [...new Set(lastTasks.map(task => task.agent_id))].map(id => ({ id, name: id }));
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
      field("操作", "action", filters.action || "", { options: [["", "全部操作"], ...["validate", "deploy", "install", "uninstall", "start", "stop", "restart", "status", "upgrade-agent", "ip-quality", "enable-bbr", "disable-bbr", "configure-tcp", "panel-failure"].map(action => [action, actionName(action)])] }),
      field("条数", "limit", filters.limit || "100", { type: "number", min: 1, max: 500 })),
  ], async (values, _, request) => {
    const sequence = ++readSequence; filtering = true;
    try {
      const tasks = await request.api(query("/tasks", values));
      if (!request.current() || sequence !== readSequence) return;
      ctx.state.data.taskFilters = values; draw(tasks);
    } finally { if (sequence === readSequence) filtering = false; }
  }, { status: "", agent_id: "", action: "", limit: "100" });
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
        actionName(task.action), agents.find(agent => agent.id === task.agent_id)?.name || task.agent_id || "面板", taskEngine(task), stack(actions(status(task.status)), muted(taskTiming(task)), retryState.missingConfigVersion && muted("缺少配置历史关联")), date(task.created_at),
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
        if (!ctx.can('agents.read')) { agents = []; delete ctx.state.data.taskAgents; return; }
        if (Date.now() - agentsLoadedAt < 30000) return;
        if (agentsPending) return agentsPending;
        agentsPending = (async () => {
        try {
          const latest = await readOrderedNodes(ctx.api);
          if (!ctx.current()) return;
          agents = latest; ctx.state.data.taskAgents = agents; syncAgents(); if (lastTasks.length) draw(lastTasks);
        } catch (error) {
          if (error.name === 'AbortError') throw error;
          if (!ctx.current()) return;
          if (error.status === 403) { agents = []; delete ctx.state.data.taskAgents; }
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
            if (!queryTouched && !ctx.state.data.taskFilters && [50, 100, 500].includes(settings.task_page_size)) {
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
