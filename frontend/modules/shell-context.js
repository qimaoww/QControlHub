import { orderNodesBySavedOrder } from "./node-order.js";
import { installedEngineCount } from "./agent-service-state.js";

export function createShellContext({ state, can, esc, engineName, ago, engines }) {
function contextMarkup(title) {
  if (state.route === "users")
    return `<div class="context-section-label"><span>用户</span><b>${(state.data.users || []).length}</b></div><nav class="context-list" aria-label="用户列表">${(state.data.users || []).map((user) => `<a href="#users" data-user-select="${esc(user.id)}" class="${user.id === state.data.userID ? "active" : ""}"><i class="status-dot ${user.disabled ? "" : "ok"}"></i><span><strong>${esc(user.display_name || user.username)}</strong><small>${esc(user.username)} · ${user.role === "admin" ? "管理员" : "用户"}</small></span></a>`).join("")}</nav>`;
  if (state.route === "my-quota")
    return '<nav class="context-menu" aria-label="个人账户"><a class="active" href="#my-quota">共享与额度</a></nav>';
  if (state.route === "dashboard") {
    const sections = [
      ["summary", "运行概览", true],
      ["panel-host", "面板主机", can("panel-metrics.read")],
      ["traffic-usage", "节点流量", can("traffic.read")],
      ["fleet", "节点状态", can("agents.read")],
      ["activity", "最近任务", can("tasks.read")],
    ].filter(([, , visible]) => visible);
    const selected = sections.some(([id]) => id === state.anchor) ? state.anchor : "summary";
    return `<nav class="context-menu" aria-label="总览目录">${sections.map(([id, label], index) => `<a${selected === id ? ' class="active" aria-current="location"' : ""} href="#${id}"><span>${String(index + 1).padStart(2, "0")}</span>${label}</a>`).join("")}</nav>`;
  }
  if (state.route === "ip-quality")
    return `<nav class="context-menu" aria-label="IP 质量检测目录"><a class="active" href="#ip-quality"><span>01</span>按日期查看</a></nav><p class="ip-quality-context-note">数据来源：节点执行的 IPQuality 检测任务。每日检测需手动启用。</p>`;
  if (state.route === "agents") {
    const items = orderNodesBySavedOrder(state.data.agents || []);
    return `<div class="context-section-label"><span>内核配置预设</span><b>${items.length}</b></div><nav class="context-list" aria-label="节点内核预设">${items.map((agent) => `<a class="${state.data.selectedAgent === agent.id ? "active" : ""}" href="#node-${esc(agent.id)}" data-context-agent="${esc(agent.id)}"><span class="context-engine">${(agent.capabilities || []).length}</span><span><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "node-settings") {
    // The node overview cards replace the per-node sidebar list; the whole
    // context column is hidden on this route via the no-context body class.
    return "";
  }
  if (state.route === "client-access") {
    const items = orderNodesBySavedOrder(state.data.agents || []);
    const entries = state.data.clientAccessEntries || [];
    return `<a class="context-back" href="#node-settings">← 返回节点设置</a><a class="context-primary ${state.data.accessAgent ? "" : "active"}" href="#client-access" data-access-agent="">全部客户端配置</a><div class="context-section-label"><span>按节点查看</span><b>${items.length}</b></div><nav class="context-list" aria-label="客户端配置节点">${items.map((agent) => { const profiles = entries.filter((entry) => entry.agent_id === agent.id).reduce((total, entry) => total + (entry.profiles || []).length, 0); return `<a class="${state.data.accessAgent === agent.id ? "active" : ""}" href="#client-access" data-access-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${profiles ? `${profiles} 个客户端入站` : "尚无客户端配置"}</small></span></a>`; }).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "substore-sync") {
    const resource = state.data.subStoreSync || {};
    const profiles = resource.profiles || [];
    const agents = [];
    const seen = new Set();
    for (const profile of profiles) {
      if (!profile.agent_id || seen.has(profile.agent_id)) continue;
      seen.add(profile.agent_id);
      agents.push({
        id: profile.agent_id,
        name: profile.agent_name || "已删除节点",
        status: profile.agent_status || "unknown",
        profiles: profiles.filter((item) => item.agent_id === profile.agent_id).length,
      });
    }
    const orderedAgents = orderNodesBySavedOrder(agents);
    const selected = state.data.subStoreAgent || "";
    return `<a class="context-back" href="#client-access">← 返回客户端配置</a><a class="context-primary ${selected ? "" : "active"}" href="#substore-sync" data-substore-agent="">全部节点</a><div class="context-section-label"><span>按节点查看</span><b>${orderedAgents.length}</b></div><nav class="context-list" aria-label="Sub-Store 同步节点">${orderedAgents.map((agent) => `<a class="${selected === agent.id ? "active" : ""}" href="#substore-sync" data-substore-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${agent.profiles} 个客户端入站</small></span></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "live-config") {
    const items = orderNodesBySavedOrder(state.data.agents || []);
    return `<div class="context-section-label"><span>选择节点</span><b>${items.length}</b></div><nav class="context-list" aria-label="配置节点">${items.map((agent) => { const installed = installedEngineCount(agent); return `<a class="${agent.id === state.data.liveAgent ? "active" : ""}" href="#live-config" data-live-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${installed ? `${installed} 个已安装内核` : "尚未安装内核"}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`; }).join("") || "<p>还没有节点</p>"}</nav><a class="context-primary" href="#archive-config">配置档案 →</a>`;
  }
  if (state.route === "archive-config") {
    const items = state.data.configs || [];
    return `${can("configs.write") ? '<a class="context-primary" id="new-config" href="#new-config">＋ 新建配置档案</a>' : ""}<div class="context-section-label"><span>配置档案</span><b>${items.length}</b></div><nav class="context-list config-context-list">${items.map((item) => `<a class="${item.id === state.data.archiveConfigId ? "active" : ""}" href="#archive-config" data-archive-config="${esc(item.id)}"><span class="context-engine ${esc(item.engine)}">${esc(engineName(item.engine))}</span><span><strong>${esc(item.name)}</strong><small>v${item.version} · ${esc(ago(item.updated_at))}</small></span></a>`).join("") || "<p>还没有保存的配置</p>"}</nav><a class="context-primary" href="#live-config">← 节点实际配置</a>`;
  }
  if (state.route === "tasks")
    return `<nav class="context-menu task-context-menu" aria-label="任务状态">${[
      ["", "全部任务"],
      ["pending", "准备中"],
      ["running", "执行中"],
      ["succeeded", "成功"],
      ["failed", "失败"],
      ["canceled", "已取消"],
    ]
      .map(
        ([status, text]) =>
          `<a class="${(state.data.taskFilters?.status || "") === status ? "active" : ""}" href="#tasks" data-task-status-filter="${status}">${text}</a>`,
      )
      .join("")}</nav>`;
  if (state.route === "system-bbr") {
    const agents = orderNodesBySavedOrder(state.data.agents || []);
    const selected = state.data.bbrAgent || "";
    return `<a class="context-primary ${selected ? "" : "active"}" href="#system-bbr">全部节点</a><div class="context-section-label"><span>系统 BBR</span><b>${agents.length}</b></div><nav class="context-list" aria-label="系统 BBR 节点">${agents.map((agent) => `<a class="${selected === agent.id ? "active" : ""}" href="#system-bbr-agent-${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${(agent.features || []).includes("system-bbr-v1") ? "系统 TCP 参数" : "需升级 Agent"}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "core-logs") {
    const agents = orderNodesBySavedOrder(state.data.agents || []);
    const selected = state.data.coreLogFilters?.agent_id || "";
    return `<a class="context-primary ${selected ? "" : "active"}" href="#core-logs" data-core-log-agent="">全部节点日志</a><div class="context-section-label"><span>按节点查看</span><b>${agents.length}</b></div><nav class="context-list" aria-label="内核日志节点">${agents.map((agent) => `<a class="${selected === agent.id ? "active" : ""}" href="#core-logs" data-core-log-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${(agent.features || []).includes("core-logs-v1") ? "集中日志已启用" : "需升级 Agent"}</small></span><em>${agent.status === "online" ? "在线" : "离线"}</em></a>`).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "traffic") {
    const agents = orderNodesBySavedOrder(state.data.agents || []);
    const allPolicies = state.data.trafficPolicies || [];
    const suppressedPorts = new Set(allPolicies.filter((policy) => policy.monitoring_enabled === false).map((policy) => `${policy.agent_id}:${policy.port}`));
    const policies = allPolicies.filter((policy) => policy.monitoring_enabled !== false);
    const endpoints = (state.data.trafficEndpoints || []).filter((endpoint) => !suppressedPorts.has(`${endpoint.agent_id}:${endpoint.port}`));
    const selected = state.data.trafficFilters?.agent_id || "";
    return `${can("traffic.manage") ? '<a class="context-primary" href="#traffic-new">＋ 添加端口配额</a>' : ""}<a class="context-primary ${selected ? "" : "active"}" href="#traffic-all" data-context-traffic-agent="">全部节点</a><div class="context-section-label"><span>按节点查看</span><b>${agents.length}</b></div><nav class="context-list" aria-label="端口流量节点">${agents.map((agent) => { const agentPolicies = policies.filter((policy) => policy.agent_id === agent.id); const configured = new Set([...agentPolicies.map((policy) => policy.port), ...endpoints.filter((endpoint) => endpoint.agent_id === agent.id).map((endpoint) => endpoint.port)]).size; const quotas = agentPolicies.filter((policy) => policy.quota_enabled !== false).length; const blocked = agentPolicies.filter((policy) => policy.blocked).length; return `<a class="${selected === agent.id ? "active" : ""}" href="#traffic-agent-${esc(agent.id)}" data-context-traffic-agent="${esc(agent.id)}"><i class="status-dot ${blocked ? "bad" : agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${configured ? `${configured} 个监控端口${quotas ? ` · ${quotas} 个配额` : ""}${blocked ? ` · ${blocked} 个封禁` : ""}` : "尚无配置端口"}</small></span></a>`; }).join("") || "<p>还没有节点</p>"}</nav>`;
  }
  if (state.route === "access-control") {
    const entries = state.data.accessControls || [];
    const agents = [];
    const seen = new Set();
    for (const entry of entries) {
      if (seen.has(entry.agent_id)) continue;
      seen.add(entry.agent_id);
      agents.push({
        id: entry.agent_id,
        name: entry.agent_name,
        status: entry.agent_status,
        count: entries.filter((item) => item.agent_id === entry.agent_id).length,
        enabled: entries.filter(
          (item) =>
            item.agent_id === entry.agent_id &&
            (item.block_mainland_destination || item.block_mainland_source),
        ).length,
      });
    }
    const selected = state.data.accessControlAgent || "";
    const orderedAgents = orderNodesBySavedOrder(agents);
    return `<a class="context-primary ${selected ? "" : "active"}" href="#access-control-all" data-access-control-agent="">全部节点</a><div class="context-section-label"><span>按节点查看</span><b>${orderedAgents.length}</b></div><nav class="context-list" aria-label="访问限制节点">${orderedAgents.map((agent) => `<a class="${selected === agent.id ? "active" : ""}" href="#access-control-agent-${esc(agent.id)}" data-access-control-agent="${esc(agent.id)}"><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i><span><strong>${esc(agent.name)}</strong><small>${agent.count} 个入站端口${agent.enabled ? ` · ${agent.enabled} 个已限制` : ""}</small></span></a>`).join("") || "<p>还没有可限制的入站</p>"}</nav>`;
  }
  if (state.route === "settings")
    return `<nav class="context-menu" aria-label="设置目录"><a class="active" href="#settings-engines"><span>01</span>默认内核能力</a><a href="#settings-basic"><span>02</span>基础设置</a><a href="#settings-runtime"><span>03</span>任务与同步</a><a href="#settings-data"><span>04</span>数据与日志</a><a href="#settings-notify"><span>05</span>事件通知</a><a href="#settings-komari"><span>06</span>Komari 联动</a><a href="#settings-cnip"><span>07</span>CN IP 数据源</a><a href="#settings-deployment"><span>08</span>部署状态</a></nav>`;
  const agent = (state.data.agents || []).find(
    (item) => item.id === state.data.agentId,
  ) || (state.data.presetAgent?.id === state.data.agentId ? state.data.presetAgent : null);
  const caps = agent?.capabilities || engines;
  const installed = installedEngineCount(agent);
  return `<a class="context-back" href="#agents">← 返回内核预设</a><div class="context-section-label"><span>选择内核</span><b>${installed}/${caps.length}</b></div><nav class="context-list engine-context-list">${caps.map((engine) => `<a class="${state.data.engine === engine ? "active" : ""}" href="#agent-config" data-engine-select="${esc(engine)}"><span class="context-engine ${esc(engine)}">${esc(engineName(engine))}</span><span><strong>${esc(engineName(engine))}</strong><small>${agent?.runtime?.[engine]?.installed ? "服务端入站" : "尚未安装"}</small></span></a>`).join("")}</nav><ol class="context-steps"><li class="active"><b>1</b><span>选择入站</span></li><li><b>2</b><span>编辑参数</span></li><li><b>3</b><span>校验或部署</span></li></ol>`;
}

  return contextMarkup;
}
