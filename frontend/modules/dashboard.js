const utcMonth = (value = new Date()) => {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  return date.toISOString().slice(0, 7);
};

export function dashboardTrafficMonthDays(month) {
  if (!/^\d{4}-\d{2}$/.test(String(month || ""))) return [];
  const [year, monthNumber] = month.split("-").map(Number);
  const count = new Date(Date.UTC(year, monthNumber, 0)).getUTCDate();
  return Array.from({ length: count }, (_, index) =>
    `${month}-${String(index + 1).padStart(2, "0")}`,
  );
}

export function aggregateDashboardTrafficDays(rows, month) {
  const totals = new Map(dashboardTrafficMonthDays(month).map((day) => [day, {
    day,
    received_bytes: 0,
    sent_bytes: 0,
    used_bytes: 0,
    peak_receive_bps: 0,
    peak_send_bps: 0,
  }]));
  (rows || []).forEach((row) => {
    const current = totals.get(row.day);
    if (!current) return;
    current.received_bytes += Number(row.received_bytes || 0);
    current.sent_bytes += Number(row.sent_bytes || 0);
    current.used_bytes += Number(row.used_bytes || 0);
    current.peak_receive_bps = Math.max(current.peak_receive_bps, Number(row.peak_receive_bps || 0));
    current.peak_send_bps = Math.max(current.peak_send_bps, Number(row.peak_send_bps || 0));
  });
  return [...totals.values()];
}

// Keep the shared edge between stacked segments square so the two colors
// touch without the small gap created by two independently rounded rects.
const trafficBarPath = (x, top, bottom, width, corners) => {
  const right = x + width;
  const height = Math.max(0, bottom - top);
  const radius = Math.min(2, width / 2, height / 2);
  const topLeft = corners.includes("top-left") ? radius : 0;
  const topRight = corners.includes("top-right") ? radius : 0;
  const bottomRight = corners.includes("bottom-right") ? radius : 0;
  const bottomLeft = corners.includes("bottom-left") ? radius : 0;
  return [
    `M ${x + topLeft} ${top}`,
    `H ${right - topRight}`,
    topRight ? `Q ${right} ${top} ${right} ${top + topRight}` : `V ${top}`,
    `V ${bottom - bottomRight}`,
    bottomRight ? `Q ${right} ${bottom} ${right - bottomRight} ${bottom}` : `H ${right}`,
    `H ${x + bottomLeft}`,
    bottomLeft ? `Q ${x} ${bottom} ${x} ${bottom - bottomLeft}` : `H ${x}`,
    `V ${top + topLeft}`,
    topLeft ? `Q ${x} ${top} ${x + topLeft} ${top}` : `H ${x}`,
    "Z",
  ].join(" ");
};

const trafficBarSegments = (x, top, middle, bottom, width = 14) => {
  const hasSent = middle > top;
  const hasReceived = bottom > middle;
  if (!hasSent && !hasReceived) return "";

  if (hasSent && hasReceived) {
    return `<path class="traffic-bar-sent" d="${trafficBarPath(x, top, middle, width, ["top-left", "top-right"])}"></path><path class="traffic-bar-received" d="${trafficBarPath(x, middle, bottom, width, ["bottom-left", "bottom-right"])}"></path>`;
  }

  const path = trafficBarPath(x, top, bottom, width, [
    "top-left",
    "top-right",
    "bottom-left",
    "bottom-right",
  ]);
  return `<path class="${hasSent ? "traffic-bar-sent" : "traffic-bar-received"}" d="${path}"></path>`;
};

export function installDashboard(ctx) {
  const {
    api, state, esc, engineName, heartbeat, statusTone, ago, short, actionName, shell,
    can = () => false,
    bytes = (value) => `${Number(value || 0)} B`,
    rate = (value) => `${Number(value || 0)} B/s`,
  } = ctx;
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
async function dashboard({ overview: preloadedOverview } = {}) {
  const trafficMonth = state.data.dashboardTrafficMonth || utcMonth();
  const [overview, agents, tasks, trafficUsage] = await Promise.all([
    preloadedOverview || api("/overview"),
    api("/agents"),
    api("/tasks?limit=7"),
    can("traffic.read")
      ? api(`/traffic-usage?month=${encodeURIComponent(trafficMonth)}`)
      : Promise.resolve({ month: trafficMonth, timezone: "UTC", days: [] }),
  ]);
  state.data.overview = overview;
  state.data.agents = agents;
  state.data.dashboardTrafficMonth = trafficMonth;
  state.data.dashboardTrafficUsage = trafficUsage;
  const dailyTraffic = aggregateDashboardTrafficDays(trafficUsage.days, trafficMonth);
  const receivedTraffic = dailyTraffic.reduce((sum, day) => sum + day.received_bytes, 0);
  const sentTraffic = dailyTraffic.reduce((sum, day) => sum + day.sent_bytes, 0);
  const usedTraffic = receivedTraffic + sentTraffic;
  const maxTrafficDay = Math.max(1, ...dailyTraffic.map((day) => day.used_bytes));
  const trafficChartHeight = 112;
  const trafficChartWidth = Math.max(20, dailyTraffic.length * 20);
  const trafficBars = dailyTraffic.map((day, index) => {
    const usedHeight = Math.round(day.used_bytes / maxTrafficDay * trafficChartHeight);
    const receivedHeight = Math.round(day.received_bytes / maxTrafficDay * trafficChartHeight);
    const sentHeight = Math.max(0, usedHeight - receivedHeight);
    const x = index * 20 + 3;
    const receivedY = trafficChartHeight - receivedHeight;
    const sentY = receivedY - sentHeight;
    return `<g><title>${esc(day.day)} · 接收 ${esc(bytes(day.received_bytes))} · 发送 ${esc(bytes(day.sent_bytes))} · 合计 ${esc(bytes(day.used_bytes))}</title>${trafficBarSegments(x, sentY, receivedY, trafficChartHeight)}</g>`;
  }).join("");
  const trafficAxis = dailyTraffic.map((day, index) => {
    const dayNumber = index + 1;
    return `<span>${index % 5 === 0 || index === dailyTraffic.length - 1 ? `${dayNumber}日` : ""}</span>`;
  }).join("");
  const [trafficYear, trafficMonthNumber] = trafficMonth.split("-").map(Number);
  const trafficMonthLabel = `${trafficYear}年${String(trafficMonthNumber).padStart(2, "0")}月`;
  const trafficMonthOptions = Array.from({ length: 12 }, (_, index) => `<button type="button" data-dashboard-month-option data-month-index="${index + 1}">${index + 1}月</button>`).join("");
  const trafficDetailRows = dailyTraffic.map((day) => `<tr><td>${esc(day.day)}</td><td>${bytes(day.received_bytes)}</td><td>${bytes(day.sent_bytes)}</td><td><b>${bytes(day.used_bytes)}</b></td><td>${rate(day.peak_receive_bps)} / ${rate(day.peak_send_bps)}</td></tr>`).join("");
  const trafficHistory = can("traffic.read") ? `<section class="traffic-history dashboard-traffic-history" id="traffic-usage">
    <header class="dashboard-traffic-head"><div class="dashboard-traffic-title"><span class="eyebrow">流量总览</span><h3>${esc(trafficMonth)} 月流量</h3><small>控制面持久化汇总 · UTC 自然日</small></div><div class="dashboard-traffic-total"><span>本月累计</span><strong>${bytes(usedTraffic)}</strong></div><dl><div><dt>接收</dt><dd>${bytes(receivedTraffic)}</dd></div><div><dt>发送</dt><dd>${bytes(sentTraffic)}</dd></div></dl><div class="dashboard-traffic-month"><span>月份</span><details class="dashboard-month-picker" data-dashboard-traffic-month><summary><b data-dashboard-month-summary>${esc(trafficMonthLabel)}</b><i>⌄</i></summary><div class="dashboard-month-popover"><header><button type="button" data-dashboard-month-year-shift="-1" aria-label="上一年">‹</button><strong data-dashboard-month-year>${trafficYear}</strong><button type="button" data-dashboard-month-year-shift="1" aria-label="下一年">›</button></header><div class="dashboard-month-grid">${trafficMonthOptions}</div><footer><button type="button" data-dashboard-current-month>回到本月</button></footer></div></details></div></header>
    <div class="traffic-chart-legend"><span class="received">接收</span><span class="sent">发送</span><small>每日接收与发送合计</small></div>
    <div class="traffic-month-chart dashboard-traffic-chart"><svg viewBox="0 0 ${trafficChartWidth} ${trafficChartHeight}" preserveAspectRatio="none" role="img" aria-label="${esc(trafficMonth)} 每日接收和发送流量图">${trafficBars}</svg></div><div class="dashboard-traffic-axis" aria-hidden="true">${trafficAxis}</div>
    <footer class="dashboard-traffic-actions"><button class="button small" type="button" data-dashboard-traffic-details>查看 ${dailyTraffic.length} 天明细</button><a href="#traffic">管理流量配额 →</a></footer>
    <dialog class="traffic-edit-dialog dashboard-traffic-dialog" data-dashboard-traffic-dialog aria-labelledby="dashboard-traffic-dialog-title"><header><span class="traffic-edit-icon" aria-hidden="true">↕</span><div><p class="eyebrow">每日用量</p><h2 id="dashboard-traffic-dialog-title">${esc(trafficMonth)} 流量明细</h2><p>接收、发送和峰值按 UTC 自然日汇总</p></div><button class="deploy-command-close" type="button" data-dashboard-traffic-close aria-label="关闭流量明细弹窗">×</button></header><div class="dashboard-traffic-detail-body"><table><thead><tr><th>日期</th><th>接收</th><th>发送</th><th>合计</th><th>接收 / 发送峰值</th></tr></thead><tbody>${trafficDetailRows}</tbody></table></div><footer class="dashboard-traffic-dialog-actions"><span>共 ${dailyTraffic.length} 个自然日</span><button class="button" type="button" data-dashboard-traffic-close>关闭</button></footer></dialog>
  </section>` : "";
  const fleet =
    agents
      .slice(0, 7)
      .map(
        (agent) =>
          `<a href="#settings-node-${esc(agent.id)}" data-dashboard-agent="${esc(agent.id)}"><span class="node-avatar">●</span><span><strong>${esc(agent.name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)}</small><span class="fleet-engines">${(agent.capabilities || []).map((engine) => `<em class="${esc(engine)}">${esc(engineName(engine))}</em>`).join("")}</span></span><span class="status-label ${statusTone(agent.status)}">${agent.status === "online" ? "在线" : "离线"}</span><time>${esc(heartbeat(agent.last_seen))}</time><i>›</i></a>`,
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
    `<section class="dashboard-head" id="summary"><h2>运行总览</h2><span class="trust-badge ${!overview.agents ? "inactive" : overview.agents_online === overview.agents ? "" : "warn"}"><i></i>${!overview.agents ? "等待节点接入" : overview.agents_online === overview.agents ? "全部在线" : `${overview.agents_online} / ${overview.agents} 在线`}</span></section><nav class="ops-stats" aria-label="运行概览快捷入口"><a href="#node-settings" aria-label="查看在线节点"><span class="stat-icon green"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/><path d="M8 12h2l1.3-3 2.1 6 1.4-3H17"/></svg></span><div><small>在线节点</small><strong>${overview.agents_online}<em>/${overview.agents}</em></strong></div></a><a href="#live-config" aria-label="打开节点实际配置"><span class="stat-icon blue"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h5"/></svg></span><div><small>节点配置</small><strong>${overview.node_configs}</strong></div></a><a href="#tasks" data-dashboard-status="pending" aria-label="查看活动任务"><span class="stat-icon amber"><svg viewBox="0 0 24 24"><path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/></svg></span><div><small>活动任务</small><strong>${overview.tasks_pending}</strong></div></a><a href="#tasks" data-dashboard-status="failed" aria-label="查看失败任务"><span class="stat-icon red"><svg viewBox="0 0 24 24"><path d="M12 3.5 21 20H3zM12 9v5M12 17.5h.01"/></svg></span><div><small>失败任务</small><strong>${overview.tasks_failed}</strong></div></a></nav>${trafficHistory}<div class="dashboard-columns"><section class="workspace-panel fleet-overview" id="fleet"><header><h3>节点</h3><a href="#node-settings">全部 →</a></header><div class="fleet-overview-list">${fleet}</div></section><section class="workspace-panel recent-tasks" id="activity"><header><h3>最近任务</h3><a href="#tasks">全部 →</a></header><div>${activity}</div></section></div>`,
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
  const trafficMonthPicker = document.querySelector("[data-dashboard-traffic-month]");
  if (trafficMonthPicker) {
    let pickerYear = trafficYear;
    const currentMonth = utcMonth();
    const currentYear = Number(currentMonth.slice(0, 4));
    const yearLabel = trafficMonthPicker.querySelector("[data-dashboard-month-year]");
    const monthButtons = [...trafficMonthPicker.querySelectorAll("[data-dashboard-month-option]")];
    const yearButtons = [...trafficMonthPicker.querySelectorAll("[data-dashboard-month-year-shift]")];
    const renderMonthPicker = () => {
      yearLabel.textContent = pickerYear;
      monthButtons.forEach((button) => {
        const value = `${pickerYear}-${String(button.dataset.monthIndex).padStart(2, "0")}`;
        button.dataset.dashboardMonth = value;
        button.disabled = value > currentMonth;
        button.classList.toggle("active", value === trafficMonth);
      });
      yearButtons.forEach((button) => {
        button.disabled = Number(button.dataset.dashboardMonthYearShift) > 0 && pickerYear >= currentYear;
      });
    };
    const selectMonth = async (value) => {
      const previousMonth = state.data.dashboardTrafficMonth;
      state.data.dashboardTrafficMonth = value;
      trafficMonthPicker.open = false;
      try {
        await dashboard({ overview: state.data.overview });
      } catch {
        state.data.dashboardTrafficMonth = previousMonth;
      }
    };
    yearButtons.forEach((button) => {
      button.onclick = () => {
        pickerYear += Number(button.dataset.dashboardMonthYearShift);
        renderMonthPicker();
      };
    });
    monthButtons.forEach((button) => {
      button.onclick = () => selectMonth(button.dataset.dashboardMonth);
    });
    trafficMonthPicker.querySelector("[data-dashboard-current-month]").onclick = () => selectMonth(currentMonth);
    renderMonthPicker();
  }
  const trafficDetailsButton = document.querySelector("[data-dashboard-traffic-details]");
  const trafficDetailsDialog = document.querySelector("[data-dashboard-traffic-dialog]");
  if (trafficDetailsButton && trafficDetailsDialog) {
    trafficDetailsButton.onclick = () => trafficDetailsDialog.showModal();
    trafficDetailsDialog.querySelectorAll("[data-dashboard-traffic-close]").forEach((button) => {
      button.onclick = () => trafficDetailsDialog.close();
    });
    trafficDetailsDialog.onclick = (event) => {
      if (event.target === trafficDetailsDialog) trafficDetailsDialog.close();
    };
  }
}
  return dashboard;
}
