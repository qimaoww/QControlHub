export function installDashboard(ctx) {
  const { api, state, esc, engineName, heartbeat, statusTone, ago, short, actionName, shell } = ctx;
  const taskActivity = (items, limit = 7) => {
    const groups = [];
    for (const task of items) {
      const previous = groups.at(-1);
      if (previous && previous.task.action === task.action && previous.task.agent_id === task.agent_id && previous.task.engine === task.engine && previous.task.status === task.status) previous.count += 1;
      else groups.push({ task, count: 1 });
      if (groups.length >= limit) break;
    }
    return groups;
  };
async function dashboard() {
  const [overview, agents, tasks] = await Promise.all([
    api("/overview"),
    api("/agents"),
    api("/tasks?limit=7"),
  ]);
  state.data.overview = overview;
  state.data.agents = agents;
  const fleet =
    agents
      .slice(0, 7)
      .map(
        (agent) =>
          `<a href="#agents" data-dashboard-agent="${esc(agent.id)}"><span class="node-avatar">●</span><span><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small><span class="fleet-engines">${(agent.capabilities || []).map((engine) => `<em class="${esc(engine)}">${esc(engineName(engine))}</em>`).join("")}</span></span><span class="status-label ${statusTone(agent.status)}">${agent.status === "online" ? "在线" : "离线"}</span><time>${esc(heartbeat(agent.last_seen))}</time><i>›</i></a>`,
      )
      .join("") ||
    '<div class="empty compact"><strong>还没有节点</strong><p>请先注册节点。</p></div>';
  const activity =
    taskActivity(tasks)
      .map(
        ({ task, count }) => {
          const taskEngineLabel = task.action === "upgrade-agent" ? "QAgent" : engineName(task.engine);
          return `<a href="#tasks" data-dashboard-task="${esc(task.id)}"><i class="status-dot ${statusTone(task.status)}"></i><span><strong>${esc(actionName(task.action))}</strong><small>${esc(taskEngineLabel)} · ${esc(short(task.agent_id))}${count > 1 ? ` · 连续 ${count} 次` : ""}</small></span><time>${esc(ago(task.created_at))}</time><b>›</b></a>`;
        },
      )
      .join("") ||
    '<div class="empty compact"><strong>还没有任务</strong></div>';
  shell(
    `<section class="dashboard-head" id="summary"><h2>运行总览</h2><span class="trust-badge ${!overview.agents ? "inactive" : overview.agents_online === overview.agents ? "" : "warn"}"><i></i>${!overview.agents ? "等待节点接入" : overview.agents_online === overview.agents ? "全部在线" : `${overview.agents_online} / ${overview.agents} 在线`}</span></section><nav class="ops-stats" aria-label="运行概览快捷入口"><a href="#agents" aria-label="查看在线节点"><span class="stat-icon green"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/><path d="M8 12h2l1.3-3 2.1 6 1.4-3H17"/></svg></span><div><small>在线节点</small><strong>${overview.agents_online}<em>/${overview.agents}</em></strong></div></a><a href="#live-config" aria-label="打开节点实际配置"><span class="stat-icon blue"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h5"/></svg></span><div><small>节点配置</small><strong>${overview.node_configs}</strong></div></a><a href="#tasks" data-dashboard-status="pending" aria-label="查看活动任务"><span class="stat-icon amber"><svg viewBox="0 0 24 24"><path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/></svg></span><div><small>活动任务</small><strong>${overview.tasks_pending}</strong></div></a><a href="#tasks" data-dashboard-status="failed" aria-label="查看失败任务"><span class="stat-icon red"><svg viewBox="0 0 24 24"><path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/></svg></span><div><small>失败任务</small><strong>${overview.tasks_failed}</strong></div></a></nav><div class="dashboard-columns"><section class="workspace-panel fleet-overview" id="fleet"><header><h3>节点</h3><a href="#agents">全部 →</a></header><div class="fleet-overview-list">${fleet}</div></section><section class="workspace-panel recent-tasks" id="activity"><header><h3>最近任务</h3><a href="#tasks">全部 →</a></header><div>${activity}</div></section></div>`,
    "总览",
  );
  document.querySelectorAll("[data-dashboard-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.selectedAgent = link.dataset.dashboardAgent;
    };
  });
  document.querySelectorAll("[data-dashboard-status]").forEach((link) => {
    link.onclick = () => {
      state.data.taskFilters = { status: link.dataset.dashboardStatus };
    };
  });
  document.querySelectorAll("[data-dashboard-task]").forEach((link) => {
    link.onclick = () => {
      state.data.focusTask = link.dataset.dashboardTask;
    };
  });
}
  return dashboard;
}
