import { trafficBatchBarMarkup, trafficBatchState, clearTrafficBatchResults } from "./traffic-batch-view.js";
import { cardMoveGrip, cardMoveHelp } from "./card-move-view.js";
import { orderedNodeList } from "./node-order.js";
import { gibibyte, dateInputValue, cycleName, protocolName, policyStatusKey, policyStatus, eligibleAgents, mergeTrafficPorts, orderTrafficItems, trafficCardIdentity, trafficRateForDisplay, trafficInUserScope, trafficPolicyEditable } from "./traffic-model.js";
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
    const items = mergeTrafficPorts(policies, endpoints).filter(item => trafficInUserScope(item.policy || item.endpoint, currentFilters));
    const batch = trafficBatchState(state);
    const filterKey = JSON.stringify([currentFilters.user_scope || "current", currentFilters.agent_id || "", currentFilters.engine || "", currentFilters.endpoint_key || "", currentFilters.status || ""]);
    if (batch.filterKey && batch.filterKey !== filterKey) {
      batch.selected = [];
      clearTrafficBatchResults(batch);
    }
    batch.filterKey = filterKey;
    if (currentFilters.endpoint_key && !items.some((item) => item.key === currentFilters.endpoint_key)) currentFilters.endpoint_key = "";
    if (currentFilters.engine && !items.some((item) => (item.policy || item.endpoint).engine === currentFilters.engine)) currentFilters.engine = "";
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
    const eligibleIDs = new Set(filteredItems.filter(item => item.policy && (batch.action !== "remove-quota" || item.policy.quota_enabled !== false) && trafficPolicyEditable(item.policy, agentByID.get(item.policy.agent_id), can)).map(item => item.policy.id));
    batch.selected = batch.selected.filter(id => eligibleIDs.has(id));
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
      <label>用户范围<select data-traffic-filter="user_scope"><option value="current" ${currentFilters.user_scope !== "all" ? "selected" : ""}>当前用户</option><option value="all" ${currentFilters.user_scope === "all" ? "selected" : ""}>全部</option></select></label>
      <label>内核<select data-traffic-filter="engine">${selectOptions(allEngines.map((engine) => ({ value: engine, label: engineName(engine) })), currentFilters.engine, "全部内核")}</select></label>
      <label>配置端口<select data-traffic-filter="endpoint_key">${selectOptions(endpointChoices, currentFilters.endpoint_key, "全部端口")}</select></label>
      <label>状态<select data-traffic-filter="status">${selectOptions([{ value: "offline", label: "节点离线" }, { value: "stopped", label: "服务已停止" }, { value: "metering", label: "持续统计" }, { value: "active", label: "已设置配额" }, { value: "blocked", label: "已封禁" }, { value: "waiting", label: "等待同步" }, { value: "error", label: "监控异常" }], currentFilters.status, "全部状态")}</select></label>
      <button class="button small" type="button" data-traffic-filter-reset ${Object.entries(currentFilters).some(([key,value]) => value && (key !== "user_scope" || value === "all")) ? "" : "disabled"}>清除筛选</button>
    </div><section class="traffic-total"><span data-traffic-refresh-label>${esc(selectedScope)} · 所选端口累计</span><div><strong>${bytes(totalUsed)}</strong>${quotaPolicies.length ? `<small>/ ${bytes(totalLimit)}</small>` : ""}</div>${quotaPolicies.length ? `<progress max="100" value="${percent(totalUsed, totalLimit)}"></progress>` : `<span class="traffic-total-metering">${filteredPolicies.length ? "全部端口正在持续统计" : "等待 Agent 建立监控"}</span>`}<em>${filteredPolicies.length} 个监控端口 · ${quotaPolicies.length} 个配额</em></section></section>`;
    const selectableAgents = eligibleAgents(agents);
    const createDialog = can("traffic.manage")
      ? `<dialog class="traffic-edit-dialog traffic-create-dialog" id="traffic-new" aria-labelledby="traffic-create-title"><header><span class="traffic-edit-icon" aria-hidden="true">＋</span><div><p class="eyebrow">流量配额</p><h2 id="traffic-create-title">添加端口配额</h2><p>监控已自动开启；这里仅设置用量上限和超额处理</p></div><button class="deploy-command-close" type="button" data-traffic-create-close aria-label="关闭添加配额弹窗">×</button></header>${selectableAgents.length ? `<form id="traffic-policy-form"><div class="traffic-edit-body">${policyFields({ cycle_anchor: new Date(), protocol: "both", cycle: "monthly", auto_block: true }, selectableAgents, "create")}</div><footer><span></span><button class="button" type="button" data-traffic-create-close>取消</button><button class="button primary" type="submit">保存配额</button></footer></form>` : '<div class="traffic-create-unavailable"><strong>暂无可配置节点</strong><p>节点需在线并升级到支持端口流量统计的 Agent 后才能添加。</p><button class="button" type="button" data-traffic-create-close>关闭</button></div>'}</dialog>`
      : "";
    const cardHeader = (value, agent, status, tone, editable = false) => {
      const nodeName = agent?.name || value.agent_id;
      return `<header class="traffic-card-header">
        <div class="traffic-card-heading"><div class="traffic-card-node">${batch.mode && value.id ? `<label class="node-card-select"><input type="checkbox" data-traffic-choice="${esc(value.id)}" aria-label="选择 ${esc(nodeName)} :${esc(value.port)}" ${batch.selected.includes(value.id) ? "checked" : ""} ${!editable || batch.busy ? "disabled" : ""}><span aria-hidden="true"></span></label>` : ""}<strong title="${esc(nodeName)}">${esc(nodeName)}</strong><div class="traffic-card-port"><code aria-label="监听端口 ${esc(value.port)}">${esc(value.port)}</code></div></div><span class="traffic-card-controls"><span class="traffic-policy-status ${tone}"><i></i>${esc(status)}</span>${batch.mode ? "" : cardMoveGrip("traffic", "traffic-card-grip", `${esc(nodeName)} :${esc(value.port)}`)}</span></div>
        <div class="traffic-card-facts"><div class="traffic-card-core"><span class="engine-badge ${esc(value.engine)}" title="${esc(engineName(value.engine))} 内核">${esc(engineName(value.engine))}</span></div><div class="traffic-card-config"><strong title="${esc(value.name || `端口 ${value.port}`)}">${esc(value.name || `端口 ${value.port}`)}</strong><small>${esc(protocolName(value.protocol))}${currentFilters.user_scope === "all" ? ` · 所属用户：${esc(value.owner_name || "未知用户")}` : ""}</small></div></div>
      </header>`;
    };
    const cards = filteredItems.map((item) => {
      if (item.kind === "endpoint") {
        const endpoint = item.endpoint;
        const agent = agentByID.get(endpoint.agent_id);
        const configurable = selectableAgents.some((candidate) => candidate.id === endpoint.agent_id && (candidate.capabilities || []).includes(endpoint.engine));
        const sourceStatus = configurable ? "正在建立自动监控" : (agent?.features || []).includes("port-traffic-v1") ? "等待控制面同步" : "需升级 Agent 后监控";
        return `<article class="traffic-policy-card traffic-configured-port" aria-label="${esc(agent?.name || endpoint.agent_id)}，端口 ${esc(endpoint.port)}，${esc(engineName(endpoint.engine))} 内核" data-refresh-key="traffic-endpoint-${esc(item.key)}" data-traffic-agent-card="${esc(endpoint.agent_id)}" data-traffic-card-key="${esc(trafficCardIdentity(item))}">
          ${cardHeader(endpoint, agent, "等待同步", "warn")}
          <section class="traffic-configured-port-body"><span aria-hidden="true">↳</span><div><strong>已发现监听端口</strong><p>控制面正在建立持续流量统计，无需先设置配额。</p></div></section>
          <footer><span class="traffic-card-sync"><i></i>${sourceStatus}</span></footer>
        </article>`;
      }
      const policy = item.policy;
      const agent = agentByID.get(policy.agent_id);
      const [status, tone] = policyStatus(policy, agent);
      const quotaEnabled = policy.quota_enabled !== false;
      const editable = trafficPolicyEditable(policy, agent, can);
      const usedPercent = percent(policy.used_bytes, policy.limit_bytes);
      const sampledAt = policy.last_collected_at || policy.last_reported_at;
      const receiveBPS = policy.enforcement_available === false ? 0 : trafficRateForDisplay(policy.receive_bps, sampledAt, agent?.status);
      const sendBPS = policy.enforcement_available === false ? 0 : trafficRateForDisplay(policy.send_bps, sampledAt, agent?.status);
      const period = policy.period_start && policy.period_end
        ? `${dateInputValue(policy.period_start)} 至 ${dateInputValue(policy.period_end)}`
        : `从 ${dateInputValue(policy.cycle_anchor)} 开始${cycleName(policy.cycle)}重置`;
      return `<article class="traffic-policy-card ${policy.blocked ? "is-blocked" : ""}" aria-label="${esc(agent?.name || policy.agent_id)}，端口 ${esc(policy.port)}，${esc(engineName(policy.engine))} 内核" id="traffic-${esc(policy.id)}" data-refresh-key="traffic-policy-${esc(policy.id)}" data-traffic-agent-card="${esc(policy.agent_id)}" data-traffic-card-key="${esc(trafficCardIdentity(item))}">
        ${cardHeader(policy, agent, status, tone, editable)}
        <section class="traffic-card-quota ${quotaEnabled ? "" : "is-monitor-only"}" aria-label="${quotaEnabled ? "配额用量" : "未设置配额的端口流量"}"><header><span>${quotaEnabled ? "当前周期用量" : "本月累计流量"}</span>${quotaEnabled ? `<b>${policy.shared_quota ? "共享额度" : `${usedPercent.toFixed(1)}%`}</b>` : ""}</header><div><strong>${bytes(policy.used_bytes)}</strong>${quotaEnabled ? `<span>/ ${bytes(policy.limit_bytes)}</span>` : ""}</div>${quotaEnabled ? `<progress max="100" value="${usedPercent}"></progress>` : ""}<small>${esc(period)}${policy.shared_quota ? `<br>共享累计 ${(policy.shared_quota.used_bytes / gibibyte).toFixed(2)} / ${policy.shared_quota.limit_bytes ? `${(policy.shared_quota.limit_bytes / gibibyte).toFixed(2)} GiB` : "不限量"}` : ""}</small></section>
        <section class="traffic-card-transfer"><div><i class="received" aria-hidden="true">↓</i><span><small>接收流量</small><strong>${bytes(policy.received_bytes)}</strong></span><em>${rate(receiveBPS)}</em></div><div><i class="sent" aria-hidden="true">↑</i><span><small>发送流量</small><strong>${bytes(policy.sent_bytes)}</strong></span><em>${rate(sendBPS)}</em></div></section>
        <footer><span class="traffic-card-sync"><i class="${policy.last_reported_at ? "ok" : ""}"></i>${policy.last_reported_at ? `${ago(policy.last_reported_at)}更新` : "等待 Agent 上报"}</span>${renderTrafficAccounting(policy, esc, bytes)}${editable && !batch.mode ? `<div class="traffic-card-actions"><button class="button small danger-button" type="button" data-traffic-monitor-delete="${esc(policy.id)}">删除</button><button class="button small" type="button" data-traffic-reset="${esc(policy.id)}">清零</button><button class="button small" type="button" data-traffic-edit-open="${esc(policy.id)}">${quotaEnabled ? "编辑配额" : "设置配额"}</button></div>` : ""}</footer>${editable && !batch.mode ? `<dialog class="traffic-edit-dialog" data-traffic-edit-dialog="${esc(policy.id)}" aria-labelledby="traffic-edit-title-${esc(policy.id)}"><header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">端口配额</p><h2 id="traffic-edit-title-${esc(policy.id)}">${quotaEnabled ? "编辑" : "设置"} ${esc(policy.name)} 的配额</h2><p>流量统计不会因配额变更而停止 · ${esc(agent?.name || policy.agent_id)} :${esc(policy.port)}</p></div><button class="deploy-command-close" type="button" data-traffic-edit-close aria-label="关闭编辑弹窗">×</button></header><form data-traffic-edit-form="${esc(policy.id)}"><div class="traffic-edit-body">${policyFields(policy, [agent].filter(Boolean), `edit-${policy.id}`)}</div><footer>${quotaEnabled ? `<button class="button small danger-button" type="button" data-traffic-delete="${esc(policy.id)}">取消配额</button>` : "<span></span>"}<span></span><button class="button" type="button" data-traffic-edit-close>取消</button><button class="button primary" type="submit">保存配额</button></footer></form></dialog>` : ""}
      </article>`;
    }).join("");
    const empty = items.length
      ? '<div class="empty large"><strong>没有符合筛选条件的端口</strong><p>调整上方筛选条件后再查看。</p></div>'
      : '<div class="empty large"><strong>暂无已部署端口</strong><p>成功部署配置后，端口会显示在这里。</p></div>';
    const listHeader = `<header class="traffic-policy-list-head"><div><h2>监控端口</h2><span>${filteredItems.length}</span></div><div class="traffic-policy-list-actions"><small>${esc(selectedScope)} · Agent 实时上报</small>${can("traffic.manage") ? `<button class="button small" type="button" data-traffic-batch-toggle ${batch.busy ? "disabled" : ""}>${batch.mode ? "退出批量" : "批量操作"}</button><button class="button small" type="button" data-traffic-sync ${batch.busy ? "disabled" : ""}>同步端口</button>` : ""}</div></header>`;
    const trafficResultKey = JSON.stringify([currentFilters.agent_id || "", currentFilters.engine || "", currentFilters.endpoint_key || "", currentFilters.status || "", currentFilters.user_scope || "current"]);
    shell(`<div class="traffic-workspace">${toolbar}${listHeader}${cards && !batch.mode ? cardMoveHelp("traffic") : ""}${batch.mode ? `<form class="node-batch-form" data-traffic-batch-form>` : ""}<section class="${cards ? `traffic-policy-grid ${batch.mode ? "node-card-grid " : ""}` : ""}qch-swap-panel" data-refresh-key="traffic-results" data-motion-region="traffic-results" data-motion-key="${esc(trafficResultKey)}">${cards || empty}</section>${batch.mode ? `${trafficBatchBarMarkup(batch, esc)}</form>` : ""}${createDialog}</div>`, "流量配额", { viewKey: "traffic" });

    return { selectableAgents, filteredItems, orderedItems, eligibleIDs };
  };
}
