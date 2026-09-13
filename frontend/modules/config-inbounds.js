import { bindEvent, reconcileView } from "./refresh.js";
import { canonicalConfigJSON } from "./config-files.js";

// Formatting and JSON object-key order do not constitute a different source.
// YAML remains conservative: an unrecognized difference requires an explicit
// source save, never a silent replacement by the database's preset snapshot.
export function sameConfigContent(left, right) {
  const clean = value => String(value ?? "").replaceAll("\r\n", "\n").trim();
  if (clean(left) === clean(right)) return true;
  try { return canonicalConfigJSON(left) === canonicalConfigJSON(right); }
  catch { return false; }
}

// Reuse the actual preset form and field editors, not a second implementation
// of protocol options. Only the requested editor is mounted: no second source
// editor, inbound sidebar, engine selector or top-level page tabs.
export function renderEmbeddedPreset(host, markup, { viewKey, commonFields = [], selectedField }) {
  const template = document.createElement("template");
  template.innerHTML = markup;
  const source = template.content;
  const fresh = host.root.cloneNode(false);
  const content = document.createElement("div");
  content.dataset.refreshKey = viewKey;
  const toolbar = document.createElement("div");
  toolbar.className = "config-command-bar inbound-editor-toolbar";
  if (host.kind === "add") {
    const label = document.createElement("label");
    label.append("协议");
    const select = document.createElement("select");
    select.dataset.presetProtocol = "";
    source.querySelectorAll("[data-protocol]").forEach(link => {
      const option = new Option(link.querySelector("strong")?.textContent || link.textContent.trim(), link.dataset.protocol, link.classList.contains("active"), link.classList.contains("active"));
      select.append(option);
    });
    label.append(select);
    toolbar.append(label);
  } else if (host.kind.startsWith("common-") && commonFields.length) {
    const label = document.createElement("label");
    label.append("通用配置项");
    const select = document.createElement("select");
    select.dataset.commonField = "";
    commonFields.forEach(field => {
      select.append(new Option(`${field.label} · ${field.key}`, field.key,
        field.key === selectedField?.key, field.key === selectedField?.key));
    });
    label.append(select);
    toolbar.append(label);
  }
  const refresh = source.querySelector("[data-refresh-preset]");
  if (refresh) toolbar.append(refresh);
  content.append(toolbar);
  const callout = source.querySelector(".config-execution-callout");
  if (callout && host.kind !== "history") content.append(callout);
  if (["add", "modify"].includes(host.kind)) {
    const recipe = source.querySelector(".recipe-workspace");
    const operation = recipe.querySelector(".config-mutation");
    const hidden = document.createElement("input");
    hidden.type = "hidden"; hidden.name = "operation"; hidden.value = host.kind;
    operation.replaceWith(hidden);
    content.append(recipe);
  } else if (host.kind.startsWith("common-")) {
    content.append(source.querySelector("#common-options"));
  } else {
    source.querySelectorAll(host.kind === "history" ? "#revisions" : "#inbound-options, #advanced").forEach(studio => {
      studio.open = true;
      content.append(studio);
    });
  }
  fresh.append(content);
  reconcileView(host.root, fresh);
}

export function bindConfigInbounds(ctx) {
  const { state, api, can, esc, engineName, notify, confirmAction, agent, engine, saved, workspace,
    form, files, selection, container, sourceMode, sourceContent, mountEditor, onSaved, onRefresh, renderConfigDiff } = ctx;
  if (!container) return;
  const data = state.data, epoch = state.navigationEpoch;
  const current = () => state.data === data && state.navigationEpoch === epoch && container.isConnected &&
    state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine;
  const input = form?.querySelector("[data-code-input]");
  const baseline = input?.value;
  const target = () => files?.selectedInbound() || (!files && selection?.selectedInbound()) || null;
  const commonSelected = () => Boolean(files ? files.selectedCommon() : selection?.selectedCommon());
  const dirty = () => files ? files.dirty() : Boolean(input && input.value !== baseline);
  const writable = () => can("agent-config.write") && can("tasks.execute") && sourceMode !== "import" &&
    !agent.runtime?.[engine]?.existing_config_unsupported_reason;
  const editable = chosen => workspace.inbounds?.find(item => item.tag === chosen?.tag && item.port === chosen?.port);
  let busy = false, dialog = null;
  let navigation = form?.querySelector(".config-file-buttons");
  if (!navigation) {
    navigation = document.createElement("nav");
    navigation.className = "config-file-buttons config-empty-actions";
    container.querySelector(".live-config-details").after(navigation);
  }
  const menu = document.createElement("details");
  menu.className = "config-inbound-menu";
  menu.innerHTML = `<summary class="button" aria-haspopup="menu"><span data-config-operation-label>入站操作</span> <span aria-hidden="true">▾</span></summary>
    <div class="config-inbound-menu-items" role="menu">
      <div data-common-actions role="group" aria-label="通用配置操作" hidden>
        <button type="button" role="menuitem" data-common-action="add">＋ 增加通用配置项</button>
        <button type="button" role="menuitem" data-common-action="modify">修改通用配置项</button>
        <button type="button" role="menuitem" class="danger-text" data-common-action="delete">删除通用配置项</button>
      </div>
      <button type="button" role="menuitem" data-inbound-action="modify">修改入站</button>
      <button type="button" role="menuitem" class="danger-text" data-inbound-action="delete">删除入站</button>
    </div>`;
  navigation.append(menu);
  // Adding an inbound is independent of the selected file. Keep one persistent
  // button directly before merged preview, never inside the common-field menu.
  let sourceActions = form?.querySelector(".config-file-actions");
  if (!sourceActions) {
    const row = document.createElement("div");
    row.className = "config-file-navigation";
    const label = document.createElement("span");
    label.textContent = form ? "完整配置源码" : "入站配置";
    sourceActions = document.createElement("div");
    sourceActions.className = "config-file-actions";
    row.append(label, sourceActions);
    const toolbar = form?.querySelector(".code-editor-toolbar");
    if (toolbar) toolbar.after(row);
    else navigation.after(row);
  }
  const addInbound = document.createElement("button");
  addInbound.type = "button";
  addInbound.className = "button";
  addInbound.dataset.inboundAction = "add";
  addInbound.textContent = "＋ 增加入站";
  sourceActions.prepend(addInbound);
  const tools = document.createElement("nav");
  tools.className = "config-workspace-tools";
  tools.setAttribute("aria-label", "配置工具");
  tools.innerHTML = `<button class="button small" type="button" data-inbound-action="advanced">高级字段</button>
    ${can("configs.read") ? '<button class="button small" type="button" data-inbound-action="history">版本历史</button>' : ""}
    ${can("deployments.read") && can("configs.read") ? '<button class="button small" type="button" data-inbound-action="diff">配置差异</button>' : ""}
    ${can("client-access.read") ? '<a class="button small" href="#client-access" data-config-client>客户端配置 ↗</a>' : ""}
    <button class="button small" type="button" data-config-refresh>${sourceMode === "personal" ? "刷新我的配置" : agent.runtime?.[engine]?.installed ? "重新读取节点配置" : "刷新配置"}</button>`;
  container.append(tools);
  const actionKind = button => button.dataset.commonAction ? `common-${button.dataset.commonAction}` : button.dataset.inboundAction;
  const isMutation = kind => ["add", "modify", "delete", "advanced", "common-add", "common-modify", "common-delete"].includes(kind);
  const triggers = [addInbound, ...menu.querySelectorAll("[data-inbound-action], [data-common-action]"),
    ...tools.querySelectorAll("[data-inbound-action]")];
  const update = () => {
    const common = commonSelected();
    menu.querySelector("[data-common-actions]").hidden = !common;
    menu.querySelector("[data-config-operation-label]").textContent = common ? "通用配置操作" : target() || !files ? "入站操作" : "配置操作";
    for (const button of triggers) {
      const kind = actionKind(button), commonAction = kind.startsWith("common-");
      button.hidden = ["modify", "delete"].includes(kind) && common;
      button.disabled = busy || (isMutation(kind) && !writable()) || (commonAction && !common) ||
        (kind !== "add" && !saved) || (["modify", "delete"].includes(kind) && !target()) ||
        (kind === "modify" && !editable(target()));
      button.title = commonAction && !saved ? "请先通过“增加入站”创建配置，再编辑通用配置项" :
        commonAction && !common ? "请先选择公共配置；合并预览不可操作通用配置项" :
        ["modify", "delete"].includes(kind) && !target() ? "请先选择一个入站；公共配置和合并预览不可修改或删除" :
        kind === "modify" && !editable(target()) ? "此入站无法还原为预设参数，请使用源码或高级字段编辑" : "";
    }
  };
  input?.addEventListener("input", update);
  input?.addEventListener("config-selection", update);
  update();
  bindEvent(menu, "keydown", event => {
    if (event.key === "Escape") { menu.open = false; menu.querySelector("summary").focus(); }
    if (!["ArrowDown", "ArrowUp"].includes(event.key)) return;
    event.preventDefault();
    menu.open = true;
    const items = [...menu.querySelectorAll("button:not(:disabled)")].filter(button => !button.closest("[hidden]"));
    if (items.length) {
      const index = items.indexOf(document.activeElement);
      const next = index < 0 ? event.key === "ArrowDown" ? 0 : items.length - 1 :
        (index + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length;
      items[next].focus();
    }
  });
  bindEvent(menu, "focusout", event => { if (!menu.contains(event.relatedTarget)) menu.open = false; });
  bindEvent(tools.querySelector("[data-config-client]"), "click", () => {
    state.data.accessAgent = agent.id;
    state.data.accessEngine = engine;
  });
  bindEvent(tools.querySelector("[data-config-refresh]"), "click", async () => {
    if (!current() || busy) return;
    if (dirty() && !(await confirmAction("重新读取会放弃当前未保存的源码，确定继续？", "重新读取配置"))) return;
    if (!current()) return;
    try { await onRefresh(); }
    catch (error) { if (current()) notify(`读取配置失败：${error.message}`, "error"); }
  });

  const mutationReady = () => {
    if (!writable()) return false;
    if (agent.runtime?.[engine]?.installed && sourceContent === undefined) {
      notify("请先读取当前节点配置，再操作配置项。", "error"); return false;
    }
    if (dirty()) { notify("配置源码有未保存修改，请先保存，再操作配置项。", "error"); return false; }
    if (!sameConfigContent(saved?.content, sourceContent) && !(sourceMode !== "import" && !agent.runtime?.[engine]?.installed && !saved)) {
      notify("当前节点快照与已保存配置不同，请先保存当前源码，再操作配置项，避免覆盖节点配置。", "error");
      return false;
    }
    return true;
  };
  const open = async (kind, trigger) => {
    menu.open = false;
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
    opened.className = `config-inbound-dialog${kind === "delete" ? " config-inbound-delete" : commonAction ? " config-common-dialog" : ""}`;
    opened.setAttribute("aria-labelledby", "config-inbound-title");
    const titles = { add:"增加入站", modify:"修改入站", delete:"删除入站", advanced:"高级字段", history:"版本历史", diff:"配置差异",
      "common-add":"增加通用配置项", "common-modify":"修改通用配置项", "common-delete":"删除通用配置项" };
    opened.innerHTML = `<header class="config-inbound-heading"><div><h2 id="config-inbound-title">${titles[kind]}</h2><p>${esc(agent.name)} / ${esc(engineName(engine))}${commonAction ? " / 公共配置" : chosen && kind !== "add" ? ` / ${esc(chosen.tag)} · :${Number(chosen.port)}` : ""}</p></div><button class="config-access-close" type="button" data-inbound-close aria-label="关闭弹窗">×</button></header><div class="config-inbound-body" data-inbound-body><p role="status">正在加载…</p></div>`;
    const body = opened.querySelector("[data-inbound-body]");
    const active = () => current() && dialog === opened && opened.isConnected;
    const dispose = (discard = false) => {
      state.routeSignal?.removeEventListener("abort", abort);
      editor?.dispose(discard);
      opened.close(); opened.remove();
      if (dialog === opened) dialog = null;
      busy = false;
      if (current()) { update(); trigger.focus(); }
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
    bindEvent(opened, "click", event => {
      const rect = opened.getBoundingClientRect();
      if (event.target === opened && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) void close();
    });
    state.routeSignal?.addEventListener("abort", abort, { once:true });
    document.body.append(opened);
    opened.showModal();
    const commit = async (result, inbound = chosen) => {
      dispose(true);
      if (state.data === data) await onSaved(result, inbound);
    };
    try {
      const base = `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}`;
      const fresh = await api(`${base}/workspace`, {method:"GET"});
      if (!active()) return;
      if (mutation && (fresh.config?.version || 0) !== (saved?.version || 0)) {
        throw new Error("配置版本已变化，请关闭弹窗并重新读取、核对配置后再编辑。");
      }
      if (mutation && !mutationReady()) { dispose(); return; }
      if (commonAction && !commonSelected()) { dispose(); return; }
      if (["modify", "delete"].includes(kind) && (target()?.tag !== chosen.tag || target()?.port !== chosen.port)) {
        dispose(); return;
      }
      if (kind === "diff") {
        const deployments = await api("/deployments", {method:"GET"});
        if (!active()) return;
        const deployed = deployments.find(item => item.agent_id === agent.id && item.engine === engine);
        if (!deployed) body.innerHTML = '<p>当前已保存配置尚未部署。</p>';
        else {
          const revision = await api(`/configs/${encodeURIComponent(deployed.config_id)}/revisions/${deployed.config_version}`, {method:"GET"});
          if (!active()) return;
          body.innerHTML = `<p>已部署 v${deployed.config_version} → 已保存 v${fresh.config.version}</p>${renderConfigDiff(fresh.config.content, revision.content) || "<p>配置内容一致。</p>"}`;
        }
      } else if (kind === "delete") {
        body.innerHTML = `<p>确定删除入站 <strong>${esc(chosen.tag)}</strong>（端口 ${Number(chosen.port)}）？不会删除其他入站。</p><p class="validation-note">仅校验不会改变节点运行配置；部署会应用删除并重启内核。</p><p role="alert" data-inbound-error></p><footer class="inbound-delete-actions"><button class="button" type="button" data-delete-intent="validate">删除并校验</button><button class="button danger" type="button" data-delete-intent="deploy">删除并部署</button></footer>`;
        const buttons = [...body.querySelectorAll("[data-delete-intent]")];
        buttons.forEach(button => {
          button.disabled = !writable() || fresh.agent?.status !== "online" || !fresh.agent?.runtime?.[engine]?.installed;
          button.onclick = async () => {
            if (!active() || opened.dataset.saving === "1" || !mutationReady()) return;
            opened.dataset.saving = "1";
            buttons.forEach(item => { item.disabled = true; });
            try {
              const result = await api(`${base}/server-inbounds`, {method:"POST", body:JSON.stringify({
                operation:"delete", original_tag:chosen.tag, input:{tag:chosen.tag, port:chosen.port}, expected_version:fresh.config.version,
                name:fresh.config.name, description:fresh.config.description, intent:button.dataset.deleteIntent,
              })});
              await commit(result, null);
            } catch (error) {
              if (!active()) return;
              body.querySelector("[data-inbound-error]").textContent = `${error.message}${error.status === 409 || !error.status ? " 请重新读取核对配置后再提交。" : ""}`;
              if (error.status && error.status < 500 && error.status !== 409) buttons.forEach(item => { item.disabled = false; });
            } finally { delete opened.dataset.saving; }
          };
        });
      } else {
        editor = mountEditor({ root:body, kind, isCurrent:active, onSaved:commit }, fresh, kind === "add" ? null : chosen);
        await editor.ready;
      }
    } catch (error) {
      if (active()) body.innerHTML = `<p class="alert error" role="alert">${esc(error.message)}</p>`;
    } finally {
      busy = false;
      if (current()) update();
    }
  };
  triggers.forEach(trigger => bindEvent(trigger, "click", () => open(actionKind(trigger), trigger)));
  return { selectedInbound:target, selectedCommon:commonSelected };
}
