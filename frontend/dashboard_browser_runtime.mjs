import { accountStorage, setStorageAccount } from "./modules/account-storage.js";

const assert = (condition, message) => {
  if (!condition) throw new Error(message);
};
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const waitFor = async (test, message) => {
  const deadline = performance.now() + 7000;
  while (!test()) {
    if (performance.now() > deadline) throw new Error(message);
    await delay(25);
  }
};
const GiB = 1024 ** 3;

export async function testDashboardRuntime(mode, preview = false) {
  const cspViolations = [];
  document.addEventListener("securitypolicyviolation", (event) => cspViolations.push(event.effectiveDirective));
  const limited = mode === "dashboard-limited";
  const readonly = mode === "dashboard-readonly";
  const hasPanel = !limited && !readonly;
  const session = { role: limited || readonly ? "user" : "admin", user_id: limited || readonly ? "dashboard-user" : undefined, csrf_token: "browser-test-csrf",
    permissions: limited ? ["overview.read"] : ["overview.read", "agents.read", "tasks.read", "traffic.read", "metrics.read", "settings.read", "agent-config.read"] };
  setStorageAccount(session);
  accountStorage.setItem("qcontrolhub:node-card-order", JSON.stringify(["tokyo", "hongkong", "singapore", "frankfurt"]));
  const agents = [
    { id: "hongkong", name: "香港 · HK-01", os: "Debian", arch: "amd64", status: "online", capabilities: ["xray", "sing-box"] },
    { id: "singapore", name: "新加坡 · SG-02", os: "Ubuntu", arch: "arm64", status: "online", capabilities: ["mihomo", "ss-rust"] },
    { id: "tokyo", name: "东京 · JP-03", os: "Debian", arch: "amd64", status: "online", capabilities: ["sing-box", "xray", "mihomo", "ss-rust"] },
    { id: "frankfurt", name: "法兰克福 · DE-04", os: "Alpine", arch: "amd64", status: "offline", capabilities: ["sing-box"] },
  ].map((agent) => ({ ...agent, last_seen: new Date(Date.now() - (agent.status === "offline" ? 12 * 60000 : 1000)).toISOString(), runtime: {}, metrics: {}, features: [], labels: {} }));
  const tasks = [
    { id: "task-1", agent_id: "hongkong", action: "deploy", engine: "xray", status: "succeeded" },
    { id: "task-2", agent_id: "singapore", action: "upgrade-agent", engine: "", status: "running" },
    { id: "task-3", agent_id: "tokyo", action: "restart", engine: "sing-box", status: "pending" },
    { id: "task-4", agent_id: "frankfurt", action: "install", engine: "sing-box", status: "failed" },
  ].map((task, index) => ({ ...task, created_at: new Date(Date.now() - (index + 1) * 60000).toISOString() }));
  const fixture = {
    calls: [],
    failed: mode === "dashboard-unavailable",
    gate: null,
    metrics: {
      cpu_available: true, cpu_percent: 23.6,
      memory_available: true, memory_used_bytes: 3.48 * GiB, memory_total_bytes: 8 * GiB,
      disk_available: true, disk_used_bytes: 26.4 * GiB, disk_total_bytes: 80 * GiB,
      network_available: true, network_rx_bps: 1.6 * 1024 ** 2, network_tx_bps: 864 * 1024,
      network_rx_bytes: 68 * GiB, network_tx_bytes: 42 * GiB,
    },
  };
  window.__dashboardBrowserTestAPI = fixture;
  const json = (value, status = 200) => new Response(JSON.stringify(value), {
    status, headers: { "Content-Type": "application/json" },
  });
  window.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    fixture.calls.push({ path, query: url.search });
    if (path === "/auth/session") return json(session);
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") return json({ agents: 4, agents_online: 3, node_configs: 12, configs: 2, tasks_pending: 2, tasks_queued: 1, tasks_running: 1, tasks_failed: 1 });
    if (path === "/settings") return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "relative", task_poll_interval_ms: 5000 });
    if (path === "/agents") return json(agents);
    if (path === "/tasks") return json(tasks);
    if (path === "/panel-metrics") {
      assert(hasPanel, "a user without panel-metrics.read requested global host counters");
      // Deliberately allow a late response after abort: the view must reject
      // it by route ownership even when a transport cannot cancel its work.
      const sample = { collected_at: new Date().toISOString(), ...fixture.metrics };
      if (fixture.gate) await fixture.gate;
      return fixture.failed ? json({ error: "test metrics temporarily unavailable" }, 503) : json(sample);
    }
    if (path === "/traffic-usage") {
      if (fixture.trafficFailed) return json({ error: "test traffic temporarily unavailable" }, 503);
      const month = url.searchParams.get("month");
      const current = new Date().toISOString().slice(0, 7);
      const dayCount = month === current ? new Date().getUTCDate() : 28;
      return json({ month, timezone: "UTC", days: Array.from({ length: dayCount }, (_, index) => {
        const received = (1.2 + ((index * 7) % 11) * .43) * GiB;
        const sent = (0.7 + ((index * 3) % 7) * .32) * GiB;
        return { day: `${month}-${String(index + 1).padStart(2, "0")}`, received_bytes: received, sent_bytes: sent, used_bytes: received + sent, peak_receive_bps: 3 * 1024 ** 2, peak_send_bps: 1.2 * 1024 ** 2 };
      }) });
    }
    throw new Error(`unexpected dashboard fixture request: ${path}`);
  };
  location.hash = "#dashboard";
  await import("./app.js");
  await waitFor(() => document.querySelector(".dashboard-workspace"), "dashboard did not render");
  const panel = () => document.querySelector("#panel-host");
  const cpu = () => panel()?.querySelector('[data-panel-metric="cpu"] [data-panel-value]');
  const cpuFill = () => {
    const track = panel().querySelector('[data-panel-metric="cpu"] .panel-metric-track');
    return track.firstElementChild.getBoundingClientRect().width / track.getBoundingClientRect().width * 100;
  };
  const reads = (path) => fixture.calls.filter((call) => call.path === path).length;
  assert(!document.body.textContent.includes("undefined"), "missing overview fields leaked into the page");
  if (!limited) {
    const nodeLink = document.querySelector('.dock-nav a[href="#node-settings"]');
    assert(nodeLink?.title === "节点", "node navigation tooltip must use the short label");
    assert(nodeLink.querySelector(".dock-label")?.textContent === "节点", "desktop and mobile node navigation labels must match");
  }
  if (!hasPanel) {
    assert(!panel(), "global panel-host card leaked to an unprivileged user");
    assert(reads("/panel-metrics") === 0, "unprivileged dashboard polled panel metrics");
    assert(!document.querySelector('.context-menu a[href="#panel-host"]'), "unprivileged sidebar exposed panel metrics");
    if (limited) {
      for (const path of ["/agents", "/tasks", "/traffic-usage", "/settings"])
        assert(reads(path) === 0, `overview-only user requested ${path}`);
      assert(!document.querySelector(".ops-stats a, #fleet, #activity"), "overview-only dashboard exposed inaccessible shortcuts");
    } else {
      assert(document.querySelector("#traffic-usage"), "readonly users lost their own traffic view");
    }
    return;
  }
  await waitFor(() => fixture.failed ? panel()?.textContent.includes("暂时无法读取") : cpu()?.textContent === "23.6%", "initial panel metrics did not settle");
  if (preview) return;
  if (fixture.failed) {
    assert(cpu().textContent === "—", "failed first sample was rendered as zero utilization");
    assert(document.querySelector("#fleet") && document.querySelector("#traffic-usage"), "optional host failure prevented the rest of the dashboard");
    fixture.failed = false;
    panel().querySelector("[data-panel-metrics-refresh]").click();
    await waitFor(() => cpu()?.textContent === "23.6%", "first-sample failure did not recover");
  }
  await waitFor(() => Math.abs(cpuFill() - 23.6) < 0.2, "CPU progress bar ignored its value under the production CSP");
  assert(document.querySelector("[data-dashboard-agent]")?.dataset.dashboardAgent === "tokyo", "dashboard ignored the saved node order");
  assert(document.querySelector(".recent-tasks").textContent.includes("香港 · HK-01"), "recent tasks did not show recognizable node names");
  const assertLayout = async () => {
    await delay(80);
    assert(document.documentElement.scrollWidth <= innerWidth + 1, `dashboard overflowed the ${innerWidth}px viewport`);
    for (const element of document.querySelectorAll(".dashboard-workspace, .dashboard-stat, .panel-metric, .panel-metrics-grid, .dashboard-traffic-head, .dashboard-traffic-summary, .dashboard-columns, .fleet-overview-list>a, .recent-tasks>div>a")) {
      assert(element.scrollWidth <= element.clientWidth + 1, `dashboard content overflow: ${element.className}`);
    }
    for (const value of document.querySelectorAll(".dashboard-stat strong, .dashboard-stat small")) {
      assert(value.getBoundingClientRect().height <= parseFloat(getComputedStyle(value).fontSize) * 2, `dashboard statistic wrapped instead of keeping one line: ${value.textContent}`);
    }
    for (const icon of document.querySelectorAll(".dashboard-stat .stat-icon")) {
      assert(icon.getClientRects().length > 0, "legacy mobile styles hid a statistic icon and collapsed its text column");
      assert(icon.getBoundingClientRect().right + 8 <= icon.nextElementSibling.getBoundingClientRect().left, "a legacy hero icon overlapped the compact statistic text");
    }
  };
  const root = document.documentElement;
  for (const theme of ["light", "dark"]) {
    root.dataset.theme = theme;
    for (const scale of ["0.9", "1", "1.1", "1.35"]) {
      root.style.setProperty("--ui-font-scale", scale);
      await assertLayout();
    }
  }
  root.style.setProperty("--ui-font-scale", "1");
  root.dataset.theme = "light";
  const stablePanel = panel(), stableCPU = cpu();
  const stableFleet = document.querySelector("[data-dashboard-agent]");
  const stableChart = document.querySelector(".dashboard-traffic-chart svg");
  const baselineReads = Object.fromEntries(["/overview", "/agents", "/tasks", "/traffic-usage"].map((path) => [path, reads(path)]));
  const picker = document.querySelector("[data-dashboard-traffic-month]");
  picker.querySelector("summary").click();
  fixture.metrics.cpu_percent = 92;
  panel().querySelector("[data-panel-metrics-refresh]").click();
  await waitFor(() => cpu()?.textContent === "92.0%", "manual panel refresh did not update");
  await waitFor(() => Math.abs(cpuFill() - 92) < 0.2, "CPU progress bar did not refresh under the production CSP");
  assert(panel() === stablePanel && cpu() === stableCPU, "host refresh replaced stable metric elements");
  assert(picker.open, "host refresh closed the active month picker");
  assert(panel().querySelector('[data-panel-metric="cpu"]').classList.contains("high"), "high resource usage is not distinguished");
  picker.open = false;
  const detailButton = document.querySelector("[data-dashboard-traffic-details]");
  detailButton.click();
  const dialog = document.querySelector("[data-dashboard-traffic-dialog]");
  assert(dialog.matches(":modal"), "traffic detail button did not open a modal");
  const scrollRegion = dialog.querySelector(".dashboard-traffic-detail-body");
  scrollRegion.scrollTop = 150;
  const scrollBefore = scrollRegion.scrollTop;
  fixture.metrics.cpu_percent = 41.2;
  await waitFor(() => cpu()?.textContent === "41.2%", "automatic panel metrics refresh did not run");
  assert(dialog.matches(":modal") && scrollRegion.scrollTop === scrollBefore, "polling disturbed the traffic detail dialog");
  assert(document.querySelector("[data-dashboard-agent]") === stableFleet && document.querySelector(".dashboard-traffic-chart svg") === stableChart, "host polling re-rendered unrelated dashboard sections");
  for (const [path, count] of Object.entries(baselineReads))
    assert(reads(path) === count, `host polling queried ${path} again`);
  dialog.querySelector("[data-dashboard-traffic-close]").click();
  fixture.failed = true;
  panel().querySelector("[data-panel-metrics-refresh]").click();
  await waitFor(() => panel()?.textContent.includes("刷新失败 · 保留上次数据"), "failed refresh did not mark cached data");
  assert(cpu().textContent === "41.2%", "failed refresh cleared usable counters");
  fixture.failed = false;
  fixture.metrics.cpu_percent = 0;
  fixture.metrics.memory_available = false;
  fixture.metrics.network_available = false;
  panel().querySelector("[data-panel-metrics-refresh]").click();
  await waitFor(() => cpu()?.textContent === "0.0%", "idle CPU did not recover as a real zero");
  await waitFor(() => cpuFill() === 0, "idle CPU progress bar did not clear");
  assert(panel().querySelector('[data-panel-metric="memory"] [data-panel-value]').textContent === "—", "missing memory sample was rendered as zero");
  assert(panel().querySelector("[data-panel-rx]").textContent === "—", "missing network sample was rendered as zero");
  fixture.metrics.collected_at = new Date(Date.now() - 60000).toISOString();
  panel().querySelector("[data-panel-metrics-refresh]").click();
  await waitFor(() => panel()?.textContent.includes("数据已过期"), "stale backend sample was presented as live");
  delete fixture.metrics.collected_at;

  // A failed month change must leave both the existing chart and its
  // independently running host poller usable.
  fixture.trafficFailed = true;
  const activePicker = document.querySelector("[data-dashboard-traffic-month]");
  activePicker.querySelector("summary").click();
  activePicker.querySelector('[data-dashboard-month-year-shift="-1"]').click();
  const beforeFailedMonth = reads("/traffic-usage");
  activePicker.querySelector('[data-dashboard-month-option][data-month-index="1"]').click();
  await waitFor(() => reads("/traffic-usage") > beforeFailedMonth, "failed month request did not run");
  fixture.metrics.cpu_percent = 37;
  await waitFor(() => cpu()?.textContent === "37.0%", "failed month selection stopped host polling");
  fixture.trafficFailed = false;

  // Pick February to catch the former hardcoded 31-column chart axis.
  picker.querySelector("summary").click();
  picker.querySelector('[data-dashboard-month-option][data-month-index="2"]').click();
  const februaryDays = new Date(Date.UTC(new Date().getUTCFullYear() - 1, 2, 0)).getUTCDate();
  await waitFor(() => document.querySelector(".dashboard-traffic-axis")?.children.length === februaryDays, "February did not use its natural day count");
  assert(getComputedStyle(document.querySelector(".dashboard-traffic-axis")).gridTemplateColumns.split(" ").length === februaryDays, "February axis still reserves 31 columns");

  if (mode === "dashboard") {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
    const beforeHidden = reads("/panel-metrics");
    await delay(2200);
    assert(reads("/panel-metrics") === beforeHidden, "hidden dashboard continued polling");
    delete document.hidden;
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => reads("/panel-metrics") > beforeHidden, "visible dashboard did not resume polling");
  }

  assert(cspViolations.length === 0, `dashboard violated the production CSP: ${cspViolations.join(", ")}`);
  let release;
  fixture.gate = new Promise((resolve) => { release = resolve; });
  const beforeGate = reads("/panel-metrics");
  panel().querySelector("[data-panel-metrics-refresh]").click();
  await waitFor(() => reads("/panel-metrics") > beforeGate, "slow host request did not start");
  location.hash = "#tasks";
  await waitFor(() => document.body.classList.contains("page-tasks"), "dashboard navigation did not finish");
  const afterLeave = reads("/panel-metrics");
  fixture.gate = null;
  release();
  await delay(2200);
  assert(!panel() && reads("/panel-metrics") === afterLeave, "late host response or timer survived route departure");
}
