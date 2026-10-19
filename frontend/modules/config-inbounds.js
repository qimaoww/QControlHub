import { redactDiagnostic } from "./errors.js";
import { bindEvent } from "./refresh.js";
import { bindConfigOutbounds } from "./config-outbounds.js";
import { bindConfigMenu } from "./config-menu.js";
import { updateFeedback, updateFeedbackHTML, setVisible } from "./presence-motion.js";
import { bindDialogBackdrop, closePopup, closeRetiringDialog } from "./popup.js";
import { composeConfigWorkspaceToolbar } from "./config-workspace-toolbar.js";
export function bindConfigInbounds(ctx) {
  const { state, api, can, esc, engineName, notify, confirmAction, agent, engine, saved, workspace,
    form, files, selection, container, sourceMode, workspaceRecord, workspaces, sourceContent, mountEditor, beforeDeploy, onSaved, renderConfigDiff } = ctx;
  if (!container) return;
  const data = state.data, epoch = state.navigationEpoch;
  const current = () => state.data === data && state.navigationEpoch === epoch && container.isConnected &&
    state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine && state.data.liveConfigSource === sourceMode;
  const input = form?.querySelector("[data-code-input]");
  const baseline = input?.value;
  const target = () => files?.selectedInbound() || (!files && selection?.selectedInbound()) || null;
  const commonSelected = () => Boolean(files ? files.selectedCommon() : selection?.selectedCommon());
  const dirty = () => files ? files.dirty() : Boolean(input && input.value !== baseline);
  const writable = () => can("agent-config.write") && can("tasks.execute") && sourceMode !== "import" && !workspaceRecord?.conflict &&
    !agent.runtime?.[engine]?.existing_config_unsupported_reason;
  const editable = chosen => dirty() || workspace.inbounds?.find(item => item.tag === chosen?.tag && item.port === chosen?.port);
  let busy = false, dialog = null;
  const navigation = container.querySelector("[data-config-object-tools]");
  const menu = container.querySelector("[data-config-inbound-menu]");
  if (!navigation || !menu) return;
  bindConfigMenu(menu);
  bindConfigOutbounds({ navigation, container, api, agent, engine, saved, current, content:() => files?.content() ?? sourceContent, writable, notify, confirmAction, beforeDeploy, onSaved, state, workspaceRecord, workspaces, selectedInbound:target, selectedOutbound:() => files?.selectedOutbound(), input, canReadPeers:can("client-access.read") });
  const addInbound = container.querySelector('[data-inbound-action="add"]');
  const tools = composeConfigWorkspaceToolbar({ can, container, form, navigation, sourceMode, agent, engine });
  const actionKind = button => button.dataset.commonAction ? `common-${button.dataset.commonAction}` : button.dataset.inboundAction;
  const isMutation = kind => ["add", "modify", "delete", "advanced", "common-add", "common-modify", "common-delete"].includes(kind);
  const triggers = [addInbound, ...menu.querySelectorAll("[data-inbound-action], [data-common-action]"),
    ...tools.querySelectorAll("[data-inbound-action]")].filter(Boolean);
  const update = () => {
    const common = commonSelected();
    setVisible(menu, !(!common && !target() && Boolean(files)));
    setVisible(menu.querySelector("[data-common-actions]"), !(!common));
    menu.querySelector("[data-config-operation-label]").textContent = common ? "公共配置操作" : target() || !files ? "入站操作" : "配置操作";
    for (const button of triggers) {
      const kind = actionKind(button), commonAction = kind.startsWith("common-");
      setVisible(button, !(["modify", "delete"].includes(kind) && common));
      button.disabled = busy || (isMutation(kind) && !writable()) || (commonAction && !common) ||
        (kind !== "add" && !saved) || (["modify", "delete"].includes(kind) && !target()) ||
        (kind === "modify" && !editable(target()));
      button.title = commonAction && !saved ? "请先通过“增加入站”创建配置，再编辑通用配置项" :
        commonAction && !common ? "请先选择公共配置；合并预览不可操作通用配置项" :
        ["modify", "delete"].includes(kind) && !target() ? "请先选择一个入站；公共配置和合并预览不可修改或删除" :
        kind === "modify" && !editable(target()) ? "此入站无法还原为预设参数，请使用源码或高级字段编辑" : "";
    }
  };
  bindEvent(input, "input", update, {bindingKey:"config-inbounds"});
  bindEvent(input, "config-selection", update, {bindingKey:"config-inbounds"});
  update();
  bindEvent(tools.querySelector("[data-config-client]"), "click", () => {
    state.data.accessAgent = agent.id;
    state.data.accessEngine = engine;
  });
  const mutationReady = () => {
    if (!writable()) return false;
    if (workspaceRecord?.conflict) { notify("服务端版本已变化，请先核对保留的草稿。", "error"); return false; }
    if (agent.runtime?.[engine]?.installed && sourceContent === undefined) {
      notify("请先读取当前节点配置，再操作配置项。", "error"); return false;
    }
    try { files?.content(); }
    catch (error) { notify(`请先修正源码：${error.message}`, "error"); return false; }
    return true;
  };
  const open = async (kind, trigger) => {
    closePopup(menu);
    if (busy || dialog || !current()) return;
    const mutation = isMutation(kind), commonAction = kind.startsWith("common-");
    if (commonAction && (!commonSelected() || !saved)) return;
    if (mutation && !mutationReady()) return;
    const chosen = !commonAction && target() ? { ...target() } : null;
    if (["modify", "delete"].includes(kind) && !chosen) return;
    busy = true; update();
    let editor, confirming = false;
    dialog = document.createElement("dialog");
    const opened = dialog;
    opened.className = `config-inbound-dialog${["add", "modify"].includes(kind) ? " config-outbound-dialog" : ""}${kind === "delete" ? " config-inbound-delete" : commonAction ? " config-common-dialog" : ""}`;
    opened.setAttribute("aria-labelledby", "config-inbound-title");
    const titles = { add:"增加入站", modify:"修改入站", delete:"删除入站", advanced:"高级字段", history:"版本历史", diff:"配置差异",
      "common-add":"增加通用配置项", "common-modify":"修改通用配置项", "common-delete":"删除通用配置项" };
    opened.innerHTML = `<header class="config-inbound-heading"><div><h2 id="config-inbound-title">${titles[kind]}</h2><p>${esc(agent.name)} / ${esc(engineName(engine))}${commonAction ? " / 公共配置" : chosen && kind !== "add" ? ` / ${esc(chosen.tag)} · :${Number(chosen.port)}` : ""}</p></div><button class="config-access-close" type="button" data-inbound-close aria-label="关闭弹窗">×</button></header><div class="config-inbound-body" data-inbound-body><p role="status">正在加载…</p></div>`;
    const body = opened.querySelector("[data-inbound-body]");
    const active = () => current() && dialog === opened && opened.isConnected;
    const dispose = (discard = false) => {
      state.routeSignal?.removeEventListener("abort", abort);
      editor?.dispose(discard);
      if (workspaceRecord) { workspaceRecord.formEditor = null; workspaces?.capture(); }
      closeRetiringDialog(opened); opened.remove();
      if (dialog === opened) dialog = null;
      busy = false;
      if (current()) {
        update();
        (trigger.closest("details")?.querySelector("summary") || trigger).focus();
      }
    };
    const abort = () => dispose(false);
    const close = async () => {
      if (confirming || editor?.busy() || opened.dataset.saving === "1") return;
      confirming = true;
      try {
        if (editor?.dirty() && !(await confirmAction("当前弹窗有未保存的修改，确定放弃并关闭？", "放弃更改"))) return;
        dispose(true);
      } finally { confirming = false; }
    };
    bindEvent(opened.querySelector("[data-inbound-close]"), "click", close);
    bindEvent(opened, "cancel", event => { event.preventDefault(); void close(); });
    bindDialogBackdrop(opened, () => { void close(); });
    state.routeSignal?.addEventListener("abort", abort, { once:true });
    document.body.append(opened);
    opened.showModal();
    const commit = async (result, inbound = chosen) => {
      dispose(true);
      if (state.data === data) await onSaved(result, inbound);
    };
    try {
      const base = `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}`;
      let fresh = await api(`${base}/workspace`, {method:"GET"});
      if (!active()) return;
      if (mutation && (fresh.config?.version || 0) !== (saved?.version || 0)) {
        throw new Error("配置版本已变化，请关闭弹窗并重新读取、核对配置后再编辑。");
      }
      if (mutation && !mutationReady()) { dispose(); return; }
      const draftContent = mutation ? files?.content() ?? sourceContent : undefined;
      if (mutation && draftContent !== undefined && draftContent !== fresh.config?.content) {
        fresh = await api(`${base}/workspace/preview`, {method:"POST", body:JSON.stringify({
          content:draftContent, expected_version:saved?.version || 0,
        })});
        if (!active()) return;
      }
      if (commonAction && !commonSelected()) { dispose(); return; }
      if (["modify", "delete"].includes(kind) && (target()?.tag !== chosen.tag || target()?.port !== chosen.port)) {
        dispose(); return;
      }
      if (kind === "modify" && !fresh.inbounds?.some(item => item.tag === chosen.tag && item.port === chosen.port)) {
        throw new Error("此入站无法还原为协议表单，请继续使用源码或高级字段编辑。");
      }
      if (kind === "diff") {
        const deployments = await api("/deployments", {method:"GET"});
        if (!active()) return;
        const deployed = deployments.find(item => item.agent_id === agent.id && item.engine === engine && item.config_id === fresh.config?.id);
        if (!deployed) updateFeedbackHTML(body, '<p>当前已保存配置尚未部署。</p>');
        else {
          const revision = await api(`/configs/${encodeURIComponent(deployed.config_id)}/revisions/${deployed.config_version}`, {method:"GET"});
          if (!active()) return;
          updateFeedbackHTML(body, `<p>已部署 v${deployed.config_version} → 已保存 v${fresh.config.version}</p>${renderConfigDiff(fresh.config.content, revision.content) || "<p>配置内容一致。</p>"}`);
        }
      } else if (kind === "delete") {
        updateFeedbackHTML(body, `<p>确定删除入站 <strong>${esc(chosen.tag)}</strong>（端口 ${Number(chosen.port)}）？不会删除其他入站。</p><p class="validation-note">仅校验不会改变节点运行配置；部署会应用删除并重启内核。</p><div role="alert" class="event-result" data-inbound-error></div><footer class="inbound-delete-actions"><button class="button" type="button" data-delete-intent="validate">删除并校验</button><button class="button danger" type="button" data-delete-intent="deploy">删除并部署</button></footer>`);
        const buttons = [...body.querySelectorAll("[data-delete-intent]")];
        buttons.forEach(button => {
          button.disabled = !writable() || fresh.agent?.status !== "online" || !fresh.agent?.runtime?.[engine]?.installed;
          button.onclick = async () => {
            if (!active() || opened.dataset.saving === "1" || !mutationReady()) return;
            opened.dataset.saving = "1";
            buttons.forEach(item => { item.disabled = true; });
            try {
              if (button.dataset.deleteIntent === "deploy" && beforeDeploy) {
                updateFeedback(body.querySelector("[data-inbound-error]"), "正在核验 Agent 当前配置…");
                await beforeDeploy();
                if (!active()) return;
                updateFeedback(body.querySelector("[data-inbound-error]"), "");
              }
              const result = await api(`${base}/server-inbounds`, {method:"POST", body:JSON.stringify({
                operation:"delete", original_tag:chosen.tag, input:{tag:chosen.tag, port:chosen.port}, expected_version:fresh.config.version,
                name:fresh.config.name, description:fresh.config.description, intent:button.dataset.deleteIntent,
                content:draftContent,
              })});
              await commit(result, null);
            } catch (error) {
              if (!active()) return;
              const reason = error.status === 409 ? "配置版本已变化，请重新读取后重试。" : /统计|accounting/.test(error.message) ? "统计配置引用冲突，请查看详情。" : "请查看详情并重试。";
              updateFeedbackHTML(body.querySelector("[data-inbound-error]"), `<div class="task-diagnostic"><b>删除入站失败</b><small>${esc(reason)}</small></div><details><summary>错误详情</summary><div class="task-result-block"><pre class="task-error">${esc(redactDiagnostic(error.message))}</pre></div></details>`);
              if (error.deployPreflight || error.status && error.status < 500 && error.status !== 409)
                buttons.forEach(item => { item.disabled = false; });
            } finally { delete opened.dataset.saving; }
          };
        });
      } else {
        editor = mountEditor({ root:body, kind, sourceMode, draftContent, workspaceRecord, operationKey:workspaceRecord?.operationKey, isCurrent:active, beforeDeploy, onSaved:commit }, fresh, kind === "add" ? null : chosen);
        if (workspaceRecord) workspaceRecord.formEditor = editor;
        await editor.ready;
        body.addEventListener("input", () => workspaces?.capture());
        body.addEventListener("change", () => workspaces?.capture());
      }
    } catch (error) {
      if (active()) updateFeedbackHTML(body, `<p class="alert error" role="alert">${esc(error.message)}</p>`);
    } finally {
      busy = false;
      if (current()) update();
    }
  };
  triggers.forEach(trigger => bindEvent(trigger, "click", () => open(actionKind(trigger), trigger)));
  return { selectedInbound:target, selectedCommon:commonSelected };
}
