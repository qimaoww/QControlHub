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
  const policyStatus = (policy, agent) => {
    if (!(agent?.features || []).includes("port-traffic-v1"))
      return ["需升级 Agent", "warn"];
    if (policy.blocked) return ["已超额封禁", "bad"];
    if (!policy.last_reported_at) return ["等待 Agent 同步", "warn"];
    if (!policy.enforcement_available) return ["监控不可用", "bad"];
    return ["监控中", "ok"];
  };
  const engineOptions = (agent, selected = "") =>
    (agent?.capabilities || [])
      .map((engine) => `<option value="${esc(engine)}" ${engine === selected ? "selected" : ""}>${esc(engineName(engine))}</option>`)
      .join("");
  const policyFields = (policy, agents, prefix) => {
    const selectedAgent = agents.find((agent) => agent.id === policy.agent_id) || agents[0];
    return `<div class="traffic-form-grid">
      <label>节点<select name="agent_id" data-traffic-agent-select="${esc(prefix)}" required>${agents.map((agent) => `<option value="${esc(agent.id)}" ${agent.id === selectedAgent?.id ? "selected" : ""}>${esc(agent.name)}</option>`).join("")}</select></label>
      <label>内核<select name="engine" data-traffic-engine-select="${esc(prefix)}" required>${engineOptions(selectedAgent, policy.engine)}</select></label>
      <label>名称<input name="name" value="${esc(policy.name || "")}" required maxlength="100" placeholder="例如 Reality 入口"></label>
      <label>端口<input name="port" type="number" value="${esc(policy.port || "")}" min="1" max="65535" required placeholder="443"></label>
      <label>协议<select name="protocol"><option value="both" ${policy.protocol === "both" || !policy.protocol ? "selected" : ""}>TCP + UDP</option><option value="tcp" ${policy.protocol === "tcp" ? "selected" : ""}>TCP</option><option value="udp" ${policy.protocol === "udp" ? "selected" : ""}>UDP</option></select></label>
      <label>统计周期<select name="cycle"><option value="monthly" ${policy.cycle === "monthly" || !policy.cycle ? "selected" : ""}>每月</option><option value="yearly" ${policy.cycle === "yearly" ? "selected" : ""}>每年</option></select></label>
      <label>周期起始日期<input name="cycle_anchor" type="date" value="${dateInputValue(policy.cycle_anchor)}" max="${dateInputValue()}" required></label>
      <label>周期额度（G）<input name="limit_gb" type="number" value="${quotaInputValue(policy.limit_bytes)}" min="0.000001" max="8388607" step="0.000001" required placeholder="100"></label>
    </div>`;
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
    };
  };

  const renderTraffic = (agents, policies, resetCreate) => {
    state.data.agents = agents;
    state.data.trafficPolicies = policies;
    const agentByID = new Map(agents.map((agent) => [agent.id, agent]));
    const blocked = policies.filter((policy) => policy.blocked).length;
    const available = policies.filter((policy) => policy.enforcement_available).length;
    const totalUsed = policies.reduce((sum, policy) => sum + Number(policy.used_bytes || 0), 0);
    const totalLimit = policies.reduce((sum, policy) => sum + Number(policy.limit_bytes || 0), 0);
    const createForm = can("traffic.manage") && agents.length
      ? `<details class="traffic-create" id="traffic-new"><summary><span><b>＋ 添加端口配额</b><small>按节点、内核和端口独立统计</small></span><i>＋</i></summary><form id="traffic-policy-form">${policyFields({ cycle_anchor: new Date(), protocol: "both", cycle: "monthly" }, agents, "create")}<p class="traffic-form-note">流量按接收与发送之和计算；达到额度后约 2 秒内封禁该端口，新周期会自动清零并解封。</p><button class="button primary" type="submit">开始监控</button></form></details>`
      : "";
    const cards = policies.map((policy) => {
      const agent = agentByID.get(policy.agent_id);
      const [status, tone] = policyStatus(policy, agent);
      const usedPercent = percent(policy.used_bytes, policy.limit_bytes);
      const period = policy.period_start && policy.period_end
        ? `${dateInputValue(policy.period_start)} 至 ${dateInputValue(policy.period_end)}`
        : `从 ${dateInputValue(policy.cycle_anchor)} 开始${cycleName(policy.cycle)}重置`;
      return `<article class="traffic-policy-card ${policy.blocked ? "is-blocked" : ""}" id="traffic-${esc(policy.id)}" data-refresh-key="traffic-policy-${esc(policy.id)}" data-traffic-agent-card="${esc(policy.agent_id)}">
        <header><div><span class="engine-badge ${esc(policy.engine)}">${esc(engineName(policy.engine))}</span><strong>${esc(policy.name)}</strong><code>:${esc(policy.port)} / ${esc(protocolName(policy.protocol))}</code></div><span class="traffic-policy-status ${tone}"><i></i>${esc(status)}</span></header>
        <section class="traffic-usage"><div><b>${bytes(policy.used_bytes)}</b><span>/ ${bytes(policy.limit_bytes)}</span></div><progress max="100" value="${usedPercent}"></progress><small>${usedPercent.toFixed(1)}% · ${esc(period)}</small></section>
        <dl class="traffic-directions"><div><dt>接收</dt><dd>${bytes(policy.received_bytes)}</dd><small>${rate(policy.receive_bps)}</small></div><div><dt>发送</dt><dd>${bytes(policy.sent_bytes)}</dd><small>${rate(policy.send_bps)}</small></div><div><dt>节点</dt><dd>${esc(agent?.name || policy.agent_id)}</dd><small>${policy.last_reported_at ? `${ago(policy.last_reported_at)}更新` : "尚未上报"}</small></div></dl>
        ${policy.enforcement_error ? `<p class="traffic-error">${esc(policy.enforcement_error)}</p>` : ""}
        ${can("traffic.manage") ? `<footer><button class="button small" type="button" data-traffic-reset="${esc(policy.id)}">立即清零并解封</button><details class="traffic-edit"><summary>编辑</summary><form data-traffic-edit-form="${esc(policy.id)}">${policyFields(policy, agents, `edit-${policy.id}`)}<div><button class="button small" type="submit">保存</button><button class="button small danger-button" type="button" data-traffic-delete="${esc(policy.id)}">删除</button></div></form></details></footer>` : ""}
      </article>`;
    }).join("");
    shell(`<header class="traffic-hero"><div><p class="eyebrow">端口流量</p><h2>流量配额与自动封禁</h2><p>四种内核统一按系统端口统计，接收和发送流量都计入额度。</p></div><section><div><span>监控端口</span><b>${policies.length}</b></div><div><span>正常上报</span><b>${available}</b></div><div class="${blocked ? "bad" : ""}"><span>已封禁</span><b>${blocked}</b></div></section></header><section class="traffic-total"><span data-traffic-refresh-label>全部端口当前周期</span><strong>${bytes(totalUsed)} / ${bytes(totalLimit)}</strong><progress max="100" value="${percent(totalUsed, totalLimit)}"></progress></section>${createForm}${cards ? `<section class="traffic-policy-grid">${cards}</section>` : '<div class="empty large"><strong>还没有端口流量配额</strong><p>添加一个端口后，Agent 会开始统计并在超额时自动封禁。</p></div>'}`, "流量配额");
    bindTrafficForms(agents);
    if (resetCreate) document.querySelector("#traffic-policy-form")?.reset();
    if (state.anchor === "traffic-new") {
      const create = document.querySelector("#traffic-new");
      if (create) create.open = true;
      requestAnimationFrame(() => create?.scrollIntoView({ block: "start" }));
      state.anchor = "traffic";
    } else if (state.anchor?.startsWith("traffic-agent-")) {
      const agentID = state.anchor.slice(14);
      requestAnimationFrame(() => document.querySelector(`[data-traffic-agent-card="${CSS.escape(agentID)}"]`)?.scrollIntoView({ block: "start" }));
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
        (signal) =>
          Promise.all([
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
    document.querySelectorAll('a[href="#traffic-new"]').forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        const create = document.querySelector("#traffic-new");
        if (create) create.open = true;
        create?.scrollIntoView({ block: "start" });
      };
    });
    document.querySelectorAll("[data-traffic-agent-select]").forEach((select) => {
      select.onchange = () => {
        const engineSelect = select.closest("form")?.querySelector("[name=engine]");
        const agent = agents.find((item) => item.id === select.value);
        if (engineSelect) engineSelect.innerHTML = engineOptions(agent);
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
