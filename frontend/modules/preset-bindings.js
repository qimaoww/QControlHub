import { bindEvent } from "./refresh.js";
import { bindProtocolOptionVisibility, bindServerPlanRegeneration, installGeneratedFieldButtons, readServerPlanInput } from "./server-plan-form.js";
import { revealSelectedFields } from "./config-fields.js";
import { configFieldURL } from "./ss-rust-fields.js";

export function createPresetBindings({ api, state, can, engineName, notify, confirmAction }, {
  editor, drafts, presetVisible, capturePresetDrafts, agentConfig, refreshPresetPage, status, deployments,
}) {
  const { operationFor, saveOperation, renderPresetStatus, watchPresetOperation } = status;
  const { recordPendingDeploy } = deployments;
function bindAgentConfigPage(ctx, fieldsOnly = false) {
  const root = ctx.root;
  const visible = () => ctx.host === editor.host && presetVisible() && editor.context === ctx &&
    state.data === ctx.sessionData && state.navigationEpoch === ctx.navigationEpoch;
  const current = () => visible() &&
    state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine;
  const draftKey = selector => `${ctx.draftScope}${ctx.draftVersion}|${selector}|${
    selector === "#server-plan-form" ? ctx.selectedInbound?.tag || `new:${ctx.protocol?.key}` :
    selector === "#field-form" ? `${ctx.selectedField?.key}|${ctx.commonMutation || "advanced"}` :
    selector === "#inbound-field-form" ? `${ctx.selectedInbound?.tag}|${ctx.selectedInboundField?.key}` : "source"}`;
  const navigate = (selection, reuseWorkspace = true) => {
    if (editor.savePending || ctx.generating || !visible()) return;
    const previous = {
      engine: ctx.engine, protocol: ctx.protocol?.key, inboundTag: ctx.selectedInbound?.tag || "",
      configField: ctx.selectedField?.key, configInboundField: ctx.selectedInboundField?.key,
    };
    if (!ctx.navigating && Object.entries(selection).every(([key,value]) => previous[key] === value)) return;
    capturePresetDrafts();
    const navigation = ++editor.navigation;
    ctx.navigating = navigation;
    editor.syncControls();
    Object.assign(state.data, previous, selection);
    const rendering = agentConfig(reuseWorkspace ? { workspace: ctx.workspace } : {});
    const request = editor.request;
    void rendering.catch(async (error) => {
      if (request !== editor.request || !visible()) return;
      Object.assign(state.data, previous);
      // Request IDs are monotonic; old async reads stay invalidated.
      notify(`切换配置失败：${error.message}`, "error");
      // Rebind any deferred editors whose earlier request was invalidated.
      try { await agentConfig({workspace:ctx.workspace}); }
      catch (recoveryError) { if (visible()) notify(recoveryError.message, "error"); }
    }).finally(() => {
      if (ctx.navigating === navigation) ctx.navigating = false;
      editor.syncControls();
    });
  };
  revealSelectedFields(root);
  root.querySelectorAll(".config-field-studio").forEach((studio) => {
    bindEvent(studio, "toggle", () => { if (studio.open) revealSelectedFields(root); });
  });
  if (!ctx.engineInstalled)
    root
      .querySelectorAll(
        "#field-form button[type=submit], #inbound-field-form button[type=submit], #source-config-form button[type=submit]",
      )
      .forEach((button) => (button.disabled = true));
  root.querySelectorAll("[data-engine-select]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        if (link.dataset.engineSelect === ctx.engine && !ctx.navigating) return;
        navigate({ engine: link.dataset.engineSelect, protocol: "", inboundTag: "" }, false);
      }),
  );
  root.querySelectorAll("[data-inbound]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        const input = ctx.workspace.inbounds.find(
          (item) => item.tag === link.dataset.inbound,
        );
        if (input) navigate({ inboundTag: input.tag, protocol: input.protocol });
      }),
  );
  root.querySelectorAll("[data-protocol]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        navigate({ protocol: link.dataset.protocol, inboundTag: "" });
      }),
  );
  bindEvent(root.querySelector("[data-preset-protocol]"), "change", event => {
    navigate({protocol:event.target.value, inboundTag:""});
  });
  bindEvent(root.querySelector("[data-common-field]"), "change", event => {
    if (ctx.commonFields.some(field => field.key === event.target.value))
      navigate({configField:event.target.value});
  });
  bindEvent(root.querySelector("[data-new-inbound]"), "click", () => {
      if (editor.savePending || ctx.generating || !current()) return;
      navigate({ inboundTag: "", protocol: ctx.protocol.key });
  });
  root.querySelectorAll("[data-config-field]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        navigate({ configField: link.dataset.configField });
      }),
  );
  root.querySelectorAll("[data-inbound-field]").forEach((link) => {
    link.onclick = (event) => {
      event.preventDefault();
      navigate({ configInboundField: link.dataset.inboundField });
    };
  });
  root.querySelectorAll("[data-secret-visibility]").forEach(
    (button) =>
      (button.onclick = () => {
        const input = button.parentElement.querySelector("input");
        input.type = input.type === "password" ? "text" : "password";
        button.textContent = input.type === "password" ? "显示" : "隐藏";
      }),
  );
  const serverPlanForm = root.querySelector("#server-plan-form");
  if (serverPlanForm) drafts().bind(serverPlanForm, draftKey("#server-plan-form"));
  if (serverPlanForm && !fieldsOnly) {
    bindProtocolOptionVisibility(serverPlanForm);
    installGeneratedFieldButtons(serverPlanForm, ctx.protocol);
  }
  const regenerateStatus = serverPlanForm?.querySelector(
    "[data-regenerate-status]",
  );
  const regenerateButtons = [
    ...(serverPlanForm?.querySelectorAll("[data-regenerate]") || []),
  ];
  if (!fieldsOnly && serverPlanForm && regenerateButtons.length && regenerateStatus && can("agent-config.write")) {
    bindServerPlanRegeneration({
      form: serverPlanForm,
      buttons: regenerateButtons,
      api,
      base: ctx.base,
      protocol: ctx.protocol,
      canApply: () => current() && !editor.savePending && !ctx.navigating,
      onBusy: busy => { ctx.generating = busy; editor.syncControls(); },
      report: (message, tone = "success") => {
        regenerateStatus.classList.toggle("error", tone === "error");
        regenerateStatus.setAttribute(
          "role",
          tone === "error" ? "alert" : "status",
        );
        regenerateStatus.textContent = message;
      },
      onApplied: (plan) => {
        if (!ctx.selectedInbound) state.data.serverPlans[
          `${ctx.agent.id}|${ctx.engine}|${ctx.protocol.key}`
        ] = plan;
      },
    });
  }
  const operationKey = `${ctx.agent.id}|${ctx.engine}`;
  const showStatus = (message, tone = "", task) => {
    const previous = operationFor(operationKey);
    const reload = task?.id && previous?.task?.id === task.id && previous.reload;
    // Preserve the active watcher when only the post-save refresh failed.
    if (task?.id && previous?.task?.id === task.id) Object.assign(previous, {message, tone, reload});
    else saveOperation({key:operationKey, message, tone, task, reload});
    renderPresetStatus();
  };
  renderPresetStatus();
  if (!fieldsOnly) watchPresetOperation(operationFor(operationKey));
  const forms = ["#server-plan-form", "#field-form", "#inbound-field-form", "#source-config-form"];
  const canWrite = can("agent-config.write");
  const canSubmit = canWrite && can("tasks.execute") &&
    ctx.agent.status === "online" && !ctx.agent.runtime?.[ctx.engine]?.existing_config_unsupported_reason;
  const canSubmitForm = selector => canSubmit && (ctx.engineInstalled ||
    (selector === "#server-plan-form" && ctx.canAutoInstall && serverPlanForm?.elements.operation.value === "add"));
  const needsReload = () => operationFor(operationKey)?.reload;
  const refreshButton = root.querySelector("[data-refresh-preset]");
  bindEvent(refreshButton, "click", async () => {
    if (editor.savePending || ctx.generating || ctx.navigating || !current()) return;
    capturePresetDrafts();
    ctx.navigating = ++editor.navigation;
    editor.syncControls();
    try {
      await refreshPresetPage();
    } catch (error) {
      if (visible()) {
        showStatus(`刷新失败：${error.message}。草稿已保留，可重试刷新。`, "error");
      }
    } finally {
      ctx.navigating = false;
      editor.syncControls();
    }
  });
  editor.syncControls = () => {
    if (!visible()) return;
    const blocked = editor.savePending || Boolean(ctx.navigating);
    if (refreshButton) {
      refreshButton.disabled = blocked || ctx.generating || needsReload();
      refreshButton.textContent = ctx.navigating ? "正在加载…" : "刷新状态";
    }
    root.querySelector(".config-command-bar")?.setAttribute("aria-busy", String(blocked));
    const protocolSelect = root.querySelector("[data-preset-protocol]");
    if (protocolSelect) protocolSelect.disabled = blocked || ctx.generating;
    const commonSelect = root.querySelector("[data-common-field]");
    if (commonSelect) commonSelect.disabled = blocked || ctx.generating;
    for (const selector of forms) {
      const element = root.querySelector(selector);
      if (!element) continue;
      element.inert = blocked;
      element.querySelectorAll("button[type=submit]").forEach((button) => {
        button.disabled = !canSubmitForm(selector) || editor.savePending || ctx.generating || ctx.navigating || needsReload();
      });
    }
  };
  editor.syncControls();
  for (const selector of forms) {
    const element = root.querySelector(selector);
    if (!element) continue;
    drafts().bind(element, draftKey(selector));
    element.noValidate = true;
    element.querySelectorAll("button[type=submit]").forEach((button) => {
      button.disabled = !canSubmitForm(selector) || editor.savePending || ctx.generating || ctx.navigating || needsReload();
    });
    if (!canWrite) element.querySelectorAll("input, select, textarea, [data-regenerate]").forEach((control) => {
      control.disabled = true;
    });
    bindEvent(element, "submit", async (event) => {
      event.preventDefault();
      if (editor.savePending || ctx.generating || ctx.navigating || !current()) return;
      if (!canSubmitForm(selector) || needsReload()) {
        notify("当前节点、内核状态或账号权限不允许提交任务", "error");
        return;
      }
      // Keep DOM and intent references before the first await: currentTarget
      // is cleared by the browser when event dispatch returns.
      const formElement = element;
      const sessionData = state.data;
      const submitter = event.submitter;
      const values = new FormData(formElement);
      const isPlan = selector === "#server-plan-form";
      const isSource = selector === "#source-config-form";
      const perPort = selector === "#inbound-field-form";
      const intent = submitter?.dataset[isPlan ? "planIntent" : isSource ? "sourceIntent" : "fieldIntent"] || "validate";
      const operation = values.get("operation");
      const deleting = isPlan && operation === "delete";
      const commonField = selector === "#field-form" && ctx.commonMutation;
      const deletingCommon = commonField && ctx.commonMutation === "delete";
      if (commonField && (values.get("mutation") !== ctx.commonMutation ||
          !ctx.commonFields.some(field => field.key === ctx.selectedField?.key) || ctx.fieldValue.error ||
          Boolean(ctx.fieldValue.present) !== (ctx.commonMutation !== "add"))) {
        notify("通用配置项状态或操作已变化，请重新加载后再提交。", "error");
        return;
      }
      if (!deleting && !deletingCommon) {
        const invalid = [...formElement.elements].find((control) => control.willValidate && !control.validity.valid);
        if (invalid) {
          const section = invalid.closest(".builder-section");
          if (section) root.querySelector(`[data-builder-step="${section.id}"]`)?.click();
          for (let parent = invalid.parentElement; parent; parent = parent.parentElement)
            if (parent.tagName === "DETAILS") parent.open = true;
          invalid.focus();
          invalid.reportValidity();
          showStatus("请检查已标出的必填项或参数格式。", "error");
          return;
        }
      }
      editor.savePending = true;
      editor.syncControls();
      const buttons = forms.flatMap((id) => [...root.querySelectorAll(`${id} button[type=submit]`)]);
      const originalLabel = submitter?.textContent;
      buttons.forEach((button) => (button.disabled = true));
      formElement.setAttribute("aria-busy", "true");
      if (submitter) submitter.textContent = "正在保存…";
      let committedConfig, committedTask;
      try {
        if (drafts().otherDirty(draftKey(selector), ctx.draftScope) && !(await confirmAction(
          "本次只保存当前表单。其他入站、字段或源码中有未保存的修改，继续后这些草稿将被清除。是否继续？", "存在其他未保存修改",
        ))) return;
        if (deleting && !(await confirmAction(
          `确定删除入站“${ctx.selectedInbound?.tag}”？${intent === "deploy" ? "将部署配置并重启内核。" : "本次只保存并校验，节点配置需部署后生效。"}`,
          "删除入站",
        ))) return;
        if (deletingCommon && !(await confirmAction(
          `确定删除通用配置项“${ctx.selectedField.label}”（${ctx.selectedField.key}）？不会删除公共配置文件或其他入站。${intent === "deploy" ? "将部署配置并重启内核。" : "本次只保存并校验，节点配置需部署后生效。"}`,
          "删除通用配置项",
        ))) return;
        if (!current() || !formElement.isConnected) return;
        if (intent === "deploy" && ctx.beforeDeploy) {
          showStatus("正在核验 Agent 当前配置…");
          if (submitter) submitter.textContent = "正在核验…";
          await ctx.beforeDeploy();
          if (!current() || !formElement.isConnected) return;
        }
        showStatus("正在保存配置并创建任务…");
        if (submitter) submitter.textContent = "正在保存…";
        let result;
        let input;
        if (isPlan) {
          input = deleting ? ctx.selectedInbound : readServerPlanInput(formElement, ctx.protocol);
          result = await api(`${ctx.base}/server-inbounds`, {
            method: "POST",
            body: JSON.stringify({
              operation,
              install_if_missing: operation === "add" && !ctx.engineInstalled,
              original_tag: operation === "add" ? "" : ctx.selectedInbound?.tag || "",
              expected_version: ctx.workspace.config?.version || 0,
              name: ctx.workspace.config?.name || `${ctx.agent.name} · ${engineName(ctx.engine)}`,
              description: `${ctx.protocol.name} 服务端入站，由 QControlHub 方案生成`,
              intent,
              preserve_ss_rust_globals: ctx.engine === "ss-rust",
              input,
            }),
          });
        } else if (isSource) {
          result = await api(`${ctx.base}/source`, {
            method: "POST",
            body: JSON.stringify({
              agent_id: ctx.agent.id, engine: ctx.engine, name: values.get("name"),
              description: values.get("description"), content: values.get("content"),
              version: ctx.workspace.config.version,
              intent,
            }),
          });
        } else {
          result = await api(configFieldURL(ctx.base,
            (perPort ? ctx.selectedInboundField : ctx.selectedField).key,
            perPort ? ctx.selectedInbound?.tag || "" : undefined), {
            method: "POST", body: JSON.stringify({
              mutation: commonField ? ctx.commonMutation : values.get("mutation"),
              fragment: deletingCommon ? "" : values.get("fragment"),
              expected_version: ctx.workspace.config.version,
              name: ctx.workspace.config.name, description: ctx.workspace.config.description, intent,
            }),
          });
        }
        if (state.data !== sessionData) return;
        committedConfig = result.config;
        committedTask = result.task;
        drafts().clear(ctx.draftScope);
        if (intent === "deploy" && result?.task?.id) {
          recordPendingDeploy(result.task.id, ctx.agent.id, ctx.engine);
        }
        showStatus(`配置 v${result.config.version} 已保存，${intent === "deploy" ? "部署" : "校验"}任务已提交。`, "", result.task);
        const operationStatus = operationFor(operationKey);
        Object.assign(operationStatus, {version:result.config.version, intent});
        watchPresetOperation(operationStatus);
        if (isPlan) {
          if (state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine)
            state.data.inboundTag = deleting ? "" : input.tag;
          // Never reuse a committed identity when starting another inbound,
          // even if the user left the preset page while saving.
          state.data.serverPlans[operationKey + "|" + ctx.protocol.key] = null;
        }
        if (ctx.host) {
          // The parent replaces its saved snapshot only after the atomic
          // mutation succeeds, then refreshes the existing source editor.
          await ctx.host.onSaved(result, input);
          return;
        }
        if (!current()) {
          if (state.route === "agent-config" && state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine) {
            await refreshPresetPage();
          }
          return;
        }
        // Keep the whole operation locked until the saved revision is bound.
        await refreshPresetPage();
      } catch (error) {
        if (current()) {
          if (committedConfig && committedTask) {
            showStatus(`配置 v${committedConfig.version} 已保存且任务已提交，页面刷新失败：${error.message}。请重新加载后继续编辑。`, "error", committedTask);
            operationFor(operationKey).reload = true;
            renderPresetStatus();
          } else {
            const uncertain = !error.deployPreflight && (!error.status || error.status >= 500);
            showStatus(error.deployPreflight ? error.message : error.status === 409
              ? `配置或节点状态已变化：${error.message}。草稿已保留，请重新加载后再编辑。`
              : uncertain ? `未能确认保存结果：${error.message}。草稿已保留，请重新加载核对版本后再提交。` : error.message, "error");
            if (!error.deployPreflight && (error.status === 409 || uncertain)) {
              operationFor(operationKey).reload = true;
              renderPresetStatus();
            }
          }
          notify(error.message, "error");
        }
      } finally {
        if (state.data === sessionData) editor.savePending = false;
        formElement.removeAttribute("aria-busy");
        if (submitter) submitter.textContent = originalLabel;
        if (formElement.isConnected && current())
          buttons.forEach((button) => (button.disabled = !canSubmitForm(selector)));
        if (state.data === sessionData) editor.syncControls();
      }
    });
  }
  root.querySelectorAll("[data-builder-workbench]").forEach((workbench) => {
    const links = [...workbench.querySelectorAll("[data-builder-step]")];
    const sections = [...workbench.querySelectorAll(".builder-sections > .builder-section")];
    if (!links.length || !sections.length) return;
    links[0].parentElement?.setAttribute("role", "tablist");
    const activate = (id) => {
      const selected = sections.find((section) => section.id === id) || sections[0];
      state.data.builderStep = selected.id;
      sections.forEach((section) => {
        const active = section === selected;
        section.hidden = !active;
        section.setAttribute("role", "tabpanel");
        section.setAttribute("aria-hidden", active ? "false" : "true");
      });
      links.forEach((link) => {
        const active = link.dataset.builderStep === selected.id;
        link.classList.toggle("active", active);
        link.setAttribute("role", "tab");
        link.setAttribute("aria-selected", active ? "true" : "false");
        link.tabIndex = active ? 0 : -1;
      });
    };
    links.forEach((link) => bindEvent(link, "click", (event) => {
      event.preventDefault();
      activate(link.dataset.builderStep);
    }));
    links.forEach((link,index) => bindEvent(link, "keydown", event => {
      if (!["ArrowLeft","ArrowRight","Home","End"].includes(event.key)) return;
      event.preventDefault();
      const next = event.key === "Home" ? 0 : event.key === "End" ? links.length - 1 :
        (index + (event.key === "ArrowRight" ? 1 : -1) + links.length) % links.length;
      activate(links[next].dataset.builderStep);
      links[next].focus();
    }));
    activate(state.data.builderStep || sections[0].id);
  });
}


  return bindAgentConfigPage;
}
