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
  const session = {
    role: limited || readonly ? "user" : "admin",
    user_id: limited || readonly ? "dashboard-user" : undefined,
    csrf_token: "browser-test-csrf",
    permissions: limited
      ? ["overview.read"]
      : readonly
        ? ["overview.read", "agents.read", "tasks.read", "traffic.read", "metrics.read", "settings.read", "agent-config.read"]
        : ["overview.read", "agents.read", "tasks.read", "tasks.execute", "traffic.read", "metrics.read", "panel-metrics.read", "core-logs.read", "certificates.read", "settings.read", "agent-config.read"],
  };
  setStorageAccount(session);
  accountStorage.setItem("qcontrolhub:node-card-order", JSON.stringify(["tokyo", "hongkong", "singapore", "frankfurt"]));

  const nowISO = new Date().toISOString();
  const agents = [
    { id: "hongkong", name: "香港 · HK-01", region_code: "HK", os: "Debian", arch: "amd64", status: "online", capabilities: ["xray", "sing-box"], public_ipv4: "203.0.113.10" },
    { id: "singapore", name: "新加坡 · SG-02", region_code: "SG", os: "Ubuntu", arch: "arm64", status: "online", capabilities: ["mihomo", "ss-rust"], public_ipv4: "198.51.100.20", public_ipv6: "2001:db8::20" },
    { id: "tokyo", name: "东京 · JP-03", region_code: "JP", os: "Debian", arch: "amd64", status: "online", capabilities: ["sing-box", "xray", "mihomo", "ss-rust"], public_ipv4: "192.0.2.30", clock_skew: true },
    { id: "frankfurt", name: "法兰克福 · DE-04", region_code: "DE", os: "Alpine", arch: "amd64", status: "offline", capabilities: ["sing-box"] },
  ].map((agent) => ({
    ...agent,
    last_seen: new Date(Date.now() - (agent.status === "offline" ? 12 * 60000 : 1000)).toISOString(),
    runtime: Object.fromEntries(agent.capabilities.map((engine, index) => [engine, { installed: true, service_status: index ? "inactive" : "active" }])),
    metrics: {
      cpu_available: agent.status === "online",
      cpu_percent: agent.status === "online" ? 18.4 : 0,
      memory_available: agent.status === "online",
      memory_used_bytes: 2.1 * GiB,
      memory_total_bytes: 4 * GiB,
      network_available: true,
      network_rx_bps: 2048,
      network_tx_bps: 1024,
      collected_at: nowISO,
      public_ipv4: agent.public_ipv4 || "",
      public_ipv6: agent.public_ipv6 || "",
      clock_sync: agent.clock_skew ? { status: "skewed", offset_ms: 4800 } : { status: "ok", offset_ms: 4 },
    },
    features: [],
    labels: {},
  }));

  const tasks = [
    { id: "task-1", agent_id: "hongkong", action: "deploy", engine: "xray", status: "succeeded" },
    { id: "task-2", agent_id: "singapore", action: "upgrade-agent", engine: "", status: "running" },
    { id: "task-3", agent_id: "tokyo", action: "restart", engine: "sing-box", status: "pending" },
    { id: "task-4", agent_id: "frankfurt", action: "install", engine: "sing-box", status: "failed" },
  ].map((task, index) => ({ ...task, created_at: new Date(Date.now() - (index + 1) * 60000).toISOString() }));

  const diagNodes = [
    { agent_id: "tokyo", instance_id: "main", name: "东京 · JP-03", region_code: "JP", online: true, capabilities: { ping: true, addresses: ["192.0.2.30"] } },
    { agent_id: "hongkong", instance_id: "main", name: "香港 · HK-01", region_code: "HK", online: true, capabilities: { ping: true, addresses: ["203.0.113.10"] } },
    { agent_id: "singapore", instance_id: "main", name: "新加坡 · SG-02", region_code: "SG", online: true, capabilities: { ping: true, addresses: ["198.51.100.20", "2001:db8::20"] } },
  ];

  const fixture = {
    calls: [],
    failed: mode === "dashboard-unavailable",
    gate: null,
    uiPrefs: { values: {}, revision: 1 },
    metrics: {
      cpu_available: true, cpu_percent: 23.6,
      memory_available: true, memory_used_bytes: 3.48 * GiB, memory_total_bytes: 8 * GiB,
      disk_available: true, disk_used_bytes: 26.4 * GiB, disk_total_bytes: 80 * GiB,
      network_available: true, network_rate_available: true, network_rx_bps: 1.6 * 1024 ** 2, network_tx_bps: 864 * 1024,
      network_rx_bytes: 68 * GiB, network_tx_bytes: 42 * GiB,
      panel_runtime: { sample_interval_seconds: 2, sample_window_count: 12, sample_window_capacity: 60 },
      history: [
        { collected_at: new Date(Date.now() - 4000).toISOString(), cpu_available: true, cpu_percent: 19.2, memory_available: true, memory_used_bytes: 3.3 * GiB, memory_total_bytes: 8 * GiB, network_available: true, network_rate_available: true, network_rx_bps: 1.1 * 1024 ** 2, network_tx_bps: 620 * 1024 },
        { collected_at: new Date(Date.now() - 2000).toISOString(), cpu_available: true, cpu_percent: 21.8, memory_available: true, memory_used_bytes: 3.4 * GiB, memory_total_bytes: 8 * GiB, network_available: true, network_rate_available: true, network_rx_bps: 1.4 * 1024 ** 2, network_tx_bps: 740 * 1024 },
      ],
    },
    probes: [
      {
        id: "prb_1",
        name: "东京 ↔ 香港专线",
        target_mode: "",
        node1: { agent_id: "tokyo", instance_id: "main", name: "东京 · JP-03", address: "192.0.2.30" },
        node2: { agent_id: "hongkong", instance_id: "main", name: "香港 · HK-01", address: "203.0.113.10" },
        node1_name: "东京 · JP-03",
        node1_region: "JP",
        node2_name: "香港 · HK-01",
        node2_region: "HK",
        bidirectional: true,
        parameters: { tool: "ping", protocol: "icmp", count: 5, interval_ms: 200, timeout_ms: 1000, size: 56 },
        auto_enabled: true,
        auto_interval_seconds: 60,
        revision: 1,
        availability: { ready: true, running: false },
        latest: {
          sampled_at: new Date(Date.now() - 5000).toISOString(),
          forward: { state: "succeeded", sent: 5, received: 5, loss_percent: 0, min_ms: 41.2, avg_ms: 43.8, max_ms: 46.1, stdev_ms: 1.8, reached: true },
          reverse: { state: "succeeded", sent: 5, received: 4, loss_percent: 20, min_ms: 42.0, avg_ms: 45.4, max_ms: 49.0, stdev_ms: 2.6, reached: true },
        },
        history: [
          { sampled_at: new Date(Date.now() - 65000).toISOString(), forward: { avg_ms: 42.1, loss_percent: 0, reached: true }, reverse: { avg_ms: 44.0, loss_percent: 0, reached: true } },
          { sampled_at: new Date(Date.now() - 5000).toISOString(), forward: { avg_ms: 43.8, loss_percent: 0, reached: true }, reverse: { avg_ms: 45.4, loss_percent: 20, reached: true } },
        ],
      },
      {
        id: "prb_2",
        name: "新加坡 → Cloudflare",
        target_mode: "ip",
        node1: { agent_id: "singapore", instance_id: "main", name: "新加坡 · SG-02", address: "198.51.100.20" },
        node2: { name: "1.1.1.1", address: "1.1.1.1" },
        node1_name: "新加坡 · SG-02",
        node1_region: "SG",
        bidirectional: false,
        parameters: { tool: "ping", protocol: "icmp", count: 5, interval_ms: 200, timeout_ms: 1000, size: 56 },
        auto_enabled: false,
        auto_interval_seconds: 60,
        revision: 2,
        availability: { ready: false, reason: "目标或源节点缺少 ping 能力" },
        latest: {
          sampled_at: new Date(Date.now() - 30000).toISOString(),
          forward: { state: "succeeded", sent: 5, received: 5, loss_percent: 0, min_ms: 2.1, avg_ms: 2.4, max_ms: 2.8, stdev_ms: 0.2, reached: true },
        },
        history: [],
      },
    ],
  };
  window.__dashboardBrowserTestAPI = fixture;
  const json = (value, status = 200) => new Response(JSON.stringify(value), {
    status, headers: { "Content-Type": "application/json" },
  });
  const canSession = (perm) => perm === "certificates.read" || session.role === "admin" || session.permissions.includes(perm);

  window.fetch = async (input, options = {}) => {
    const method = (options.method || (input instanceof Request ? input.method : "GET")).toUpperCase();
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    fixture.calls.push({ method, path, query: url.search });

    if (path === "/preferences/ui") {
      if (method === "PATCH") {
        const body = JSON.parse(options.body || "{}");
        fixture.uiPrefs = {
          values: { ...fixture.uiPrefs.values, ...(body.values || {}) },
          revision: fixture.uiPrefs.revision + 1,
        };
      }
      return json(fixture.uiPrefs);
    }
    if (path === "/preferences/node-order") {
      assert(canSession("agents.read"), "unauthorized node-order request");
      return json({ ids: JSON.parse(accountStorage.getItem("qcontrolhub:node-card-order") || "[]"), revision: 1 });
    }
    if (path === "/auth/session") return json(session);
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") return json({ agents: 4, agents_online: 3, node_configs: 12, configs: 2, tasks_pending: 2, tasks_queued: 1, tasks_running: 1, tasks_failed: 1 });
    if (path === "/settings") return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "relative", task_poll_interval_ms: 5000 });
    if (path === "/agents") {
      assert(canSession("agents.read"), "unauthorized /agents request");
      return json(agents.map((agent) => ({ ...agent, metrics: { ...agent.metrics, collected_at: new Date().toISOString() } })));
    }
    if (path === "/tasks") return json(tasks);
    if (path === "/panel-metrics") {
      if (fixture.denied) return json({ error: "permission denied" }, 403);
      assert(hasPanel, "a user without panel-metrics.read requested global host counters");
      const sample = { collected_at: new Date().toISOString(), ...fixture.metrics };
      if (fixture.gate) await fixture.gate;
      return fixture.failed ? json({ error: "test metrics temporarily unavailable" }, 503) : json(sample);
    }
    if (path === "/traffic-usage") {
      assert(canSession("traffic.read"), "unauthorized /traffic-usage request");
      if (fixture.trafficFailed) return json({ error: "test traffic temporarily unavailable" }, 503);
      const month = url.searchParams.get("month");
      const current = new Date().toISOString().slice(0, 7);
      const dayCount = month === current ? new Date().getUTCDate() : 28;
      if (fixture.trafficGate) await fixture.trafficGate;
      return json({
        month,
        timezone: "UTC",
        days: Array.from({ length: dayCount }, (_, index) => {
          const received = (1.2 + ((index * 7) % 11) * 0.43) * GiB * (fixture.trafficMultiplier || 1);
          const sent = (0.7 + ((index * 3) % 7) * 0.32) * GiB * (fixture.trafficMultiplier || 1);
          return {
            agent_id: agents[index % agents.length].id,
            day: `${month}-${String(index + 1).padStart(2, "0")}`,
            received_bytes: received,
            sent_bytes: sent,
            used_bytes: received + sent,
            peak_receive_bps: 3 * 1024 ** 2,
            peak_send_bps: 1.2 * 1024 ** 2,
          };
        }),
      });
    }
    if (path === "/traffic-policies") {
      assert(canSession("traffic.read"), "unauthorized /traffic-policies request");
      const ts = new Date().toISOString();
      return json({
        agents: agents.map((a) => ({ id: a.id, name: a.name, region_code: a.region_code, status: a.status, is_current_user: a.id !== "frankfurt" })),
        policies: [
          { agent_id: "tokyo", port: 443, received_bytes: 18 * GiB, sent_bytes: 9 * GiB, used_bytes: 27 * GiB, receive_bps: 650 * 1024, send_bps: 310 * 1024, limit_bytes: 40 * GiB, quota_enabled: true, auto_block: true, is_current_user: true, last_collected_at: ts },
          { agent_id: "hongkong", port: 8443, received_bytes: 12 * GiB, sent_bytes: 6 * GiB, used_bytes: 18 * GiB, receive_bps: 420 * 1024, send_bps: 180 * 1024, limit_bytes: 20 * GiB, quota_enabled: true, auto_block: true, is_current_user: true, last_collected_at: ts },
          { agent_id: "singapore", port: 443, received_bytes: 25 * GiB, sent_bytes: 10 * GiB, used_bytes: 35 * GiB, receive_bps: 0, send_bps: 0, limit_bytes: 30 * GiB, quota_enabled: true, auto_block: true, blocked: true, is_current_user: false, owner_name: "partner", last_collected_at: ts },
        ],
      });
    }
    if (path === "/dashboard/probes") {
      assert(canSession("agents.read") && canSession("tasks.read"), "unauthorized /dashboard/probes read");
      if (method === "GET") return json({ pairs: fixture.probes });
      if (method === "POST") {
        assert(canSession("tasks.execute"), "unauthorized probe create");
        const body = JSON.parse(options.body || "{}");
        const created = {
          id: `prb_${fixture.probes.length + 1}`,
          name: body.name || "新建探测对",
          target_mode: body.target_mode || "",
          node1: body.node1,
          node2: body.node2,
          bidirectional: Boolean(body.bidirectional),
          parameters: body.parameters,
          auto_enabled: Boolean(body.auto_enabled),
          auto_interval_seconds: body.auto_interval_seconds || 60,
          revision: 1,
          availability: { ready: true, running: false },
          latest: null,
          history: [],
        };
        fixture.probes = [created, ...fixture.probes];
        return json(created, 201);
      }
    }
    if (path.startsWith("/dashboard/probes/")) {
      assert(canSession("tasks.execute"), "unauthorized probe mutation");
      const segments = path.slice("/dashboard/probes/".length).split("/");
      const id = segments[0];
      const action = segments[1] || "";
      const target = fixture.probes.find((p) => p.id === id);
      if (!target) return json({ error: "not found" }, 404);
      if (action === "run" && method === "POST") {
        target.availability = { ready: true, running: true };
        target.active_state = "running";
        return json(target, 201);
      }
      if (action === "stop" && method === "POST") {
        target.availability = { ready: true, running: false };
        target.active_state = "";
        return json(target);
      }
      if (!action && method === "PUT") {
        const body = JSON.parse(options.body || "{}");
        Object.assign(target, { ...body, revision: (target.revision || 0) + 1 });
        return json(target);
      }
      if (!action && method === "DELETE") {
        fixture.probes = fixture.probes.filter((p) => p.id !== id);
        return new Response(null, { status: 204 });
      }
    }
    if (path === "/network-diagnostics/nodes") {
      assert(canSession("tasks.execute"), "unauthorized /network-diagnostics/nodes read");
      return json(diagNodes);
    }
    if (path === "/client-connections") {
      assert(canSession("core-logs.read"), "unauthorized /client-connections request");
      return json({
        ips: 18,
        flows: 142,
        sources: [{ agent_id: "tokyo", status: "ok" }, { agent_id: "hongkong", status: "ok" }],
        timeline: Array.from({ length: 12 }, (_, idx) => ({ time: new Date(Date.now() - (11 - idx) * 3600000).toISOString(), flows: idx * 5 + 3, ips: idx + 1 })),
      });
    }
    if (path === "/certificates") {
      assert(canSession("certificates.read"), "unauthorized /certificates request");
      if (limited) return json({ items: [] });
      return json({
        items: [
          { id: "crt_1", name: "edge.example.com", owned: true, status: "active", not_after: new Date(Date.now() + 5 * 86400000).toISOString() },
          { id: "crt_2", name: "ok.example.com", owned: true, status: "active", not_after: new Date(Date.now() + 90 * 86400000).toISOString() },
          { id: "crt_shared", name: "shared.example.com", owned: false, status: "active", not_after: new Date(Date.now() + 2 * 86400000).toISOString() },
        ],
      });
    }
    if (path === "/ip-quality") {
      assert(canSession("agents.read"), "unauthorized /ip-quality request");
      return json({ records: [{ agent_id: "tokyo", status: "succeeded" }, { agent_id: "hongkong", status: "succeeded" }, { agent_id: "singapore", status: "failed" }] });
    }
    if (path.startsWith("/metrics/")) {
      assert(canSession("metrics.read"), "unauthorized /metrics/{id} request");
      return json([
        { sampled_at: new Date(Date.now() - 3600000).toISOString(), cpu_percent: 12, memory_percent: 38.5, rx_rate_bps: 1024, tx_rate_bps: 512 },
        { sampled_at: new Date().toISOString(), cpu_percent: 18, memory_percent: 42.1, rx_rate_bps: 2048, tx_rate_bps: 1024 },
      ]);
    }
    throw new Error(`unexpected dashboard fixture request: ${method} ${path}`);
  };

  document.querySelector('link[href^="/assets/app.css"]')?.remove();
  location.hash = "#dashboard";
  const stylesheet = document.createElement("link");
  stylesheet.rel = "stylesheet";
  stylesheet.href = "/assets/analytics/theme.css";
  const styled = new Promise((resolve, reject) => { stylesheet.onload = resolve; stylesheet.onerror = reject; });
  document.head.append(stylesheet);
  await styled;
  (await import("./analytics/app.js")).startAnalytics({ app: document.querySelector("#app"), session });

  const card = (title) => [...document.querySelectorAll(".a-main section.a-card")].find((c) => c.querySelector("h2")?.textContent === title);
  const panel = () => card("面板运行资源");
  const traffic = () => card("流量概况");
  const ranking = () => card("流量累计排行");
  const probes = () => card("A/B 节点延迟与丢包");
  const nodes = () => card("节点运行状态");
  const activity = () => card("最近任务");
  const metric = (label) => panel()?.querySelector(`[data-refresh-key="${label}"] strong`);
  const cpu = () => metric("CPU 使用率");
  const reads = (path) => fixture.calls.filter((call) => call.path === path).length;
  const refreshButton = () => document.querySelector(".a-top-inner > .a-actions > button");
  const settled = () => !refreshButton()?.disabled;
  const refresh = async () => {
    await waitFor(settled, "refresh remained busy");
    const count = reads("/overview");
    refreshButton().click();
    await waitFor(() => reads("/overview") > count && settled(), "manual refresh did not settle");
  };

  await waitFor(() => document.title.startsWith("仪表盘") && reads("/overview"), "dashboard did not render");
  assert(!document.body.textContent.includes("undefined"), "missing overview fields leaked");

  if (!hasPanel) {
    assert(!panel(), "panel-host card leaked");
    assert(reads("/panel-metrics") === 0, "unauthorized host request");
    if (limited) {
      for (const path of ["/agents", "/tasks", "/traffic-usage", "/traffic-policies", "/dashboard/probes", "/network-diagnostics/nodes", "/client-connections", "/ip-quality", "/settings", "/preferences/node-order"]) {
        assert(reads(path) === 0, `overview-only user requested ${path}`);
      }
      assert(!nodes() && !activity() && !traffic() && !ranking() && !probes(), "inaccessible shortcuts exposed");
      await waitFor(() => document.querySelector(".a-main").textContent.includes("3/4") && document.querySelector(".a-main").textContent.includes("在线率 75%"), "overview-only user lost allowed summary");
    } else {
      assert(traffic() && ranking(), "readonly user lost own traffic");
      await waitFor(() => probes()?.querySelector('[data-refresh-key="prb_1"]'), "readonly user with agents.read+tasks.read should view probes");
      assert(![...probes().querySelectorAll("button")].some((b) => ["添加探测对", "立即探测", "删除"].includes(b.textContent)), "readonly user exposed probe write controls");
      assert(reads("/network-diagnostics/nodes") === 0, "readonly user fetched diagnostic node write metadata");
    }
    return;
  }

  await waitFor(() => (fixture.failed ? panel()?.textContent.includes("资源读取失败") : cpu()?.textContent === "23.6%"), "initial host metrics not settled");
  if (preview) return;
  if (fixture.failed) {
    assert(cpu().textContent === "—", "failed first sample became zero");
    assert(nodes() && traffic() && ranking() && probes(), "optional host failure blocked dashboard");
    fixture.failed = false;
    await refresh();
  }

  assert(cpu().textContent === "23.6%", "CPU usage differs from panel API");
  assert(metric("内存使用率").textContent === "43.5%" && metric("根目录磁盘").textContent === "33.0%", "capacity percentages differ from the panel API");
  assert(panel().querySelectorAll("svg.a-dash-spark").length >= 3, "panel sparklines missing despite multi-sample window");

  const summary = card("运行概况");
  await waitFor(() => summary?.querySelector('[data-refresh-key="运行内核"] strong')?.textContent === "3", "offline runtime counted as live or scoped installed cores lost");
  assert(summary?.querySelector('[data-refresh-key="全网实时吞吐"] strong')?.textContent !== "—", "valid fleet rates missing");
  assert(summary?.querySelector('a[href="#tasks?status=failed"]'), "failed task shortcut missing");

  await waitFor(() => traffic()?.querySelectorAll("path[data-series]").length === 2, "receive and send series are not independently plotted");
  const series = [...traffic().querySelectorAll("path[data-series]")];
  assert(series[0].getAttribute("stroke-dasharray") !== series[1].getAttribute("stroke-dasharray"), "traffic directions rely only on color");

  await waitFor(() => ranking()?.querySelector('[aria-label="节点流量排行"] a[href^="#traffic?agent="]'), "ranked nodes cannot open their own traffic");
  await waitFor(() => ranking().querySelectorAll('[aria-label="节点流量排行"] > [data-refresh-key]').length === 4, "ranking did not show authorized nodes under Top 5");
  const scopeSelect = ranking().querySelector('[name="traffic_rank_scope"]');
  assert(scopeSelect && !scopeSelect.closest("[hidden]"), "scope filter missing with shared owner traffic");
  ranking().querySelector('[name="traffic_rank_period"]').value = "cycle";
  ranking().querySelector('[name="traffic_rank_period"]').dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => ranking().textContent.includes("已封禁") && ranking().textContent.includes("当前周期"), "billing cycle view lost blocked quota state");
  scopeSelect.value = "current";
  scopeSelect.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => ranking().querySelectorAll('[aria-label="节点流量排行"] > [data-refresh-key]').length === 2, "current-user scope filter failed");
  scopeSelect.value = "all";
  scopeSelect.dispatchEvent(new Event("change", { bubbles: true }));
  ranking().querySelector('[name="traffic_rank_period"]').value = "month";
  ranking().querySelector('[name="traffic_rank_period"]').dispatchEvent(new Event("change", { bubbles: true }));

  await waitFor(() => nodes()?.querySelector(".a-dash-matrix-row a")?.textContent.includes("东京"), "saved node order ignored");
  assert(nodes().textContent.includes("时钟异常") && nodes().textContent.includes("IPv6"), "matrix omitted clock_sync or IPv6 capability badges");
  assert(!activity() && reads("/tasks") === 0, "retired recent-task area still renders or polls");

  await waitFor(() => probes()?.querySelector('[data-refresh-key="prb_1"]') && probes()?.textContent.includes("20%"), "A/B probes card did not render pair stats");
  assert(!panel().querySelector(".a-dash-chip-pill") && !/\d+\/\d+\s*样本|协程/.test(panel().textContent), "panel still renders removed sample/goroutine pills");
  const stableAddProbeBtn = [...probes().querySelectorAll("button")].find((b) => b.textContent === "添加探测对");
  assert(stableAddProbeBtn, "add A/B probe button missing for authorized operator");
  const runBtn = [...probes().querySelector('[data-refresh-key="prb_1"]').querySelectorAll("button")].find((b) => b.textContent === "立即探测");
  runBtn.click();
  await waitFor(() => probes()?.querySelector('[data-refresh-key="prb_1"]')?.textContent.includes("停止"), "run action did not enter running/stop state");
  const stopBtn = [...probes().querySelector('[data-refresh-key="prb_1"]').querySelectorAll("button")].find((b) => b.textContent === "停止");
  stopBtn.click();
  await waitFor(() => probes()?.querySelector('[data-refresh-key="prb_1"]')?.textContent.includes("立即探测"), "stop action did not settle");
  assert([...probes().querySelectorAll("button")].find((b) => b.textContent === "添加探测对") === stableAddProbeBtn, "conditional status badge replaced add probe button");

  // Add, toggle, and delete a custom A/B probe pair through the UI form & confirmation dialog.
  const addProbeBtn = [...probes().querySelectorAll("button")].find((b) => b.textContent === "添加探测对");
  assert(addProbeBtn, "add A/B probe button missing for authorized operator");
  addProbeBtn.click();
  const addDialog = document.querySelector("dialog[open]");
  assert(addDialog?.matches(":modal"), "add probe dialog missing");
  await waitFor(() => reads("/network-diagnostics/nodes") > 0 && addDialog.querySelector('select[name="node2_key"] option[value="singapore/main"]'), "diagnostic nodes not populated in dialog");
  addDialog.querySelector('input[name="name"]').value = "东京 → 新加坡测试";
  addDialog.querySelector('select[name="node2_key"]').value = "singapore/main";
  addDialog.querySelector('select[name="node2_key"]').dispatchEvent(new Event("change", { bubbles: true }));
  addDialog.querySelector('button[type="submit"]').click();
  await waitFor(() => !addDialog.open && probes().textContent.includes("东京 → 新加坡测试"), "created A/B probe pair not rendered");
  const createdCard = [...probes().querySelectorAll('[data-refresh-key]')].find((el) => el.textContent.includes("东京 → 新加坡测试"));
  [...createdCard.querySelectorAll("button")].find((b) => b.textContent === "暂停自动").click();
  await waitFor(() => createdCard.textContent.includes("开启自动"), "toggling auto probe state failed");
  [...createdCard.querySelectorAll("button")].find((b) => b.textContent === "删除").click();
  const delConfirm = document.querySelector("dialog[open]");
  [...delConfirm.querySelectorAll("button")].find((b) => b.textContent === "删除探测对").click();
  await waitFor(() => !probes().textContent.includes("东京 → 新加坡测试"), "deleted A/B probe pair stayed rendered");

  // Verify node telemetry matrix 24h dialog opens and renders metrics without interfering with polling.
  const trendBtn = [...nodes().querySelector(".a-dash-matrix-row").querySelectorAll("button")].find((b) => b.textContent === "24h 趋势");
  assert(trendBtn, "matrix 24h trend button missing");
  trendBtn.click();
  await waitFor(() => document.querySelector('dialog[open] [aria-label="节点历史指标"]') && document.querySelector("dialog[open] svg.a-chart"), "matrix 24h dialog did not render chart");
  document.querySelector("dialog[open]").close();
  await delay(60);

  await waitFor(() => card("入站连接脉冲 (24h)")?.querySelector("svg.a-dash-pulse-svg"), "connections pulse card missing");
  await waitFor(() => card("证书健康")?.textContent.includes("14 天内到期 1"), "certificates expiring count wrong or included unowned cert");
  await waitFor(() => card("IP 质量概况")?.textContent.includes("异常/失败"), "ip-quality summary card missing");

  const bands = () => [...traffic().querySelectorAll("[data-chart-day]")];
  const firstDay = bands()[0], firstDate = firstDay.dataset.chartDay;
  firstDay.scrollIntoView({ block: "center", behavior: "instant" });
  firstDay.dispatchEvent(new PointerEvent("pointerover", { bubbles: true, pointerType: "mouse" }));
  await waitFor(() => document.querySelector(".a-tooltip"), "daily traffic hover has no tooltip");
  const tip = document.querySelector(".a-tooltip");
  assert(tip.textContent.includes(firstDate) && tip.textContent.replace(/\s/g, "").includes("接收1.2GB") && tip.textContent.includes("发送") && tip.textContent.includes("合计"), "daily tooltip omits date, directions or actual usage");
  fixture.trafficMultiplier = 2;
  await refresh();
  await waitFor(() => tip.textContent.replace(/\s/g, "").includes("接收2.4GB"), "daily tooltip did not update its current sample");
  assert(document.querySelector(".a-tooltip") === tip && bands()[0] === firstDay, "poll recreated the hovered day or tooltip");
  fixture.trafficMultiplier = 1;
  await refresh();
  firstDay.focus();
  firstDay.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
  const keyboardDay = bands()[Math.min(1, bands().length - 1)];
  await waitFor(() => document.activeElement === keyboardDay && document.querySelector(".a-tooltip")?.textContent.includes(keyboardDay.dataset.chartDay), "keyboard cannot inspect adjacent daily traffic");
  firstDay.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerType: "touch" }));
  firstDay.dispatchEvent(new PointerEvent("pointerout", { bubbles: true, pointerType: "touch" }));
  assert(document.querySelector(".a-tooltip")?.textContent.includes(firstDate), "touch cannot inspect daily traffic");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert(!document.querySelector(".a-tooltip"), "Escape did not dismiss daily tooltip");

  const assertLayout = () => {
    assert(document.documentElement.scrollWidth <= innerWidth + 1, `viewport overflow ${innerWidth}`);
    for (const element of document.querySelectorAll(".a-card-body,.a-chart-container,.a-chart-meta,.a-dash-deck,.a-dash-subbar,.a-dash-cmd-grid,.a-dash-cmd-cell,.a-dash-dial-grid,.a-dash-dial-cell,.a-dash-host-io,.a-dash-traffic-ledger,.a-dash-ledger-cell,.a-dash-matrix,.a-dash-matrix-row,.a-dash-probe-grid,.a-dash-probe-lane,.a-dash-flow-row,.a-dash-rank-list,.a-dash-rank-headbar,.a-dash-rank-row,.a-dash-edge-grid,.a-dash-edge-stats,.a-dash-edge-viz")) {
      assert(element.scrollWidth <= element.clientWidth + 1, `content overflow ${element.className}`);
    }
    for (const element of document.querySelectorAll('[aria-label="节点运行状态"]')) {
      assert(element.scrollWidth <= element.clientWidth + 1, `dashboard summary needs horizontal scrolling ${element.getAttribute("aria-label")}`);
    }
    if (panel()) {
      assert(panel().querySelectorAll(".a-dash-dial-svg").length === 3, "panel dial gauges missing");
    }
    const statSummary = traffic().querySelector('[data-refresh-key="月累计流量"]');
    assert(Math.abs(statSummary.querySelector(".a-dash-ledger-head").getBoundingClientRect().left - statSummary.querySelector(".a-dash-ledger-duplex").getBoundingClientRect().left) < 1, "monthly traffic and direction counters are not aligned");
    assert(Math.abs(statSummary.querySelector(".a-dash-ledger-head").getBoundingClientRect().left - traffic().querySelector("h2").getBoundingClientRect().left) < 1, "traffic summary ignores the card content edge");
    assert(Math.abs(traffic().querySelector("[name=traffic_month]").getBoundingClientRect().bottom - [...traffic().querySelectorAll("button")].find((b) => b.textContent === "流量明细").getBoundingClientRect().bottom) < 1, "month and detail controls are not aligned");
  };
  for (const scale of [90, 100, 110, 135]) {
    document.documentElement.style.fontSize = `${scale}%`;
    await delay(80);
    assertLayout();
  }
  document.documentElement.style.fontSize = "100%";
  await delay(80);

  const scrollHost = document.scrollingElement || document.documentElement;
  scrollHost.scrollTop = 200;
  const beforePageScroll = scrollHost.scrollTop;
  const stablePanel = panel(), stableCPU = cpu(), stableNode = nodes().querySelector(".a-dash-matrix-row"), stableSVG = traffic().querySelector("svg.a-chart");
  const stableNodeAction = nodes().querySelector(".a-dash-matrix-row button, .a-dash-matrix-row a");
  const stableProbesSVG = probes().querySelector("svg"), stableRankRow = ranking().querySelector(".a-dash-rank-row");
  const stablePanelSVGs = [...panel().querySelectorAll("svg")];
  const stablePanelGrads = [...panel().querySelectorAll("linearGradient")].map((g) => g.id);
  const stableProbeGrads = [...probes().querySelectorAll("linearGradient")].map((g) => g.id);
  await refresh();
  assert(scrollHost.scrollTop === beforePageScroll, "unchanged refresh disturbed page scroll");
  assert(stablePanelSVGs.length > 0 && [...panel().querySelectorAll("svg")].every((svg, idx) => svg === stablePanelSVGs[idx]), "unchanged refresh rebuilt panel SVG");
  assert(JSON.stringify([...panel().querySelectorAll("linearGradient")].map((g) => g.id)) === JSON.stringify(stablePanelGrads), "panel gradient IDs churned across refresh");
  assert(JSON.stringify([...probes().querySelectorAll("linearGradient")].map((g) => g.id)) === JSON.stringify(stableProbeGrads), "probe gradient IDs churned across refresh");
  assert(probes().querySelector("svg") === stableProbesSVG && ranking().querySelector(".a-dash-rank-row") === stableRankRow && nodes().querySelector(".a-dash-matrix-row button, .a-dash-matrix-row a") === stableNodeAction, "unchanged refresh replaced card DOM nodes");
  const month = () => traffic().querySelector('[name="traffic_month"]');
  month().focus();
  fixture.metrics.history = [];
  fixture.metrics.cpu_percent = 92;
  await refresh();
  assert(cpu().textContent === "92.0%", "manual metrics refresh failed");
  assert(panel() === stablePanel && cpu() === stableCPU, "refresh replaced metric nodes");
  assert(document.activeElement === month(), "refresh stole filter focus");
  [...traffic().querySelectorAll("button")].find((button) => button.textContent === "流量明细").click();
  const dialog = document.querySelector("dialog[open]");
  assert(dialog?.matches(":modal"), "traffic detail modal missing");
  const detailTable = dialog.querySelector('[aria-label="每日流量明细"]');
  assert(detailTable, "traffic daily data missing");
  const scroll = dialog.querySelector(".a-dialog-body");
  scroll.scrollTop = 150;
  const beforeScroll = scroll.scrollTop;
  fixture.metrics.cpu_percent = 41.2;
  await waitFor(() => cpu()?.textContent === "41.2%", "automatic metrics refresh failed");
  assert(dialog.matches(":modal") && scroll.scrollTop === beforeScroll, "poll disturbed detail modal");
  assert(nodes().querySelector(".a-dash-matrix-row") === stableNode && traffic().querySelector("svg.a-chart") === stableSVG, "poll rebuilt unchanged business content");
  dialog.close();
  fixture.failed = true;
  await refresh();
  assert(panel().textContent.includes("刷新失败") && cpu().textContent === "41.2%", "failed refresh discarded metrics");
  fixture.failed = false;
  fixture.metrics.cpu_percent = 0;
  fixture.metrics.memory_available = false;
  fixture.metrics.network_available = false;
  await refresh();
  assert(cpu().textContent === "0.0%", "idle CPU became unavailable");
  assert(metric("内存使用率").textContent === "—", "unavailable memory became zero");
  assert(metric("网络接收速率").textContent === "—", "unavailable network became zero");
  fixture.metrics.memory_available = true;
  const memoryTotal = fixture.metrics.memory_total_bytes;
  for (const total of [0, -1, null, "10"]) {
    fixture.metrics.memory_total_bytes = total;
    await refresh();
    assert(metric("内存使用率").textContent === "—", "invalid memory capacity was displayed as a valid percentage");
  }
  fixture.metrics.memory_total_bytes = memoryTotal;
  fixture.metrics.memory_available = false;
  fixture.metrics.network_available = true;
  fixture.metrics.network_rx_bps = null;
  await refresh();
  assert(metric("网络接收速率").textContent === "—", "invalid network sample became a real rate");
  fixture.metrics.network_rx_bps = "<img src=x onerror=alert(1)>";
  await refresh();
  assert(metric("网络接收速率").textContent === "—" && !panel().querySelector("img"), "untrusted metric became HTML or a real sample");
  fixture.metrics.network_rx_bps = 2048;
  fixture.metrics.network_tx_bps = 512;
  await refresh();
  assert(metric("网络接收速率").textContent.replace(/\s/g, "") === "2KB/s" && panel().textContent.includes("容器可见"), "network recovery or container scope lost");
  fixture.metrics.collected_at = "invalid";
  await refresh();
  assert(panel().textContent.includes("尚无有效采样"), "invalid timestamp presented a fresh sample");
  assert(!/NaN|undefined|Infinity/.test(panel().textContent), "invalid metric text leaked");

  fixture.metrics.collected_at = new Date(Date.now() - 60000).toISOString();
  await refresh();
  assert(panel().textContent.includes("采样已过期"), "stale sample presented live");
  delete fixture.metrics.collected_at;
  fixture.denied = true;
  await refresh();
  assert(cpu().textContent === "—" && panel().textContent.includes("权限"), "denied host read retained protected data");
  fixture.denied = false;
  await refresh();
  assert(cpu().textContent === "0.0%", "manual refresh did not recover host metrics after 403 cleared");

  const chooseMonth = (value) => {
    month().value = value;
    month().dispatchEvent(new Event("change", { bubbles: true }));
  };
  const period = () => traffic().querySelector(".a-chart-meta").textContent;
  const oldMonth = month().value;
  assert(traffic().textContent.includes("UTC 今日"), "current-month total lost UTC day semantics");
  fixture.trafficFailed = true;
  const oldReads = reads("/traffic-usage");
  chooseMonth(`${new Date().getUTCFullYear() - 1}-01`);
  await waitFor(() => reads("/traffic-usage") > oldReads && traffic().textContent.includes("刷新失败"), "failed month request not shown");
  assert(period().includes(oldMonth), "old chart attributed to failed month");
  fixture.metrics.cpu_percent = 37;
  await waitFor(() => cpu()?.textContent === "37.0%", "failed month stopped host polling");
  fixture.trafficFailed = false;
  const february = `${new Date().getUTCFullYear() - 1}-02`;
  chooseMonth(february);
  assert(!traffic().textContent.includes("读取中"), "month change flashed loading text over ready chart");
  await waitFor(() => period().includes(february), "February not loaded");
  const days = new Date(Date.UTC(new Date().getUTCFullYear() - 1, 2, 0)).getUTCDate();
  assert(traffic().textContent.includes(`· ${days} 天`), "historical average uses incorrect day count");
  assert(!traffic().textContent.includes("UTC 今日"), "historical month claims current day");
  assert(traffic().querySelector("svg.a-chart [d]") && traffic().querySelector("svg.a-chart").textContent.includes(`02-${days}`), "February axis lost final day");
  assertLayout();

  if (mode === "dashboard") {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
    const hidden = reads("/panel-metrics");
    await delay(5200);
    assert(reads("/panel-metrics") === hidden, "hidden dashboard polled");
    delete document.hidden;
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => reads("/panel-metrics") > hidden, "visible dashboard did not refresh");
  }

  // A pending optional resource cannot delay node/traffic refreshes or page navigation.
  let release;
  fixture.gate = new Promise((resolve) => { release = resolve; });
  const beforeGate = reads("/panel-metrics");
  refreshButton().click();
  await waitFor(() => reads("/panel-metrics") > beforeGate, "slow metrics read missing");
  const beforeNodes = reads("/agents");
  await delay(5300);
  assert(reads("/agents") > beforeNodes, "slow host read blocked node polling");
  location.hash = "#tasks";
  await waitFor(() => document.title.startsWith("任务"), "route departure blocked on metrics");
  const afterLeave = reads("/panel-metrics");
  fixture.gate = null;
  release();
  await delay(2200);
  assert(!panel() && reads("/panel-metrics") === afterLeave, "old metrics response/timer survived navigation");
  assert(cspViolations.length === 0, `CSP violations: ${cspViolations.join(", ")}`);
}
