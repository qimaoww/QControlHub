import { assert, delay, waitFor } from "./assertions.mjs";
import { ipQualityToday, nextIPQualityDay } from "../modules/ip-quality-model.js";

export async function testIPQualityRuntime(mode, preview = false) {
  const readonly = mode === "ip-quality-readonly";
  if (mode === "ip-quality-mobile") {
    assert.ok(innerWidth <= 480 && matchMedia("(pointer:coarse)").matches,
      "IP quality mobile scenario requires a real narrow touch viewport");
  }
  const today = ipQualityToday(), yesterday = nextIPQualityDay(today, -1);
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
  const completeRecord = (taskID = "quality-task") => ({
    task_id: taskID, agent_id: "quality-a", status: "succeeded",
    created_at: `${today}T06:00:00Z`, finished_at: `${today}T06:05:00Z`,
    archives: [4, 6].map((family) => ({ family, rendered_at: `${today}T06:05:00Z`, sha256: "a".repeat(64) })),
    result: { reports: [report("203.0.113.10"), report("2001:db8:1234:5678:90ab:cdef:1234:5678")] },
  });
  const fixture = {
    records: [completeRecord()],
    // A plan enabled by another administrator is still the node's active plan.
    schedules: [{ agent_id: "quality-a", enabled: true, next_run_at: new Date(Date.now() + 86400000).toISOString() }],
    calls: [], failed: false, checks: 0, scheduleWrites: 0,
  };
  window.__ipQualityFixture = fixture;
  const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
  window.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const method = options.method || "GET";
    fixture.calls.push({ path, method, search: url.search });
    if (path === "/auth/session") return json(session);
    if (path === "/agent-access") return json({ isolated: false, shares: [] });
    if (path === "/overview") return json({ agents: 3, agents_online: 2 });
    if (path === "/settings") return json({ panel_name: "QControlHub", ui_font_scale: 100, time_display: "absolute" });
    if (path === "/agents") return json(agents);
    if (path === "/ip-quality" && method === "GET") {
      if (fixture.failed) return json({ error: "检测记录暂不可用" }, 503);
      const date = url.searchParams.get("date");
      return json({ date, timezone: url.searchParams.get("timezone"), records: date === today ? fixture.records : [], schedules: fixture.schedules });
    }
    if (path === "/ip-quality" && method === "POST") {
      assert.ok(!readonly, "read-only page submitted a check");
      assert.equal(JSON.parse(options.body).agent_id, "quality-a");
      fixture.checks += 1;
      fixture.records = [{ task_id: "new-quality-task", agent_id: "quality-a", status: "pending", created_at: new Date().toISOString() }];
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
  const card = () => document.querySelector('[data-ip-quality-agent="quality-a"]');
  await waitFor(() => card()?.textContent.includes("已完成"), "IP quality page did not load");
  assert.equal(document.querySelectorAll(".ip-quality-node-card").length, 3, "shared host was exposed");
  assert.ok(document.querySelector('.dock-nav a[href="#ip-quality"]'), "IP quality navigation is missing");
  assert.ok(document.querySelector('[data-ip-quality-day="1"]').disabled, "future day is selectable");
  card().querySelector(".ip-quality-details>summary").click();
  const images = [...card().querySelectorAll(".ip-quality-archive img")];
  assert.equal(images.length, 2, "database report images are missing");
  await waitFor(() => images.every((image) => image.complete && image.naturalWidth > 0), "archived SVG images failed to load");
  assert.ok(images.every((image) => image.src.startsWith(location.origin+"/api/v1/ip-quality/")), "preview fetched an upstream URL");
  assert.equal(card().querySelectorAll(".ip-quality-report").length, 2, "dual-stack report lost a family");
  const [ipv4Report, ipv6Report] = card().querySelectorAll(".ip-quality-report");
  assert.ok(ipv4Report.textContent.includes("干净"), "IPv4 lost its DNS blacklist results");
  assert.ok(ipv6Report.textContent.includes("未检测：当前上游仅查询 IPv4 DNS 黑名单"), "IPv6 blacklist was presented as measured");
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
    assert.ok(document.querySelector('[data-ip-quality-run="quality-b"]').disabled, "legacy Agent is executable");
    assert.ok(document.querySelector('[data-ip-quality-run="quality-c"]').disabled, "offline Agent is executable");
  }
  const dateInput = document.querySelector("[data-ip-quality-date]");
  const readsBeforeClear = fixture.calls.filter((call) => call.path === "/ip-quality" && call.method === "GET").length;
  dateInput.value = "";
  dateInput.dispatchEvent(new Event("change", { bubbles: true }));
  assert.equal(dateInput.value, today, "clearing the date did not restore the selected day");
  // The real five-second timer must survive rejected input, not just a manual
  // refresh. Leave enough of its interval for the shared four-second wait.
  await delay(2000);
  await waitFor(() => fixture.calls.filter((call) => call.path === "/ip-quality" && call.method === "GET").length > readsBeforeClear,
    "clearing the date stopped automatic refresh");
  const refresh = async () => {
    const before = fixture.calls.filter((call) => call.path === "/ip-quality").length;
    document.querySelector("[data-ip-quality-refresh]").click();
    await waitFor(() => fixture.calls.filter((call) => call.path === "/ip-quality").length > before &&
      !document.querySelector("[data-ip-quality-refresh]").disabled, "refresh did not finish");
  };
  await refresh();
  assert.ok(card().querySelector(".ip-quality-details").open, "refresh closed an expanded report");
  document.querySelector('[data-ip-quality-day="-1"]').click();
  await waitFor(() => document.querySelector("[data-ip-quality-date]").value === yesterday &&
    card()?.textContent.includes("当天未检测"), "previous-day navigation did not load history");
  assert.equal(card().querySelector(".ip-quality-report"), null, "previous date retained today's report");
  document.querySelector('[data-ip-quality-day="1"]').click();
  await waitFor(() => card()?.textContent.includes("已完成"), "return to today failed");
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
    fixture.records = [completeRecord("new-quality-task")];
    await refresh();
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
  if (preview) await new Promise(() => {});
}
