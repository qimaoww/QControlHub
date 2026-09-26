import { orderedNodeList } from "./node-order.js";
import { gibibyte, dateInputValue, cycleName, protocolName, policyStatusKey, policyStatus, eligibleAgents, mergeTrafficPorts, orderTrafficItems, trafficCardIdentity, trafficRateForDisplay } from "./traffic-model.js";
import { renderTrafficAccounting } from "./traffic-accounting-view.js";
export function createTrafficView(ctx, { filters, storage, savedTrafficCardOrder, policyFields, selectOptions }) {
  const { state, can, esc, engineName, bytes, rate, percent, ago, shell } = ctx;
  return (agents, policies, endpoints) => {
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
    const nodeOrder = orderedNodeList(agents, storage).map((agent) => agent.id);
    const orderedItems = orderTrafficItems(items, savedTrafficCardOrder(), nodeOrder);
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
    const toolbar = `<section class="traffic-control-panel"><div class="traffic-overview"><section class="traffic-total" aria-label="流量概览"><span data-traffic-refresh-label>${esc(selectedScope)} · 所选端口累计</span><div><strong>${bytes(totalUsed)}</strong>${quotaPolicies.length ? `<small>/ ${bytes(totalLimit)}</small>` : ""}</div>${quotaPolicies.length ? `<progress aria-label="累计流量占配额合计比例" max="100" value="${percent(totalUsed, totalLimit)}"></progress>` : `<span class="traffic-total-metering">${filteredPolicies.length ? "全部端口正在持续统计" : "等待 Agent 建立监控"}</span>`}</section><dl class="traffic-overview-stats"><div><dt>监控端口</dt><dd>${filteredPolicies.length}<small>个</small></dd></div><div><dt>已设配额</dt><dd>${quotaPolicies.length}<small>个</small></dd></div><div><dt>等待同步</dt><dd>${filteredItems.length - filteredPolicies.length}<small>个</small></dd></div></dl></div><div class="traffic-filters" aria-label="流量筛选">
      <label>内核<select data-traffic-filter="engine">${selectOptions(allEngines.map((engine) => ({ value: engine, label: engineName(engine) })), currentFilters.engine, "全部内核")}</select></label>
      <label>配置端口<select data-traffic-filter="endpoint_key">${selectOptions(endpointChoices, currentFilters.endpoint_key, "全部端口")}</select></label>
      <label>状态<select data-traffic-filter="status">${selectOptions([{ value: "metering", label: "持续统计" }, { value: "active", label: "已设置配额" }, { value: "blocked", label: "已封禁" }, { value: "waiting", label: "等待同步" }, { value: "error", label: "监控异常" }], currentFilters.status, "全部状态")}</select></label>
      <button class="button small" type="button" data-traffic-filter-reset ${Object.values(currentFilters).some(Boolean) ? "" : "disabled"}>清除筛选</button>
    </div></section>`;
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
          <header><div class="traffic-card-identity"><span class="engine-badge ${esc(endpoint.engine)}">${esc(engineName(endpoint.engine))}</span><span><strong title="${esc(endpoint.name || `端口 ${endpoint.port}`)}">${esc(endpoint.name || `端口 ${endpoint.port}`)}</strong><small><span class="traffic-node-tag" title="${esc(agent?.name || endpoint.agent_id)}">${esc(agent?.name || endpoint.agent_id)}</span><code class="traffic-port-tag">:${esc(endpoint.port)}</code><span class="traffic-protocol-tag">${esc(protocolName(endpoint.protocol))}</span></small></span></div><span class="traffic-card-controls"><span class="traffic-policy-status warn"><i></i>等待同步</span><span class="node-card-grip traffic-card-grip" title="拖动调整顺序" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg></span></span></header>
          <section class="traffic-configured-port-body"><span aria-hidden="true">↳</span><div><strong>已发现监听端口</strong><p>控制面正在建立持续流量统计，无需先设置配额。</p></div></section>
          <footer><span class="traffic-card-sync"><i></i>${sourceStatus}</span></footer>
        </article>`;
      }
      const policy = item.policy;
      const agent = agentByID.get(policy.agent_id);
      const [status, tone] = policyStatus(policy, agent);
      const quotaEnabled = policy.quota_enabled !== false;
      const editable = can("traffic.manage", agent) && agent?.can_manage !== false && !policy.shared_quota;
      const usedPercent = percent(policy.used_bytes, policy.limit_bytes);
      const sampledAt = policy.last_collected_at || policy.last_reported_at;
      const receiveBPS = policy.enforcement_available === false ? 0 : trafficRateForDisplay(policy.receive_bps, sampledAt, agent?.status);
      const sendBPS = policy.enforcement_available === false ? 0 : trafficRateForDisplay(policy.send_bps, sampledAt, agent?.status);
      const period = policy.period_start && policy.period_end
        ? `${dateInputValue(policy.period_start)} 至 ${dateInputValue(policy.period_end)}`
        : `从 ${dateInputValue(policy.cycle_anchor)} 开始${cycleName(policy.cycle)}重置`;
      return `<article class="traffic-policy-card ${policy.blocked ? "is-blocked" : ""}" id="traffic-${esc(policy.id)}" data-refresh-key="traffic-policy-${esc(policy.id)}" data-traffic-agent-card="${esc(policy.agent_id)}" data-traffic-card-key="${esc(trafficCardIdentity(item))}">
        <header><div class="traffic-card-identity"><span class="engine-badge ${esc(policy.engine)}">${esc(engineName(policy.engine))}</span><span><strong title="${esc(policy.name)}">${esc(policy.name)}</strong><small><span class="traffic-node-tag" title="${esc(agent?.name || policy.agent_id)}">${esc(agent?.name || policy.agent_id)}</span><code class="traffic-port-tag">:${esc(policy.port)}</code><span class="traffic-protocol-tag">${esc(protocolName(policy.protocol))}</span></small></span></div><span class="traffic-card-controls"><span class="traffic-policy-status ${tone}"><i></i>${esc(status)}</span><span class="node-card-grip traffic-card-grip" title="拖动调整顺序" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg></span></span></header>
        <section class="traffic-card-quota ${quotaEnabled ? "" : "is-monitor-only"}"><header><span>${quotaEnabled ? "当前周期用量" : "本月累计流量"}</span><b>${policy.shared_quota ? "共享额度" : quotaEnabled ? `${usedPercent.toFixed(1)}%` : "未设置配额"}</b></header><div><strong>${bytes(policy.used_bytes)}</strong>${quotaEnabled ? `<span>/ ${bytes(policy.limit_bytes)}</span>` : ""}</div>${quotaEnabled ? `<progress aria-label="当前周期配额使用比例" max="100" value="${usedPercent}"></progress>` : ""}<small>${esc(period)}${policy.shared_quota ? `<br>共享累计 ${(policy.shared_quota.used_bytes / gibibyte).toFixed(2)} / ${policy.shared_quota.limit_bytes ? `${(policy.shared_quota.limit_bytes / gibibyte).toFixed(2)} GiB` : "不限量"}` : ""}</small></section>
        <section class="traffic-card-transfer"><div><i class="received" aria-hidden="true">↓</i><span><small>接收流量</small><strong>${bytes(policy.received_bytes)}</strong></span><em>${rate(receiveBPS)}</em></div><div><i class="sent" aria-hidden="true">↑</i><span><small>发送流量</small><strong>${bytes(policy.sent_bytes)}</strong></span><em>${rate(sendBPS)}</em></div></section>
        <footer><div class="traffic-card-meta"><span class="traffic-card-sync"><i class="${policy.last_reported_at ? "ok" : ""}"></i>${policy.last_reported_at ? `${ago(policy.last_reported_at)}更新` : "等待 Agent 上报"}</span>${renderTrafficAccounting(policy, esc, bytes)}</div>${editable ? `<div class="traffic-card-actions"><button class="button small danger-button" type="button" data-traffic-monitor-delete="${esc(policy.id)}">删除</button><button class="button small" type="button" data-traffic-reset="${esc(policy.id)}">清零</button><button class="button small" type="button" data-traffic-edit-open="${esc(policy.id)}">${quotaEnabled ? "编辑配额" : "设置配额"}</button></div>` : ""}</footer>${editable ? `<dialog class="traffic-edit-dialog" data-traffic-edit-dialog="${esc(policy.id)}" aria-labelledby="traffic-edit-title-${esc(policy.id)}"><header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">端口配额</p><h2 id="traffic-edit-title-${esc(policy.id)}">${quotaEnabled ? "编辑" : "设置"} ${esc(policy.name)} 的配额</h2><p>流量统计不会因配额变更而停止 · ${esc(agent?.name || policy.agent_id)} :${esc(policy.port)}</p></div><button class="deploy-command-close" type="button" data-traffic-edit-close aria-label="关闭编辑弹窗">×</button></header><form data-traffic-edit-form="${esc(policy.id)}"><div class="traffic-edit-body">${policyFields(policy, [agent].filter(Boolean), `edit-${policy.id}`)}</div><footer>${quotaEnabled ? `<button class="button small danger-button" type="button" data-traffic-delete="${esc(policy.id)}">取消配额</button>` : "<span></span>"}<span></span><button class="button" type="button" data-traffic-edit-close>取消</button><button class="button primary" type="submit">保存配额</button></footer></form></dialog>` : ""}
      </article>`;
    }).join("");
    const empty = items.length
      ? '<div class="empty large"><strong>没有符合筛选条件的端口</strong><p>调整上方筛选条件后再查看。</p></div>'
      : '<div class="empty large"><strong>尚未读取到配置端口</strong><p>节点保存或部署内核配置后，已有监听端口会直接显示在这里。</p></div>';
    const listHeader = `<header class="traffic-policy-list-head"><div><h2>监控端口</h2><span>${filteredItems.length}</span></div><div class="traffic-policy-list-actions"><small>${esc(selectedScope)} · 自动发现配置 · Agent 实时上报</small>${can("traffic.manage") ? `<button class="button small" type="button" data-traffic-sync>同步端口</button>` : ""}</div></header>`;
    const trafficResultKey = `${currentFilters.agent_id || "all"}-${currentFilters.engine || "all"}-${currentFilters.endpoint_key || "all"}-${currentFilters.status || "all"}`;
    shell(`<div class="traffic-workspace">${toolbar}${listHeader}${cards ? `<section class="traffic-policy-grid qch-swap-panel" data-refresh-key="traffic-results-${esc(trafficResultKey)}">${cards}</section>` : `<div class="qch-swap-panel" data-refresh-key="traffic-results-${esc(trafficResultKey)}-empty">${empty}</div>`}${createDialog}</div>`, "流量配额", { viewKey: `traffic-${trafficResultKey}` });

    return { selectableAgents, filteredItems, orderedItems };
  };
}
