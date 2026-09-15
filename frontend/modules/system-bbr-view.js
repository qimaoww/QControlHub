import { diagnosticError } from "./errors.js";
import { orderNodesBySavedOrder } from "./node-order.js";
import { prepareTCPPreset, systemTCPPresets } from "./system-bbr-presets.js";

import { systemBBRState, systemBBRFeature, taskLabel, actionLabel, dialogID } from "./system-bbr-model.js";
export function createSystemBBRView({ state, can, esc, date, shell }, { lifecycle, editable, selectedID }) {
  function dialogMarkup(agent, kind, title, content) {
    const id = dialogID(agent.id, kind);
    const task = lifecycle.localTasks.get(agent.id);
    const status = [systemBBRState(agent).text, task ? taskLabel(task.status) : "", lifecycle.refreshFailed ? "刷新失败，显示上次数据" : ""].filter(Boolean).join(" · ");
    return `<dialog class="traffic-edit-dialog bbr-dialog ${kind === "editor" ? "bbr-editor" : "bbr-parameters"}" id="${esc(id)}" data-refresh-live aria-labelledby="${esc(id)}-title" aria-describedby="${esc(id)}-status"><header><span class="traffic-edit-icon" aria-hidden="true">${kind === "editor" ? "≡" : "≋"}</span><div><p class="eyebrow">${esc(agent.name)}</p><h2 id="${esc(id)}-title">${title}</h2><p id="${esc(id)}-status" data-bbr-dialog-status>${esc(status)}</p></div><button class="deploy-command-close" type="button" data-bbr-dialog-close aria-label="关闭${title}" autofocus>×</button></header>${content}</dialog>`;
  }

  function dialogButton(agent, kind, title) {
    return `<button class="bbr-dialog-open" type="button" data-bbr-dialog-open="${esc(dialogID(agent.id, kind))}" aria-haspopup="dialog" aria-controls="${esc(dialogID(agent.id, kind))}"><span>${title}${kind === "editor" ? `<small data-bbr-draft-label="${esc(agent.id)}" ${Object.keys(lifecycle.drafts[agent.id] || {}).length ? "" : "hidden"}>有未提交草稿</small>` : ""}</span><span aria-hidden="true">→</span></button>`;
  }

  function editor(agent, disabled) {
    if (!editable(agent) || !agent.metrics?.bbr || !(agent.features || []).includes(systemBBRFeature)) return "";
    const current = agent.metrics.bbr.parameters || {};
    const draft = lifecycle.drafts[agent.id] || {};
    const presets = systemTCPPresets.map((preset) => {
      let unavailable = "";
      try { prepareTCPPreset(preset.id, lifecycle.rules, current); }
      catch (error) { unavailable = error.message; }
      return `<div class="bbr-preset"><div><strong>${esc(preset.name)}</strong><p>BBR + fq；收发缓冲区上限 32 MiB，TCP 最小 / 默认缓冲区 4 / 64 KiB；开启 MTU 黑洞探测和窗口缩放。</p><small>填入并勾选 ${Object.keys(preset.settings).length} 项，覆盖同名草稿，保留其他选择。核对下方参数后保存并应用。缓冲区上限需结合节点内存与并发连接评估。</small>${unavailable ? `<p class="bbr-preset-unavailable">${esc(unavailable)}</p>` : ""}</div><button class="button small" type="button" data-tcp-preset="${esc(preset.id)}" ${disabled || unavailable ? "disabled" : ""}>填入预设</button></div>`;
    }).join("");
    const content = `<form novalidate data-tcp-form="${esc(agent.id)}"><div class="bbr-dialog-body" data-refresh-scroll><p class="bbr-note">勾选需要管理的参数；编辑会自动勾选。仅应用勾选项，其他系统参数和既有托管项保持不变。关闭弹窗保留草稿，刷新浏览器会丢失。</p><section class="bbr-presets" aria-label="BBR 参数预设">${presets}</section><div class="bbr-fields">${lifecycle.rules.map((rule) => {
      const available = Object.hasOwn(current, rule.key);
      const value = draft[rule.key] ?? current[rule.key] ?? "";
      const choices = rule.choices || [];
      const field = choices.length
        ? `<select data-tcp-value="${esc(rule.key)}" aria-label="${esc(rule.label)}" ${disabled || !available ? "disabled" : ""}>${!choices.includes(value) ? `<option value="${esc(value)}">${esc(value || "未上报")}</option>` : ""}${choices.map((choice) => `<option value="${choice}" ${value === choice ? "selected" : ""}>${choice}</option>`).join("")}</select>`
        : `<input data-tcp-value="${esc(rule.key)}" aria-label="${esc(rule.label)}" type="${rule.tuple ? "text" : "number"}" ${rule.tuple ? 'placeholder="最小 默认 最大"' : `min="${rule.min || 0}" max="${rule.max}" step="1"`} value="${esc(value)}" ${disabled || !available ? "disabled" : ""}>`;
      return `<div class="bbr-field" data-refresh-key="tcp-field-${esc(agent.id)}-${esc(rule.key)}"><label class="bbr-field-label"><input type="checkbox" data-tcp-selected="${esc(rule.key)}" ${Object.hasOwn(draft, rule.key) ? "checked" : ""} ${disabled || !available ? "disabled" : ""}><span><b>${esc(rule.label)}</b><code>${esc(rule.key)}</code></span></label>${field}<small>当前：${esc(current[rule.key] || "此系统未提供，不能修改")}${choices.length ? "" : ` · 范围 ${rule.min || 0}–${rule.max}`}</small></div>`;
    }).join("")}</div></div><p class="bbr-warning" data-tcp-error role="alert" ${lifecycle.editorErrors.get(agent.id) ? "" : "hidden"}>${esc(lifecycle.editorErrors.get(agent.id) || "")}</p><footer><span data-tcp-draft-status role="status">${Object.keys(draft).length} 项待提交</span><div><button class="button small" type="button" data-bbr-dialog-close>关闭</button><button class="button small" type="button" data-tcp-reset ${disabled ? "disabled" : ""}>清空选择</button><button class="button small primary" type="submit" ${disabled ? "disabled" : ""}>保存并应用选中参数</button></div></footer></form>`;
    return dialogButton(agent, "editor", "自定义 BBR / TCP 参数") + dialogMarkup(agent, "editor", "自定义 BBR / TCP 参数", content);
  }

  function render(agents) {
    agents = orderNodesBySavedOrder(
      agents.filter((agent) => agent.can_manage !== false),
    );
    const focused = document.activeElement;
    state.data.agents = agents;
    state.data.bbrAgent = selectedID();
    const visible = selectedID() ? agents.filter((agent) => agent.id === selectedID()) : agents;
    const cards = visible.map((agent) => {
      const info = systemBBRState(agent);
      const status = agent.metrics?.bbr;
      const task = lifecycle.localTasks.get(agent.id);
      const busy = lifecycle.submitting.has(agent.id) || ["pending", "running"].includes(task?.status);
      const disabled = !editable(agent) || !info.controllable || busy;
      const presetDisabled = disabled || !status?.parameters?.["net.core.default_qdisc"];
      const hasFeature = (agent.features || []).includes(systemBBRFeature);
      const value = (entry) => esc(entry || "未上报");
      const persistence = status?.persistence === "managed"
        ? `面板管理 ${Object.keys(status.configured_parameters || {}).length} 项`
        : status?.persistence === "error" ? "配置冲突，需人工核对" : "未由 QControlHub 管理";
      const drift = status?.persistence === "managed" && Object.entries(status.configured_parameters || {}).some(([key, value]) => String(status.parameters?.[key] || "").trim().replace(/\s+/g, " ") !== value);
      return `<article class="bbr-card" data-refresh-key="bbr-${esc(agent.id)}" aria-busy="${busy}">
        <header><div><h2>${esc(agent.name)}</h2><span>${value(status?.kernel_release)} · ${esc(agent.os)} / ${esc(agent.arch)}</span></div><span class="status-label ${info.tone}">${esc(info.text)}</span></header>
        ${hasFeature && status ? `<dl class="bbr-primary-values"><div><dt>当前默认拥塞算法</dt><dd>${value(status.congestion_control)}</dd></div><div><dt>当前默认队列</dt><dd>${value(status.default_qdisc)}</dd></div><div><dt>已保存的重启配置</dt><dd>${esc(persistence)}</dd></div></dl>
        <div class="bbr-note"><strong>已加载算法</strong><span>${value((status.available_algorithms || []).join(" · "))}</span><small>未列出 BBR 不一定代表内核不支持；启用时由系统尝试加载自带模块。</small></div>
        ${drift ? '<p class="bbr-warning" role="status">当前生效参数与已保存配置不一致，请核对其他系统配置是否覆盖。</p>' : ""}
        ${status.error ? `<p class="bbr-warning" role="status">${esc(diagnosticError(status.error))}</p>` : ""}
        ${dialogButton(agent, "parameters", "生效参数与网卡队列")}
        ${dialogMarkup(agent, "parameters", "生效参数与网卡队列", `<div class="bbr-dialog-body" data-refresh-scroll>
          <div class="bbr-table-wrap"><table><caption>内核当前参数（包括面板外设置）</caption><thead><tr><th>参数</th><th>当前值</th><th>面板保存值</th></tr></thead><tbody>${Object.entries(status.parameters || {}).map(([key, entry]) => `<tr><td><code>${esc(key)}</code></td><td><code>${value(entry)}</code></td><td><code>${esc(status.configured_parameters?.[key] || "—")}</code></td></tr>`).join("")}</tbody></table></div>
          <div class="bbr-table-wrap"><table><caption>网卡实际队列（不自动重置）</caption><thead><tr><th>网卡</th><th>队列</th><th>层级</th></tr></thead><tbody>${(status.qdiscs || []).map((qdisc) => `<tr><td>${esc(qdisc.device)}</td><td><code>${esc(qdisc.kind)}</code></td><td>${qdisc.root ? "root" : value(qdisc.parent)} ${esc(qdisc.handle || "")}</td></tr>`).join("") || '<tr><td colspan="3">暂无可读取的网卡队列</td></tr>'}</tbody></table></div>
          ${status.qdisc_error ? `<p class="bbr-note">${esc(diagnosticError(status.qdisc_error))}</p>` : ""}
        </div><footer class="bbr-dialog-footer"><small>参数采集：${status.collected_at ? esc(date(status.collected_at)) : "尚未上报"}</small><button class="button small" type="button" data-bbr-dialog-close>关闭</button></footer>`)}` : `<div class="empty"><strong>${hasFeature ? "等待 Agent 上报系统参数" : "请先升级此节点的 Agent"}</strong><p>本页不会把未上报或旧版本节点显示为 BBR 已关闭。</p></div>`}
        ${editor(agent, disabled)}
        ${task ? `<div class="bbr-task" role="status"><span>${esc(actionLabel(task.action))} · ${esc(taskLabel(task.status))}</span>${can("tasks.read") ? `<a href="#tasks">查看任务记录 →</a>` : ""}${task.error ? `<p>${esc(diagnosticError(task.error))}</p>` : ""}</div>` : ""}
        <footer><small>参数采集：${status?.collected_at ? esc(date(status.collected_at)) : "尚未上报"}</small>${editable(agent) ? `<div class="bbr-actions"><button class="button small" type="button" data-bbr-agent="${esc(agent.id)}" data-bbr-action="disable-bbr" ${presetDisabled ? "disabled" : ""}>关闭 BBR / 切换 CUBIC</button><button class="button small primary" type="button" data-bbr-agent="${esc(agent.id)}" data-bbr-action="enable-bbr" ${presetDisabled ? "disabled" : ""}>启用 BBR</button></div>` : '<span class="bbr-readonly">只读权限</span>'}</footer>
      </article>`;
    }).join("");
    shell(`<div class="bbr-workspace"><div class="bbr-toolbar"><small data-bbr-refresh-status aria-live="polite">${lifecycle.refreshFailed ? "刷新失败，显示上次数据 · 将自动重试" : "自动刷新 · Agent 心跳采集"}</small><button class="button small" type="button" data-bbr-refresh>刷新状态</button></div><section class="bbr-grid">${cards || '<div class="empty large"><strong>当前范围没有节点</strong><p>添加节点后即可查看系统 BBR 状态。</p></div>'}</section></div>`, "BBR / TCP 调优", { viewKey: `system-bbr-${selectedID() || "all"}` });

    return { agents, focused };
  }
  return render;
}
