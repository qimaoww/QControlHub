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
const compact = value => String(value || "").replace(/\s/g, "");

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
  const longTokyoName = "东京 · JP-03 · 超长节点名称用于验证排行与节点列表在窄屏下安全换行而不会挤破卡片布局";
  const agents = [
    { id: "hongkong", name: "香港 · HK-01", os: "Debian", arch: "amd64", status: "online", capabilities: ["xray", "sing-box"] },
    { id: "singapore", name: "新加坡 · SG-02", os: "Ubuntu", arch: "arm64", status: "online", capabilities: ["mihomo", "ss-rust"] },
    { id: "tokyo", name: longTokyoName, os: "Debian", arch: "amd64", status: "online", capabilities: ["sing-box", "xray", "mihomo", "ss-rust"] },
    { id: "frankfurt", name: "法兰克福 · DE-04", os: "Alpine", arch: "amd64", status: "offline", capabilities: ["sing-box"] },
  ].map((agent) => ({ ...agent, last_seen: new Date(Date.now() - (agent.status === "offline" ? 12 * 60000 : 1000)).toISOString(),
    runtime: Object.fromEntries(agent.capabilities.map((engine, index) => [engine, { installed: true, service_status: index ? "inactive" : "active" }])),
    metrics: { network_available: true, network_rate_available: true, network_rx_bps: 2048, network_tx_bps: 1024 }, features: [], labels: {} }));
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
    trafficGate: null,
    periods: null,
    reverseRanking: false,
    metrics: {
      cpu_available: true, cpu_percent: 23.6,
      memory_available: true, memory_used_bytes: 3.48 * GiB, memory_total_bytes: 8 * GiB,
      disk_available: true, disk_used_bytes: 26.4 * GiB, disk_total_bytes: 80 * GiB,
      network_available: true, network_rate_available: true,
      network_rx_bps: 1.6 * 1024 ** 2, network_tx_bps: 864 * 1024,
      network_rx_bytes: 68 * GiB, network_tx_bytes: 42 * GiB,
    },
  };
  window.__dashboardBrowserTestAPI = fixture;
  const json = (value, status = 200) => new Response(JSON.stringify(value), {
    status, headers: { "Content-Type": "application/json" },
  });
  const defaultPeriods = () => {
    const day = new Date().toISOString().slice(0, 10);
    const monday = new Date(`${day}T00:00:00Z`);
    monday.setUTCDate(monday.getUTCDate() - (monday.getUTCDay() + 6) % 7);
    const reported = new Date().toISOString();
    return {
      today: { start: day, received_bytes: 1.5 * GiB, sent_bytes: .5 * GiB, total_bytes: 2 * GiB, record_count: 2, sample_count: 4, last_reported_at: reported },
      week: { start: monday.toISOString().slice(0, 10), received_bytes: 5 * GiB, sent_bytes: GiB, total_bytes: 6 * GiB, record_count: 3, sample_count: 6, last_reported_at: reported },
      month: { start: `${day.slice(0, 8)}01`, received_bytes: 9 * GiB, sent_bytes: 3 * GiB, total_bytes: 12 * GiB, record_count: 4, sample_count: 8, last_reported_at: reported },
    };
  };
  const trafficDays = month => {
    const [year, monthNumber] = month.split("-").map(Number);
    const count = new Date(Date.UTC(year, monthNumber, 0)).getUTCDate();
    const today = new Date().toISOString().slice(0, 10);
    return Array.from({ length: count }, (_, index) => {
      const day = `${month}-${String(index + 1).padStart(2, "0")}`;
      const received = (1.2 + ((index * 7) % 11) * .43) * GiB * (fixture.trafficMultiplier || 1);
      const sent = (0.7 + ((index * 3) % 7) * .32) * GiB * (fixture.trafficMultiplier || 1);
      const base = { day, received_bytes: received, sent_bytes: sent, total_bytes: received + sent,
        peak_receive_bps: 3 * 1024 ** 2, peak_send_bps: 1.2 * 1024 ** 2, record_count: 1, sample_count: 2 };
      if (day > today || index === 1) return { ...base, received_bytes: 0, sent_bytes: 0, total_bytes: 0, peak_receive_bps: 0, peak_send_bps: 0, record_count: 0, sample_count: 0 };
      if (index === 2) return { ...base, received_bytes: 0, sent_bytes: 0, total_bytes: 0, peak_receive_bps: 0, peak_send_bps: 0, record_count: 1, sample_count: 3 };
      if (index === 3) return { ...base, sample_count: 0 };
      return base;
    });
  };
  const trafficNodes = () => {
    const multiplier = fixture.trafficMultiplier || 1;
    const entries = [
      { agent_id: "singapore", name: "新加坡 · SG-02", received_bytes: 3 * GiB, sent_bytes: GiB, total_bytes: 4 * GiB, record_count: 1, sample_count: 4 },
      { agent_id: "tokyo", name: longTokyoName, received_bytes: 5 * GiB, sent_bytes: GiB, total_bytes: 6 * GiB, record_count: 1, sample_count: 6 },
      { agent_id: "hongkong", name: "香港 · HK-01", received_bytes: 2 * GiB, sent_bytes: 2 * GiB, total_bytes: 4 * GiB, record_count: 1, sample_count: 4 },
    ].map(entry => Object.fromEntries(Object.entries(entry).map(([key, value]) => [key, key.endsWith("_bytes") ? value * multiplier : value])));
    return fixture.reverseRanking ? entries.reverse() : entries;
  };
  window.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    fixture.calls.push({ path, query: url.search });
    if (path === "/preferences/node-order") return json({ ids: JSON.parse(accountStorage.getItem("qcontrolhub:node-card-order") || "[]"), revision: 1 });
    if (path === "/auth/session") return json(session);
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") return json({ agents: 4, agents_online: 3, node_configs: 12, configs: 2, tasks_pending: 2, tasks_queued: 1, tasks_running: 1, tasks_failed: 1 });
    if (path === "/settings") return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "relative", task_poll_interval_ms: 5000 });
    if (path === "/agents") return json(agents.map(agent => ({ ...agent, metrics: { ...agent.metrics, collected_at: new Date().toISOString(),
      network_sampled_at: agent.metrics.network_sampled_at === undefined ? new Date().toISOString() : agent.metrics.network_sampled_at } })));
    if (path === "/tasks") return json(tasks);
    if (path === "/panel-metrics") {
      if (fixture.denied) return json({ error: "permission denied" }, 403);
      assert(hasPanel, "a user without panel-metrics.read requested global host counters");
      // Deliberately allow a late response after abort: route ownership must
      // reject it even when a transport cannot cancel its work.
      const sample = { collected_at: new Date().toISOString(), network_sampled_at: new Date().toISOString(), ...fixture.metrics };
      if (fixture.gate) await fixture.gate;
      return fixture.failed ? json({ error: "test metrics temporarily unavailable" }, 503) : json(sample);
    }
    if (path === "/traffic-usage") {
      if (fixture.trafficFailed) return json({ error: "test traffic temporarily unavailable" }, 503);
      assert(url.searchParams.get("view") === "dashboard", "dashboard did not request its persistent aggregate view");
      const month = url.searchParams.get("month");
      if (fixture.trafficGate) await fixture.trafficGate;
      return json({ month, timezone: "UTC", current_period: fixture.periods || defaultPeriods(), days: trafficDays(month), nodes: trafficNodes() });
    }
    throw new Error(`unexpected dashboard fixture request: ${path}`);
  };
  document.querySelector('link[href^="/assets/app.css"]')?.remove();
  location.hash = "#dashboard";
  const stylesheet = document.createElement("link"); stylesheet.rel = "stylesheet"; stylesheet.href = "/assets/analytics/theme.css";
  const styled = new Promise((resolve, reject) => { stylesheet.onload=resolve;stylesheet.onerror=reject; });
  document.head.append(stylesheet);await styled;
  (await import("./analytics/app.js")).startAnalytics({app:document.querySelector("#app"),session});
  const card = title => [...document.querySelectorAll(".a-main section.a-card")].find(card=>card.querySelector("h2")?.textContent===title);
  const summary = () => card("运行概况"), panel = () => card("面板运行资源"), traffic = () => card("流量趋势"), ranking = () => card("节点累计排行"), nodes = () => card("节点运行状态"), activity = () => card("最近任务");
  const metric = label => panel()?.querySelector(`[data-refresh-key="${label}"] strong`);
  const live = label => summary()?.querySelector(`[data-refresh-key="${label}"]`);
  const periodCard = key => summary()?.querySelector(`[data-refresh-key="period-${key}"]`);
  const periodSnapshot = () => ["today", "week", "month"].map(key => periodCard(key)?.textContent).join("|");
  const cpu = () => metric("CPU 使用率");
  const reads = path => fixture.calls.filter(call => call.path === path).length;
  const refreshButton = () => document.querySelector(".a-top-inner > .a-actions > button");
  const settled = () => !refreshButton()?.disabled;
  const refresh = async () => { await waitFor(settled,"refresh remained busy");const count=reads("/overview");refreshButton().click();await waitFor(()=>reads("/overview")>count&&settled(),"manual refresh did not settle"); };
  await waitFor(() => document.title.startsWith("仪表盘") && reads("/overview"), "dashboard did not render");
  assert(!document.body.textContent.includes("undefined"), "missing overview fields leaked");
  if (!hasPanel) {
    assert(!panel(), "panel-host card leaked");assert(reads("/panel-metrics") === 0, "unauthorized host request");
    if (limited) {
      for (const path of ["/agents", "/tasks", "/traffic-usage", "/settings", "/preferences/node-order"]) assert(reads(path) === 0, `overview-only user requested ${path}`);
      assert(!nodes() && !activity() && !traffic() && !ranking(), "inaccessible shortcuts exposed");
      await waitFor(() => compact(summary()?.querySelector(".a-dashboard-node-count")?.textContent).includes("3/4个节点"), "overview-only user lost allowed summary");
      assert(!summary().querySelector(".a-dashboard-periods") && !live("节点速率合计"), "overview-only user received traffic or node rates");
    } else {
      assert(traffic() && ranking() && nodes(), "readonly user lost owned dashboard data");
      assert(summary().querySelector(".a-dashboard-periods"), "readonly user lost current traffic periods");
    }
    assert(cspViolations.length === 0, `CSP violations: ${cspViolations.join(", ")}`);
    return;
  }
  await waitFor(() => fixture.failed ? panel()?.textContent.includes("资源读取失败") : cpu()?.textContent === "23.6%", "initial host metrics not settled");
  await waitFor(() => periodCard("today")?.dataset.state === "ready" && ranking()?.querySelectorAll(".a-dashboard-rank").length === 3, "initial dashboard aggregates not settled");
  if (preview) return;
  if (fixture.failed) { assert(cpu().textContent === "—", "failed first sample became zero");assert(nodes() && traffic(), "optional host failure blocked dashboard");fixture.failed=false;await refresh(); }
  assert(cpu().textContent === "23.6%", "CPU usage differs from panel API");
  assert(metric("内存使用率").textContent === "43.5%" && metric("根目录磁盘").textContent === "33.0%", "capacity percentages differ from the panel API");
  const expectedPeriods = defaultPeriods();
  for (const [key, title, range] of [["today", "今日累计", "UTC 自然日"], ["week", "本周累计", "周一至当前 · UTC"], ["month", "本月累计", "UTC 自然月"]]) {
    const current = periodCard(key), expected = expectedPeriods[key];
    assert(current?.querySelector("h3")?.textContent === title && current.textContent.includes(range), `${key} period UTC semantics missing`);
    assert(current.textContent.includes(`${expected.start} 起`), `${key} period has the wrong UTC start`);
    assert(compact(current.querySelector(".a-dashboard-period-value")?.textContent) === compact(`${expected.total_bytes / GiB} GB`), `${key} period total differs from current_period`);
    assert(current.dataset.state === "ready", `${key} sampled period is not ready`);
  }
  await waitFor(() => compact(summary()?.querySelector(".a-dashboard-node-count")?.textContent).includes("3/4个节点") && compact(live("节点速率合计")?.querySelector(".a-dashboard-rate")?.textContent) === "9KB/s", "online counts or valid fleet rates did not settle");
  const overviewRegions = summary().querySelector(".a-dashboard-overview").children;
  assert(overviewRegions[0].classList.contains("a-dashboard-live") && overviewRegions[1].classList.contains("a-dashboard-periods"), "live health is not prioritized over cumulative periods");
  const primaryRegions = document.querySelector(".a-dashboard-main").children, diagnosticRegions = document.querySelector(".a-dashboard-secondary").children;
  assert(primaryRegions[0] === traffic() && primaryRegions[1] === ranking(), "trend and ranking do not form the primary workspace");
  assert(diagnosticRegions[0] === panel() && diagnosticRegions[1] === nodes(), "panel and fleet do not form the diagnostic workspace");
  const engineSummary = nodes()?.querySelector(".a-card-body")?.textContent || "";
  for (const text of ["Mihomo 1/2", "sing-box 1/3", "SS Rust 0/2", "Xray 1/2"]) assert(engineSummary.includes(text), `offline runtime counted as live or scoped installed cores lost: ${text}`);
  assert(compact(live("节点速率合计")?.querySelector(".a-dashboard-rate")?.textContent) === "9KB/s", "valid fleet rates missing or offline node counted");
  await waitFor(() => traffic()?.querySelectorAll("path[data-series]").length >= 2, "receive and send series are not independently plotted");
  const receiveSeries = traffic().querySelector('path[data-series="received_bytes"]'), sendSeries = traffic().querySelector('path[data-series="sent_bytes"]');
  assert(receiveSeries && sendSeries && receiveSeries.getAttribute("stroke-dasharray") !== sendSeries.getAttribute("stroke-dasharray"), "traffic directions rely only on color");
  const rankOrder = () => [...ranking().querySelectorAll(".a-dashboard-rank")].map(item => item.dataset.refreshKey);
  assert(JSON.stringify(rankOrder()) === JSON.stringify(["tokyo", "hongkong", "singapore"]), "node ranking is not total-descending with an agent-id tie break");
  assert(ranking().querySelector('[data-refresh-key="tokyo"] a')?.textContent === longTokyoName, "ranking dropped the long API node name");
  assert(ranking().querySelector('[data-refresh-key="tokyo"] a')?.getAttribute("href") === "#traffic?agent=tokyo", "ranked nodes cannot open their own traffic");
  await waitFor(() => nodes()?.querySelector("tbody a")?.textContent.includes("东京"), "saved node order ignored");
  assert(!activity() && reads("/tasks") === 0, "retired recent-task area still renders or polls");
  const bands = () => [...traffic().querySelectorAll("[data-chart-day]")];
  const firstDay = bands()[0], firstDate = firstDay.dataset.chartDay;
  firstDay.scrollIntoView({block:"center",behavior:"instant"});
  firstDay.dispatchEvent(new PointerEvent("pointerover", {bubbles:true,pointerType:"mouse"}));
  await waitFor(() => document.querySelector(".a-tooltip"), "daily traffic hover has no tooltip");
  let tip = document.querySelector(".a-tooltip");
  assert(tip.textContent.includes(firstDate) && compact(tip.textContent).includes("接收1.2GB") && tip.textContent.includes("发送") && tip.textContent.includes("合计"), "daily tooltip omits date, directions or actual usage");
  document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true}));
  const currentBeforeTrendRefresh = periodSnapshot();
  fixture.trafficMultiplier=2;await refresh();
  await waitFor(() => compact(firstDay.getAttribute("aria-label")).includes("接收2.4GB"), "daily sample did not update after refresh");
  assert(bands()[0] === firstDay && periodSnapshot() === currentBeforeTrendRefresh, "trend refresh replaced the selected day or changed current periods");
  fixture.reverseRanking=true;await refresh();
  assert(JSON.stringify(rankOrder()) === JSON.stringify(["tokyo", "hongkong", "singapore"]), "ranking changed when the API order changed");
  fixture.trafficMultiplier=1;fixture.reverseRanking=false;await refresh();
  firstDay.focus();
  firstDay.dispatchEvent(new KeyboardEvent("keydown",{key:"ArrowRight",bubbles:true,cancelable:true}));
  const keyboardDay=bands()[Math.min(1,bands().length-1)];
  await waitFor(() => document.activeElement===keyboardDay && document.querySelector(".a-tooltip")?.textContent.includes(keyboardDay.dataset.chartDay), "keyboard cannot inspect adjacent daily traffic");
  firstDay.dispatchEvent(new PointerEvent("pointerup",{bubbles:true,pointerType:"touch"}));
  firstDay.dispatchEvent(new PointerEvent("pointerout",{bubbles:true,pointerType:"touch"}));
  assert(document.querySelector(".a-tooltip")?.textContent.includes(firstDate), "touch cannot inspect daily traffic");
  document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true}));
  assert(!document.querySelector(".a-tooltip"), "Escape did not dismiss daily tooltip");
  const assertLayout = () => {
    assert(document.documentElement.scrollWidth <= innerWidth + 1, `viewport overflow ${innerWidth}`);
    for (const element of document.querySelectorAll(".a-card-body,.a-chart-container,.a-chart-meta,.a-dashboard-periods,.a-dashboard-live,.a-dashboard-resources,.a-dashboard-main,.a-dashboard-secondary,.a-dashboard-ranking,.a-dashboard-rank,.a-dashboard-trend-summary")) assert(element.scrollWidth <= element.clientWidth + 1, `content overflow ${element.className}`);
    for (const element of document.querySelectorAll('[aria-label="节点运行状态"],[aria-label="节点流量排行"]')) assert(element.scrollWidth <= element.clientWidth + 1, `dashboard summary needs horizontal scrolling ${element.getAttribute("aria-label")}`);
    const cards = ["today", "week", "month"].map(periodCard);
    if (innerWidth <= 640) assert(cards[1].getBoundingClientRect().top >= cards[0].getBoundingClientRect().bottom - 1, "mobile current periods did not stack");
    else assert(Math.abs(cards[0].getBoundingClientRect().top - cards[2].getBoundingClientRect().top) < 1, "desktop current periods are not aligned");
    assert(Math.abs(traffic().querySelector("[name=traffic_month]").getBoundingClientRect().bottom - [...traffic().querySelectorAll("button")].find(button => button.textContent === "流量明细").getBoundingClientRect().bottom) < 1, "month and detail controls are not aligned");
  };
  for (const scale of [90,100,110,135]) { document.documentElement.style.fontSize=`${scale}%`;await delay(80);assertLayout(); }
  document.documentElement.style.fontSize="100%";await delay(80);
  const stablePanel=panel(),stableCPU=cpu(),stableNode=nodes().querySelector("tbody tr"),stableSVG=traffic().querySelector("svg");
  const month = () => traffic().querySelector('[name="traffic_month"]');
  month().focus();fixture.metrics.cpu_percent=92;await refresh();
  assert(cpu().textContent === "92.0%", "manual metrics refresh failed");
  assert(panel() === stablePanel && cpu() === stableCPU, "refresh replaced metric nodes");
  assert(document.activeElement === month(), "refresh stole filter focus");
  [...traffic().querySelectorAll("button")].find(button => button.textContent === "流量明细").click();
  const dialog=document.querySelector("dialog[open]");assert(dialog?.matches(":modal"), "traffic detail modal missing");
  const detailTable=dialog.querySelector('[aria-label="每日流量明细"]');assert(detailTable, "traffic daily data missing");
  const scroll=dialog.querySelector(".a-dialog-body");scroll.scrollTop=150;const beforeScroll=scroll.scrollTop;
  fixture.metrics.cpu_percent=41.2;await waitFor(() => cpu()?.textContent === "41.2%", "automatic metrics refresh failed");
  assert(dialog.matches(":modal") && scroll.scrollTop === beforeScroll, "poll disturbed detail modal");
  assert(nodes().querySelector("tbody tr") === stableNode && traffic().querySelector("svg") === stableSVG, "poll rebuilt unchanged business content");
  dialog.close();fixture.failed=true;await refresh();
  assert(panel().textContent.includes("刷新失败") && cpu().textContent === "41.2%", "failed refresh discarded metrics");
  fixture.failed=false;fixture.metrics.cpu_percent=0;fixture.metrics.memory_available=false;fixture.metrics.network_available=false;await refresh();
  assert(cpu().textContent === "0.0%", "idle CPU became unavailable");assert(metric("内存使用率").textContent === "—", "unavailable memory became zero");assert(metric("网络接收速率").textContent === "—", "unavailable network became zero");
  fixture.metrics.memory_available=true;
  const memoryTotal=fixture.metrics.memory_total_bytes;
  for (const total of [0,-1,null,"10"]) {
    fixture.metrics.memory_total_bytes=total;await refresh();
    assert(metric("内存使用率").textContent === "—", "invalid memory capacity was displayed as a valid percentage");
  }
  fixture.metrics.memory_total_bytes=memoryTotal;fixture.metrics.memory_available=false;
  fixture.metrics.network_available=true;fixture.metrics.network_rx_bps=null;await refresh();
  assert(metric("网络接收速率").textContent === "—", "invalid network sample became a real rate");
  fixture.metrics.network_rx_bps="<img src=x onerror=alert(1)>";await refresh();
  assert(metric("网络接收速率").textContent === "—" && !panel().querySelector("img"), "untrusted metric became HTML or a real sample");
  fixture.metrics.network_rx_bps=2048;fixture.metrics.network_tx_bps=512;await refresh();
  assert(compact(metric("网络接收速率").textContent) === "2KB/s" && panel().textContent.includes("容器可见"), "network recovery or container scope lost");
  const onlineAgents = agents.filter(agent => agent.status === "online");
  for (const state of ["false", "missing", "stale", "missing-time"]) {
    for (const agent of onlineAgents) {
      if (state === "false") agent.metrics.network_rate_available=false;
      if (state === "missing") delete agent.metrics.network_rate_available;
      if (state === "stale") { agent.metrics.network_rate_available=true;agent.metrics.network_sampled_at=new Date(Date.now()-16000).toISOString(); }
      if (state === "missing-time") agent.metrics.network_sampled_at=null;
    }
    await refresh();
    assert(live("节点速率合计")?.querySelector(".a-dashboard-rate")?.textContent === "—", `node rate survived ${state} availability/timestamp`);
    assert(live("节点速率合计")?.textContent.includes("0 个有效采样"), `node invalid ${state} sample remained counted`);
  }
  for (const agent of onlineAgents) { agent.metrics.network_rate_available=true;delete agent.metrics.network_sampled_at; }
  await refresh();
  assert(compact(live("节点速率合计")?.querySelector(".a-dashboard-rate")?.textContent) === "9KB/s", "valid node rates did not recover");
  agents.find(agent => agent.id === "tokyo").metrics.network_rate_available=false;await refresh();
  assert(compact(live("节点速率合计")?.querySelector(".a-dashboard-rate")?.textContent) === "6KB/s", "one invalid node rate polluted or discarded the remaining fleet total");
  agents.find(agent => agent.id === "tokyo").metrics.network_rate_available=true;
  for (const state of ["false", "missing", "stale", "missing-time"]) {
    if (state === "false") fixture.metrics.network_rate_available=false;
    if (state === "missing") delete fixture.metrics.network_rate_available;
    if (state === "stale") { fixture.metrics.network_rate_available=true;fixture.metrics.network_sampled_at=new Date(Date.now()-16000).toISOString(); }
    if (state === "missing-time") fixture.metrics.network_sampled_at=null;
    await refresh();
    assert(metric("网络接收速率").textContent === "—" && metric("网络发送速率").textContent === "—", `panel rate survived ${state} availability/timestamp`);
  }
  fixture.metrics.network_rate_available=true;delete fixture.metrics.network_sampled_at;await refresh();
  assert(compact(metric("网络接收速率").textContent) === "2KB/s" && compact(metric("网络发送速率").textContent) === "512B/s", "panel rates did not recover with fresh explicit availability");
  fixture.metrics.collected_at="invalid";await refresh();
  assert(panel().textContent.includes("尚无有效采样"), "invalid timestamp presented a fresh sample");
  assert(!/NaN|undefined|Infinity/.test(panel().textContent), "invalid metric text leaked");
  fixture.metrics.collected_at=new Date(Date.now()-60000).toISOString();await refresh();assert(panel().textContent.includes("采样已过期"), "stale sample presented live");delete fixture.metrics.collected_at;
  fixture.denied=true;await refresh();assert(cpu().textContent === "—" && panel().textContent.includes("权限"), "denied host read retained protected data");fixture.denied=false;await refresh();
  fixture.periods=defaultPeriods();
  Object.assign(fixture.periods.today,{received_bytes:0,sent_bytes:0,total_bytes:0,record_count:0,sample_count:0});
  Object.assign(fixture.periods.week,{record_count:1,sample_count:0});
  Object.assign(fixture.periods.month,{received_bytes:0,sent_bytes:0,total_bytes:0,record_count:1,sample_count:2});
  await refresh();
  assert(periodCard("today").dataset.state === "empty" && periodCard("today").textContent.includes("暂无采集记录") && periodCard("today").querySelector(".a-dashboard-period-value").textContent === "—", "empty today became real zero or valid traffic");
  assert(periodCard("week").dataset.state === "incomplete" && periodCard("week").textContent.includes("采集数据不完整") && periodCard("week").querySelector(".a-dashboard-period-value").textContent === "—", "incomplete week exposed a valid total");
  assert(periodCard("month").dataset.state === "ready" && compact(periodCard("month").querySelector(".a-dashboard-period-value").textContent) === "0B", "sampled zero month was confused with no records");
  fixture.periods=null;await refresh();
  const chooseMonth = value => { month().value=value;month().dispatchEvent(new Event("change",{bubbles:true})); };
  const trendPeriod = () => traffic().querySelector(".a-dashboard-chart-head").textContent;
  const oldMonth=month().value, currentBeforeHistory=periodSnapshot();
  fixture.trafficFailed=true;const oldReads=reads("/traffic-usage");chooseMonth(`${new Date().getUTCFullYear()-1}-01`);
  await waitFor(() => reads("/traffic-usage") > oldReads && traffic().textContent.includes("刷新失败"), "failed month request not shown");
  assert(trendPeriod().includes(oldMonth) && periodSnapshot() === currentBeforeHistory, "failed month relabelled old history or changed current periods");
  fixture.metrics.cpu_percent=37;await waitFor(() => cpu()?.textContent === "37.0%", "failed month stopped host polling");fixture.trafficFailed=false;
  const february=`${new Date().getUTCFullYear()-1}-02`;
  let releaseHistory;fixture.trafficGate=new Promise(resolve=>{releaseHistory=resolve;});const beforeHistory=reads("/traffic-usage");chooseMonth(february);
  await waitFor(() => reads("/traffic-usage") > beforeHistory, "historical month request missing");
  assert(periodSnapshot() === currentBeforeHistory && trendPeriod().includes(oldMonth), "pending history changed current cards or discarded the previous trend");
  fixture.trafficGate=null;releaseHistory();await waitFor(() => trendPeriod().includes(february), "February not loaded");
  assert(periodSnapshot() === currentBeforeHistory, "historical month changed first-screen today/week/month totals");
  const days=new Date(Date.UTC(new Date().getUTCFullYear()-1,2,0)).getUTCDate();
  assert(traffic().querySelector(".a-dashboard-trend-summary").textContent.includes(`/ ${days} 天`), "historical coverage uses incorrect day count");
  assert(traffic().querySelector("svg [d]") && traffic().querySelector("svg").textContent.includes(`02-${days}`), "February axis lost final day");
  const historyBands=bands();
  assert(historyBands[1].getAttribute("aria-label").includes("无采集记录"), "no-record day was plotted as real zero");
  assert(compact(historyBands[2].getAttribute("aria-label")).includes("接收0B") && compact(historyBands[2].getAttribute("aria-label")).includes("发送0B"), "sampled zero day was not preserved as real zero");
  assert(historyBands[3].getAttribute("aria-label").includes("采集数据不完整"), "incomplete day was presented as valid");
  [...traffic().querySelectorAll("button")].find(button => button.textContent === "流量明细").click();
  const historyDialog=document.querySelector("dialog[open]"), rows=[...historyDialog.querySelectorAll('[aria-label="每日流量明细"] tbody tr')];
  assert(rows[1].textContent.includes("无记录") && rows[1].textContent.includes("—"), "daily detail converted no records into zero");
  assert(rows[2].textContent.includes("有效") && compact(rows[2].textContent).includes("0B"), "daily detail discarded sampled zero");
  assert(rows[3].textContent.includes("不完整"), "daily detail hid incomplete sampling");
  historyDialog.close();assertLayout();
  if (mode === "dashboard") {
    Object.defineProperty(document,"hidden",{configurable:true,value:true});document.dispatchEvent(new Event("visibilitychange"));const hidden=reads("/panel-metrics");await delay(5200);assert(reads("/panel-metrics") === hidden, "hidden dashboard polled");delete document.hidden;document.dispatchEvent(new Event("visibilitychange"));await waitFor(() => reads("/panel-metrics") > hidden, "visible dashboard did not refresh");
  }
  // A pending optional resource cannot delay node/traffic refreshes or page navigation.
  let release;fixture.gate=new Promise(resolve=>{release=resolve;});const beforeGate=reads("/panel-metrics");refreshButton().click();await waitFor(() => reads("/panel-metrics") > beforeGate, "slow metrics read missing");
  const beforeNodes=reads("/agents");await delay(5300);assert(reads("/agents") > beforeNodes, "slow host read blocked node polling");
  location.hash="#tasks";await waitFor(() => document.title.startsWith("任务"), "route departure blocked on metrics");const afterLeave=reads("/panel-metrics");fixture.gate=null;release();await delay(2200);assert(!panel() && reads("/panel-metrics") === afterLeave, "old metrics response/timer survived navigation");
  assert(cspViolations.length === 0, `CSP violations: ${cspViolations.join(", ")}`);
}
