export function installCoreLogs(ctx) {
  const { api, state, engines, can, esc, engineName, date, shell } = ctx;

  const levelName = (value) =>
    ({ debug: "调试", info: "信息", warning: "警告", error: "错误", critical: "严重" })[value] || value;

  async function coreLogs() {
    const filters = state.data.coreLogFilters || {};
    const params = new URLSearchParams();
    if (filters.agent_id) params.set("agent_id", filters.agent_id);
    if (filters.engine) params.set("engine", filters.engine);
    if (filters.level) params.set("level", filters.level);
    if (filters.q) params.set("q", filters.q);
    params.set("limit", String(filters.limit || 200));
    const [entries, agents] = await Promise.all([
      api(`/core-logs?${params}`),
      can("agents.read") ? api("/agents") : Promise.resolve([]),
    ]);
    state.data.coreLogs = entries;
    state.data.agents = agents;

    const agentsByID = new Map(agents.map((agent) => [agent.id, agent]));
    const errorCount = entries.filter((entry) => ["error", "critical"].includes(entry.level)).length;
    const warningCount = entries.filter((entry) => entry.level === "warning").length;
    const rows = entries
      .map((entry) => {
        const agent = agentsByID.get(entry.agent_id);
        return `<article class="core-log-row level-${esc(entry.level)}"><time datetime="${esc(entry.logged_at)}">${esc(date(entry.logged_at))}</time><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="core-log-level">${esc(levelName(entry.level))}</span><span class="core-log-agent" title="${esc(agent?.name || entry.agent_id)}">${esc(agent?.name || entry.agent_id)}</span><pre>${esc(entry.message)}</pre></article>`;
      })
      .join("");

    shell(
      `<div class="core-log-workspace" data-core-log-page><header class="core-log-header"><div><p class="eyebrow">Runtime logs</p><h2>内核日志</h2></div><dl><div><dt>当前结果</dt><dd>${entries.length}</dd></div><div><dt>警告</dt><dd>${warningCount}</dd></div><div class="${errorCount ? "bad" : ""}"><dt>错误</dt><dd>${errorCount}</dd></div></dl></header><form class="core-log-filters" id="core-log-filters"><label>节点<select name="agent_id"><option value="">全部节点</option>${agents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select></label><label>内核<select name="engine"><option value="">全部内核</option>${engines.map((engine) => `<option value="${esc(engine)}">${esc(engineName(engine))}</option>`).join("")}</select></label><label>级别<select name="level"><option value="">全部级别</option>${["debug", "info", "warning", "error", "critical"].map((level) => `<option value="${level}">${levelName(level)}</option>`).join("")}</select></label><label class="core-log-search">关键词<input name="q" type="search" maxlength="120" value="${esc(filters.q || "")}" placeholder="搜索日志内容"></label><label>数量<select name="limit">${[100, 200, 500].map((limit) => `<option value="${limit}">${limit} 条</option>`).join("")}</select></label><button class="button primary" type="submit">应用</button><button class="button" type="button" data-reset-core-logs>重置</button></form><div class="core-log-status" role="status"><span><i></i>自动更新</span><span>面板保留 7 天</span></div><section class="core-log-stream" aria-label="内核运行日志">${rows || '<div class="core-log-empty"><strong>暂无日志</strong><span>等待已安装内核产生新的运行记录。</span></div>'}</section></div>`,
      "内核日志",
    );

    const form = document.querySelector("#core-log-filters");
    if (form) {
      form.elements.agent_id.value = filters.agent_id || "";
      form.elements.engine.value = filters.engine || "";
      form.elements.level.value = filters.level || "";
      form.elements.limit.value = String(filters.limit || 200);
      form.onsubmit = async (event) => {
        event.preventDefault();
        const values = new FormData(form);
        state.data.coreLogFilters = {
          agent_id: values.get("agent_id") || "",
          engine: values.get("engine") || "",
          level: values.get("level") || "",
          q: String(values.get("q") || "").trim(),
          limit: Number(values.get("limit") || 200),
        };
        await coreLogs();
      };
    }
    document.querySelector("[data-reset-core-logs]")?.addEventListener("click", async () => {
      state.data.coreLogFilters = {};
      await coreLogs();
    });
    document.querySelectorAll("[data-core-log-agent]").forEach((link) => {
      link.addEventListener("click", async (event) => {
        event.preventDefault();
        state.data.coreLogFilters = { ...(state.data.coreLogFilters || {}), agent_id: link.dataset.coreLogAgent || "" };
        await coreLogs();
      });
    });

    const schedulePoll = () => {
      clearTimeout(state.coreLogPollTimer);
      if (state.route !== "core-logs") return;
      state.coreLogPollTimer = setTimeout(() => {
        if (document.activeElement?.closest?.("#core-log-filters")) {
          schedulePoll();
          return;
        }
        coreLogs();
      }, 10000);
    };
    schedulePoll();
  }

  return coreLogs;
}
