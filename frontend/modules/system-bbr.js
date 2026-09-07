import { bindEvent, createPoller, createRefreshChannel } from "./refresh.js";

export const systemBBRFeature = "system-bbr-v1";
export const systemBBRActions = ["enable-bbr", "disable-bbr", "configure-tcp"];

export function validateTCPSelection(input, rules) {
  if (!Object.keys(input || {}).length) throw new Error("请至少勾选一个需要保存的参数");
  return Object.fromEntries(Object.entries(input).map(([key, raw]) => {
    const rule = rules.find((entry) => entry.key === key);
    const value = String(raw).trim().replace(/\s+/g, " ");
    if (!rule || new TextEncoder().encode(String(raw)).length > 100 || /[\r\n\x00]/.test(raw)) throw new Error(`不支持的参数或取值：${key}`);
    if (rule.choices?.length) {
      if (!rule.choices.includes(value)) throw new Error(`${rule.label}：请选择支持的选项`);
      return [key, value];
    }
    const values = value.split(" ");
    if (values.length !== (rule.tuple ? 3 : 1) || values.some((part, index) => !/^\d+$/.test(part) || Number(part) < (rule.min || 0) || Number(part) > rule.max || (index > 0 && Number(part) < Number(values[index - 1]))))
      throw new Error(`${rule.label}：请输入 ${rule.min || 0}–${rule.max} 范围内的${rule.tuple ? "三个递增整数" : "整数"}`);
    return [key, values.map(Number).join(" ")];
  }));
}

export function systemBBRState(agent, now = Date.now()) {
  const status = agent.metrics?.bbr;
  if (!(agent.features || []).includes(systemBBRFeature))
    return { text: "需升级 Agent", tone: "", controllable: false };
  if (agent.status !== "online")
    return { text: "节点离线 · 历史状态", tone: "", controllable: false };
  const collected = Date.parse(status?.collected_at || "");
  if (!Number.isFinite(collected) || now - collected > 90_000 || collected > now + 30_000)
    return { text: "等待最新状态", tone: "warn", controllable: false };
  if (!status?.available)
    return { text: "系统参数不可用", tone: "warn", controllable: false };
  return {
    text: /^bbr/.test(status.congestion_control) ? `BBR 已启用 · ${status.congestion_control}` : `当前默认 ${status.congestion_control || "未知"}`,
    tone: /^bbr/.test(status.congestion_control) ? "ok" : "",
    controllable: status.persistence !== "error",
  };
}

export function installSystemBBR(ctx) {
  const { api, state, can, esc, date, shell, notify, confirmAction,
    setTimer = (fn, delay) => (state.bbrPollTimer = setTimeout(fn, delay)),
    clearTimer = clearTimeout } = ctx;
  const submitting = new Set();
  const localTasks = new Map();
  const drafts = (state.data.bbrDrafts ||= {});
  let rules = null;
  let lastAgents = null;
  let refreshFailed = false;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "system-bbr",
    getScope: () => state.navigationEpoch,
  });
  const editable = () => can("agents.manage") && can("tasks.execute");
  const selectedID = () => state.anchor?.startsWith("system-bbr-agent-")
    ? state.anchor.slice("system-bbr-agent-".length) : "";
  const taskLabel = (status) => ({ pending: "等待执行", running: "执行中", succeeded: "执行成功", failed: "执行失败", canceled: "已取消", submitted: "已提交（无任务查看权限）" })[status] || status;
  const actionLabel = (action) => ({ "enable-bbr": "启用 BBR", "disable-bbr": "切换 CUBIC", "configure-tcp": "自定义 TCP 调优" })[action] || action;

  function editor(agent, disabled) {
    if (!editable() || !agent.metrics?.bbr || !(agent.features || []).includes(systemBBRFeature)) return "";
    const current = agent.metrics.bbr.parameters || {};
    const draft = drafts[agent.id] || {};
    return `<details class="bbr-details bbr-editor" data-refresh-key="tcp-editor-${esc(agent.id)}"><summary>自定义 BBR / TCP 参数${Object.keys(draft).length ? " · 有未提交草稿" : ""}</summary><form novalidate data-tcp-form="${esc(agent.id)}"><p class="bbr-note">勾选需要管理的参数；编辑会自动勾选。仅应用勾选项，其他系统参数和既有托管项保持不变。草稿在本次页面会话中保留，刷新浏览器会丢失。</p><div class="bbr-fields">${rules.map((rule) => {
      const available = Object.hasOwn(current, rule.key);
      const value = draft[rule.key] ?? current[rule.key] ?? "";
      const choices = rule.choices || [];
      const field = choices.length
        ? `<select data-tcp-value="${esc(rule.key)}" aria-label="${esc(rule.label)}" ${disabled || !available ? "disabled" : ""}>${!choices.includes(value) ? `<option value="${esc(value)}">${esc(value || "未上报")}</option>` : ""}${choices.map((choice) => `<option value="${choice}" ${value === choice ? "selected" : ""}>${choice}</option>`).join("")}</select>`
        : `<input data-tcp-value="${esc(rule.key)}" aria-label="${esc(rule.label)}" type="${rule.tuple ? "text" : "number"}" ${rule.tuple ? 'placeholder="最小 默认 最大"' : `min="${rule.min || 0}" max="${rule.max}" step="1"`} value="${esc(value)}" ${disabled || !available ? "disabled" : ""}>`;
      return `<div class="bbr-field" data-refresh-key="tcp-field-${esc(agent.id)}-${esc(rule.key)}"><label class="bbr-field-label"><input type="checkbox" data-tcp-selected="${esc(rule.key)}" ${Object.hasOwn(draft, rule.key) ? "checked" : ""} ${disabled || !available ? "disabled" : ""}><span><b>${esc(rule.label)}</b><code>${esc(rule.key)}</code></span></label>${field}<small>当前：${esc(current[rule.key] || "此系统未提供，不能修改")}${choices.length ? "" : ` · 范围 ${rule.min || 0}–${rule.max}`}</small></div>`;
    }).join("")}</div><footer><span data-tcp-draft-status role="status">${Object.keys(draft).length} 项待提交</span><div><button class="button small" type="button" data-tcp-reset ${disabled ? "disabled" : ""}>清空选择</button><button class="button small primary" type="submit" ${disabled ? "disabled" : ""}>保存并应用选中参数</button></div></footer></form></details>`;
  }

  function render(agents) {
    state.data.agents = agents;
    state.data.bbrAgent = selectedID();
    const visible = selectedID() ? agents.filter((agent) => agent.id === selectedID()) : agents;
    const cards = visible.map((agent) => {
      const info = systemBBRState(agent);
      const status = agent.metrics?.bbr;
      const task = localTasks.get(agent.id);
      const busy = submitting.has(agent.id) || ["pending", "running"].includes(task?.status);
      const disabled = !editable() || !info.controllable || busy;
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
        ${status.error ? `<p class="bbr-warning" role="status">${esc(status.error)}</p>` : ""}
        <details class="bbr-details" data-refresh-key="bbr-parameters-${esc(agent.id)}"><summary>生效参数与网卡队列</summary>
          <div class="bbr-table-wrap"><table><caption>内核当前参数（包括面板外设置）</caption><thead><tr><th>参数</th><th>当前值</th><th>面板保存值</th></tr></thead><tbody>${Object.entries(status.parameters || {}).map(([key, entry]) => `<tr><td><code>${esc(key)}</code></td><td><code>${value(entry)}</code></td><td><code>${esc(status.configured_parameters?.[key] || "—")}</code></td></tr>`).join("")}</tbody></table></div>
          <div class="bbr-table-wrap"><table><caption>网卡实际队列（不自动重置）</caption><thead><tr><th>网卡</th><th>队列</th><th>层级</th></tr></thead><tbody>${(status.qdiscs || []).map((qdisc) => `<tr><td>${esc(qdisc.device)}</td><td><code>${esc(qdisc.kind)}</code></td><td>${qdisc.root ? "root" : value(qdisc.parent)} ${esc(qdisc.handle || "")}</td></tr>`).join("") || '<tr><td colspan="3">暂无可读取的网卡队列</td></tr>'}</tbody></table></div>
          ${status.qdisc_error ? `<p class="bbr-note">${esc(status.qdisc_error)}</p>` : ""}
        </details>` : `<div class="empty"><strong>${hasFeature ? "等待 Agent 上报系统参数" : "请先升级此节点的 Agent"}</strong><p>本页不会把未上报或旧版本节点显示为 BBR 已关闭。</p></div>`}
        ${editor(agent, disabled)}
        ${task ? `<div class="bbr-task" role="status"><span>${esc(actionLabel(task.action))} · ${esc(taskLabel(task.status))}</span>${can("tasks.read") ? `<a href="#tasks">查看任务记录 →</a>` : ""}${task.error ? `<p>${esc(task.error)}</p>` : ""}</div>` : ""}
        <footer><small>参数采集：${status?.collected_at ? esc(date(status.collected_at)) : "尚未上报"}</small>${editable() ? `<div class="bbr-actions"><button class="button small" type="button" data-bbr-agent="${esc(agent.id)}" data-bbr-action="disable-bbr" ${presetDisabled ? "disabled" : ""}>关闭 BBR / 切换 CUBIC</button><button class="button small primary" type="button" data-bbr-agent="${esc(agent.id)}" data-bbr-action="enable-bbr" ${presetDisabled ? "disabled" : ""}>启用 BBR</button></div>` : '<span class="bbr-readonly">只读权限</span>'}</footer>
      </article>`;
    }).join("");
    shell(`<div class="bbr-workspace"><div class="bbr-toolbar"><small data-bbr-refresh-status aria-live="polite">${refreshFailed ? "刷新失败，显示上次数据 · 将自动重试" : "自动刷新 · Agent 心跳采集"}</small><button class="button small" type="button" data-bbr-refresh>刷新状态</button></div><section class="bbr-grid">${cards || '<div class="empty large"><strong>当前范围没有节点</strong><p>添加节点后即可查看系统 BBR 状态。</p></div>'}</section></div>`, "BBR / TCP 调优", { viewKey: `system-bbr-${selectedID() || "all"}` });
    bindEvent(document.querySelector("[data-bbr-refresh]"), "click", () => systemBBR());
    document.querySelectorAll("[data-bbr-action]").forEach((button) => {
      bindEvent(button, "click", () => submitChange(
        agents.find((entry) => entry.id === button.dataset.bbrAgent), button.dataset.bbrAction,
      ));
    });
    document.querySelectorAll("[data-tcp-form]").forEach((form) => {
      const agent = agents.find((entry) => entry.id === form.dataset.tcpForm);
      // Attribute reconciliation alone does not clear the browser's dirty
      // value/checked flags. The per-node draft is the authoritative editor
      // state; synchronize properties after an explicit clear or submission.
      form.querySelectorAll("[data-tcp-selected]").forEach((input) => {
        input.checked = Object.hasOwn(drafts[agent.id] || {}, input.dataset.tcpSelected);
      });
      form.querySelectorAll("[data-tcp-value]").forEach((input) => {
        const desired = drafts[agent.id]?.[input.dataset.tcpValue] ?? agent.metrics?.bbr?.parameters?.[input.dataset.tcpValue] ?? "";
        if (input.value !== desired) input.value = desired;
      });
      const capture = () => {
        const draft = {};
        form.querySelectorAll("[data-tcp-selected]").forEach((checkbox) => {
          if (checkbox.checked) draft[checkbox.dataset.tcpSelected] = form.querySelector(`[data-tcp-value="${checkbox.dataset.tcpSelected}"]`).value;
        });
        drafts[agent.id] = draft;
        const label = form.querySelector("[data-tcp-draft-status]");
        if (label) label.textContent = `${Object.keys(draft).length} 项待提交 · 草稿已保留`;
      };
      form.querySelectorAll("[data-tcp-value]").forEach((input) => {
        bindEvent(input, "input", () => {
          form.querySelector(`[data-tcp-selected="${input.dataset.tcpValue}"]`).checked = true;
          capture();
        });
        bindEvent(input, "change", () => {
          form.querySelector(`[data-tcp-selected="${input.dataset.tcpValue}"]`).checked = true;
          capture();
        });
      });
      form.querySelectorAll("[data-tcp-selected]").forEach((input) => bindEvent(input, "change", capture));
      bindEvent(form.querySelector("[data-tcp-reset]"), "click", async () => {
        const epoch = state.navigationEpoch;
        if (Object.keys(drafts[agent.id] || {}).length && !(await confirmAction("确定清空此节点未提交的 TCP 参数选择？已保存的系统配置不受影响。", "清空选择"))) return;
        if (state.route !== "system-bbr" || epoch !== state.navigationEpoch) return;
        delete drafts[agent.id];
        // Local editor state must settle even when the network is unavailable.
        render(lastAgents);
      });
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        capture();
        try {
          const settings = validateTCPSelection(drafts[agent.id], rules);
          await submitChange(agent, "configure-tcp", settings);
        } catch (error) {
          notify(error.message, "error");
        }
      });
    });
  }

  async function submitChange(agent, action, settings) {
    if (!agent || !editable() || !systemBBRActions.includes(action) || !systemBBRState(agent).controllable || submitting.has(agent.id) || ["pending", "running"].includes(localTasks.get(agent.id)?.status)) return;
    submitting.add(agent.id);
    document.querySelectorAll("[data-bbr-action]").forEach((entry) => {
      if (entry.dataset.bbrAgent === agent.id) entry.disabled = true;
    });
    document.querySelectorAll("[data-tcp-form]").forEach((form) => {
      if (form.dataset.tcpForm === agent.id) form.querySelectorAll("input, select, button").forEach((entry) => (entry.disabled = true));
    });
    const epoch = state.navigationEpoch;
    try {
      const changes = settings || {
        "net.ipv4.tcp_congestion_control": action === "enable-bbr" ? "bbr" : "cubic",
        "net.core.default_qdisc": "fq",
      };
      const summary = Object.entries(changes).map(([key, value]) => `${key}: ${agent.metrics?.bbr?.parameters?.[key] || "未知"} → ${value}`).join("\n");
      if (!(await confirmAction(`确定对「${agent.name}」应用以下系统参数？\n${summary}\n保存到 /etc/sysctl.d/90-qcontrolhub-bbr.conf；未选参数保持不变，不重启网络或重置现有连接、网卡队列。请确认这些值适合该节点。`, actionLabel(action)))) return;
      if (state.route !== "system-bbr" || epoch !== state.navigationEpoch) return;
      const current = lastAgents?.find((entry) => entry.id === agent.id);
      if (!editable() || !current || !systemBBRState(current).controllable || ["pending", "running"].includes(localTasks.get(agent.id)?.status)) {
        notify("节点或任务状态已变化，请刷新核对后重新操作。", "error");
        return;
      }
      const task = await api("/tasks", { method: "POST", body: JSON.stringify({
        agent_id: agent.id, action, engine: "", ...(settings ? { tcp_settings: settings } : {}),
      }) });
      localTasks.set(agent.id, can("tasks.read") ? task : { ...task, status: "submitted" });
      if (settings) delete drafts[agent.id];
      notify("TCP 调优任务已提交，等待 Agent 执行；实际参数以采集结果为准。");
    } catch (error) {
      notify(error.message, "error");
    } finally {
      submitting.delete(agent.id);
      if (state.route === "system-bbr" && epoch === state.navigationEpoch) {
        // Cancel/failure/submission must update controls before an optional
        // network refresh, which can be slow or fail entirely.
        render(lastAgents);
        await systemBBR({ background: true });
      }
    }
  }
  const poller = createPoller({ run: () => systemBBR({ background: true }),
    isActive: () => state.route === "system-bbr", delay: () => 5000, setTimer, clearTimer });

  async function systemBBR({ background = false } = {}) {
    poller.stop();
    try {
      const params = selectedID() ? `?agent_id=${encodeURIComponent(selectedID())}` : "";
      const applied = await refresh.run((signal) => Promise.all([
        api("/agents", { signal }),
        rules ? Promise.resolve(rules) : api("/system-tcp/parameters", { signal }),
        can("tasks.read") ? api(`/system-tcp/tasks${params}`, { signal }) : Promise.resolve([]),
      ]), ([agents, parameters, tasks]) => {
        rules = parameters;
        lastAgents = agents;
        refreshFailed = false;
        if (can("tasks.read")) {
          // The response is authoritative for this scope, including tasks
          // removed by retention; never keep an obsolete busy state forever.
          if (selectedID()) localTasks.delete(selectedID());
          else localTasks.clear();
          for (const task of tasks) localTasks.set(task.agent_id, task);
        }
        render(agents);
      });
      if (applied) poller.start();
      return applied;
    } catch (error) {
      if (!background) notify(error.message, "error");
      refreshFailed = true;
      if (lastAgents) render(lastAgents);
      else shell('<div class="empty large"><strong>无法读取系统 BBR 状态</strong><p>请检查节点查看权限或稍后刷新重试。</p><button class="button" data-bbr-retry>重试</button></div>', "系统 BBR");
      bindEvent(document.querySelector("[data-bbr-retry]"), "click", () => systemBBR());
      poller.start();
      return false;
    }
  }
  return systemBBR;
}
