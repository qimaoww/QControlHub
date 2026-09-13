import { bindEvent } from "./refresh.js";
import { configJSONMembers } from "./config-files.js";

const generated = tag => /^qch-(?:trf-|stat-)/.test(tag || "");
export function outboundEntries(content) {
  const root = configJSONMembers(content);
  return configJSONMembers(root.get("outbounds") || "[]", true).map((content, index) => ({
    ...JSON.parse(content), content, index,
  })).filter(entry => !generated(entry.tag));
}
export function mutateOutbound(content, operation, index, fragment) {
  const root = configJSONMembers(content);
  const entries = configJSONMembers(root.get("outbounds") || "[]", true);
  const previous = index >= 0 ? JSON.parse(entries[index]) : null;
  if (operation !== "add" && (!previous || generated(previous.tag))) throw Error("请重新选择出站");
  let next;
  if (operation !== "delete") {
    configJSONMembers(fragment);
    next = JSON.parse(fragment);
    if (typeof next.tag !== "string" || !next.tag.trim() || generated(next.tag)) throw Error("请填写唯一出站标签，不能使用系统统计标签");
    if (entries.some((raw, i) => i !== index && JSON.parse(raw).tag === next.tag)) throw Error("出站标签已存在");
  }
  if (previous?.tag && (operation === "delete" || next.tag !== previous.tag)) {
    const references = [];
    const visit = (value, path) => {
      if (Array.isArray(value)) value.forEach((entry, i) => visit(entry, `${path}/${i}`));
      else if (value && typeof value === "object") Object.entries(value).forEach(([key, entry]) => {
        if (key !== "tag") visit(entry, `${path}/${key}`);
      });
      else if (value === previous.tag) references.push(path);
    };
    const document = JSON.parse(content);
    document.outbounds.splice(index, 1);
    visit(document, "");
    if (references.length) throw Error(`出站仍被引用：${references.join("、")}。请先调整路由或出站引用。`);
  }
  if (operation === "add") entries.push(fragment);
  else if (operation === "delete") entries.splice(index, 1);
  else entries[index] = fragment;
  root.set("outbounds", `[${entries.join(",\n")}]`);
  return `{\n${[...root].map(([key, value]) => `${JSON.stringify(key)}: ${value}`).join(",\n")}\n}`;
}

export function bindConfigOutbounds({ navigation, api, agent, engine, saved, current, dirty, writable, notify, confirmAction, onSaved, state }) {
  if (!["xray", "sing-box"].includes(engine)) return;
  const menu = document.createElement("details");
  menu.className = "config-inbound-menu";
  menu.innerHTML = '<summary class="button" aria-haspopup="menu">出站操作 <span aria-hidden="true">▾</span></summary><div class="config-inbound-menu-items" role="menu"><button type="button" role="menuitem" data-outbound-action="add">＋ 增加出站</button><button type="button" role="menuitem" data-outbound-action="modify">修改出站</button><button type="button" role="menuitem" class="danger-text" data-outbound-action="delete">删除出站</button></div>';
  navigation.append(menu);
  let dialog, busy = false;
  const triggers = [...menu.querySelectorAll("button")];
  triggers.forEach(button => { button.disabled = !saved || !writable(); });
  bindEvent(menu, "keydown", event => { if (event.key === "Escape") menu.open = false; });
  triggers.forEach(trigger => bindEvent(trigger, "click", async () => {
    menu.open = false;
    if (!current() || busy || dialog || !writable()) return;
    if (dirty()) { notify("配置源码有未保存修改，请先保存，再操作出站。", "error"); return; }
    busy = true;
    const operation = trigger.dataset.outboundAction;
    const base = `/agents/${encodeURIComponent(agent.id)}/configs/${engine}`;
    try {
      const fresh = await api(`${base}/workspace`, {method:"GET"});
      if (!current()) return;
      if (fresh.config?.version !== saved.version) throw Error("配置版本已变化，请刷新后重试");
      const entries = outboundEntries(fresh.config.content);
      if (operation !== "add" && !entries.length) throw Error("没有可编辑的出站");
      dialog = document.createElement("dialog");
      const opened = dialog;
      opened.className = "config-inbound-dialog";
      opened.setAttribute("aria-label", {add:"增加出站", modify:"修改出站", delete:"删除出站"}[operation]);
      opened.innerHTML = '<header class="config-inbound-heading"><h2></h2><button type="button" class="config-access-close" aria-label="关闭弹窗">×</button></header><form class="config-inbound-body"><label data-outbound-select>选择出站<select></select></label><label data-outbound-source>出站配置 JSON<textarea rows="16" spellcheck="false" aria-label="出站配置 JSON"></textarea></label><p role="alert"></p><footer class="inbound-delete-actions"><button class="button" type="submit" data-intent="validate">保存并校验</button><button class="button primary" type="submit" data-intent="deploy">保存并部署</button></footer></form>';
      opened.querySelector("h2").textContent = opened.getAttribute("aria-label");
      const select = opened.querySelector("select"), input = opened.querySelector("textarea");
      for (const entry of entries) select.add(new Option(entry.tag || `未命名出站 ${entry.index + 1}`, String(entry.index)));
      opened.querySelector("[data-outbound-select]").hidden = operation === "add";
      opened.querySelector("[data-outbound-source]").hidden = operation === "delete";
      let baseline;
      const selectEntry = () => { baseline = operation === "add" ? JSON.stringify(engine === "xray" ? {tag:"new-outbound", protocol:"freedom"} : {tag:"new-outbound", type:"direct"}, null, 2) : entries.find(entry => entry.index === Number(select.value)).content; input.value = baseline; opened.dataset.dirty = "0"; };
      input.addEventListener("input", () => { opened.dataset.dirty = input.value !== baseline ? "1" : "0"; });
      selectEntry();
      let selected = select.value, saving = false, confirming = false;
      const dispose = () => { state.routeSignal?.removeEventListener("abort", dispose); opened.close(); opened.remove(); if (dialog === opened) dialog = null; };
      const close = async () => {
        if (saving || confirming) return;
        confirming = true;
        try {
          if (input.value !== baseline && !(await confirmAction("出站配置有未保存修改，确定关闭？", "放弃修改"))) return;
          dispose(); if(current()) trigger.focus();
        } finally { confirming = false; }
      };
      select.onchange = async () => { const next = select.value; select.value = selected; if(input.value !== baseline && !(await confirmAction("切换会放弃当前出站修改，确定继续？", "切换出站")))return; select.value = selected = next; selectEntry(); };
      opened.querySelector("header button").onclick = close;
      opened.addEventListener("cancel", event => { event.preventDefault(); void close(); });
      state.routeSignal?.addEventListener("abort", dispose, {once:true});
      const submitButtons = [...opened.querySelectorAll("[type=submit]")];
      submitButtons.forEach(button => { button.disabled = fresh.agent.status !== "online" || !fresh.agent.runtime?.[engine]?.installed; });
      opened.querySelector("form").onsubmit = async event => {
        event.preventDefault();
        if(saving || confirming || !current() || !writable())return;
        saving = true; opened.dataset.saving = "1";
        select.disabled = input.disabled = true;
        submitButtons.forEach(button => {button.disabled = true;});
        try {
          if(dirty())throw Error("配置源码有未保存修改，请先保存");
          const content = mutateOutbound(fresh.config.content, operation, operation === "add" ? -1 : Number(select.value), input.value);
          const intent = event.submitter?.dataset.intent || "validate";
          if ((operation === "delete" || intent === "deploy") && !(await confirmAction(`${operation === "delete" ? "删除所选出站" : "保存出站修改"}${intent === "deploy" ? "并部署、重启内核" : "并校验"}？`, "确认出站操作"))) return;
          if(!current() || !opened.isConnected)return;
          const result = await api(`${base}/source`, {method:"POST", body:JSON.stringify({name:fresh.config.name, description:fresh.config.description, engine, version:fresh.config.version, content, intent})});
          dispose(); await onSaved(result);
        } catch(error) { if(opened.isConnected)opened.querySelector("[role=alert]").textContent = error.message; }
        finally {saving = false;delete opened.dataset.saving;select.disabled = input.disabled = false;submitButtons.forEach(button => {button.disabled = false;});}
      };
      document.body.append(opened); opened.showModal();
    } catch(error) { if(current())notify(error.message,"error"); }
    finally {busy = false;}
  }));
}
