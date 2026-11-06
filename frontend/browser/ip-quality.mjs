import { assert, delay, waitFor } from "./assertions.mjs";
import { testIPQualityScenarios } from "./ip-quality-scenarios.mjs";
import { testIPQualitySVG } from "./ip-quality-svg.mjs";
import { testIPQualityRefresh } from "./ip-quality-refresh.mjs";
import { ipQualityToday, nextIPQualityDay } from "../modules/ip-quality-model.js";

export async function testIPQualityRuntime(mode, preview = false) {
  const readonly = mode === "ip-quality-readonly";
  if (mode.includes("mobile")) {
    assert.ok(innerWidth <= 480 && matchMedia("(pointer:coarse)").matches,
      "IP quality mobile scenario requires a real narrow touch viewport");
  }
  const today = ipQualityToday(), yesterday = nextIPQualityDay(today, -1), reportDay = nextIPQualityDay(today, -7);
  const offlineDay = nextIPQualityDay(today, -3), olderDay = nextIPQualityDay(today, -42);
  const violations = [];
  document.addEventListener("securitypolicyviolation", (event) => violations.push(event.effectiveDirective));
  const session = { role: readonly ? "user" : "admin", user_id: readonly ? "quality-reader" : undefined,
    permissions: ["agents.read", "overview.read"], csrf_token: "quality-test-csrf" };
  const agents = [
    { id: "quality-a", name: "东京 · IPv4 / IPv6", status: "online", features: ["ip-quality-v2"] },
    { id: "quality-b", name: "新加坡 · 等待升级", status: "online", features: [] },
    { id: "quality-c", name: "法兰克福 · 离线", status: "offline", features: ["ip-quality-v2"] },
    { id: "quality-shared", name: "不应显示的共享主机", status: "online", features: ["ip-quality-v2"], can_manage: false },
  ].map((agent) => ({ can_manage: true, capabilities: ["mihomo"], supported_capabilities: ["mihomo"],
    runtime: {}, metrics: {}, labels: {}, os: "Debian", arch: "amd64", ...agent }));
  const report = (ip) => ({
    Head: { IP: ip, Time: `${today} 06:00:00 UTC`, Version: "test-fixture" },
    Info: { ASN: "64500", Organization: "Example Network <script>alert(1)</script>", Region: { Name: "日本", Code: "JP" }, City: { Name: "东京" } },
    Type: { Usage: { IPinfo: "Hosting", ipapi: "Business" } },
    Score: { IPQS: "null", SCAMALYTICS: "0", ipapi: "0.47%", DBIP: "1" },
    Factor: { Proxy: { IPinfo: false, IPQS: null }, VPN: { IPinfo: true } },
    Media: { Netflix: { Status: "Yes", Region: "JP", Type: "Native" }, ChatGPT: { Status: "Failed", Region: "", Type: "" } },
    Mail: { Port25: false, Gmail: false, DNSBlacklist: { Total: 439, Clean: 411, Marked: 28, Blacklisted: 0 } },
  });
  const completeRecord = (taskID = "quality-task", day = reportDay) => ({
    task_id: taskID, agent_id: "quality-a", status: "succeeded",
    created_at: `${day}T06:00:00Z`, finished_at: `${day}T06:05:00Z`,
    archives: [4, 6].map((family) => ({ family, rendered_at: `${day}T06:05:00Z`, sha256: "a".repeat(64) })),
    result: { reports: [report("203.0.113.10"), report("2001:0db8:1234:5678:90ab:cdef:1234:5678")] },
  });
  const fixture = {
    records: [completeRecord(), { ...completeRecord("quality-offline", nextIPQualityDay(today, -3)), agent_id: "quality-c" }],
    // A plan enabled by another administrator is still the node's active plan.
    schedules: [{ agent_id: "quality-a", enabled: true, next_run_at: new Date(Date.now() + 86400000).toISOString() }],
    calls: [], failed: false, checks: 0, scheduleWrites: 0, prunedBefore: "",
  };
  const historicalRecords = [completeRecord(), { ...completeRecord("quality-offline", offlineDay), agent_id: "quality-c" },
    completeRecord("quality-older", olderDay), { task_id: "historical-failure", agent_id: "quality-a", status: "failed",
      created_at: `${yesterday}T06:00:00Z`, error: "检测未完成，请检查节点网络。" }];
  window.__ipQualityFixture = fixture;
  let releaseShell;
  const shellReady = new Promise(resolve => { releaseShell = resolve; });
  let releaseHistory;
  const historyReady = new Promise(resolve => { releaseHistory = resolve; });
  const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
  window.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const method = options.method || "GET";
    const call = { path, method, search: url.search, at: performance.now() };
    fixture.calls.push(call);
    if (path === "/auth/session" || path === "/auth/login") return json(fixture.session || session);
    if (path === "/preferences/node-order") return json({ ids: (fixture.agents || [agents[0], agents[2], agents[1], ...agents.slice(3)]).map(agent => agent.id), revision: 1 });
    if (path === "/preferences/theme") return json({ theme_id: "analytics", revision: 1 });
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") { await shellReady; return json({ agents: 3, agents_online: 2 }); }
    if (path === "/settings") { await shellReady; return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "absolute" }); }
    if (path === "/agents") return json(fixture.agents || agents);
    if (path === "/tasks") return json([]);
    if (path === "/traffic-usage") return json({ month: url.searchParams.get("month"), timezone: "UTC", days: [] });
    if (path === "/panel-metrics") return json({});
    if (path === "/ip-quality" && method === "GET") {
      if (fixture.failed) return json({ error: "检测记录暂不可用" }, 503);
      if (fixture.readStatus) return json({ error: "检测记录请求失败" }, fixture.readStatus);
      if (fixture.invalid) return json({ records: [] });
      const date = url.searchParams.get("date");
      if (!date) await historyReady;
      const retainedHistory = historicalRecords.filter((record) => record.created_at.slice(0, 10) >= fixture.prunedBefore);
      const retainedLatest = fixture.records.filter((record) => !record.created_at || record.created_at.slice(0, 10) >= fixture.prunedBefore);
      const records = !date ? retainedLatest : retainedHistory.filter((record) => record.created_at.slice(0, 10) === date);
      const dates = [...new Set([...retainedHistory, ...retainedLatest].filter((record) => record.created_at)
        .map((record) => record.created_at.slice(0, 10)))].sort().reverse();
      const response = json({ date: date || "", timezone: url.searchParams.get("timezone"), dates, records, schedules: fixture.schedules });
      if (fixture.readGate) {
        const gate = fixture.readGate;
        fixture.readGate = null;
        return gate(response, options.signal);
      }
      call.completed = performance.now();
      return response;
    }
    if (path === "/ip-quality" && method === "POST") {
      assert.ok(!readonly, "read-only page submitted a check");
      assert.equal(JSON.parse(options.body).agent_id, "quality-a");
      if (fixture.createGate) await fixture.createGate();
      if (fixture.createFailure) return json(fixture.createFailure, 500);
      fixture.checks += 1;
      fixture.records = [{ task_id: "new-quality-task", agent_id: "quality-a", status: "pending", created_at: new Date().toISOString(),
        last_successful: fixture.records.find((record) => record.agent_id === "quality-a") }];
      return json({ id: "new-quality-task", agent_id: "quality-a", status: "pending", action: "ip-quality", created_at: fixture.records[0].created_at }, 201);
    }
    if (path === "/ip-quality/schedules/quality-a" && method === "PUT") {
      assert.ok(!readonly, "read-only page changed a schedule");
      fixture.scheduleWrites += 1;
      const schedule = { agent_id: "quality-a", enabled: JSON.parse(options.body).enabled, next_run_at: new Date(Date.now() + 86400000).toISOString() };
      fixture.schedules = [schedule];
      return json(schedule);
    }
    throw new Error(`unexpected IP quality fixture request: ${method} ${path}`);
  };
  document.querySelector('link[href^="/assets/app.css"]')?.remove();
  location.hash = "#ip-quality";
  const styles = document.createElement("link"); styles.rel = "stylesheet"; styles.href = "/assets/analytics/theme.css";
  const styled = new Promise((resolve, reject) => { styles.onload = resolve; styles.onerror = reject; });
  document.head.append(styles); await styled;
  releaseShell();
  (await import("../analytics/app.js")).startAnalytics({ app: document.querySelector("#app"), session });
  const main = () => document.querySelector(".a-main");
  const card = () => document.querySelector(".a-ipq-detail");
  const panel = id => document.querySelector(`[data-ip-quality-panel="${id}"]`);
  const sidebar = () => document.querySelector('[aria-label="检测节点"]');
  const sidebarLink = id => [...sidebar().querySelectorAll(".a-ipq-node")].find(button => button.textContent.includes(agents.find(agent => agent.id === id)?.name));
  const run = () => document.querySelector("[data-ip-quality-run]");
  const schedule = () => document.querySelector("[data-ip-quality-schedule]");
  const selectedDate = () => document.querySelector('[name="date"]');
  const refreshButton = () => document.querySelector('.a-top button:last-child');
  const reads = () => fixture.calls.filter(call => call.path === "/ip-quality" && call.method === "GET").length;
  const settled = () => !main()?.querySelector('form[aria-busy]');
  const refresh = async () => {
    await waitFor(() => settled() && !refreshButton()?.disabled, "previous read did not settle");
    const before = reads(); refreshButton().click();
    await waitFor(() => reads() > before && settled(), "refresh did not finish");
  };
  const goDate = async day => {
    selectedDate().value = day; selectedDate().dispatchEvent(new Event("change", { bubbles: true }));
    await waitFor(() => selectedDate().value === day && settled(), "date read did not settle");
  };
  const latest = async () => {
    [...main().querySelectorAll("button")].find(button => button.textContent === "重置").click();
    await waitFor(() => selectedDate().value === "" && settled(), "latest read did not settle");
  };
  await waitFor(() => panel("quality-a"), "nodes did not render before history completed");
  assert.equal([...sidebar().querySelectorAll(".a-ipq-node")].map(button => button.dataset.refreshKey).join(","), "quality-a,quality-c,quality-b", "node selector ignored authenticated server order");
  assert.ok(main().textContent.includes("正在读取检测记录"), "partial history looked completed");
  if (readonly) assert.equal(run(), null, "partial read exposed a read-only mutation");
  else assert.ok(run()?.disabled, "partial read enabled a mutation");
  releaseHistory();
  await waitFor(() => card()?.querySelectorAll("img").length === 2 && settled(), "IP quality page did not load");
  assert.ok(fixture.calls.filter(call => call.path === "/ip-quality").every(call => !new URLSearchParams(call.search).has("date")), "latest view requested a single day");
  assert.equal(selectedDate().value, "", "latest view was presented as today's results");
  assert.equal(card().querySelector("time").dateTime, `${reportDay}T06:05:00Z`, "latest view lost last week's report");
  assert.ok(document.querySelector('nav a[href="#ip-quality"]'), "IP quality navigation is missing");
  assert.equal(sidebar().querySelectorAll(".a-ipq-node").length, 3, "shared host was exposed");
  assert.equal(sidebarLink("quality-shared"), undefined, "shared host was exposed");
  assert.equal(sidebarLink("quality-a").getAttribute("aria-pressed"), "true", "selected node is not highlighted");
  assert.equal(document.querySelectorAll("[data-ip-quality-panel]").length, 1, "multiple nodes rendered");
  const images = [...card().querySelectorAll("img")];
  for (const image of images) image.scrollIntoView({ block: "center", behavior: "instant" });
  await waitFor(() => images.every(image => image.complete && image.naturalWidth > 0), "inline archives did not load");
  assert.ok(images.every(image => image.src.startsWith(location.origin + "/api/v1/ip-quality/")), "preview fetched upstream report");
  assert.equal(card().querySelector("details"), null, "reports require opening a disclosure");
  assert.equal(card().querySelectorAll("a[download]").length, 2, "report downloads missing");
  await testIPQualitySVG(images);
  const reports = images.map(image => image.getBoundingClientRect());
  for (const [index, rect] of reports.entries()) {
    const image = images[index];
    assert.ok(rect.height <= innerHeight - 64, "complete report must fit below the fixed header");
    assert.ok(Math.abs(rect.width / rect.height - image.naturalWidth / image.naturalHeight) < .01,
      "report overview must preserve the complete SVG aspect ratio");
    assert.equal(image.closest('a')?.target, '_blank', "original report must remain accessible");
  }
  if (innerWidth > 1200) assert.ok(Math.abs(reports[0].top - reports[1].top) < 1,
    "IPv4 and IPv6 reports must be visible side by side on desktop");
  assert.equal(card().querySelector("script"), null, "upstream data became executable HTML");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "IP quality overflows viewport");
  if (readonly) { assert.equal(run(), null); assert.equal(schedule(), null); }
  else assert.equal(schedule().getAttribute("aria-pressed"), "true", "existing administrator schedule looked disabled");
  const dateOptions = [...selectedDate().options].map(option => option.value);
  for (const day of [yesterday, offlineDay, reportDay, olderDay]) assert.ok(dateOptions.includes(day), "historical date unavailable");
  assert.ok(!dateOptions.includes(nextIPQualityDay(today, 1)), "future date selectable");
  const beforeFocus = reads(); selectedDate().focus();
  await delay(5100);
  await waitFor(() => reads() > beforeFocus, "date interaction stopped polling");
  assert.equal(document.activeElement, selectedDate(), "polling stole date focus");
  assert.equal(card().querySelector("img"), images[0], "polling replaced report");
  if (!readonly) {
    for (const id of ["quality-b", "quality-c"]) {
      sidebarLink(id).click(); await waitFor(() => panel(id), "node switch failed");
      assert.ok(run().disabled, "offline/unsupported node executable");
      assert.equal(sidebarLink(id).getAttribute("aria-pressed"), "true");
      if (id === "quality-c") assert.equal(card().querySelector("time").dateTime, `${offlineDay}T06:05:00Z`, "node dates were merged");
      assert.equal(document.querySelectorAll("[data-ip-quality-panel]").length, 1);
    }
    sidebarLink("quality-a").click();
  }
  await goDate(yesterday);
  assert.ok(card().textContent.includes("失败"), "historical failure missing");
  assert.equal(card().querySelector("img"), null, "historical date retained another day's image");
  assert.equal(sidebar().querySelectorAll(".a-ipq-node").length, 1, "history includes unrecorded nodes");
  assert.equal(run(), null, "history can submit checks"); assert.equal(schedule(), null, "history can change schedule");
  assert.ok(!card().textContent.includes("在线"), "historical record presented current presence");
  await goDate(offlineDay); assert.ok(panel("quality-c"), "offline historical node missing");
  await goDate(reportDay); assert.ok(panel("quality-a"));
  await goDate(olderDay); assert.equal(card().querySelector("time").dateTime, `${olderDay}T06:05:00Z`);
  fixture.prunedBefore = reportDay; await refresh();
  selectedDate().blur(); await refresh();
  assert.ok([...selectedDate().options].some(option => option.value === reportDay), "retained date unavailable after pruning");
  await goDate(reportDay);
  assert.ok(![...selectedDate().options].some(option => option.value === olderDay), "pruned date remains selectable");
  fixture.prunedBefore = ""; await latest();
  fixture.failed = true; await refresh();
  assert.ok(main().textContent.includes("读取失败"), "read failure hidden");
  assert.ok(!document.body.textContent.includes("演示数据"), "read failure fabricated data");
  if (!readonly) assert.ok(run().disabled, "failed read allowed mutation");
  fixture.failed = false; await refresh();
  if (!readonly) {
    const confirm = async (external = true) => {
      const dialog = await waitFor(() => document.querySelector("dialog[open]"), "confirmation missing");
      if (external) assert.ok(dialog.textContent.includes("第三方"), "external access consent missing");
      dialog.querySelector("button.primary").click();
    };
    let releaseCreate;
    fixture.createGate = () => new Promise(resolve => { releaseCreate = resolve; });
    const requestID = `req_${"a".repeat(100)}`;
    fixture.createFailure = {error:"任务创建失败，尚未进入 Agent 执行。",code:"task_schema_incompatible",stage:"task_creation",request_id:requestID};
    const posts = fixture.calls.filter(call => call.path === "/ip-quality" && call.method === "POST").length;
    run().click(); await confirm();
    await waitFor(() => releaseCreate && card().textContent.includes("任务创建中…"), "creation pending feedback missing");
    run().click(); assert.equal(fixture.calls.filter(call => call.path === "/ip-quality" && call.method === "POST").length, posts + 1, "duplicate creation submitted");
    releaseCreate();
    await waitFor(() => card().textContent.includes(requestID), "creation failure lost request ID");
    assert.ok(!run().disabled, "creation failure blocked retry");
    assert.equal(card().querySelectorAll("img").length, 2, "creation failure erased successful report");
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "long error overflowed viewport");
    fixture.createGate = fixture.createFailure = null;
    run().click(); await confirm();
    await waitFor(() => card().textContent.includes("排队中，等待 Agent 领取"), "queued task missing");
    assert.equal(fixture.checks, 1); assert.ok(run().disabled, "pending task duplicable");
    assert.ok(card().textContent.includes("已排队"), "queue timing missing");
    assert.ok(!card().textContent.includes(requestID), "successful creation retained prior error");
    assert.ok(card().textContent.includes("上次成功检测"));
    assert.equal(card().querySelector("time").dateTime, `${reportDay}T06:05:00Z`);
    assert.equal(card().querySelectorAll("img").length, 2);
    assert.ok(card().querySelector("img").src.includes("quality-task/archives"), "pending task claimed previous archive");
    fixture.records[0].status = "running"; fixture.records[0].started_at = new Date(Date.now()-83000).toISOString();
    await refresh(); assert.ok(card().textContent.includes("已执行 1 分") && card().textContent.includes("数分钟"), "running timing/guidance missing");
    assert.equal(card().querySelector('[role="progressbar"]'), null, "invented task percentage");
    fixture.records[0].status = "canceled"; fixture.records[0].finished_at = new Date().toISOString();
    await refresh(); assert.ok(card().textContent.includes("已取消") && card().textContent.includes("执行耗时"));
    fixture.records[0].status = "failed"; fixture.records[0].error = "本次检测超时";
    await refresh(); assert.ok(card().textContent.includes("最新检测未成功") && card().textContent.includes("本次检测超时")); assert.ok(!run().disabled);
    fixture.records = [completeRecord("new-quality-task", today)]; await refresh();
    assert.ok(!card().textContent.includes("上次成功检测"));
    schedule().click(); await confirm(false);
    await waitFor(() => schedule().getAttribute("aria-pressed") === "false", "existing schedule could not be disabled");
    assert.equal(fixture.scheduleWrites, 1);
    schedule().click(); await confirm();
    await waitFor(() => schedule().getAttribute("aria-pressed") === "true", "schedule could not be enabled");
    assert.equal(fixture.scheduleWrites, 2);
  }
  assert.equal(violations.length, 0, `production CSP violations: ${violations.join(", ")}`);
  await testIPQualityRefresh({ fixture, refresh, card, reportDay, olderDay, historicalRecords, mode, goDate, latest, sidebarLink, selectedDate, settled, refreshButton });
  await testIPQualityScenarios({ fixture, refresh, card });
  let releaseOld;
  fixture.readGate = response => new Promise(resolve => { releaseOld = () => resolve(response); });
  refreshButton().click(); await waitFor(() => releaseOld, "account-switch read did not start");
  fixture.readStatus = 401; [...main().querySelectorAll("button")].find(button => button.textContent === "重置").click();
  await waitFor(() => document.querySelector('form[aria-label="登录控制台"]'), "expired session did not clear page");
  assert.equal(document.querySelector(".a-report-image"), null, "expired account kept reports");
  fixture.readStatus = 0; fixture.session = { ...session, user_id: "quality-second-account", csrf_token: "quality-second-csrf" };
  const priorRecords = fixture.records; fixture.records = [];
  const login = document.querySelector('form[aria-label="登录控制台"]');
  login.elements.username.value = "fixture-user"; login.elements.token.value = "fixture-password";
  login.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => panel("quality-a") && settled(), "second account page did not load");
  releaseOld(); await delay(30); assert.equal(card().querySelector("img"), null, "obsolete response restored first account report");
  fixture.records = priorRecords; await refresh();
  if (preview) await new Promise(() => {});
}
