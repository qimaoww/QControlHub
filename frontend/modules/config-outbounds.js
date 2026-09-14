import { bindEvent } from "./refresh.js";
import { configJSONMembers } from "./config-files.js";
import { bindConfigMenu } from "./config-menu.js";
import { diagnosticError } from "./errors.js";
import { formatConfigContent } from "./code-format.js";
import { bindOutboundPresets } from "./outbound-presets.js";

const generated = tag => /^qch-(?:trf-|stat-)/.test(tag || "");
const objectText = entries => `{\n${[...entries].map(([key, value]) => `${JSON.stringify(key)}: ${value}`).join(",\n")}\n}`;
const fields = engine => {
  if (engine === "xray") return { routeKey:"routing", matchKey:"inboundTag", targetKey:"outboundTag", actionKey:"type", portKey:"port" };
  if (engine === "sing-box") return { routeKey:"route", matchKey:"inbound", targetKey:"outbound", actionKey:"action", portKey:"listen_port" };
  throw Error("此内核暂不支持出站操作");
};
const exit = (entry, engine) => !["block", "blackhole", "dns"].includes(entry[engine === "xray" ? "protocol" : "type"]);

export function outboundEntries(content) {
  const root = configJSONMembers(content);
  return configJSONMembers(root.get("outbounds") || "[]", true).map((content, index) => {
    configJSONMembers(content);
    return { ...JSON.parse(content), content, index };
  }).filter(entry => !generated(entry.tag));
}

function referencesTo(content, index) {
  const document = JSON.parse(content), previous = document.outbounds[index], references = [];
  document.outbounds.splice(index, 1);
  const visit = (value, path) => {
    if (Array.isArray(value)) value.forEach((entry, i) => visit(entry, `${path}/${i}`));
    else if (value && typeof value === "object") Object.entries(value).forEach(([key, entry]) => {
      if (key !== "tag") visit(entry, `${path}/${key}`);
    });
    else if (value === previous.tag) references.push(path);
  };
  visit(document, "");
  return references;
}

export function mutateOutbound(content, operation, index, fragment, ignoredReferences = []) {
  if (!["add", "modify", "delete"].includes(operation)) throw Error("无效出站操作");
  const root = configJSONMembers(content);
  const entries = configJSONMembers(root.get("outbounds") || "[]", true);
  const previous = operation !== "add" && Number.isInteger(index) && index >= 0 && entries[index] ? JSON.parse(entries[index]) : null;
  if (operation !== "add" && (!previous || generated(previous.tag))) throw Error("请重新选择出站");
  let next;
  if (operation !== "delete") {
    configJSONMembers(fragment);
    next = JSON.parse(fragment);
    if (typeof next.tag !== "string" || !next.tag.trim() || next.tag !== next.tag.trim() || generated(next.tag))
      throw Error("请填写唯一出站标签，不能使用系统统计标签或首尾空格");
    if (entries.some((raw, i) => (operation === "add" || i !== index) && JSON.parse(raw).tag === next.tag))
      throw Error("出站标签已存在");
  }
  if (previous?.tag && (operation === "delete" || next.tag !== previous.tag)) {
    const references = referencesTo(content, index).filter(path => !ignoredReferences.includes(path));
    if (references.length) throw Error(`出站仍被引用：${references.join("、")}。请先调整路由或出站引用。`);
  }
  if (operation === "add") {
    // Keep generated per-inbound exits as an ordered suffix for paired files.
    const firstGenerated = entries.findIndex(raw => generated(JSON.parse(raw).tag));
    entries.splice(firstGenerated < 0 ? entries.length : firstGenerated, 0, fragment);
  } else if (operation === "delete") entries.splice(index, 1);
  else entries[index] = fragment;
  root.set("outbounds", `[${entries.join(",\n")}]`);
  return objectText(root);
}

function bindingState(content, engine, inbound) {
  const keys = fields(engine), root = configJSONMembers(content);
  const inbounds = configJSONMembers(root.get("inbounds") || "[]", true).map(raw => JSON.parse(raw));
  if (!inbound?.tag || !Number.isInteger(inbound.port) || inbound.port < 1 || inbound.port > 65535 ||
      inbounds.filter(entry => entry.tag === inbound.tag).length !== 1 ||
      !inbounds.some(entry => entry.tag === inbound.tag && Number(entry[keys.portKey]) === inbound.port))
    throw Error("入站已变化，请重新选择入站");
  const route = configJSONMembers(root.get(keys.routeKey) || "{}");
  const rules = configJSONMembers(route.get("rules") || "[]", true);
  const plain = rule => Object.keys(rule).every(key => [keys.matchKey, keys.targetKey, keys.actionKey].includes(key)) &&
    (engine === "xray" ? rule.type === "field" : !rule.action || rule.action === "route");
  const matches = value => Array.isArray(value) ? value.length === 1 && value[0] === inbound.tag :
    engine === "sing-box" && value === inbound.tag;
  const indices = rules.flatMap((raw, index) => {
    const rule = JSON.parse(raw);
    return plain(rule) && matches(rule[keys.matchKey]) && typeof rule[keys.targetKey] === "string" &&
      !generated(rule[keys.targetKey]) ? [index] : [];
  });
  if (indices.length > 1) throw Error("当前入站有多条出站绑定，请先在公共配置中整理路由");
  const index = indices[0] ?? -1;
  return { ...keys, root, route, rules, plain, index, tag:index < 0 ? "" : JSON.parse(rules[index])[keys.targetKey] };
}

export function inboundOutbound(content, engine, inbound) {
  return bindingState(content, engine, inbound).tag;
}

export function bindOutbound(content, engine, inbound, tag, remove = false) {
  const { root, route, rules, plain, index, routeKey, matchKey, targetKey } = bindingState(content, engine, inbound);
  if (remove) {
    if (index >= 0) rules.splice(index, 1);
  } else {
    if (!outboundEntries(content).some(entry => entry.tag === tag && exit(entry, engine)))
      throw Error("请选择存在的流量出站，不能绑定系统统计、拦截或 DNS 出站");
    const rule = engine === "xray" ? {type:"field", inboundTag:[inbound.tag], outboundTag:tag} : {inbound:[inbound.tag], outbound:tag};
    if (index >= 0) rules[index] = JSON.stringify(rule);
    else {
      // Conditional policy rules stay ahead of the new fallback. Do not
      // silently override a multi-inbound binding that needs a source review.
      if (rules.some(raw => {
        const value = JSON.parse(raw), tags = value[matchKey];
        return plain(value) && Array.isArray(tags) && tags.length > 1 && tags.includes(inbound.tag);
      })) throw Error("当前入站使用多入站共享路由，请先在公共配置中拆分绑定");
      const fallback = rules.findIndex(raw => {
        const value = JSON.parse(raw);
        return plain(value) && value[matchKey] === undefined && typeof value[targetKey] === "string" && !generated(value[targetKey]);
      });
      if (fallback >= 0 && !outboundEntries(content).some(entry => entry.tag === JSON.parse(rules[fallback])[targetKey] && exit(entry, engine)) ||
          engine === "sing-box" && rules.some(raw => {
            const value = JSON.parse(raw);
            return value.action === "reject" && Object.keys(value).every(key => ["action", "method", "no_drop"].includes(key));
          })) throw Error("已有全局拦截或 DNS 兜底，请先在公共配置中明确调整路由，再绑定出站");
      rules.splice(fallback < 0 ? rules.length : fallback, 0, JSON.stringify(rule));
    }
  }
  route.set("rules", `[${rules.join(",\n")}]`);
  root.set(routeKey, objectText(route));
  return objectText(root);
}

export function changeInboundOutbound(content, engine, inbound, operation, index, fragment) {
  if (operation === "bind") return bindOutbound(content, engine, inbound, fragment);
  const binding = bindingState(content, engine, inbound);
  const ignored = [];
  if (operation !== "add") {
    const entry = outboundEntries(content).find(entry => entry.index === index);
    if (!entry || entry.tag !== binding.tag) throw Error("只能修改或删除当前入站绑定的出站");
    const ownReference = `/${binding.routeKey}/rules/${binding.index}/${binding.targetKey}`;
    const defaultTag = engine === "sing-box" && JSON.parse(content).route?.final || outboundEntries(content)[0]?.tag;
    if (entry.tag === defaultTag || referencesTo(content, index).some(path => path !== ownReference))
      throw Error("此出站是默认或共享出口，不能仅为当前入站修改或删除。请增加专属出站并绑定；共享模板可在公共配置中管理。");
    ignored.push(ownReference);
  }
  const changed = mutateOutbound(content, operation, index, fragment, ignored);
  return bindOutbound(changed, engine, inbound, operation === "delete" ? "" : JSON.parse(fragment).tag, operation === "delete");
}

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
    menu.open = false;
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
      opened.close(); opened.remove();
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
    bindEvent(opened, "click", event => {
      const rect = opened.getBoundingClientRect();
      if (event.target === opened && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) void close();
    });
    state.routeSignal?.addEventListener("abort", dispose, {once:true});
    document.body.append(opened);
    opened.showModal();
    update();
    try {
      const fresh = await api(`${base}/workspace`, {method:"GET", signal:readController.signal});
      if (!active()) return;
      if (!fresh.config || fresh.config.version !== saved.version) throw Error("配置版本已变化，请关闭弹窗并重新读取后再编辑");
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
      body.querySelector("[data-outbound-hint]").textContent = deleting
        ? "删除此出站并移除当前入站绑定，入站将恢复全局路由。默认或共享出口不可在此删除。"
        : "绑定作为当前入站的兜底出口，已有条件分流和限制规则保持不变。修改仅限本入站独占的出站模板。";
      body.querySelector("[data-outbound-state]").textContent = bound ? `当前绑定：${bound}` : "尚未绑定 · 当前按全局路由";
      const form = body.querySelector("form"), select = body.querySelector("select"), input = body.querySelector("textarea");
      for (const candidate of entries) select.add(new Option(candidate.tag, String(candidate.index), candidate.tag === bound, candidate.tag === bound));
      body.querySelector("[data-outbound-select]").hidden = !selecting;
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
      if (!available) status.textContent = "节点离线或内核未安装，暂不能保存并提交任务。";
      sync();
      form.onsubmit = async event => {
        event.preventDefault();
        if (saving || confirming || uncertain || !available || !active() || !writable()) return;
        const intent = event.submitter?.dataset.intent || "validate";
        if (!["validate", "deploy"].includes(intent)) return;
        saving = true;
        errorBox.hidden = true;
        status.textContent = "正在检查出站绑定…";
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
            status.textContent = "正在核验 Agent 当前配置…";
            await beforeDeploy();
            if (!active()) return;
          }
          status.textContent = "正在保存配置并提交任务…";
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
          errorBox.textContent = `${diagnosticError(error.message)}${uncertain ? " 草稿已保留，请关闭弹窗并重新读取、核对保存结果后再提交。" : ""}`;
          errorBox.hidden = false;
        } finally {
          saving = false;
          status.textContent = "";
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
        message.textContent = diagnosticError(error.message);
        body.append(message);
      }
    }
  }));
}
