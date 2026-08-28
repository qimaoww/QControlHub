import {
  bindEvent,
  createInteractionGate,
  createPoller,
  createRefreshChannel,
} from "./refresh.js";
import {
  animateNodeCardDrop,
  nodeCardDropIndex,
} from "./agents.js";

const trafficPortIdentity = (item) => `${item.agent_id}:${item.port}`;
const trafficEndpointKey = (item) => `endpoint:${item.agent_id}:${item.engine}:${item.port}:${item.protocol}`;
const trafficCardOrderKey = "qcontrolhub:traffic-card-order";

export const trafficCardIdentity = (item) =>
  trafficPortIdentity(item.policy || item.endpoint || item);

export function orderTrafficItems(items = [], savedOrder = []) {
  if (!savedOrder.length) return items;
  const position = new Map(savedOrder.map((key, index) => [key, index]));
  return [...items].sort(
    (left, right) =>
      (position.get(trafficCardIdentity(left)) ?? savedOrder.length) -
      (position.get(trafficCardIdentity(right)) ?? savedOrder.length),
  );
}

// Reordering a filtered view changes only the visible slots in the complete
// card order. Hidden cards keep their relative positions and are not lost
// when the persisted order is updated.
export function mergeVisibleTrafficCardOrder(allKeys = [], visibleKeys = []) {
  const visible = new Set(visibleKeys);
  let nextVisible = 0;
  const merged = allKeys.map((key) =>
    visible.has(key) ? visibleKeys[nextVisible++] : key,
  );
  for (const key of visibleKeys) {
    if (!merged.includes(key)) merged.push(key);
  }
  return merged;
}

// The stored rate describes the last complete Agent report interval. It is no
// longer live once that Agent is offline or the next report is overdue.
export function trafficRateForDisplay(
  value,
  lastReportedAt,
  agentStatus,
  now = Date.now(),
) {
  const reportedAt = Date.parse(lastReportedAt || "");
  const age = now - reportedAt;
  if (
    agentStatus !== "online" ||
    !Number.isFinite(reportedAt) ||
    age < -5000 ||
    age > 45_000
  )
    return 0;
  return Number(value || 0);
}

export function mergeTrafficPorts(policies = [], endpoints = []) {
  const monitored = new Set(policies.map(trafficPortIdentity));
  const visiblePolicies = policies.filter((policy) => policy.monitoring_enabled !== false);
  const discovered = new Map();
  for (const endpoint of endpoints) {
    const identity = trafficPortIdentity(endpoint);
    if (!monitored.has(identity) && !discovered.has(identity)) discovered.set(identity, endpoint);
  }
  return [
    ...visiblePolicies.map((policy) => ({ kind: "policy", key: `policy:${policy.id}`, policy })),
    ...[...discovered.values()]
      .map((endpoint) => ({ kind: "endpoint", key: trafficEndpointKey(endpoint), endpoint })),
  ];
}

export function resetTrafficCreateForm(form, agents = [], renderEngineOptions = () => "") {
  if (!form) return;
  form.reset();
  const agentSelect = form.querySelector("[name=agent_id]");
  const engineSelect = form.querySelector("[name=engine]");
  const agent = agents.find((candidate) => candidate.id === agentSelect?.value);
  if (engineSelect) engineSelect.innerHTML = renderEngineOptions(agent);
}

export function installTraffic(ctx) {
  const {
    api, state, can, esc, engineName, bytes, rate, percent, ago, shell,
    notify, confirmAction,
    storage = globalThis.localStorage,
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
  const cardInteractions = createInteractionGate();
  let cancelTrafficCardDrag = () => {};
  let pendingTrafficRender = null;
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
    if (policy.quota_enabled === false) return "metering";
    return "active";
  };
  const policyStatus = (policy, agent) => {
    if (!(agent?.features || []).includes("port-traffic-v1")) return ["需升级 Agent", "warn"];
    if (policy.blocked) return ["已超额封禁", "bad"];
    if (!policy.last_reported_at) return ["等待 Agent 同步", "warn"];
    if (!policy.enforcement_available) return ["监控不可用", "bad"];
    if (policy.quota_enabled === false) return ["持续统计", "ok"];
    if (policy.auto_block === false) return ["配额统计", "neutral"];
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
      <label>周期额度（G）<input name="limit_gb" type="number" value="${quotaInputValue(policy.quota_enabled === false ? 0 : policy.limit_bytes)}" min="0.000001" max="8388607" step="0.000001" required placeholder="100"></label>
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

  const savedTrafficCardOrder = () => {
    try {
      const parsed = JSON.parse(storage?.getItem(trafficCardOrderKey));
      if (!Array.isArray(parsed)) return [];
      return [...new Set(parsed.filter((key) => typeof key === "string" && key))];
    } catch {
      return [];
    }
  };
  const saveTrafficCardOrder = (keys) => {
    try {
      storage?.setItem(trafficCardOrderKey, JSON.stringify(keys));
    } catch {}
  };

  function enableTrafficCardDrag(grid, allKeys) {
    let drag = null;
    let cancelLanding = null;
    const cards = () => [...grid.querySelectorAll("[data-traffic-card-key]")];
    const clear = (clearAnimationStyles = true) => {
      if (!drag) return;
      drag.card.classList.remove("dragging");
      document.body.classList.remove("traffic-card-dragging");
      cards().forEach((card) => {
        card.classList.remove("drop-target");
        if (clearAnimationStyles) {
          card.style.transform = "";
          card.style.transition = "";
        }
      });
      drag.ghost?.remove();
      drag = null;
    };
    const reset = () => {
      const landing = cancelLanding;
      cancelLanding = null;
      const releaseInteraction = drag?.releaseInteraction;
      landing?.();
      clear(true);
      if (!landing) releaseInteraction?.();
    };
    cancelTrafficCardDrag = reset;
    const highlight = (index) => {
      cards().forEach((card) => card.classList.remove("drop-target"));
      if (index == null) return;
      cards().filter((card) => card !== drag.card)[index]?.classList.add("drop-target");
    };
    const finish = (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      const { card, moved, drop, releaseInteraction } = drag;
      if (!moved || !grid.contains(card)) return reset();
      const rest = cards().filter((item) => item !== card);
      const ghostRect = drag.ghost
        ? drag.ghost.getBoundingClientRect()
        : card.getBoundingClientRect();
      const oldRects = new Map(
        [card, ...rest].map((item) => [
          item,
          item === card ? ghostRect : item.getBoundingClientRect(),
        ]),
      );
      const target = drop == null || drop >= rest.length ? null : rest[drop];
      if (target) target.before(card);
      else grid.append(card);
      const next = cards();
      const visibleKeys = next.map((item) => item.dataset.trafficCardKey);
      saveTrafficCardOrder(mergeVisibleTrafficCardOrder(allKeys, visibleKeys));
      clear(false);
      let settled = false;
      const cancelAnimation = animateNodeCardDrop(next, oldRects, {
        onSettled: () => {
          settled = true;
          cancelLanding = null;
          releaseInteraction();
        },
      });
      if (!settled) cancelLanding = cancelAnimation;
    };
    const cancel = (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      reset();
    };
    grid.querySelectorAll(".traffic-card-grip").forEach((grip) => {
      bindEvent(grip, "pointerdown", (event) => {
        if (event.button !== 0 || drag) return;
        const releaseInteraction = cardInteractions.begin();
        cancelLanding?.();
        cancelLanding = null;
        const card = grip.closest("[data-traffic-card-key]");
        if (!card) {
          releaseInteraction();
          return;
        }
        event.preventDefault();
        const rect = card.getBoundingClientRect();
        drag = {
          card,
          pointerId: event.pointerId,
          startX: event.clientX,
          startY: event.clientY,
          grabOffset: {
            x: event.clientX - (rect.left + rect.width / 2),
            y: event.clientY - (rect.top + rect.height / 2),
          },
          rect,
          moved: false,
          drop: null,
          ghost: null,
          releaseInteraction,
        };
        grip.setPointerCapture(event.pointerId);
      });
      bindEvent(grip, "pointermove", (event) => {
        if (!drag || event.pointerId !== drag.pointerId) return;
        if (!drag.card.isConnected) return reset();
        if (!drag.ghost) {
          if (Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4) return;
          drag.card.classList.add("dragging");
          document.body.classList.add("traffic-card-dragging");
          const ghost = drag.card.cloneNode(true);
          ghost.classList.remove("dragging");
          ghost.classList.add("traffic-card-ghost");
          ghost.removeAttribute("id");
          ghost.removeAttribute("data-traffic-card-key");
          ghost.querySelectorAll("dialog,[id]").forEach((element) => {
            if (element.matches("dialog")) element.remove();
            else element.removeAttribute("id");
          });
          ghost.style.position = "fixed";
          ghost.style.left = `${drag.rect.left}px`;
          ghost.style.top = `${drag.rect.top}px`;
          ghost.style.width = `${drag.rect.width}px`;
          drag.ghost = ghost;
          document.body.appendChild(ghost);
        }
        drag.moved = true;
        const dx = event.clientX - drag.startX;
        const dy = event.clientY - drag.startY;
        drag.ghost.style.transform = `translate(${dx}px, ${dy}px) scale(.99) rotate(.3deg)`;
        const rects = cards().map((card) => card.getBoundingClientRect());
        drag.drop = nodeCardDropIndex(
          rects,
          { x: event.clientX, y: event.clientY },
          drag.grabOffset,
        );
        highlight(drag.drop);
      });
      bindEvent(grip, "pointerup", finish);
      bindEvent(grip, "pointercancel", cancel);
      bindEvent(grip, "lostpointercapture", cancel);
    });
  }

  const renderTraffic = (agents, policies, endpoints, resetCreate = false) => {
    if (cardInteractions.activeCount()) {
      pendingTrafficRender = [agents, policies, endpoints, resetCreate];
      cardInteractions.defer(() => {
        const pending = pendingTrafficRender;
        pendingTrafficRender = null;
        if (pending && state.route === "traffic") renderTraffic(...pending);
      }, "traffic-card-refresh");
      return;
    }
    cancelTrafficCardDrag();
    cancelTrafficCardDrag = () => {};
    state.data.agents = agents;
    state.data.trafficPolicies = policies;
    state.data.trafficEndpoints = endpoints;
    const currentFilters = filters();
    if (state.anchor === "traffic-all") {
      currentFilters.agent_id = "";
      state.anchor = "traffic";
    } else if (state.anchor?.startsWith("traffic-agent-")) {
      currentFilters.agent_id = state.anchor.slice(14);
      state.anchor = "traffic";
    }
    const agentByID = new Map(agents.map((agent) => [agent.id, agent]));
    const items = mergeTrafficPorts(policies, endpoints);
    const orderedItems = orderTrafficItems(items, savedTrafficCardOrder());
    const filteredItems = orderedItems.filter((item) => {
      const value = item.policy || item.endpoint;
      if (currentFilters.agent_id && value.agent_id !== currentFilters.agent_id) return false;
      if (currentFilters.engine && value.engine !== currentFilters.engine) return false;
      if (currentFilters.endpoint_key && item.key !== currentFilters.endpoint_key) return false;
      const status = item.kind === "endpoint" ? "waiting" : policyStatusKey(item.policy, agentByID.get(value.agent_id));
      return !currentFilters.status || status === currentFilters.status;
    });
    const filteredPolicies = filteredItems.filter((item) => item.kind === "policy").map((item) => item.policy);
    const quotaPolicies = filteredPolicies.filter((policy) => policy.quota_enabled !== false);
    const totalUsed = filteredPolicies.reduce((sum, policy) => sum + Number(policy.used_bytes || 0), 0);
    const totalLimit = quotaPolicies.reduce((sum, policy) => sum + Number(policy.limit_bytes || 0), 0);
    const allEngines = [...new Set(items.map((item) => (item.policy || item.endpoint).engine).filter(Boolean))];
    const endpointChoices = items.map((item) => {
      const value = item.policy || item.endpoint;
      return {
        value: item.key,
        label: `${value.name || "未命名端口"} · ${engineName(value.engine)} :${value.port}`,
      };
    }).sort((a, b) => a.label.localeCompare(b.label));
    const selectedScope = currentFilters.agent_id
      ? agentByID.get(currentFilters.agent_id)?.name || "所选节点"
      : "全部节点";
    const toolbar = `<section class="traffic-control-panel"><div class="traffic-filters" aria-label="流量筛选">
      <label>内核<select data-traffic-filter="engine">${selectOptions(allEngines.map((engine) => ({ value: engine, label: engineName(engine) })), currentFilters.engine, "全部内核")}</select></label>
      <label>配置端口<select data-traffic-filter="endpoint_key">${selectOptions(endpointChoices, currentFilters.endpoint_key, "全部端口")}</select></label>
      <label>状态<select data-traffic-filter="status">${selectOptions([{ value: "metering", label: "持续统计" }, { value: "active", label: "已设置配额" }, { value: "blocked", label: "已封禁" }, { value: "waiting", label: "等待同步" }, { value: "error", label: "监控异常" }], currentFilters.status, "全部状态")}</select></label>
      <button class="button small" type="button" data-traffic-filter-reset ${Object.values(currentFilters).some(Boolean) ? "" : "disabled"}>清除筛选</button>
    </div><section class="traffic-total"><span data-traffic-refresh-label>${esc(selectedScope)} · 所选端口累计</span><div><strong>${bytes(totalUsed)}</strong>${quotaPolicies.length ? `<small>/ ${bytes(totalLimit)}</small>` : ""}</div>${quotaPolicies.length ? `<progress max="100" value="${percent(totalUsed, totalLimit)}"></progress>` : `<span class="traffic-total-metering">${filteredPolicies.length ? "全部端口正在持续统计" : "等待 Agent 建立监控"}</span>`}<em>${filteredPolicies.length} 个监控端口 · ${quotaPolicies.length} 个配额</em></section></section>`;
    const selectableAgents = eligibleAgents(agents);
    const createDialog = can("traffic.manage")
      ? `<dialog class="traffic-edit-dialog traffic-create-dialog" id="traffic-new" aria-labelledby="traffic-create-title"><header><span class="traffic-edit-icon" aria-hidden="true">＋</span><div><p class="eyebrow">流量配额</p><h2 id="traffic-create-title">添加端口配额</h2><p>监控已自动开启；这里仅设置用量上限和超额处理</p></div><button class="deploy-command-close" type="button" data-traffic-create-close aria-label="关闭添加配额弹窗">×</button></header>${selectableAgents.length ? `<form id="traffic-policy-form"><div class="traffic-edit-body">${policyFields({ cycle_anchor: new Date(), protocol: "both", cycle: "monthly", auto_block: true }, selectableAgents, "create")}</div><footer><span></span><button class="button" type="button" data-traffic-create-close>取消</button><button class="button primary" type="submit">保存配额</button></footer></form>` : '<div class="traffic-create-unavailable"><strong>暂无可配置节点</strong><p>节点需在线并升级到支持端口流量统计的 Agent 后才能添加。</p><button class="button" type="button" data-traffic-create-close>关闭</button></div>'}</dialog>`
      : "";
    const cards = filteredItems.map((item) => {
      if (item.kind === "endpoint") {
        const endpoint = item.endpoint;
        const agent = agentByID.get(endpoint.agent_id);
        const configurable = selectableAgents.some((candidate) => candidate.id === endpoint.agent_id && (candidate.capabilities || []).includes(endpoint.engine));
        const sourceStatus = configurable ? "正在建立自动监控" : (agent?.features || []).includes("port-traffic-v1") ? "等待控制面同步" : "需升级 Agent 后监控";
        return `<article class="traffic-policy-card traffic-configured-port" data-refresh-key="traffic-endpoint-${esc(item.key)}" data-traffic-agent-card="${esc(endpoint.agent_id)}" data-traffic-card-key="${esc(trafficCardIdentity(item))}">
          <header><div class="traffic-card-identity"><span class="engine-badge ${esc(endpoint.engine)}">${esc(engineName(endpoint.engine))}</span><span><strong>${esc(endpoint.name || `端口 ${endpoint.port}`)}</strong><small>${esc(agent?.name || endpoint.agent_id)}<i>·</i><code>:${esc(endpoint.port)}</code><i>·</i>${esc(protocolName(endpoint.protocol))}</small></span></div><span class="traffic-card-controls"><span class="traffic-policy-status warn"><i></i>等待同步</span><span class="node-card-grip traffic-card-grip" title="拖动调整顺序" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg></span></span></header>
          <section class="traffic-configured-port-body"><span aria-hidden="true">↳</span><div><strong>已发现监听端口</strong><p>控制面正在建立持续流量统计，无需先设置配额。</p></div></section>
          <footer><span class="traffic-card-sync"><i></i>${sourceStatus}</span></footer>
        </article>`;
      }
      const policy = item.policy;
      const agent = agentByID.get(policy.agent_id);
      const [status, tone] = policyStatus(policy, agent);
      const quotaEnabled = policy.quota_enabled !== false;
      const usedPercent = percent(policy.used_bytes, policy.limit_bytes);
      const receiveBPS = trafficRateForDisplay(policy.receive_bps, policy.last_reported_at, agent?.status);
      const sendBPS = trafficRateForDisplay(policy.send_bps, policy.last_reported_at, agent?.status);
      const period = policy.period_start && policy.period_end
        ? `${dateInputValue(policy.period_start)} 至 ${dateInputValue(policy.period_end)}`
        : `从 ${dateInputValue(policy.cycle_anchor)} 开始${cycleName(policy.cycle)}重置`;
      return `<article class="traffic-policy-card ${policy.blocked ? "is-blocked" : ""}" id="traffic-${esc(policy.id)}" data-refresh-key="traffic-policy-${esc(policy.id)}" data-traffic-agent-card="${esc(policy.agent_id)}" data-traffic-card-key="${esc(trafficCardIdentity(item))}">
        <header><div class="traffic-card-identity"><span class="engine-badge ${esc(policy.engine)}">${esc(engineName(policy.engine))}</span><span><strong>${esc(policy.name)}</strong><small>${esc(agent?.name || policy.agent_id)}<i>·</i><code>:${esc(policy.port)}</code><i>·</i>${esc(protocolName(policy.protocol))}</small></span></div><span class="traffic-card-controls"><span class="traffic-policy-status ${tone}"><i></i>${esc(status)}</span><span class="node-card-grip traffic-card-grip" title="拖动调整顺序" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg></span></span></header>
        <section class="traffic-card-quota ${quotaEnabled ? "" : "is-monitor-only"}"><header><span>${quotaEnabled ? "当前周期用量" : "本月累计流量"}</span><b>${quotaEnabled ? `${usedPercent.toFixed(1)}%` : "未设置配额"}</b></header><div><strong>${bytes(policy.used_bytes)}</strong>${quotaEnabled ? `<span>/ ${bytes(policy.limit_bytes)}</span>` : ""}</div>${quotaEnabled ? `<progress max="100" value="${usedPercent}"></progress>` : ""}<small>${esc(period)}</small></section>
        <section class="traffic-card-transfer"><div><i class="received" aria-hidden="true">↓</i><span><small>接收流量</small><strong>${bytes(policy.received_bytes)}</strong></span><em>${rate(receiveBPS)}</em></div><div><i class="sent" aria-hidden="true">↑</i><span><small>发送流量</small><strong>${bytes(policy.sent_bytes)}</strong></span><em>${rate(sendBPS)}</em></div></section>
        ${policy.enforcement_error ? `<p class="traffic-error">${esc(policy.enforcement_error)}</p>` : ""}
        <footer><span class="traffic-card-sync"><i class="${policy.last_reported_at ? "ok" : ""}"></i>${policy.last_reported_at ? `${ago(policy.last_reported_at)}更新` : "等待 Agent 上报"}</span>${can("traffic.manage") ? `<div class="traffic-card-actions"><button class="button small danger-button" type="button" data-traffic-monitor-delete="${esc(policy.id)}">删除</button><button class="button small" type="button" data-traffic-reset="${esc(policy.id)}">清零</button><button class="button small" type="button" data-traffic-edit-open="${esc(policy.id)}">${quotaEnabled ? "编辑配额" : "设置配额"}</button></div>` : ""}</footer>${can("traffic.manage") ? `<dialog class="traffic-edit-dialog" data-traffic-edit-dialog="${esc(policy.id)}" aria-labelledby="traffic-edit-title-${esc(policy.id)}"><header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">端口配额</p><h2 id="traffic-edit-title-${esc(policy.id)}">${quotaEnabled ? "编辑" : "设置"} ${esc(policy.name)} 的配额</h2><p>流量统计不会因配额变更而停止 · ${esc(agent?.name || policy.agent_id)} :${esc(policy.port)}</p></div><button class="deploy-command-close" type="button" data-traffic-edit-close aria-label="关闭编辑弹窗">×</button></header><form data-traffic-edit-form="${esc(policy.id)}"><div class="traffic-edit-body">${policyFields(policy, [agent].filter(Boolean), `edit-${policy.id}`)}</div><footer>${quotaEnabled ? `<button class="button small danger-button" type="button" data-traffic-delete="${esc(policy.id)}">取消配额</button>` : "<span></span>"}<span></span><button class="button" type="button" data-traffic-edit-close>取消</button><button class="button primary" type="submit">保存配额</button></footer></form></dialog>` : ""}
      </article>`;
    }).join("");
    const empty = items.length
      ? '<div class="empty large"><strong>没有符合筛选条件的端口</strong><p>调整上方筛选条件后再查看。</p></div>'
      : '<div class="empty large"><strong>尚未读取到配置端口</strong><p>节点保存或部署内核配置后，已有监听端口会直接显示在这里。</p></div>';
    const listHeader = `<header class="traffic-policy-list-head"><div><h2>监控端口</h2><span>${filteredItems.length}</span></div><small>${esc(selectedScope)} · 自动发现配置 · Agent 实时上报</small></header>`;
    shell(`<div class="traffic-workspace">${toolbar}${listHeader}${cards ? `<section class="traffic-policy-grid">${cards}</section>` : empty}${createDialog}</div>`, "流量配额");
    bindTrafficForms(selectableAgents, endpoints);
    const cardGrid = document.querySelector(".traffic-policy-grid");
    if (cardGrid && filteredItems.length > 1) {
      enableTrafficCardDrag(cardGrid, orderedItems.map(trafficCardIdentity));
    }
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
    delay: () => 2000,
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
          background && Array.isArray(state.data.trafficEndpoints)
            ? Promise.resolve(state.data.trafficEndpoints)
            : api("/traffic-endpoints", { signal }),
        ]),
        ([agents, policies, endpoints]) => renderTraffic(agents, policies, endpoints, resetCreate),
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

  function bindTrafficForms(agents, endpoints) {
    document.querySelectorAll("[data-traffic-filter]").forEach((control) => {
      control.onchange = () => {
        filters()[control.dataset.trafficFilter] = control.value;
        renderTraffic(state.data.agents || [], state.data.trafficPolicies || [], state.data.trafficEndpoints || []);
      };
    });
    const reset = document.querySelector("[data-traffic-filter-reset]");
    if (reset) reset.onclick = () => {
      state.data.trafficFilters = {};
      renderTraffic(state.data.agents || [], state.data.trafficPolicies || [], state.data.trafficEndpoints || []);
    };
    document.querySelectorAll('a[href="#traffic-new"]').forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        const create = document.querySelector("#traffic-new");
        resetTrafficCreateForm(create?.querySelector("form"), agents, engineOptions);
        create?.showModal();
      };
    });
    document.querySelectorAll("[data-traffic-configure]").forEach((button) => {
      button.onclick = () => {
        const endpoint = endpoints.find((candidate) => trafficEndpointKey(candidate) === button.dataset.trafficConfigure);
        const form = document.querySelector("#traffic-policy-form");
        const dialog = document.querySelector("#traffic-new");
        if (!endpoint || !form || !dialog) return;
        resetTrafficCreateForm(form, agents, engineOptions);
        const agentSelect = form.querySelector("[name=agent_id]");
        const engineSelect = form.querySelector("[name=engine]");
        if (agentSelect) agentSelect.value = endpoint.agent_id;
        const agent = agents.find((candidate) => candidate.id === endpoint.agent_id);
        if (engineSelect) {
          engineSelect.innerHTML = engineOptions(agent, endpoint.engine);
          engineSelect.value = endpoint.engine;
        }
        const values = { name: endpoint.name, port: endpoint.port, protocol: endpoint.protocol };
        Object.entries(values).forEach(([name, value]) => {
          const field = form.querySelector(`[name="${name}"]`);
          if (field) field.value = value;
        });
        dialog.showModal();
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
        if (!(await confirmAction("取消配额后会解除自动封禁，但该端口仍会继续统计流量。确定继续？", "取消配额"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficDelete)}`, { method: "DELETE" });
          notify("配额已取消，端口流量继续统计");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
    document.querySelectorAll("[data-traffic-monitor-delete]").forEach((button) => {
      button.onclick = async () => {
        if (!(await confirmAction("确定停止监控这个端口，并永久删除当前流量记录和每日历史？以后重新设置配额可再次启用。", "删除流量记录"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficMonitorDelete)}/monitoring`, { method: "DELETE" });
          notify("流量记录已删除");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
  }

  return traffic;
}
