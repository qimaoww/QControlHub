import { diagnosticError } from "./errors.js";
import { orderNodesBySavedOrder } from "./node-order.js";
import { prepareTCPPreset, systemTCPPresets } from "./system-bbr-presets.js";

import { hasTCPParameter, systemBBRState, systemBBRFeature, taskLabel, actionLabel, dialogID } from "./system-bbr-model.js";
export function createSystemBBRView({ state, can, esc, date, shell }, { lifecycle, editable, selectedID }) {
  function dialogMarkup(agent, kind, title, content) {
    const id = dialogID(agent.id, kind);
    const task = lifecycle.localTasks.get(agent.id);
    const status = [systemBBRState(agent).text, task ? taskLabel(task.status) : "", lifecycle.refreshFailed ? "刷新失败，显示上次数据" : ""].filter(Boolean).join(" · ");
    return `<dialog class="traffic-edit-dialog bbr-dialog ${kind === "editor" ? "bbr-editor" : "bbr-parameters"}" id="${esc(id)}" data-refresh-live aria-labelledby="${esc(id)}-title" aria-describedby="${esc(id)}-status"><header><span class="traffic-edit-icon" aria-hidden="true">${kind === "editor" ? "≡" : "≋"}</span><div><p class="eyebrow">${esc(agent.name)}</p><h2 id="${esc(id)}-title">${title}</h2><p id="${esc(id)}-status" data-bbr-dialog-status>${esc(status)}</p></div><button class="deploy-command-close" type="button" data-bbr-dialog-close aria-label="关闭${title}" autofocus>×</button></header>${content}</dialog>`;
  }

  function dialogButton(agent, kind) {
    const shortTitle = kind === "editor" ? "编辑参数" : "参数详情";
    return `<button class="bbr-dialog-open" type="button" data-bbr-dialog-open="${esc(dialogID(agent.id, kind))}" aria-label="${shortTitle} · ${esc(agent.name)}" aria-haspopup="dialog" aria-controls="${esc(dialogID(agent.id, kind))}"><span>${shortTitle}${kind === "editor" ? `<small data-bbr-draft-label="${esc(agent.id)}" ${Object.keys(lifecycle.drafts[agent.id] || {}).length ? "" : "hidden"}>有草稿</small>` : ""}</span><span aria-hidden="true">→</span></button>`;
  }

  function editor(agent, disabled) {
    if (!editable(agent) || !agent.metrics?.bbr || !(agent.features || []).includes(systemBBRFeature)) return "";
    const current = agent.metrics.bbr.parameters || {};
    const draft = lifecycle.drafts[agent.id] || {};
    const presets = systemTCPPresets.map((preset) => {
      let unavailable = "";
      try { prepareTCPPreset(preset.id, lifecycle.rules, current); }
      catch (error) { unavailable = error.message; }
      return `<div class="bbr-preset"><div><strong>${esc(preset.name)}</strong><small>填入 ${Object.keys(preset.settings).length} 项，保留其他选择</small>${unavailable ? `<p class="bbr-preset-unavailable">${esc(unavailable)}</p>` : ""}</div><button class="button small" type="button" data-tcp-preset="${esc(preset.id)}" ${disabled || unavailable ? "disabled" : ""}>填入预设</button></div>`;
    }).join("");
    const content = `<form novalidate data-tcp-form="${esc(agent.id)}"><div class="bbr-dialog-body" data-refresh-scroll><section class="bbr-presets" aria-label="BBR 参数预设">${presets}</section><p class="bbr-note">仅应用勾选项；关闭保留草稿，刷新页面清空。</p><div class="bbr-fields">${lifecycle.rules.map((rule) => {
      const available = hasTCPParameter(current, rule.key);
      const selected = Object.hasOwn(draft, rule.key);
      const value = draft[rule.key] ?? current[rule.key] ?? "";
      const choices = rule.choices || [];
      const field = choices.length
        ? `<select data-tcp-value="${esc(rule.key)}" aria-label="${esc(rule.label)}" ${disabled || !available ? "disabled" : ""}>${!choices.includes(value) ? `<option value="${esc(value)}">${esc(value || "未上报")}</option>` : ""}${choices.map((choice) => `<option value="${esc(choice)}" ${value === choice ? "selected" : ""}>${esc(choice)}</option>`).join("")}</select>`
        : `<input data-tcp-value="${esc(rule.key)}" aria-label="${esc(rule.label)}" type="${rule.tuple ? "text" : "number"}" ${rule.tuple ? 'placeholder="最小 默认 最大"' : `min="${rule.min || 0}" max="${rule.max}" step="1"`} value="${esc(value)}" ${disabled || !available ? "disabled" : ""}>`;
      return `<div class="bbr-field" data-refresh-key="tcp-field-${esc(agent.id)}-${esc(rule.key)}"><label class="bbr-field-label"><input type="checkbox" data-tcp-selected="${esc(rule.key)}" ${selected ? "checked" : ""} ${disabled || (!available && !selected) ? "disabled" : ""}><span><b>${esc(rule.label)}</b><code>${esc(rule.key)}</code></span></label>${field}<small>当前：${esc(available ? current[rule.key] : "未上报")}${choices.length || !available ? "" : ` · 范围 ${rule.min || 0}–${rule.max}`}</small></div>`;
    }).join("")}</div></div><p class="bbr-warning" data-tcp-error role="alert" ${lifecycle.editorErrors.get(agent.id) ? "" : "hidden"}>${esc(lifecycle.editorErrors.get(agent.id) || "")}</p><footer><span data-tcp-draft-status role="status">${Object.keys(draft).length} 项待应用</span><div><button class="button small" type="button" data-bbr-dialog-close>关闭</button><button class="button small" type="button" data-tcp-reset ${disabled ? "disabled" : ""}>清空选择</button><button class="button small primary" type="submit" ${disabled ? "disabled" : ""}>保存并应用</button></div></footer></form>`;
    return dialogMarkup(agent, "editor", "编辑 TCP 参数", content);
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
      const presetDisabled = disabled || !hasTCPParameter(status?.parameters, "net.core.default_qdisc") || !hasTCPParameter(status?.parameters, "net.ipv4.tcp_congestion_control");
      const hasFeature = (agent.features || []).includes(systemBBRFeature);
      const value = (entry) => esc(entry || "未上报");
      const persistence = status?.persistence === "managed"
        ? `托管 ${Object.keys(status.configured_parameters || {}).length} 项`
        : status?.persistence === "error" ? "配置冲突" : "未托管";
      const drift = status?.persistence === "managed" && Object.entries(status.configured_parameters || {}).some(([key, value]) => String(status.parameters?.[key] || "").trim().replace(/\s+/g, " ") !== value);
      return `<article class="bbr-card" data-refresh-key="bbr-${esc(agent.id)}" aria-busy="${busy}">
        <header><div><h2>${esc(agent.name)}</h2><span>${value(status?.kernel_release)} · ${esc(agent.os)} / ${esc(agent.arch)}</span></div><span class="status-label ${info.tone}">${esc(info.text)}</span></header>
        ${hasFeature && status ? `<dl class="bbr-primary-values"><div><dt>默认算法</dt><dd>${value(status.congestion_control)}</dd></div><div><dt>默认队列</dt><dd>${value(status.default_qdisc)}</dd></div><div><dt>持久配置</dt><dd>${esc(persistence)}</dd></div></dl>
        ${drift ? '<p class="bbr-warning" role="status">当前生效参数与已保存配置不一致，请核对其他系统配置是否覆盖。</p>' : ""}
        ${status.error ? `<p class="bbr-warning" role="status">${esc(diagnosticError(status.error))}</p>` : ""}
        <div class="bbr-card-tools">${dialogButton(agent, "parameters")}${editable(agent) ? dialogButton(agent, "editor") : ""}</div>
        ${dialogMarkup(agent, "parameters", "参数详情", `<div class="bbr-dialog-body" data-refresh-scroll>
          <div class="bbr-note"><strong>已加载算法</strong><span data-bbr-algorithms>${value((status.available_algorithms || []).join(" · "))}</span></div>
          <div class="bbr-table-wrap"><table><caption>内核当前参数（包括面板外设置）</caption><thead><tr><th>参数</th><th>当前值</th><th>面板保存值</th></tr></thead><tbody>${Object.entries(status.parameters || {}).map(([key, entry]) => `<tr><td><code>${esc(key)}</code></td><td><code>${value(entry)}</code></td><td><code>${esc(status.configured_parameters?.[key] || "—")}</code></td></tr>`).join("")}</tbody></table></div>
          <div class="bbr-table-wrap"><table><caption>网卡实际队列（不自动重置）</caption><thead><tr><th>网卡</th><th>队列</th><th>层级</th></tr></thead><tbody>${(status.qdiscs || []).map((qdisc) => `<tr><td>${esc(qdisc.device)}</td><td><code>${esc(qdisc.kind)}</code></td><td>${qdisc.root ? "root" : value(qdisc.parent)} ${esc(qdisc.handle || "")}</td></tr>`).join("") || '<tr><td colspan="3">暂无可读取的网卡队列</td></tr>'}</tbody></table></div>
          ${status.qdisc_error ? `<p class="bbr-note">${esc(diagnosticError(status.qdisc_error))}</p>` : ""}
        </div><footer class="bbr-dialog-footer"><small>参数采集：${status.collected_at ? esc(date(status.collected_at)) : "尚未上报"}</small><button class="button small" type="button" data-bbr-dialog-close>关闭</button></footer>`)}` : `<div class="empty bbr-empty"><strong>${hasFeature ? "等待 Agent 上报系统参数" : "升级 Agent 后可管理 TCP 参数"}</strong></div>`}
        ${editor(agent, disabled)}
        ${task ? `<div class="bbr-task" role="status"><span>${esc(actionLabel(task.action))} · ${esc(taskLabel(task.status))}</span>${can("tasks.read") ? `<a href="#tasks">查看任务记录 →</a>` : ""}${task.error ? `<p>${esc(diagnosticError(task.error))}</p>` : ""}</div>` : ""}
        <footer>${editable(agent) ? `<div class="bbr-actions"><button class="button small" type="button" data-bbr-agent="${esc(agent.id)}" data-bbr-action="disable-bbr" ${presetDisabled ? "disabled" : ""}>切换 CUBIC</button><button class="button small primary" type="button" data-bbr-agent="${esc(agent.id)}" data-bbr-action="enable-bbr" ${presetDisabled ? "disabled" : ""}>启用 BBR</button></div>` : '<span class="bbr-readonly">只读权限</span>'}</footer>
      </article>`;
    }).join("");
    shell(`<div class="bbr-workspace"><div class="bbr-toolbar"><small data-bbr-refresh-status aria-live="polite">${lifecycle.refreshFailed ? "刷新失败，显示上次数据 · 将自动重试" : "自动刷新"}</small><button class="button small" type="button" data-bbr-refresh>刷新状态</button></div><section class="bbr-grid">${cards || '<div class="empty large"><strong>当前范围没有节点</strong><p>添加节点后即可查看系统 BBR 状态。</p></div>'}</section></div>`, "BBR / TCP 调优", { viewKey: `system-bbr-${selectedID() || "all"}` });

    return { agents, focused };
  }
  return render;
}
