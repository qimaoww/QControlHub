import { assert, delay, waitFor } from "./assertions.mjs";
import { testIPQualityScenarios } from "./ip-quality-scenarios.mjs";
import { testIPQualitySVG } from "./ip-quality-svg.mjs";
import { ipQualityToday, nextIPQualityDay } from "../modules/ip-quality-model.js";

export async function testIPQualityRuntime(mode, preview = false) {
  const readonly = mode === "ip-quality-readonly";
  if (mode === "ip-quality-mobile") {
    assert.ok(innerWidth <= 480 && matchMedia("(pointer:coarse)").matches,
      "IP quality mobile scenario requires a real narrow touch viewport");
  }
  const today = ipQualityToday(), yesterday = nextIPQualityDay(today, -1), reportDay = nextIPQualityDay(today, -7);
  const offlineDay = nextIPQualityDay(today, -3), olderDay = nextIPQualityDay(today, -42);
  const violations = [];
  document.addEventListener("securitypolicyviolation", (event) => violations.push(event.effectiveDirective));
  const session = { role: readonly ? "user" : "admin", user_id: readonly ? "quality-reader" : undefined,
    permissions: ["agents.read"], csrf_token: "quality-test-csrf" };
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
    fixture.calls.push({ path, method, search: url.search });
    if (path === "/auth/session") return json(session);
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") { await shellReady; return json({ agents: 3, agents_online: 2 }); }
    if (path === "/settings") { await shellReady; return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "absolute" }); }
    if (path === "/agents") return json(agents);
    if (path === "/ip-quality" && method === "GET") {
      if (fixture.failed) return json({ error: "检测记录暂不可用" }, 503);
      const date = url.searchParams.get("date");
      if (!date) await historyReady;
      const retainedHistory = historicalRecords.filter((record) => record.created_at.slice(0, 10) >= fixture.prunedBefore);
      const retainedLatest = fixture.records.filter((record) => !record.created_at || record.created_at.slice(0, 10) >= fixture.prunedBefore);
      const records = !date ? retainedLatest : retainedHistory.filter((record) => record.created_at.slice(0, 10) === date);
      const dates = [...new Set([...retainedHistory, ...retainedLatest].filter((record) => record.created_at)
        .map((record) => record.created_at.slice(0, 10)))].sort().reverse();
      return json({ date: date || "", timezone: url.searchParams.get("timezone"), dates, records, schedules: fixture.schedules });
    }
    if (path === "/ip-quality" && method === "POST") {
      assert.ok(!readonly, "read-only page submitted a check");
      assert.equal(JSON.parse(options.body).agent_id, "quality-a");
      fixture.checks += 1;
      fixture.records = [{ task_id: "new-quality-task", agent_id: "quality-a", status: "pending", created_at: new Date().toISOString(),
        last_successful: fixture.records.find((record) => record.agent_id === "quality-a") }];
      return json({ id: "new-quality-task", status: "pending", action: "ip-quality" }, 201);
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
  location.hash = "#ip-quality";
  await import("../app.js");
  const card = () => document.querySelector('[data-ip-quality-panel="quality-a"]');
  const panel = (id) => document.querySelector(`[data-ip-quality-panel="${id}"]`);
  const sidebar = () => document.querySelector(".context-sidebar");
  const sidebarLink = (id) => sidebar().querySelector(`[data-ip-quality-agent="${id}"]`);
  await waitFor(() => card()?.textContent.includes("读取中"), "nodes did not render before history completed");
  if (readonly) assert.equal(card().querySelector("[data-ip-quality-run]"), null, "partial read exposed a read-only mutation");
  else assert.ok(card().querySelector("[data-ip-quality-run]")?.disabled, "partial read enabled a mutation");
  assert.ok(document.querySelector(".ip-quality-summary")?.textContent.includes("—"),
    "partial read displayed zero detections");
  releaseHistory();
  await waitFor(() => card()?.textContent.includes("已完成"), "IP quality page did not load");
  assert.ok(fixture.calls.filter((call) => call.path === "/ip-quality").every((call) => !new URLSearchParams(call.search).has("date")),
    "default view requested a single day");
  assert.equal(document.querySelector("[data-ip-quality-latest]").getAttribute("aria-pressed"), "true");
  assert.equal(card().querySelector("time").dateTime, `${reportDay}T06:05:00Z`, "default view lost the report from last week");
  releaseShell();
  assert.ok(document.querySelector('.dock-nav a[href="#ip-quality"]'), "IP quality navigation is missing");
  // The project-standard context sidebar filters the page down to one node.
  assert.notEqual(getComputedStyle(sidebar()).display, "none", "IP quality hid the node sidebar");
  assert.equal(sidebar().querySelectorAll("[data-ip-quality-agent]").length, 3, "shared host was exposed");
  assert.equal(sidebarLink("quality-shared"), null, "shared host was exposed");
  assert.ok(sidebarLink("quality-a").classList.contains("active"), "the selected node is not highlighted");
  assert.equal(sidebar().querySelectorAll("[data-ip-quality-panel]").length, 0, "the sidebar reused the panel attribute");
  assert.equal(document.querySelectorAll("[data-ip-quality-panel]").length, 1, "the page rendered more than one node");
  assert.ok(document.querySelector('[data-ip-quality-day="1"]').disabled, "future day is selectable");
  card().querySelector(".ip-quality-details>summary").click();
  const images = [...card().querySelectorAll(".ip-quality-archive img")];
  assert.equal(images.length, 2, "database report images are missing");
  await waitFor(() => images.every((image) => image.complete && image.naturalWidth > 0), "archived SVG images failed to load");
  assert.ok(images.every((image) => image.src.startsWith(location.origin+"/api/v1/ip-quality/")), "preview fetched an upstream URL");
  await testIPQualitySVG(images);
  assert.equal(card().querySelectorAll(".ip-quality-report").length, 2, "dual-stack report lost a family");
  const [ipv4Report, ipv6Report] = card().querySelectorAll(".ip-quality-report");
  assert.ok(ipv4Report.textContent.includes("干净"), "IPv4 lost its DNS blacklist results");
  assert.ok(ipv6Report.textContent.includes("未检测（仅支持 IPv4）"), "IPv6 blacklist was presented as measured");
  assert.ok(![...ipv6Report.querySelectorAll("dt")].some((term) => term.textContent === "干净"), "IPv4 DNS counts leaked into the IPv6 summary");
  assert.equal(JSON.parse(ipv6Report.querySelector(".ip-quality-raw pre").textContent).Mail.DNSBlacklist.Total, 439,
    "original IPv6 JSON was rewritten");
  assert.ok(card().textContent.includes("0.47%"), "provider scores were rewritten");
  assert.ok(card().textContent.includes("未知"), "missing result became a zero score");
  assert.equal(card().querySelector("script"), null, "upstream text became executable HTML");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "IP quality page overflows the viewport");
  assert.ok(card().getBoundingClientRect().width <= innerWidth, "report card exceeds screen width");
  if (readonly) {
    assert.equal(document.querySelector("[data-ip-quality-run]"), null, "read-only session has mutation controls");
    assert.equal(document.querySelector("[data-ip-quality-schedule]"), null, "read-only session can enable a schedule");
  } else {
    assert.equal(card().querySelector("[data-ip-quality-schedule]").getAttribute("aria-pressed"), "true",
      "an existing administrator plan was shown as disabled");
  }
  const dateSummary = document.querySelector("[data-ip-quality-date]");
  if (mode === "ip-quality-mobile") assert.ok(dateSummary.getBoundingClientRect().width >= 150,
    "mobile history date is too narrow to read");
  assert.equal(dateSummary.dataset.selectedDate, "", "latest view was presented as today's results");
  dateSummary.click();
  const calendar = document.querySelector("[data-ip-quality-calendar]");
  assert.ok(calendar.open, "history calendar did not open");
  const popover = calendar.querySelector(".ip-quality-calendar-popover");
  const bounds = popover.getBoundingClientRect();
  assert.ok(bounds.left >= 0 && bounds.right <= innerWidth, "calendar overflows the viewport");
  const unrecorded = calendar.querySelector("[data-ip-quality-date-option]:disabled");
  assert.ok(unrecorded && Number(getComputedStyle(unrecorded).opacity) < 0.6, "unrecorded days are not visibly disabled");
  const readsBeforeDisabled = fixture.calls.filter((call) => call.path === "/ip-quality" && call.method === "GET").length;
  unrecorded.click();
  assert.equal(fixture.calls.filter((call) => call.path === "/ip-quality" && call.method === "GET").length, readsBeforeDisabled,
    "disabled date requested history");
  const currentMonthLabel = popover.querySelector("header strong").textContent;
  calendar.querySelector('[data-ip-quality-month][aria-label="上个月"]').click();
  assert.notEqual(popover.querySelector("header strong").textContent, currentMonthLabel, "calendar month did not change");
  calendar.querySelector('[data-ip-quality-month][aria-label="下个月"]').click();
  assert.equal(popover.querySelector("header strong").textContent, currentMonthLabel, "calendar month did not return");
  assert.equal(fixture.calls.filter((call) => call.path === "/ip-quality" && call.method === "GET").length, readsBeforeDisabled,
    "browsing months requested report data");
  calendar.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert.ok(!calendar.open && document.activeElement === dateSummary, "Escape did not close the calendar and restore focus");
  // Disabled dates and month browsing must leave the real poller active.
  await delay(2000);
  await waitFor(() => fixture.calls.filter((call) => call.path === "/ip-quality" && call.method === "GET").length > readsBeforeDisabled,
    "calendar interaction stopped automatic refresh");
  const refresh = async () => {
    const before = fixture.calls.filter((call) => call.path === "/ip-quality").length;
    document.querySelector("[data-ip-quality-refresh]").click();
    await waitFor(() => fixture.calls.filter((call) => call.path === "/ip-quality").length > before &&
      !document.querySelector("[data-ip-quality-refresh]").disabled, "refresh did not finish");
  };
  await refresh();
  assert.ok(card().querySelector(".ip-quality-details").open, "refresh closed an expanded report");
  if (!readonly) {
    // Selecting a node repaints the panel in place and moves the sidebar highlight.
    for (const [id, reason] of [["quality-b", "legacy Agent is executable"], ["quality-c", "offline Agent is executable"]]) {
      sidebarLink(id).click();
      await waitFor(() => panel(id), `selecting ${id} did not switch the panel`);
      assert.ok(panel(id).querySelector("[data-ip-quality-run]").disabled, reason);
      if (id === "quality-c") assert.equal(panel(id).querySelector("time").dateTime, `${nextIPQualityDay(today, -3)}T06:05:00Z`,
        "latest view used one shared date for different nodes");
      assert.ok(sidebarLink(id).classList.contains("active"), `${id} was not highlighted after selection`);
      assert.equal(document.querySelectorAll("[data-ip-quality-panel]").length, 1, "selection left stale panels behind");
    }
    sidebarLink("quality-a").click();
    await waitFor(() => card(), "returning to the recorded node failed");
  }
  document.querySelector('[data-ip-quality-day="-1"]').click();
  await waitFor(() => document.querySelector("[data-ip-quality-date]").dataset.selectedDate === yesterday &&
    card()?.textContent.includes("检测失败"), "previous-day navigation did not load history");
  assert.equal(card().querySelector(".ip-quality-report"), null, "previous date retained today's report");
  assert.equal(sidebar().querySelectorAll("[data-ip-quality-agent]").length, 1, "history lists nodes with no records");
  assert.equal(document.querySelectorAll("[data-ip-quality-panel]").length, 1, "history rendered more than one node");
  assert.equal(document.querySelector("[data-ip-quality-run]"), null, "history has a run button");
  assert.equal(document.querySelector("[data-ip-quality-schedule]"), null, "history can change schedules");
  assert.ok(!card().textContent.includes("节点在线"), "history displays a current online state");
  document.querySelector('[data-ip-quality-day="-1"]').click();
  await waitFor(() => document.querySelector("[data-ip-quality-date]").dataset.selectedDate === offlineDay && panel("quality-c"),
    "history navigation did not skip the unrecorded day");
  document.querySelector("[data-ip-quality-date]").click();
  // The relative fixture dates may fall in adjacent months near a month boundary.
  if (reportDay.slice(0, 7) !== offlineDay.slice(0, 7))
    document.querySelector('[data-ip-quality-month][aria-label="上个月"]').click();
  document.querySelector(`[data-ip-quality-date-option="${reportDay}"]`).click();
  await waitFor(() => document.querySelector("[data-ip-quality-date]").dataset.selectedDate === reportDay && card(),
    "available calendar date did not load history");
  assert.ok(!document.querySelector("[data-ip-quality-calendar]").open, "selecting a date left the calendar open");
  document.querySelector('[data-ip-quality-day="-1"]').click();
  await waitFor(() => document.querySelector("[data-ip-quality-date]").dataset.selectedDate === olderDay &&
    card()?.querySelector("time")?.dateTime === `${olderDay}T06:05:00Z`, "older history did not load");
  fixture.prunedBefore = reportDay;
  await refresh();
  document.querySelector("[data-ip-quality-date]").click();
  const retainedDate = document.querySelector(`[data-ip-quality-date-option="${reportDay}"]`);
  assert.ok(retainedDate && !retainedDate.disabled, "pruning left the calendar outside the retained date range");
  assert.ok(document.querySelector('[data-ip-quality-month][aria-label="上个月"]').disabled,
    "pruned calendar allowed navigation before its oldest retained month");
  retainedDate.click();
  await waitFor(() => document.querySelector("[data-ip-quality-date]").dataset.selectedDate === reportDay && card(),
    "calendar could not select a retained date after pruning");
  fixture.prunedBefore = "";
  document.querySelector("[data-ip-quality-latest]").click();
  await waitFor(() => card()?.textContent.includes("已完成") && !document.querySelector("[data-ip-quality-refresh]").disabled,
    "return to latest results failed");
  fixture.failed = true;
  await refresh();
  assert.ok(document.querySelector(".ip-quality-alert")?.textContent.includes("检测记录暂不可用"), "API error was hidden");
  assert.ok(!document.body.textContent.includes("演示数据"), "API failure fabricated demo results");
  if (!readonly) assert.ok(card().querySelector("[data-ip-quality-run]").disabled, "failed refresh allowed mutation");
  fixture.failed = false;
  await refresh();
  if (!readonly) {
    const confirm = async (external = true) => {
      const dialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "confirmation did not open");
      if (external) assert.ok(dialog.textContent.includes("第三方"), "confirmation omitted external service access");
      dialog.querySelector("[data-confirm-accept]").click();
    };
    card().querySelector("[data-ip-quality-run]").click();
    await confirm();
    await waitFor(() => card()?.textContent.includes("等待执行"), "queued check was not shown");
    assert.equal(fixture.checks, 1, "check was submitted more than once");
    assert.ok(card().querySelector("[data-ip-quality-run]").disabled, "active check can be duplicated");
    assert.ok(card().textContent.includes("上次成功检测") && card().querySelector("time").dateTime === `${reportDay}T06:05:00Z`,
      "fresh check discarded the previous report");
    assert.equal(card().querySelectorAll(".ip-quality-archive img").length, 2, "fresh check removed the previous archives");
    assert.ok(card().querySelector(".ip-quality-archive img").src.includes("quality-task/archives"),
      "previous archive was attributed to the pending task");
    fixture.records[0].status = "failed";
    fixture.records[0].error = "本次检测超时";
    await refresh();
    assert.ok(card().textContent.includes("最新检测未成功") && card().textContent.includes("本次检测超时"));
    assert.ok(!card().querySelector("[data-ip-quality-run]").disabled, "failed check prevented retry");
    fixture.records = [completeRecord("new-quality-task", today)];
    await refresh();
    assert.ok(!card().textContent.includes("上次成功检测"), "completed check still shows the previous report");
    card().querySelector("[data-ip-quality-schedule]").click();
    await confirm(false);
    await waitFor(() => card()?.querySelector("[data-ip-quality-schedule]").getAttribute("aria-pressed") === "false",
      "existing daily schedule could not be disabled");
    assert.equal(fixture.scheduleWrites, 1, "schedule disable was submitted more than once");
    card().querySelector("[data-ip-quality-schedule]").click();
    await confirm();
    await waitFor(() => card()?.querySelector("[data-ip-quality-schedule]").getAttribute("aria-pressed") === "true", "daily schedule did not enable");
    assert.equal(fixture.scheduleWrites, 2, "schedule enable was submitted more than once");
  }
  card().querySelector(".ip-quality-details>summary").click();
  await delay(60);
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "expanded report overflows on mobile");
  assert.equal(violations.length, 0, `production CSP violations: ${violations.join(", ")}`);
  await testIPQualityScenarios({ fixture, refresh, card });
  if (preview) await new Promise(() => {});
}
