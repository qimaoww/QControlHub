import { testConnectionReadLifecycle } from './connection-read-lifecycle.mjs';
import { assert, waitFor } from "./assertions.mjs";

export async function testClientConnectionsRuntime(preview = false) {
  const fallback = window.fetch.bind(window), requests = [], locationRequests = [];
  const now = new Date().toISOString();
  let releaseLocations, releaseShell, releaseFirstHistory, holdHistory;
  const locationsReady = new Promise(resolve => { releaseLocations = resolve; });
  const shellReady = new Promise(resolve => { releaseShell = resolve; });
  holdHistory = new Promise(resolve => { releaseFirstHistory = resolve; });
  const row = { id: 2, agent_id: "alpha", agent_name: "Alpha · 东京", engine: "xray", protocol: "vless", inbound: "vless-443", transport: "tcp", client_ip: "2001:db8::8", location: { country_code: "CN", country: "China", province: "广东" }, client_port: 52000, local_ip: "192.0.2.1", local_port: 443, endpoints: [{ agent_id: "alpha", agent_name: "Alpha · 东京", engine: "xray", local_port: 443 }, { agent_id: "alpha", agent_name: "Alpha · 东京", engine: "xray", local_port: 8443 }], first_seen: now, last_seen: now };
  const rows = [
    { ...row, id: 10, engine: "mihomo", endpoints: [{ agent_id: "alpha", engine: "mihomo", local_port: 443 }] },
    { ...row, id: 20, engine: "sing-box", endpoints: [{ agent_id: "alpha", engine: "sing-box", local_port: 1443 }] },
    { ...row, id: 30 },
    { ...row, id: 40, agent_id: "bravo", agent_name: "Bravo · 新加坡", endpoints: [{ agent_id: "bravo", engine: "xray", local_port: 9443 }] },
  ];
  window.fetch = async (input, options) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.href);
    if (["/api/v1/settings", "/api/v1/overview"].includes(url.pathname)) await shellReady;
    if (url.pathname === "/api/v1/client-connections") {
      if (url.searchParams.get("locations") === "only") {
        locationRequests.push(url.searchParams);
        await locationsReady;
        return new Response(JSON.stringify({ records: [...rows, ...rows.map(row => ({ ...row, id: row.id + 1 }))] }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      requests.push(url.searchParams);
      if (holdHistory) await holdHistory;
      const older = url.searchParams.get("cursor") === "next-page";
      return new Response(JSON.stringify({
        records: rows.filter(row => (!url.searchParams.get("agent_id") || row.agent_id === url.searchParams.get("agent_id")) && (!url.searchParams.get("engine") || row.engine === url.searchParams.get("engine"))).map(row => ({ ...row, id: older ? row.id + 1 : row.id, first_seen: older ? "2026-09-20T00:00:00Z" : now, location: {} })),
        ips: 1, flows: 2, next_cursor: older ? undefined : "next-page", page_cursor: older ? "second-start" : "first-start",
        sources: [{ agent_id: "alpha", agent_name: row.agent_name, updated_at: now, status: "ok", detail: "panel core logs", truncated: false }, { agent_id: "bravo", agent_name: "Bravo · 新加坡", status: "no_logs" }].filter(source => !url.searchParams.get("agent_id") || source.agent_id === url.searchParams.get("agent_id")),
        timeline: Array.from({ length: 24 }, (_, i) => ({ time: new Date(Date.now() - (23 - i) * 3600000).toISOString(), flows: i % 3 === 0 ? 1 : 2, ips: 1 })),
      }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    return fallback(input, options);
  };
  location.hash = "#client-connections";
  const styles = document.createElement("link"); styles.rel = "stylesheet"; styles.href = "/assets/analytics/theme.css";
  const styled = new Promise((resolve, reject) => { styles.onload = resolve; styles.onerror = reject; });
  document.head.append(styles); await styled;
  // Appearance is loaded before the new theme mounts its page. Location
  // enrichment remains independent of the usable history and filter controls.
  releaseShell();
  const session = await (await fallback("/api/v1/auth/session")).json();
  (await import("../analytics/app.js")).startAnalytics({ app: document.querySelector("#app"), session });
  const region = () => document.querySelector('[aria-label="客户端来源 IP 明细"]');
  const displayed = () => [...region().querySelectorAll("tbody tr")];
  const body = () => region()?.querySelector("tbody");
  const form = () => document.querySelector(".a-main form");
  const button = text => [...document.querySelectorAll(".a-main button")].find(button => button.textContent === text);
  const changed = (name, value) => {
    const control = form().elements[name];
    if (control.type === "checkbox") control.checked = value; else control.value = value;
    control.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const settled = () => document.querySelector(".a-main").textContent.includes("已读取");
  const requested = async (count, message) => waitFor(() => requests.length > count && settled(), message);
  const pageText = () => document.querySelector(".a-main").textContent;
  await waitFor(() => form() && requests.length, "first history read must not block mounting filters");
  assert.ok([...form().elements].filter(control => control.name).every(control => !control.disabled), "first history read disabled filters");
  assert.ok(pageText().includes("正在读取") && !pageText().includes("已读取") && !pageText().includes("暂无记录"), "pending history was reported as a completed empty result");
  assert.ok(pageText().includes("独立 IP —") && !pageText().includes("观测连接 0"), "pending history invented statistics");
  assert.ok(button("上一页").disabled && button("下一页").disabled, "unknown pagination must remain disabled");
  form().elements.client_ip.focus();
  const firstInput = document.activeElement;
  releaseFirstHistory(); holdHistory = null;
  await waitFor(() => body()?.textContent.includes("2001:db8::8"), "connection page did not load");
  assert.equal(document.activeElement, firstInput, "first history arrival replaced the active filter");
  assert.equal(form().elements.agent_id.options.length, 3, "authorized node choices must remain available while geography loads");
  assert.ok([...form().elements].filter(control => control.name).every(control => !control.disabled), "location lookup must not disable filters");
  assert.ok(!body().textContent.includes("广东"));
  const today = new Date();
  const localDate = new Date(+today - today.getTimezoneOffset() * 60000).toISOString().slice(0, 10);
  assert.equal(form().elements.date.value, localDate, "query date defaults to today");
  assert.equal(requests.at(-1).get("since"), new Date(`${localDate}T00:00:00`).toISOString());
  releaseLocations();
  await waitFor(() => body()?.textContent.includes("中国 · 广东"), "location enrichment did not paint");
  assert.ok(!body().textContent.includes("192.0.2.1"));
  assert.ok(!body().textContent.includes("52000"));
  assert.equal(displayed().length, 4, "same source must remain separate by engine and node");
  assert.equal(region().querySelectorAll('td[data-column="客户端来源 IP"] code').length, 4);
  assert.equal(displayed().map(row => row.querySelector('td[data-column="内核"]').textContent).join(","), "Mihomo,sing-box,Xray,Xray");
  assert.equal(displayed()[2].querySelector('td[data-column="节点"]').textContent, "Alpha · 东京", "multiple ports must not duplicate the node name");
  assert.equal(displayed()[2].querySelector('td[data-column="入站端口"]').textContent, "443 · 8443");
  assert.equal(displayed()[3].querySelector('td[data-column="入站端口"]').textContent, "9443");
  assert.equal(requests.at(-1).get("group_by"), "ip");
  assert.equal(locationRequests.at(-1).get("cursor"), "first-start", "enrichment must use the page cursor");
  assert.ok(!body().textContent.includes("未知入站"));
  assert.ok(document.querySelector('a[href="#client-connections"]'), "connection navigation missing");
  assert.ok(pageText().includes("观测连接 2") && pageText().includes("不代表当前在线"), "history must not claim a live connection count");
  document.querySelector('[data-source-status]').click();
  const dialog = document.querySelector('dialog[open]:last-of-type');
  assert.ok(dialog && !dialog.querySelector('summary'), "source status uses a dialog with text expansion buttons");
  assert.ok(dialog.textContent.includes("Bravo · 新加坡"), "missing-log source must be represented");
  dialog.querySelector('[aria-label=关闭]').click();
  const beforeNode = requests.length;
  changed("agent_id", "alpha");
  await requested(beforeNode, "node filter request missing");
  assert.equal(requests.at(-1).get("agent_id"), "alpha");
  assert.equal(form().elements.agent_id.value, "alpha");
  assert.equal(form().elements.agent_id.options.length, 3, "node filtering must retain other authorized choices");
  document.querySelector('[data-source-status]').click();
  assert.equal(document.querySelectorAll('dialog[open] [data-source-group]').length, 1, "source status must follow the selected node");
  document.querySelector('dialog[open] [aria-label=关闭]').click();
  await waitFor(() => body()?.textContent.includes("中国 · 广东"), "node geography did not finish");
  const cachedReads = requests.length, cachedLocations = locationRequests.length;
  changed("agent_id", "");
  assert.equal(displayed().length, 4, "cached all-node rows paint synchronously");
  changed("agent_id", "alpha");
  assert.equal(displayed().length, 3, "cached node rows paint synchronously");
  assert.equal(requests.length, cachedReads, "switching cached nodes does not query history");
  assert.equal(locationRequests.length, cachedLocations, "cached geography is reused");
  let releaseHistory;
  holdHistory = new Promise(resolve => { releaseHistory = resolve; });
  changed("agent_id", "bravo");
  assert.equal(displayed().length, 1, "uncached filter previews matching rows immediately");
  assert.ok(body().textContent.includes("Bravo"));
  assert.ok(!form().elements.agent_id.disabled, "slow read must not disable target selection");
  changed("agent_id", "alpha");
  assert.equal(displayed().length, 3, "can switch to a cached node during a slow read");
  releaseHistory(); holdHistory = null;
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  assert.ok(!body().textContent.includes("Bravo"), "late response must not overwrite the selected node");
  assert.equal(form().elements.include_non_public.checked, false);
  assert.ok(!requests.at(-1).has("include_non_public"));
  form().elements.client_ip.value = "2001:db8::8";
  assert.equal(form().elements.inbound, undefined);
  assert.equal(form().elements.port, undefined);
  form().elements.include_non_public.checked = true;
  const beforeQuery = requests.length;
  form().requestSubmit();
  await requested(beforeQuery, "filter request missing");
  assert.equal(requests.at(-1).get("agent_id"), "alpha", "query must preserve the selected node");
  assert.equal(requests.at(-1).get("client_ip"), "2001:db8::8");
  assert.equal(requests.at(-1).has("inbound"), false);
  assert.equal(requests.at(-1).has("port"), false);
  assert.equal(requests.at(-1).get("include_non_public"), "true");
  assert.equal(form().elements.include_non_public.checked, true);
  button("下一页").click();
  await waitFor(() => pageText().includes("第 2 页") && settled(), "next page missing");
  assert.equal(requests.at(-1).get("cursor"), "next-page");
  assert.equal(requests.at(-1).get("include_non_public"), "true");
  assert.ok(pageText().includes("观测连接 2"));
  const beforeRefresh = requests.length;
  button("刷新").click();
  await requested(beforeRefresh, "refresh did not complete");
  assert.equal(requests.at(-1).get("cursor"), "next-page", "refresh retains the current page");
  assert.equal(requests.at(-1).get("agent_id"), "alpha", "refresh retains the node filter");
  assert.ok(pageText().includes("第 2 页"));
  button("上一页").click();
  await waitFor(() => pageText().includes("第 1 页") && settled(), "previous page missing");
  assert.ok(!requests.at(-1).has("cursor"));
  const beforeRecent = requests.length;
  button("今日").click();
  await requested(beforeRecent, "today query missing");
  assert.ok(!requests.at(-1).has("include_non_public"));
  assert.equal(form().elements.include_non_public.checked, false);
  const beforeMonth = requests.length;
  button("本月").click();
  await requested(beforeMonth, "month query missing");
  assert.equal(requests.at(-1).get("since"), new Date(today.getFullYear(), today.getMonth(), 1).toISOString());
  assert.equal(requests.at(-1).get("until"), new Date(today.getFullYear(), today.getMonth() + 1, 1).toISOString());
  assert.equal(requests.at(-1).get("bucket"), "day");
  assert.equal(requests.at(-1).get("agent_id"), "alpha");
  assert.equal(requests.at(-1).get("client_ip"), "2001:db8::8");
  assert.equal(form().elements.period.value, "month");
  const monthReads = requests.length;
  button("今日").click();
  assert.equal(form().elements.date.value, localDate);
  assert.equal(form().elements.period.value, "day");
  button("本月").click();
  assert.equal(requests.length, monthReads, "today/month switching reuses separate cached queries");
  button("今日").click();
  const beforeDate = requests.length;
  changed("date", "2026-09-01");
  await requested(beforeDate, "date query missing");
  assert.equal(requests.at(-1).get("since"), new Date("2026-09-01T00:00:00").toISOString());
  assert.equal(requests.at(-1).get("until"), new Date("2026-09-02T00:00:00").toISOString());
  assert.equal(form().elements.date.value, "2026-09-01");
  button("今日").click();
  assert.equal(form().elements.date.value, localDate, "today shortcut updates an edited date");
  const beforeAll = requests.length;
  changed("agent_id", "");
  await requested(beforeAll, "all nodes request missing");
  assert.ok(!requests.at(-1).has("agent_id"));
  assert.equal(form().elements.agent_id.value, "");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "connection page overflows viewport");
  await testConnectionReadLifecycle();
}
