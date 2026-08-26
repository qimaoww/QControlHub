import {
  bindEvent,
  createPoller,
  createRefreshChannel,
} from "./refresh.js";

export function installTraffic(ctx) {
  const {
    api, state, can, esc, engineName, bytes, rate, percent, ago, shell,
    notify, confirmAction,
    setTimer = (callback, delay) => {
      state.trafficPollTimer = setTimeout(callback, delay);
      return state.trafficPollTimer;
    },
    clearTimer = (timer) => clearTimeout(timer),
  } = ctx;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "traffic",
    getScope: () => state.navigationEpoch,
  });
  const gibibyte = 1024 ** 3;
  const dateInputValue = (value = new Date()) => {
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return "";
    return date.toISOString().slice(0, 10);
  };
  const cycleName = (value) => (value === "yearly" ? "每年" : "每月");
  const protocolName = (value) => ({ tcp: "TCP", udp: "UDP", both: "TCP + UDP" })[value] || value;
  const quotaInputValue = (value) => {
    const gib = Number(value || 0) / gibibyte;
    if (!gib) return "";
    return (gib >= 0.01 ? gib.toFixed(2) : gib.toFixed(6)).replace(/\.?0+$/, "");
  };
  const filters = () => {
    state.data.trafficFilters ||= {};
    return state.data.trafficFilters;
  };
  const policyStatusKey = (policy, agent) => {
    if (!(agent?.features || []).includes("port-traffic-v1")) return "waiting";
    if (policy.blocked) return "blocked";
    if (!policy.last_reported_at) return "waiting";
    if (!policy.enforcement_available) return "error";
    return "active";
  };
  const policyStatus = (policy, agent) => {
    if (!(agent?.features || []).includes("port-traffic-v1")) return ["需升级 Agent", "warn"];
    if (policy.blocked) return ["已超额封禁", "bad"];
    if (!policy.last_reported_at) return ["等待 Agent 同步", "warn"];
    if (!policy.enforcement_available) return ["监控不可用", "bad"];
    if (policy.auto_block === false) return ["仅统计", "neutral"];
    return ["监控中", "ok"];
  };
  const eligibleAgents = (agents) => agents.filter((agent) =>
    (agent.features || []).includes("port-traffic-v1") && (agent.capabilities || []).length,
  );
  const engineOptions = (agent, selected = "") =>
    (agent?.capabilities || [])
      .map((engine) => `<option value="${esc(engine)}" ${engine === selected ? "selected" : ""}>${esc(engineName(engine))}</option>`)
      .join("");
  const agentOption = (agent, selected) =>
    `<option value="${esc(agent.id)}" ${agent.id === selected ? "selected" : ""}>${esc(agent.name)} · ${agent.status === "online" ? "在线" : "离线"}</option>`;
  const policyFields = (policy, agents, prefix) => {
    const selectedAgent = agents.find((agent) => agent.id === policy.agent_id) || agents[0];
    const editing = Boolean(policy.id);
    const agentField = editing
      ? `<label>节点<select disabled>${selectedAgent ? agentOption(selectedAgent, selectedAgent.id) : `<option>${esc(policy.agent_id)}</option>`}</select><input type="hidden" name="agent_id" value="${esc(policy.agent_id)}"></label>`
      : `<label>节点<select name="agent_id" data-traffic-agent-select="${esc(prefix)}" required>${agents.map((agent) => agentOption(agent, selectedAgent?.id)).join("")}</select></label>`;
    return `<div class="traffic-form-grid">
      ${agentField}
      <label>内核<select name="engine" data-traffic-engine-select="${esc(prefix)}" required>${engineOptions(selectedAgent, policy.engine)}</select></label>
      <label>名称<input name="name" value="${esc(policy.name || "")}" required maxlength="100" placeholder="例如 Reality 入口"></label>
      <label>端口<input name="port" type="number" value="${esc(policy.port || "")}" min="1" max="65535" required placeholder="443"></label>
      <label>协议<select name="protocol"><option value="both" ${policy.protocol === "both" || !policy.protocol ? "selected" : ""}>TCP + UDP</option><option value="tcp" ${policy.protocol === "tcp" ? "selected" : ""}>TCP</option><option value="udp" ${policy.protocol === "udp" ? "selected" : ""}>UDP</option></select></label>
      <label>统计周期<select name="cycle"><option value="monthly" ${policy.cycle === "monthly" || !policy.cycle ? "selected" : ""}>每月</option><option value="yearly" ${policy.cycle === "yearly" ? "selected" : ""}>每年</option></select></label>
      <label>周期起始日期<input name="cycle_anchor" type="date" value="${dateInputValue(policy.cycle_anchor)}" max="${dateInputValue()}" required></label>
      <label>周期额度（G）<input name="limit_gb" type="number" value="${quotaInputValue(policy.limit_bytes)}" min="0.000001" max="8388607" step="0.000001" required placeholder="100"></label>
    </div><label class="traffic-auto-block"><input type="checkbox" name="auto_block" value="1" ${policy.auto_block !== false ? "checked" : ""}><span><b>超额自动封禁</b><small>关闭后仍统计流量，但不会阻断端口</small></span></label>`;
  };
  const requestFromForm = (form) => {
    const values = new FormData(form);
    return {
      agent_id: values.get("agent_id"),
      engine: values.get("engine"),
      name: values.get("name"),
      port: Number(values.get("port")),
      protocol: values.get("protocol"),
      cycle: values.get("cycle"),
      cycle_anchor: `${values.get("cycle_anchor")}T00:00:00Z`,
      limit_bytes: Math.round(Number(values.get("limit_gb")) * gibibyte),
      auto_block: values.get("auto_block") === "1",
    };
  };
  const selectOptions = (items, value, label) =>
    `<option value="">${label}</option>${items.map((item) => `<option value="${esc(item.value)}" ${item.value === value ? "selected" : ""}>${esc(item.label)}</option>`).join("")}`;

  const renderTraffic = (agents, policies, resetCreate = false) => {
    state.data.agents = agents;
    state.data.trafficPolicies = policies;
    const currentFilters = filters();
    if (state.anchor === "traffic-all") {
      currentFilters.agent_id = "";
      state.anchor = "traffic";
    } else if (state.anchor?.startsWith("traffic-agent-")) {
      currentFilters.agent_id = state.anchor.slice(14);
      state.anchor = "traffic";
    }
    const agentByID = new Map(agents.map((agent) => [agent.id, agent]));
    const filteredPolicies = policies.filter((policy) => {
      if (currentFilters.agent_id && policy.agent_id !== currentFilters.agent_id) return false;
      if (currentFilters.engine && policy.engine !== currentFilters.engine) return false;
      if (currentFilters.policy_id && policy.id !== currentFilters.policy_id) return false;
      return !currentFilters.status || policyStatusKey(policy, agentByID.get(policy.agent_id)) === currentFilters.status;
    });
    const totalUsed = filteredPolicies.reduce((sum, policy) => sum + Number(policy.used_bytes || 0), 0);
    const totalLimit = filteredPolicies.reduce((sum, policy) => sum + Number(policy.limit_bytes || 0), 0);
    const allEngines = [...new Set(policies.map((policy) => policy.engine).filter(Boolean))];
    const policyChoices = policies.map((policy) => ({
      value: policy.id,
      label: `${policy.name || "未命名端口"} · ${engineName(policy.engine)} :${policy.port}`,
    })).sort((a, b) => a.label.localeCompare(b.label));
    const selectedScope = currentFilters.agent_id
      ? agentByID.get(currentFilters.agent_id)?.name || "所选节点"
      : "全部节点";
    const toolbar = `<section class="traffic-control-panel"><div class="traffic-filters" aria-label="流量筛选">
      <label>内核<select data-traffic-filter="engine">${selectOptions(allEngines.map((engine) => ({ value: engine, label: engineName(engine) })), currentFilters.engine, "全部内核")}</select></label>
      <label>端口配额<select data-traffic-filter="policy_id">${selectOptions(policyChoices, currentFilters.policy_id, "全部端口")}</select></label>
      <label>状态<select data-traffic-filter="status">${selectOptions([{ value: "active", label: "监控中" }, { value: "blocked", label: "已封禁" }, { value: "waiting", label: "等待同步" }, { value: "error", label: "监控异常" }], currentFilters.status, "全部状态")}</select></label>
      <button class="button small" type="button" data-traffic-filter-reset ${Object.values(currentFilters).some(Boolean) ? "" : "disabled"}>清除筛选</button>
    </div><section class="traffic-total"><span data-traffic-refresh-label>${esc(selectedScope)} · 当前周期</span><div><strong>${bytes(totalUsed)}</strong><small>/ ${bytes(totalLimit)}</small></div><progress max="100" value="${percent(totalUsed, totalLimit)}"></progress><em>${filteredPolicies.length} 个端口</em></section></section>`;
    const selectableAgents = eligibleAgents(agents);
    const createDialog = can("traffic.manage")
      ? `<dialog class="traffic-edit-dialog traffic-create-dialog" id="traffic-new" aria-labelledby="traffic-create-title"><header><span class="traffic-edit-icon" aria-hidden="true">＋</span><div><p class="eyebrow">端口流量</p><h2 id="traffic-create-title">添加端口配额</h2><p>选择节点、内核和端口后开始持续统计</p></div><button class="deploy-command-close" type="button" data-traffic-create-close aria-label="关闭添加配额弹窗">×</button></header>${selectableAgents.length ? `<form id="traffic-policy-form"><div class="traffic-edit-body">${policyFields({ cycle_anchor: new Date(), protocol: "both", cycle: "monthly", auto_block: true }, selectableAgents, "create")}</div><footer><span></span><button class="button" type="button" data-traffic-create-close>取消</button><button class="button primary" type="submit">开始监控</button></footer></form>` : '<div class="traffic-create-unavailable"><strong>暂无可配置节点</strong><p>节点需在线并升级到支持端口流量统计的 Agent 后才能添加。</p><button class="button" type="button" data-traffic-create-close>关闭</button></div>'}</dialog>`
      : "";
    const cards = filteredPolicies.map((policy) => {
      const agent = agentByID.get(policy.agent_id);
      const [status, tone] = policyStatus(policy, agent);
      const usedPercent = percent(policy.used_bytes, policy.limit_bytes);
      const period = policy.period_start && policy.period_end
        ? `${dateInputValue(policy.period_start)} 至 ${dateInputValue(policy.period_end)}`
        : `从 ${dateInputValue(policy.cycle_anchor)} 开始${cycleName(policy.cycle)}重置`;
      return `<article class="traffic-policy-card ${policy.blocked ? "is-blocked" : ""}" id="traffic-${esc(policy.id)}" data-refresh-key="traffic-policy-${esc(policy.id)}" data-traffic-agent-card="${esc(policy.agent_id)}">
        <header><div class="traffic-card-identity"><span class="engine-badge ${esc(policy.engine)}">${esc(engineName(policy.engine))}</span><span><strong>${esc(policy.name)}</strong><small>${esc(agent?.name || policy.agent_id)}<i>·</i><code>:${esc(policy.port)}</code><i>·</i>${esc(protocolName(policy.protocol))}</small></span></div><span class="traffic-policy-status ${tone}"><i></i>${esc(status)}</span></header>
        <section class="traffic-card-quota"><header><span>当前周期用量</span><b>${usedPercent.toFixed(1)}%</b></header><div><strong>${bytes(policy.used_bytes)}</strong><span>/ ${bytes(policy.limit_bytes)}</span></div><progress max="100" value="${usedPercent}"></progress><small>${esc(period)}</small></section>
        <section class="traffic-card-transfer"><div><i class="received" aria-hidden="true">↓</i><span><small>接收流量</small><strong>${bytes(policy.received_bytes)}</strong></span><em>${rate(policy.receive_bps)}</em></div><div><i class="sent" aria-hidden="true">↑</i><span><small>发送流量</small><strong>${bytes(policy.sent_bytes)}</strong></span><em>${rate(policy.send_bps)}</em></div></section>
        ${policy.enforcement_error ? `<p class="traffic-error">${esc(policy.enforcement_error)}</p>` : ""}
        <footer><span class="traffic-card-sync"><i class="${policy.last_reported_at ? "ok" : ""}"></i>${policy.last_reported_at ? `${ago(policy.last_reported_at)}更新` : "等待 Agent 上报"}</span>${can("traffic.manage") ? `<div class="traffic-card-actions"><button class="button small" type="button" data-traffic-reset="${esc(policy.id)}">清零</button><button class="button small" type="button" data-traffic-edit-open="${esc(policy.id)}">编辑</button></div>` : ""}</footer>${can("traffic.manage") ? `<dialog class="traffic-edit-dialog" data-traffic-edit-dialog="${esc(policy.id)}" aria-labelledby="traffic-edit-title-${esc(policy.id)}"><header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">端口配额</p><h2 id="traffic-edit-title-${esc(policy.id)}">编辑 ${esc(policy.name)}</h2><p>${esc(agent?.name || policy.agent_id)} · ${esc(engineName(policy.engine))} :${esc(policy.port)}</p></div><button class="deploy-command-close" type="button" data-traffic-edit-close aria-label="关闭编辑弹窗">×</button></header><form data-traffic-edit-form="${esc(policy.id)}"><div class="traffic-edit-body">${policyFields(policy, [agent].filter(Boolean), `edit-${policy.id}`)}</div><footer><button class="button small danger-button" type="button" data-traffic-delete="${esc(policy.id)}">删除配额</button><span></span><button class="button" type="button" data-traffic-edit-close>取消</button><button class="button primary" type="submit">保存修改</button></footer></form></dialog>` : ""}
      </article>`;
    }).join("");
    const empty = policies.length
      ? '<div class="empty large"><strong>没有符合筛选条件的端口</strong><p>调整上方筛选条件后再查看。</p></div>'
      : '<div class="empty large"><strong>还没有端口流量配额</strong><p>添加一个端口后，Agent 会开始上报流量。</p></div>';
    const listHeader = `<header class="traffic-policy-list-head"><div><h2>端口配额</h2><span>${filteredPolicies.length}</span></div><small>${esc(selectedScope)} · Agent 实时上报</small></header>`;
    shell(`<div class="traffic-workspace">${toolbar}${listHeader}${cards ? `<section class="traffic-policy-grid">${cards}</section>` : empty}${createDialog}</div>`, "流量配额");
    bindTrafficForms(selectableAgents);
    if (resetCreate) document.querySelector("#traffic-policy-form")?.reset();
    if (state.anchor === "traffic-new") {
      const create = document.querySelector("#traffic-new");
      create?.showModal();
      state.anchor = "traffic";
    }
  };

  const poller = createPoller({
    run: () => traffic({ background: true }),
    isActive: () => state.route === "traffic",
    delay: () => 5000,
    setTimer,
    clearTimer,
  });

  async function traffic({ background = false, resetCreate = false } = {}) {
    poller.stop();
    try {
      const applied = await refresh.run(
        (signal) => Promise.all([
          api("/agents", { signal }),
          api("/traffic-policies", { signal }),
        ]),
        ([agents, policies]) => renderTraffic(agents, policies, resetCreate),
      );
      if (applied) poller.start();
      return applied;
    } catch (error) {
      const status = document.querySelector(".traffic-total");
      if (!status && !background) throw error;
      if (status) {
        status.dataset.refreshError = "1";
        status.title = error.message;
        const label = status.querySelector("[data-traffic-refresh-label]");
        if (label) label.textContent = "刷新失败，保留上次数据";
      }
      poller.start();
      return false;
    }
  }

  function bindTrafficForms(agents) {
    document.querySelectorAll("[data-traffic-filter]").forEach((control) => {
      control.onchange = () => {
        filters()[control.dataset.trafficFilter] = control.value;
        renderTraffic(state.data.agents || [], state.data.trafficPolicies || []);
      };
    });
    const reset = document.querySelector("[data-traffic-filter-reset]");
    if (reset) reset.onclick = () => {
      state.data.trafficFilters = {};
      renderTraffic(state.data.agents || [], state.data.trafficPolicies || []);
    };
    document.querySelectorAll('a[href="#traffic-new"]').forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        const create = document.querySelector("#traffic-new");
        create?.showModal();
      };
    });
    const createDialog = document.querySelector("#traffic-new");
    if (createDialog) {
      createDialog.querySelectorAll("[data-traffic-create-close]").forEach((button) => {
        button.onclick = () => createDialog.close();
      });
      createDialog.onclick = (event) => {
        if (event.target === createDialog) createDialog.close();
      };
    }
    document.querySelectorAll("[data-traffic-agent-select]").forEach((select) => {
      select.onchange = () => {
        const engineSelect = select.closest("form")?.querySelector("[name=engine]");
        const agent = agents.find((item) => item.id === select.value);
        if (engineSelect) engineSelect.innerHTML = engineOptions(agent);
      };
    });
    document.querySelectorAll("[data-traffic-edit-open]").forEach((button) => {
      button.onclick = () => {
        const dialog = document.querySelector(`[data-traffic-edit-dialog="${CSS.escape(button.dataset.trafficEditOpen)}"]`);
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-traffic-edit-dialog]").forEach((dialog) => {
      dialog.querySelectorAll("[data-traffic-edit-close]").forEach((button) => {
        button.onclick = () => dialog.close();
      });
      dialog.onclick = (event) => {
        if (event.target === dialog) dialog.close();
      };
    });
    bindEvent(document.querySelector("#traffic-policy-form"), "submit", async (event) => {
      event.preventDefault();
      try {
        await api("/traffic-policies", { method: "POST", body: JSON.stringify(requestFromForm(event.currentTarget)) });
        notify("端口流量配额已创建，正在同步到 Agent");
        await traffic({ resetCreate: true });
      } catch (error) { notify(error.message, "error"); }
    });
    document.querySelectorAll("[data-traffic-edit-form]").forEach((form) => {
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        try {
          await api(`/traffic-policies/${encodeURIComponent(form.dataset.trafficEditForm)}`, { method: "PUT", body: JSON.stringify(requestFromForm(form)) });
          notify("端口流量配额已更新");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      });
    });
    document.querySelectorAll("[data-traffic-reset]").forEach((button) => {
      button.onclick = async () => {
        if (!(await confirmAction("确定立即清零这个端口的当前周期流量并解除封禁？", "清零并解封"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficReset)}/reset`, { method: "POST" });
          notify("当前周期流量已清零，正在同步解封");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
    document.querySelectorAll("[data-traffic-delete]").forEach((button) => {
      button.onclick = async () => {
        if (!(await confirmAction("删除后 Agent 会停止统计并移除该端口的自动封禁规则，确定继续？", "删除配额"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficDelete)}`, { method: "DELETE" });
          notify("端口流量配额已删除");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
  }

  return traffic;
}
