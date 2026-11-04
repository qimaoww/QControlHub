import { bindEvent } from "./refresh.js";
import { configJSONMembers } from "./config-files.js";
import { bindConfigMenu } from "./config-menu.js";
import { bindDialogBackdrop, closePopup, closeRetiringDialog } from "./popup.js";
import { diagnosticError } from "./errors.js";
import { updateFeedback, setVisible } from "./presence-motion.js";
import { formatConfigContent } from "./code-format.js";
import { bindOutboundPresets } from "./outbound-presets.js";

import { outboundCanBind as exit, outboundEntries, mutateOutbound, inboundOutbound, bindOutbound, changeInboundOutbound } from "./config-outbound-model.js";
export { outboundEntries, mutateOutbound, inboundOutbound, bindOutbound, changeInboundOutbound } from "./config-outbound-model.js";

export function bindConfigOutbounds({ navigation, api, agent, engine, saved, current, dirty, writable, notify,
  confirmAction, beforeDeploy, onSaved, state, selectedInbound, input:sourceInput, canReadPeers }) {
  if (!["xray", "sing-box"].includes(engine)) return;
  const menu = document.createElement("details");
  menu.className = "config-inbound-menu";
  menu.innerHTML = `<summary class="button" aria-haspopup="menu">出站操作 <span aria-hidden="true">▾</span></summary>
    <div class="config-inbound-menu-items" role="menu" aria-label="出站操作">
      <button type="button" role="menuitem" data-outbound-action="add">＋ 增加并绑定出站</button>
      <button type="button" role="menuitem" data-outbound-action="bind">绑定已有出站</button>
      <button type="button" role="menuitem" data-outbound-action="modify">修改绑定出站</button>
      <button type="button" role="menuitem" class="danger-text" data-outbound-action="delete">删除绑定出站</button>
    </div>`;
  navigation.append(menu);
  bindConfigMenu(menu);
  let dialog;
  const bindingCache = new Map();
  const triggers = [...menu.querySelectorAll("button")];
  const update = () => {
    const chosen = selectedInbound();
    setVisible(menu, chosen);
    let binding = "", reason = "";
    if (saved && chosen) {
      const key = JSON.stringify([chosen.tag, chosen.port]);
      if (!bindingCache.has(key)) {
        try { bindingCache.set(key, {binding:inboundOutbound(saved.content, engine, chosen)}); }
        catch (error) { bindingCache.set(key, {reason:error.message}); }
      }
      ({binding = "", reason = ""} = bindingCache.get(key));
    }
    triggers.forEach(button => {
      const needsBinding = ["modify", "delete"].includes(button.dataset.outboundAction);
      button.disabled = !saved || !writable() || !chosen || Boolean(dialog) || Boolean(reason) || needsBinding && !binding;
      button.title = !chosen ? "请先选择入站，再操作绑定出站" :
        reason || (needsBinding && !binding ? "当前入站尚未绑定出站，请先增加或绑定已有出站" : "");
    });
    menu.querySelector("summary").title = !chosen ? "请先选择入站，再操作绑定出站" :
      binding ? `当前绑定：${binding}` : "当前入站尚未设置专属出站绑定";
  };
  sourceInput?.addEventListener("config-selection", update);
  sourceInput?.addEventListener("input", update);
  update();

  triggers.forEach(trigger => bindEvent(trigger, "click", async () => {
    closePopup(menu);
    if (!current() || dialog || trigger.disabled || !writable() || !selectedInbound()) return;
    if (dirty()) { notify("配置源码有未保存修改，请先保存，再操作出站。", "error"); return; }
    const inbound = { ...selectedInbound() }, operation = trigger.dataset.outboundAction;
    const deleting = operation === "delete", selecting = operation === "bind";
    const base = `/agents/${encodeURIComponent(agent.id)}/configs/${engine}`;
    const opened = document.createElement("dialog");
    dialog = opened;
    opened.className = "config-inbound-dialog config-outbound-dialog";
    opened.setAttribute("aria-labelledby", "config-outbound-title");
    opened.innerHTML = `<header class="config-inbound-heading"><div><h2 id="config-outbound-title"></h2><p data-outbound-context></p></div><button type="button" class="config-access-close" data-outbound-close aria-label="关闭弹窗">×</button></header>
      <div class="config-inbound-body" data-outbound-body><p role="status">正在读取出站配置…</p></div>`;
    opened.querySelector("h2").textContent = {add:"增加并绑定出站", bind:"绑定已有出站", modify:"修改绑定出站", delete:"删除绑定出站"}[operation];
    opened.querySelector("[data-outbound-context]").textContent = `${agent.name} / ${engine === "xray" ? "Xray" : "sing-box"} · ${inbound.tag} :${inbound.port}`;
    const body = opened.querySelector("[data-outbound-body]");
    const active = () => current() && dialog === opened && opened.isConnected;
    const readController = new AbortController();
    let saving = false, confirming = false, isDirty = () => false, preset;
    const dispose = () => {
      readController.abort();
      preset?.dispose();
      state.routeSignal?.removeEventListener("abort", dispose);
      closeRetiringDialog(opened); opened.remove();
      if (dialog === opened) dialog = null;
      if (current()) { update(); menu.querySelector("summary").focus(); }
    };
    const close = async () => {
      if (saving || confirming) return;
      confirming = true;
      try {
        if (isDirty() && !(await confirmAction("出站配置有未保存修改，确定放弃并关闭？", "放弃修改"))) return;
        dispose();
      } finally { confirming = false; }
    };
    bindEvent(opened.querySelector("[data-outbound-close]"), "click", close);
    bindEvent(opened, "cancel", event => { event.preventDefault(); void close(); });
    bindDialogBackdrop(opened, () => { void close(); });
    state.routeSignal?.addEventListener("abort", dispose, {once:true});
    document.body.append(opened);
    opened.showModal();
    update();
    try {
      const fresh = await api(`${base}/workspace`, {method:"GET", signal:readController.signal});
      if (!active()) return;
      if (!fresh.config || fresh.config.version !== saved.version) throw Error("配置已变化，请关闭弹窗并重新读取后再编辑");
      if (dirty()) throw Error("配置源码有未保存修改，请先保存");
      if (selectedInbound()?.tag !== inbound.tag || selectedInbound()?.port !== inbound.port) throw Error("所选入站已变化，请重新打开出站操作");
      const entries = outboundEntries(fresh.config.content).filter(entry => exit(entry, engine) && entry.tag);
      const bound = inboundOutbound(fresh.config.content, engine, inbound);
      const entry = entries.find(entry => entry.tag === bound);
      if (operation !== "add" && !entries.length) throw Error("没有可绑定的流量出站，请先增加出站");
      if (["modify", "delete"].includes(operation) && !entry) throw Error("当前入站尚未绑定可编辑出站，请先增加或绑定已有出站");
      body.innerHTML = `<section class="config-field-studio config-outbound-studio"><div class="field-canvas">
        <div class="config-outbound-binding"><span>绑定入站 <strong data-bound-inbound></strong></span><span class="status-label" data-outbound-mark></span></div>
        <p class="field-scope-hint" data-outbound-hint></p>
        <form><div class="field-mutation"><label data-outbound-select>选择出站<select aria-label="选择出站"></select></label><span class="field-value-state" data-outbound-state></span></div>
          <label class="field-value-label" data-outbound-source>出站配置 JSON<textarea rows="12" spellcheck="false" aria-label="出站配置 JSON"></textarea></label>
          <p class="alert error" role="alert" data-outbound-error hidden></p><p class="field-editor-note" role="status" data-outbound-status></p>
          <footer><p class="field-editor-note">${deleting ? "删除后按剩余路由重新规划统计出口。" : "保存时生成独立出口及 mark。"}校验不改变节点运行配置；部署会重启当前内核。</p><div><button class="button" type="button" data-outbound-cancel>取消</button><button class="button" type="submit" data-intent="validate">${deleting ? "删除" : "保存"}并校验</button><button class="button ${deleting ? "danger" : "primary"}" type="submit" data-intent="deploy">${deleting ? "删除" : "保存"}并部署</button></div></footer>
        </form></div></section>`;
      body.querySelector("[data-bound-inbound]").textContent = `${inbound.tag} · :${inbound.port}`;
      body.querySelector("[data-outbound-mark]").textContent = `mark · 0x${(0x51430000 | inbound.port).toString(16)}`;
      updateFeedback(body.querySelector("[data-outbound-hint]"), deleting
        ? "删除此出站并移除当前入站绑定，入站将恢复全局路由。默认或共享出口不可在此删除。"
        : "绑定作为当前入站的兜底出口，已有条件分流和限制规则保持不变。修改仅限本入站独占的出站模板。");
      body.querySelector("[data-outbound-state]").textContent = bound ? `当前绑定：${bound}` : "尚未绑定 · 当前按全局路由";
      const form = body.querySelector("form"), select = body.querySelector("select"), input = body.querySelector("textarea");
      for (const candidate of entries) select.add(new Option(candidate.tag, String(candidate.index), candidate.tag === bound, candidate.tag === bound));
      setVisible(body.querySelector("[data-outbound-select]"), selecting);
      let baseline;
      if (operation === "add") {
        let tag = `exit-${inbound.port}`;
        for (let suffix = 2; entries.some(entry => entry.tag === tag); suffix++) tag = `exit-${inbound.port}-${suffix}`;
        baseline = JSON.stringify(engine === "xray" ? {tag, protocol:"freedom"} : {tag, type:"direct"}, null, 2);
      } else baseline = selecting ? entries.find(entry => entry.index === Number(select.value)).content : entry.content;
      baseline = formatConfigContent(baseline, "JSON");
      input.value = baseline;
      input.rows = Math.min(16, Math.max(6, baseline.split("\n").length + 1));
      input.readOnly = deleting || selecting;
      const selected = select.value;
      isDirty = () => Boolean(preset?.dirty()) || (selecting ? select.value !== selected : !deleting && input.value !== baseline);
      const syncDirty = () => { opened.dataset.dirty = isDirty() ? "1" : "0"; };
      input.addEventListener("input", syncDirty);
      select.addEventListener("change", () => {
        input.value = formatConfigContent(entries.find(entry => entry.index === Number(select.value)).content, "JSON");
        input.rows = Math.min(16, Math.max(6, input.value.split("\n").length + 1));
        syncDirty();
      });
      if (!selecting && !deleting) preset = bindOutboundPresets({
        form, input, engine, agentId:agent.id, api, canReadPeers, active, onChange:syncDirty,
        initialMode:operation === "add" ? "preset" : "json",
      });
      bindEvent(body.querySelector("[data-outbound-cancel]"), "click", close);
      const submits = [...form.querySelectorAll("[type=submit]")], closeButtons = [...opened.querySelectorAll("[data-outbound-close], [data-outbound-cancel]")];
      const available = writable() && fresh.agent?.status === "online" && fresh.agent?.runtime?.[engine]?.installed;
      let uncertain = false;
      const status = body.querySelector("[data-outbound-status]"), errorBox = body.querySelector("[data-outbound-error]");
      const sync = () => {
        opened.dataset.saving = saving ? "1" : "0";
        form.setAttribute("aria-busy", String(saving));
        select.disabled = input.disabled = saving;
        closeButtons.forEach(button => { button.disabled = saving; });
        submits.forEach(button => { button.disabled = saving || uncertain || !available; });
        preset?.busy(saving);
      };
      if (!available) updateFeedback(status, "节点离线或内核未安装，暂不能保存并提交任务。");
      sync();
      form.onsubmit = async event => {
        event.preventDefault();
        if (saving || confirming || uncertain || !available || !active() || !writable()) return;
        const intent = event.submitter?.dataset.intent || "validate";
        if (!["validate", "deploy"].includes(intent)) return;
        saving = true;
        setVisible(errorBox, false);
        updateFeedback(status, "正在检查出站绑定…");
        sync();
        let submitted = false;
        try {
          if (dirty()) throw Error("配置源码有未保存修改，请先保存");
          if (selectedInbound()?.tag !== inbound.tag || selectedInbound()?.port !== inbound.port) throw Error("所选入站已变化，请重新打开出站操作");
          const fragment = preset ? preset.content() : input.value;
          const tag = selecting ? entries.find(entry => entry.index === Number(select.value)).tag : deleting ? entry.tag : JSON.parse(fragment).tag;
          const content = changeInboundOutbound(fresh.config.content, engine, inbound, operation, entry?.index ?? -1, selecting ? tag : fragment);
          if ((deleting || selecting && bound && tag !== bound || intent === "deploy") &&
              !(await confirmAction(`${deleting ? "删除出站并恢复全局路由" : "保存出站绑定"}：${inbound.tag} :${inbound.port} → ${tag}。${intent === "deploy" ? "将部署并重启当前内核，确定继续？" : "仅保存并校验，确定继续？"}`, "确认出站操作"))) return;
          if (!active()) return;
          if (intent === "deploy" && beforeDeploy) {
            updateFeedback(status, "正在核验 Agent 当前配置…");
            await beforeDeploy();
            if (!active()) return;
          }
          updateFeedback(status, "正在保存配置并提交任务…");
          submitted = true;
          const result = await api(`${base}/source`, {method:"POST", body:JSON.stringify({
            name:fresh.config.name, description:fresh.config.description, engine, version:fresh.config.version, content, intent,
          })});
          if (!active()) return;
          dispose();
          await onSaved(result, inbound);
        } catch (error) {
          if (!active()) return;
          uncertain = submitted && (!error.status || error.status === 409 || error.status >= 500);
          updateFeedback(errorBox, `${diagnosticError(error.message)}${uncertain ? " 草稿已保留，请关闭弹窗并重新读取、核对保存结果后再提交。" : ""}`);
        } finally {
          saving = false;
          updateFeedback(status, "");
          sync();
        }
      };
      if (preset && operation === "add") preset.focus();
      else (selecting ? select : deleting ? body.querySelector("[data-outbound-cancel]") : input).focus({preventScroll:true});
    } catch (error) {
      if (active()) {
        body.replaceChildren();
        const message = document.createElement("p");
        message.className = "alert error";
        message.setAttribute("role", "alert");
        updateFeedback(message, diagnosticError(error.message));
        body.append(message);
      }
    }
  }));
}
