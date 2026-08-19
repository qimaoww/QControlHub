export function installAgents(ctx) {
  const { api, optionalAPI, state, engines, can, esc, engineName, statusTone, serviceStatusName, short, date, ago, heartbeat, percent, bytes, conciseVersion, rate, actionName, serviceActionDisabled, trafficChart, renderConfigDiff, notify, confirmAction, shell } = ctx;
async function agents() {
  const [agents, deployments, accessEntries, settings, tokens] =
    await Promise.all([
      api("/agents"),
      api("/deployments"),
      api("/client-access"),
      api("/settings"),
      can("admin") ? api("/enrollment-tokens") : Promise.resolve([]),
    ]);
  state.data.agents = agents;
  state.data.settings = settings;
  state.data.selectedAgent ||= agents[0]?.id || "";
  const overview = await api("/overview");
  state.data.overview = overview;

  const savedConfigs = (
    await Promise.all(
      agents.map((agent) =>
        api(`/agents/${encodeURIComponent(agent.id)}/configs`),
      ),
    )
  ).flat();
  const configByService = new Map(
    savedConfigs.map((config) => [
      `${config.agent_id}|${config.engine}`,
      config,
    ]),
  );
  const deploymentByService = new Map(
    deployments.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const accessByService = new Map(
    accessEntries.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const configDiffByService = new Map();
  await Promise.all(
    savedConfigs.map(async (saved) => {
      const key = `${saved.agent_id}|${saved.engine}`;
      const deployed = deploymentByService.get(key);
      if (
        !deployed?.config_id ||
        (deployed.config_id === saved.id &&
          deployed.config_version === saved.version)
      )
        return;
      const deployedConfig = await optionalAPI(
        `/configs/${encodeURIComponent(deployed.config_id)}/revisions/${deployed.config_version}`,
      );
      if (!deployedConfig) return;
      const diff = renderConfigDiff(saved.content, deployedConfig.content);
      if (diff) configDiffByService.set(key, diff);
    }),
  );

  const tokenRows =
    tokens
      .map(
        (token) =>
          `<article><div><strong>${esc(token.name)}</strong><small>${token.reusable ? `可重复安装 · 已安装 ${token.used_count} 次` : `旧版添加命令 · 已安装 ${token.used_count} 次`}</small></div><button class="access-history-delete" type="button" data-delete-enrollment="${esc(token.id)}">删除</button></article>`,
      )
      .join("") || "";

  const nodeCards = agents
    .map((agent) => {
      const metrics = agent.metrics || {};
      const services = (agent.capabilities || [])
        .map((engine) => {
          const key = `${agent.id}|${engine}`;
          const runtime = agent.runtime?.[engine] || {};
          const deployed = deploymentByService.get(key);
          const saved = configByService.get(key);
          const access = accessByService.get(key);
          const configDiff = configDiffByService.get(key) || "";
          const drift =
            saved &&
            (!deployed ||
              deployed.config_id !== saved.id ||
              deployed.config_version < saved.version);
          const firstProfile = access?.profiles?.[0];
          const port = firstProfile?.profile?.fields?.find(
            (field) => field.label === "端口",
          )?.value;
          const endpoint =
            access && port
              ? `${access.address}:${port}`
              : access?.address || "";
          return `<article class="service-card service-${esc(engine)}">
            <div class="service-card-main">
              <div class="service-overview"><header><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${statusTone(runtime.service_status)}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceStatusName(runtime.service_status))}</b></span></header><div class="service-version"><span class="service-version-label"><small>已安装版本</small><button class="service-version-toggle" type="button" data-open-version-form aria-label="打开 ${esc(engineName(engine))} 版本切换">切换版本</button></span><strong data-core-version="${esc(engine)}" title="${esc(runtime.version || "未检测到二进制")}">${esc(runtime.installed ? conciseVersion(engine, runtime.version) : "未检测到二进制")}</strong></div></div>
              <div class="service-deployment"><dl class="service-facts"><div><dt>已部署配置</dt><dd>${deployed?.config_version ? `v${deployed.config_version}` : "—"}</dd></div><div><dt>已保存配置</dt><dd>${saved?.version ? `v${saved.version}` : "—"}</dd></div></dl>${drift ? `<div class="deployment-drift"><span>${deployed ? "已保存版本尚未部署" : "已保存配置尚未部署"}</span><b>待部署 v${saved.version}</b></div>` : ""}${configDiff ? `<details class="config-diff-drawer"><summary>查看配置差异 <i>＋</i></summary>${configDiff}</details>` : ""}<div class="service-endpoint ${endpoint ? "" : "empty"}">${endpoint ? `<span><b>${esc(firstProfile?.protocol || "客户端入站")}</b><small>${esc(firstProfile?.profile?.format || "已部署配置")}</small></span><code>${esc(endpoint)}</code>` : `<b>${deployed ? "自定义配置" : saved ? "尚未部署" : "尚未配置"}</b>`}</div></div>
              <div class="service-primary-action">${drift ? `<button class="button service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">查看配置</button><button class="button primary" type="button" data-deploy="${esc(agent.id)}" data-engine="${esc(engine)}" data-config-id="${esc(saved.id)}">部署 v${saved.version}</button>` : `<button class="button primary service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">配置 <span>→</span></button>`}</div>
            </div>
            <details class="runtime-drawer"><summary><span><b>管理服务</b></span><i>＋</i></summary><div class="runtime-drawer-body"><div class="service-actions">${["status", "start", "restart", "stop"].map((action) => `<button class="button small ${action === "stop" ? "danger-button" : ""}" type="button" data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" data-task-action="${action}" data-service-action="${action}" ${serviceActionDisabled(action, agent.status === "online", runtime.service_status) ? "disabled" : ""}>${esc(actionName(action))}</button>`).join("")}</div></div></details>
            <details class="runtime-drawer version-drawer"><summary><span><b>版本切换</b><small>安装或切换内核版本</small></span><i>＋</i></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset><label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${agent.status !== "online" || !can("operator") ? "disabled" : ""}>${runtime.installed ? "升级或切换版本" : "安装内核"}</button><small>${runtime.installed ? "官方 Release · SHA-256 校验" : "首次安装前需准备安全目录与 systemd 单元"}</small></form></div></details>
            ${access?.profiles?.length ? `<a class="service-client-access" href="#client-access" data-client-agent="${esc(agent.id)}" data-client-engine="${esc(engine)}"><span><b>客户端配置</b><small>${esc(access.source)} · ${esc(access.address)}</small></span><strong>${access.profiles.length} 个入站 <i>→</i></strong></a>` : ""}
          </article>`;
        })
        .join("");
      const labels = Object.entries(agent.labels || {})
        .map(([key, value]) => `<span>${esc(key)}=${esc(value)}</span>`)
        .join("");
      return `<details class="machine-workspace" id="node-${esc(agent.id)}" name="node-workspace" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-available="${metrics.collected_at ? 1 : 0}" ${agent.id === state.data.selectedAgent ? "open" : ""}><summary class="machine-header"><div class="machine-identity">${can("operator") ? `<label class="batch-select" title="选择此节点参与批量操作"><input type="checkbox" data-batch-checkbox value="${esc(agent.id)}" aria-label="选择 ${esc(agent.name)} 参与批量操作"><span></span></label>` : ""}<span class="machine-avatar">●</span><span><strong>${esc(agent.name)}</strong><code>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(agent.id))}</code></span></div><section class="machine-resource-summary" aria-label="资源监控"><div><span>CPU</span><strong data-metric-text="cpu">${metrics.cpu_available ? `${Number(metrics.cpu_percent).toFixed(1)}%` : "等待采集"}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" max="100" value="${metrics.cpu_available ? Number(metrics.cpu_percent) : 0}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${metrics.memory_available ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : "等待采集"}</strong><progress aria-label="内存使用率" data-metric-progress="memory" max="100" value="${percent(metrics.memory_used_bytes, metrics.memory_total_bytes)}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${metrics.disk_available ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : "等待采集"}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" max="100" value="${percent(metrics.disk_used_bytes, metrics.disk_total_bytes)}"></progress></div><div class="machine-resource-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">${metrics.network_available ? rate(metrics.network_rx_bps) : "等待采集"}</i> · ↑ <i data-metric-text="upload-rate">${metrics.network_available ? rate(metrics.network_tx_bps) : "等待采集"}</i></strong><small>累计 ↓ <span data-metric-text="download-total">${metrics.network_available ? bytes(metrics.network_rx_bytes) : "—"}</span> · ↑ <span data-metric-text="upload-total">${metrics.network_available ? bytes(metrics.network_tx_bytes) : "—"}</span></small></div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section><div class="machine-state"><span class="status-dot ${statusTone(agent.status)}" data-agent-status-dot></span><span><b data-agent-status-label>${agent.status === "online" ? "在线" : "离线"}</b><small data-agent-heartbeat>${esc(heartbeat(agent.last_seen))}</small></span></div><i class="machine-chevron" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m7 10 5 5 5-5"/></svg></i></summary><div class="machine-body"><section class="service-canvas"><header class="service-canvas-head"><h2>节点内核</h2><span>${(agent.capabilities || []).length} 个内核</span></header><div class="service-grid">${services}</div></section><details class="machine-profile node-inspector"><summary class="node-inspector-summary"><span><b>节点身份</b><small>身份信息 · 指标趋势</small></span><i>＋</i></summary><div class="node-inspector-body"><section class="node-identity-panel"><h2>${esc(agent.name)}</h2><dl class="identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>Agent 版本</dt><dd data-agent-version>${esc(agent.version || "未知")}</dd></div><div><dt>注册时间</dt><dd>${date(agent.enrolled_at)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl>${labels ? `<div class="labels">${labels}</div>` : ""}<footer class="node-identity-refresh"><span data-metric-text="stamp">${metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据"}</span><button type="button" data-agent-refresh>刷新</button></footer></section><section class="metric-trend-empty" data-metric-history="${esc(agent.id)}" aria-label="暂无指标趋势"><span>⌁</span><b>正在载入指标趋势</b><small>打开节点详情后载入最近 24 小时的上下行速率。</small></section></div></details><footer class="machine-footer"><span><i>●</i><b>节点身份已验证</b></span>${can("admin") ? `<details><summary>节点管理</summary><button type="button" data-delete="${esc(agent.id)}">移除节点并吊销身份</button></details>` : ""}</footer></div></details>`;
    })
    .join("");

  const enrollment = can("admin")
    ? `<details class="enrollment-sheet" id="enrollment" data-has-agents="${agents.length ? 1 : 0}" ${agents.length ? "" : "open"}><summary><b>＋ 添加节点</b><i>＋</i></summary><div class="enrollment-sheet-body"><form class="access-form add-node-form" id="enrollment-form"><label>节点名称<input name="name" maxlength="100" required autocomplete="off" placeholder="例如 shanghai-edge-01"></label><button class="button primary" type="submit">生成添加节点命令</button></form><p class="enrollment-security-note"><b>添加节点命令只显示一次</b><span>命令绑定该节点，可重复安装；删除添加记录后立即失效。</span></p>${tokenRows ? `<details class="access-history"><summary>添加记录（${tokens.length}）</summary><div>${tokenRows}</div></details>` : ""}</div></details>`
    : "";
  const batch =
    agents.length && can("operator")
      ? `<form class="batch-toolbar" id="batch-form"><span class="batch-toolbar-title">批量操作</span><label>内核<select name="engine">${engines.map((engine) => `<option value="${engine}">${esc(engineName(engine))}</option>`).join("")}</select></label><label>动作<select name="action"><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label><button class="button small" type="submit" disabled>执行</button><small data-batch-count>未选择节点</small></form>`
      : "";
  shell(
    `${enrollment}${batch}${nodeCards ? `<section class="machine-stack">${nodeCards}</section>` : '<div class="empty large"><strong>还没有节点</strong><p>请先添加节点。</p></div>'}`,
    "节点",
  );
  bindAgentPage();
}

function bindAgentPage() {
  document.querySelectorAll(".machine-workspace").forEach((item) => {
    item.addEventListener("toggle", () => {
      if (item.open) {
        state.data.selectedAgent = item.id.replace(/^node-/, "");
        loadMetricHistory(state.data.selectedAgent);
      }
    });
    if (item.open) loadMetricHistory(item.dataset.agentNode);
  });
  document.querySelectorAll("[data-config]").forEach((button) => {
    button.onclick = () => {
      state.data.agentId = button.dataset.config;
      state.data.engine = button.dataset.engine;
      location.hash = "#agent-config";
    };
  });
  document.querySelectorAll("[data-client-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.accessAgent = link.dataset.clientAgent;
      state.data.accessEngine = link.dataset.clientEngine;
    };
  });
  document.querySelectorAll("[data-task-action]").forEach((button) => {
    button.onclick = async () => {
      if (
        button.dataset.taskAction === "stop" &&
        !(await confirmAction(
          `确定停止 ${engineName(button.dataset.taskEngine)} 服务？现有连接会立即中断，需再次启动才能恢复。`,
          "停止服务",
        ))
      )
        return;
      await submitTask({
        agent_id: button.dataset.taskAgent,
        engine: button.dataset.taskEngine,
        action: button.dataset.taskAction,
      });
    };
  });
  document.querySelectorAll("[data-deploy]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          `确定将已保存配置部署到 ${engineName(button.dataset.engine)} 并重启服务？`,
          button.textContent.trim(),
        ))
      )
        return;
      await submitTask({
        agent_id: button.dataset.deploy,
        engine: button.dataset.engine,
        action: "deploy",
        config_id: button.dataset.configId,
      });
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    form.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(form);
      const channel = values.get("release_channel");
      const version =
        channel === "custom" ? values.get("custom_version") : channel;
      if (
        !(await confirmAction(
          "确定提交内核安装或版本切换任务？下载和校验完成后，目标服务会重启。",
          "提交任务",
        ))
      )
        return;
      await submitTask({
        agent_id: form.dataset.versionAgent,
        engine: form.dataset.versionEngine,
        action: "install",
        core_version: version,
      });
    };
  });
  document.querySelectorAll("[data-open-version-form]").forEach((button) => {
    button.onclick = () => {
      const drawer = button
        .closest(".service-card")
        ?.querySelector(".version-drawer");
      if (drawer) drawer.open = true;
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    const custom = form.querySelector(".custom-version-field");
    const input = custom?.querySelector("input");
    const sync = () => {
      const enabled =
        form.querySelector('input[name="release_channel"]:checked')?.value ===
        "custom";
      custom?.classList.toggle("is-disabled", !enabled);
      if (input) {
        input.disabled = !enabled;
        input.required = enabled;
      }
    };
    form
      .querySelectorAll('input[name="release_channel"]')
      .forEach((radio) => radio.addEventListener("change", sync));
    sync();
  });
  document.querySelectorAll("[data-delete]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定移除此节点并永久吊销其身份？移除后 Agent 将无法再次连接。",
          "移除节点",
        ))
      )
        return;
      await api(`/agents/${encodeURIComponent(button.dataset.delete)}`, {
        method: "DELETE",
      });
      await agents();
    };
  });
  document.querySelectorAll("[data-delete-enrollment]").forEach((button) => {
    button.onclick = async () => {
      if (!(await confirmAction("确定删除这个添加节点命令？删除后命令立即失效。", "删除添加命令")))
        return;
      await api(
        `/enrollment-tokens/${encodeURIComponent(button.dataset.deleteEnrollment)}`,
        {
          method: "DELETE",
        },
      );
      await agents();
    };
  });
  const batchForm = document.querySelector("#batch-form");
  const updateBatch = () => {
    const count = document.querySelectorAll(
      "[data-batch-checkbox]:checked",
    ).length;
    const button = batchForm?.querySelector("button[type=submit]");
    if (button) button.disabled = count === 0;
    const label = batchForm?.querySelector("[data-batch-count]");
    if (label)
      label.textContent = count ? `已选择 ${count} 个节点` : "未选择节点";
  };
  document
    .querySelectorAll("[data-batch-checkbox]")
    .forEach((input) => (input.onchange = updateBatch));
  if (batchForm)
    batchForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(batchForm);
      const selected = [
        ...document.querySelectorAll("[data-batch-checkbox]:checked"),
      ];
      await Promise.all(
        selected.map((input) =>
          api("/tasks", {
            method: "POST",
            body: JSON.stringify({
              agent_id: input.value,
              engine: values.get("engine"),
              action: values.get("action"),
            }),
          }),
        ),
      );
      notify(`已提交 ${selected.length} 个任务`);
      location.hash = "#tasks";
    };
  const enrollmentForm = document.querySelector("#enrollment-form");
  if (enrollmentForm)
    enrollmentForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(enrollmentForm);
      const name = String(values.get("name") || "").trim();
      const created = await api("/enrollment-tokens", {
        method: "POST",
        body: JSON.stringify({
          name,
        }),
      });
      const escapedToken = created.token.replaceAll("'", "'\\''");
      const escapedName = name.replaceAll("'", "'\\''");
      const command = `curl -fsSL -H 'X-QControlHub-Enrollment: ${escapedToken}' ${location.origin}/install-agent.sh | sudo bash -s -- ${location.origin} '${escapedToken}' '${escapedName}'`;
      showCommand(command);
    };
  document
    .querySelectorAll("[data-agent-refresh]")
    .forEach((button) => (button.onclick = () => pollAgentMetrics()));
  clearTimeout(state.agentPollTimer);
  state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
}

async function submitTask(payload) {
  try {
    const task = await api("/tasks", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    notify("任务已提交");
    return task;
  } catch (error) {
    notify(error.message, "error");
    return null;
  }
}

async function loadMetricHistory(agentID) {
  const target = document.querySelector(
    `[data-metric-history="${CSS.escape(agentID)}"]`,
  );
  if (!target || target.dataset.loaded) return;
  target.dataset.loaded = "1";
  try {
    const samples = await api(`/metrics/${encodeURIComponent(agentID)}`);
    const chart = trafficChart(samples);
    if (!target.isConnected) return;
    if (chart) {
      const panel = document.createElement("section");
      panel.className = "metric-trend-panel";
      panel.setAttribute("aria-label", "最近 24 小时流量趋势");
      panel.innerHTML = `<header><b>流量趋势</b><small>最近 24 小时 · 每分钟采样</small></header>${chart}`;
      target.replaceWith(panel);
    } else {
      target.innerHTML =
        "<span>⌁</span><b>暂无指标趋势</b><small>节点上线并上报指标后，这里将显示最近 24 小时的上下行速率曲线。</small>";
    }
  } catch {
    if (target.isConnected) {
      target.dataset.loaded = "";
      target.innerHTML =
        "<span>⌁</span><b>指标趋势载入失败</b><small>点击节点资源刷新按钮后重试。</small>";
    }
  }
}

function updateAgentMetrics(item) {
  const root = document.querySelector(
    `[data-agent-metrics="${CSS.escape(item.id)}"]`,
  );
  if (!root) return;
  const metrics = item.metrics || {};
  const setText = (name, value) => {
    const element = root.querySelector(`[data-metric-text="${name}"]`);
    if (element) element.textContent = value;
  };
  const setProgress = (name, available, value) => {
    const element = root.querySelector(`[data-metric-progress="${name}"]`);
    if (!element) return;
    element.value = available ? value : 0;
    element.dataset.available = available ? "1" : "0";
  };
  const online = item.status === "online";
  root.dataset.available = metrics.collected_at ? "1" : "0";
  const dot = root.querySelector("[data-agent-status-dot]");
  if (dot) dot.className = `status-dot ${statusTone(item.status)}`;
  const status = root.querySelector("[data-agent-status-label]");
  if (status) status.textContent = online ? "在线" : "离线";
  const lastSeen = root.querySelector("[data-agent-heartbeat]");
  if (lastSeen) lastSeen.textContent = heartbeat(item.last_seen);
  root
    .querySelectorAll("[data-agent-version]")
    .forEach((element) => (element.textContent = item.version || "未知"));
  setText(
    "stamp",
    metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据",
  );
  setText(
    "cpu",
    metrics.cpu_available
      ? `${Number(metrics.cpu_percent).toFixed(1)}%`
      : "等待采集",
  );
  setProgress("cpu", metrics.cpu_available, Number(metrics.cpu_percent || 0));
  setText(
    "memory",
    metrics.memory_available
      ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}`
      : "等待采集",
  );
  setProgress(
    "memory",
    metrics.memory_available,
    percent(metrics.memory_used_bytes, metrics.memory_total_bytes),
  );
  setText(
    "disk",
    metrics.disk_available
      ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}`
      : "等待采集",
  );
  setProgress(
    "disk",
    metrics.disk_available,
    percent(metrics.disk_used_bytes, metrics.disk_total_bytes),
  );
  setText(
    "download-rate",
    metrics.network_available ? rate(metrics.network_rx_bps) : "等待采集",
  );
  setText(
    "upload-rate",
    metrics.network_available ? rate(metrics.network_tx_bps) : "等待采集",
  );
  setText(
    "download-total",
    metrics.network_available ? bytes(metrics.network_rx_bytes) : "—",
  );
  setText(
    "upload-total",
    metrics.network_available ? bytes(metrics.network_tx_bytes) : "—",
  );
  Object.entries(item.runtime || {}).forEach(([engine, runtime]) => {
    const version = root.querySelector(
      `[data-core-version="${CSS.escape(engine)}"]`,
    );
    if (version)
      version.textContent = runtime.installed
        ? conciseVersion(engine, runtime.version)
        : "未检测到二进制";
    const service = root.querySelector(
      `[data-core-service="${CSS.escape(engine)}"]`,
    );
    if (service) {
      service.textContent = serviceStatusName(runtime.service_status);
      service.closest(".engine-state").className =
        `engine-state ${statusTone(runtime.service_status)}`;
      service
        .closest(".service-card")
        .querySelectorAll("[data-service-action]")
        .forEach((button) => {
          button.disabled = serviceActionDisabled(
            button.dataset.serviceAction,
            online,
            runtime.service_status,
          );
        });
    }
  });
  root.querySelectorAll(".core-version-form button[type=submit]").forEach(
    (button) => (button.disabled = !online || !can("operator")),
  );
}

async function pollAgentMetrics() {
  if (state.route !== "agents" || document.hidden) return;
  const indicators = document.querySelectorAll("[data-metric-poll]");
  indicators.forEach((element) => (element.textContent = "正在刷新…"));
  try {
    const items = await api("/agents");
    items.forEach(updateAgentMetrics);
    const online = items.filter((item) => item.status === "online").length;
    const count = document.querySelector("[data-online-count]");
    if (count) {
      count.textContent = String(online);
      count.hidden = online === 0;
    }
    const sync = document.querySelector("[data-sync-state]");
    sync?.classList.toggle("inactive", online === 0);
    const syncLabel = document.querySelector("[data-sync-label]");
    if (syncLabel)
      syncLabel.textContent = online
        ? `${online} 个节点在线`
        : "等待节点连接";
    indicators.forEach((element) => (element.textContent = "刚刚更新"));
  } catch {
    indicators.forEach(
      (element) => (element.textContent = "刷新失败，保留上次数据"),
    );
  } finally {
    clearTimeout(state.agentPollTimer);
    if (state.route === "agents")
      state.agentPollTimer = setTimeout(pollAgentMetrics, 5000);
  }
}

function bindCodeEditors() {
  const formatCodeBytes = (value) => {
    if (value < 1024) return `${value} B`;
    if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
    return `${(value / (1024 * 1024)).toFixed(2)} MiB`;
  };
  document.querySelectorAll("[data-code-editor]").forEach((editor) => {
    const input = editor.querySelector("[data-code-input]");
    const gutter = editor.querySelector("[data-line-numbers]");
    const byteLabel = editor.querySelector("[data-code-bytes]");
    const position = editor.querySelector("[data-code-position]");
    const status = editor.querySelector("[data-code-status]");
    const statusDot = editor.querySelector("[data-code-status-dot]");
    const validation = editor.querySelector("[data-code-validation]");
    const reset = editor.querySelector("[data-code-reset]");
    if (!input || !gutter) return;
    const form = input.closest("form");
    const maxBytes = Number(editor.dataset.codeMaxBytes) || 2 * 1024 * 1024;
    const original = input.value;
    const baselineStatus = status?.textContent || "已保存";
    const baselineValidation = validation?.textContent || "";
    input.setAttribute("wrap", "off");
    const updatePosition = () => {
      if (!position) return;
      const before = input.value.slice(0, input.selectionStart);
      const lineStart = before.lastIndexOf("\n") + 1;
      position.textContent = `行 ${before.split("\n").length}，列 ${before.length - lineStart + 1}`;
    };
    const inspect = () => {
      const size = new Blob([input.value]).size;
      if (size > maxBytes)
        return {
          valid: false,
          status: "内容过大",
          message: "配置源码超过 2 MiB 上限，无法提交。",
          size,
        };
      if (!input.value.trim())
        return {
          valid: false,
          status: "内容为空",
          message: "配置源码不能为空。",
          size,
        };
      if ((editor.dataset.codeLanguage || "").toUpperCase() === "JSON") {
        try {
          JSON.parse(input.value);
          return { valid: true, json: true, size };
        } catch {
          return {
            valid: false,
            status: "语法错误",
            message: "JSON 语法错误，请检查括号、逗号和引号。",
            size,
          };
        }
      }
      return { valid: true, size };
    };
    const blockSubmit = (blocked) => {
      form
        ?.querySelectorAll('button[type="submit"], input[type="submit"]')
        .forEach((control) => {
          if (blocked && !control.disabled) {
            control.disabled = true;
            control.dataset.codeBlocked = "1";
          } else if (!blocked && control.dataset.codeBlocked === "1") {
            control.disabled = false;
            delete control.dataset.codeBlocked;
          }
        });
    };
    const update = () => {
      const result = inspect();
      const dirty = input.value !== original;
      gutter.textContent = Array.from(
        { length: Math.max(1, input.value.split("\n").length) },
        (_, index) => String(index + 1),
      ).join("\n");
      if (byteLabel)
        byteLabel.textContent =
          `${formatCodeBytes(result.size)}${result.size > maxBytes ? " / 2 MiB" : ""}`;
      editor.dataset.dirty = dirty ? "1" : "0";
      editor.dataset.codeValid = result.valid ? "1" : "0";
      input.classList.toggle("is-invalid", !result.valid);
      if (reset) reset.disabled = !dirty;
      if (!result.valid) {
        if (status) status.textContent = result.status;
        if (validation) validation.textContent = result.message;
        if (statusDot) statusDot.style.background = "var(--red)";
      } else if (dirty) {
        if (status) status.textContent = "未保存";
        if (validation)
          validation.textContent = result.json
            ? "JSON 语法有效；提交后仍会由节点内核校验。"
            : baselineValidation;
        if (statusDot) statusDot.style.background = "var(--amber)";
      } else {
        if (status) status.textContent = baselineStatus;
        if (validation) validation.textContent = baselineValidation;
        if (statusDot) statusDot.style.background = "var(--green)";
      }
      blockSubmit(!result.valid);
      updatePosition();
    };
    input.addEventListener("input", update);
    input.addEventListener("scroll", () => {
      gutter.scrollTop = input.scrollTop;
    });
    ["click", "keyup", "select"].forEach((name) =>
      input.addEventListener(name, updatePosition),
    );
    input.addEventListener("keydown", (event) => {
      if (
        event.key !== "Tab" ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey
      )
        return;
      event.preventDefault();
      const start = input.selectionStart;
      const end = input.selectionEnd;
      if (!event.shiftKey && start === end) {
        input.setRangeText("  ", start, end, "end");
      } else {
        const value = input.value;
        const lineStart = value.lastIndexOf("\n", Math.max(0, start - 1)) + 1;
        const nextBreak = value.indexOf("\n", end);
        const lineEnd = nextBreak === -1 ? value.length : nextBreak;
        const replacement = value
          .slice(lineStart, lineEnd)
          .split("\n")
          .map((line) =>
            event.shiftKey ? line.replace(/^(?: {1,2}|\t)/, "") : `  ${line}`,
          )
          .join("\n");
        input.setRangeText(replacement, lineStart, lineEnd, "select");
      }
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    reset?.addEventListener("click", () => {
      input.value = original;
      input.setSelectionRange(0, 0);
      update();
      input.focus();
    });
    form?.addEventListener(
      "submit",
      (event) => {
        if (inspect().valid) return;
        event.preventDefault();
        update();
        input.focus();
      },
      { capture: true },
    );
    update();
  });
}

function showCommand(command) {
  const previousFocus = document.activeElement;
  const wrap = document.createElement("div");
  wrap.className = "modal-backdrop";
  wrap.innerHTML = `<section class="deploy-command-modal" role="dialog" aria-modal="true" aria-labelledby="deploy-command-title"><header class="deploy-command-head"><span class="deploy-command-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m7 8 4 4-4 4M13 16h4"/></svg></span><div><p class="eyebrow">Linux · systemd</p><h2 id="deploy-command-title">一键添加 QAgent 节点</h2><p>复制命令到目标服务器执行，即可完成安装和节点注册。</p></div><button class="deploy-command-close" type="button" data-close aria-label="关闭弹窗">×</button></header><div class="deploy-command-body"><div class="deploy-command-notice"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 5 6v5c0 4.6 2.8 8.1 7 10 4.2-1.9 7-5.4 7-10V6l-7-3Z"/><path d="m9.5 12 1.7 1.7 3.5-3.7"/></svg><span><b>添加节点命令仅显示一次</b><small>命令绑定当前节点，可重复安装；从添加记录中删除后立即失效。</small></span></div><section class="deploy-command-shell" aria-label="添加节点命令"><header><span><i></i>Terminal</span><small>root</small></header><div><span class="deploy-command-prompt" aria-hidden="true">$</span><textarea class="deploy-command-input" rows="5" readonly spellcheck="false" aria-label="添加节点命令" data-command>${esc(command)}</textarea></div></section></div><footer class="deploy-command-actions"><span>请在目标 Linux 服务器上以 root 权限执行</span><div><button class="button" type="button" data-close>关闭</button><button class="button primary deploy-command-copy" type="button" data-copy-command><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/></svg><span data-copy-label>复制添加命令</span></button></div></footer></section>`;
  document.body.append(wrap);
  const copyButton = wrap.querySelector("[data-copy-command]");
  const commandInput = wrap.querySelector("[data-command]");
  let resetCopyLabel;
  const close = () => {
    window.clearTimeout(resetCopyLabel);
    document.removeEventListener("keydown", onKeydown);
    wrap.remove();
    if (previousFocus instanceof HTMLElement && previousFocus.isConnected) {
      previousFocus.focus();
    }
  };
  const onKeydown = (event) => {
    if (event.key === "Escape") close();
  };
  wrap
    .querySelectorAll("[data-close]")
    .forEach((button) => (button.onclick = close));
  wrap.onclick = (event) => {
    if (event.target === wrap) close();
  };
  document.addEventListener("keydown", onKeydown);
  copyButton.onclick = async () => {
    try {
      await navigator.clipboard.writeText(command);
    } catch {
      commandInput.select();
      document.execCommand("copy");
      commandInput.setSelectionRange(0, 0);
    }
    const copyLabel = copyButton.querySelector("[data-copy-label]");
    copyButton.classList.add("copied");
    copyLabel.textContent = "已复制";
    window.clearTimeout(resetCopyLabel);
    resetCopyLabel = window.setTimeout(() => {
      copyButton.classList.remove("copied");
      copyLabel.textContent = "复制添加命令";
    }, 1800);
  };
  copyButton.focus();
}
  return { agents, submitTask, bindCodeEditors, showCommand };
}
