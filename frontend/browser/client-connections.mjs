import { assert, waitFor } from "./assertions.mjs";

export async function testClientConnectionsRuntime(preview = false) {
  const fallback = window.fetch.bind(window), requests = [];
  const now = new Date().toISOString();
  let releaseLocations, releaseShell;
  const locationsReady = new Promise(resolve => { releaseLocations = resolve; });
  const shellReady = new Promise(resolve => { releaseShell = resolve; });
  const row = { id: 2, agent_id: "alpha", agent_name: "Alpha · 东京", engine: "xray", protocol: "vless", inbound: "vless-443", transport: "tcp", client_ip: "2001:db8::8", location: { country_code: "CN", country: "China", province: "广东" }, client_port: 52000, local_ip: "192.0.2.1", local_port: 443, first_seen: now, last_seen: now };
  window.fetch = async (input, options) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.href);
    if (["/api/v1/settings", "/api/v1/overview"].includes(url.pathname)) await shellReady;
    if (url.pathname === "/api/v1/client-connections") {
      if (url.searchParams.get("locations") === "only") {
        await locationsReady;
        return new Response(JSON.stringify({ records: [row] }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      requests.push(url.searchParams);
      const older = url.searchParams.has("before");
      return new Response(JSON.stringify({
        records: [{ ...row, id: older ? 1 : 2, first_seen: older ? "2026-09-20T00:00:00Z" : now, location: {} }],
        ips: 1, flows: 2, next_before: older ? undefined : 2,
        sources: [{ agent_id: "alpha", agent_name: row.agent_name, updated_at: now, status: "ok", detail: "panel core logs", truncated: false }, { agent_id: "bravo", agent_name: "Bravo · 新加坡", status: "no_logs" }].filter(source => !url.searchParams.get("agent_id") || source.agent_id === url.searchParams.get("agent_id")),
        timeline: Array.from({ length: 24 }, (_, i) => ({ time: new Date(Date.now() - (23 - i) * 3600000).toISOString(), flows: i % 3 === 0 ? 1 : 2, ips: 1 })),
      }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    return fallback(input, options);
  };
  location.hash = "#client-connections";
  await import("../app.js");
  await waitFor(() => document.querySelector("tbody")?.textContent.includes("2001:db8::8"), "connection page did not load");
  assert.ok(document.querySelector('.context-sidebar [data-connection-agent]'), "node sidebar should render before shared shell reads finish");
  assert.ok(!document.querySelector('[type="submit"]').disabled, "history must be usable before location lookup completes");
  assert.ok(!document.querySelector("tbody").textContent.includes("广东"));
  releaseShell();
  releaseLocations();
  await waitFor(() => document.querySelector("tbody")?.textContent.includes("中国 · 广东"), "location enrichment did not paint");
  assert.ok(!document.querySelector("tbody").textContent.includes("192.0.2.1"));
  assert.ok(!document.querySelector("tbody").textContent.includes("52000"));
  assert.ok(!document.querySelector("tbody").textContent.includes("未知入站"));
  assert.ok(document.querySelector('a[href="#client-connections"]'), "connection navigation missing");
  assert.ok(!document.body.classList.contains("no-context"), "connection page must show filter sidebar");
  assert.ok(document.querySelector(".context-sidebar [data-connection-agent]"), "node filters belong in context sidebar");
  assert.ok(!document.querySelector(".context-sidebar form"), "sidebar must use node navigation, not a filter form");
  document.querySelector(".connection-sources").open = true;
  assert.match(document.querySelector(".connection-source-detail").textContent, /面板保存的内核日志/);
  assert.match(document.querySelector(".connection-summary").textContent, /观测连接 2/g);
  assert.ok(document.querySelector('.connection-sources [title*="panel core logs"]'));
  assert.equal(document.querySelectorAll(".client-connections p").length, 0, "connection page should not contain explanatory paragraphs");
  const beforeNode = requests.length;
  document.querySelector('[data-connection-agent="alpha"]').click();
  await waitFor(() => requests.length > beforeNode && !document.querySelector('[type="submit"]').disabled, "node filter request missing");
  assert.equal(requests.at(-1).get("agent_id"), "alpha");
  assert.ok(document.querySelector('[data-connection-agent="alpha"]').classList.contains("active"));
  assert.equal(document.querySelectorAll(".connection-source").length, 1, "status must follow selected node");
  assert.equal(document.querySelectorAll(".context-list [data-connection-agent]").length, 2, "sidebar must retain other nodes");
  document.querySelector(".connection-filter-panel").open = true;
  const form = document.querySelector("[data-connection-filters]");
  assert.equal(form.elements.include_non_public.value, "");
  assert.ok(!requests.at(-1).has("include_non_public"));
  form.elements.client_ip.value = "2001:db8::8";
  document.querySelector(".connection-advanced").open = true;
  assert.equal(form.elements.inbound, undefined);
  assert.equal(form.elements.port, undefined);
  form.elements.include_non_public.value = "true";
  const beforeQuery = requests.length;
  form.requestSubmit();
  await waitFor(() => requests.length > beforeQuery && !document.querySelector('[type="submit"]').disabled, "filter request missing");
  assert.equal(requests.at(-1).get("agent_id"), "alpha", "query form must preserve sidebar node filter");
  assert.equal(requests.at(-1).get("client_ip"), "2001:db8::8");
  assert.equal(requests.at(-1).has("inbound"), false);
  assert.equal(requests.at(-1).has("port"), false);
  assert.equal(requests.at(-1).get("include_non_public"), "true");
  assert.equal(document.querySelector('[name="include_non_public"]').value, "true");
  document.querySelector("[data-connection-next]").click();
  await waitFor(() => document.querySelector(".connection-pagination")?.textContent.includes("第 2 页") && !document.querySelector("[data-connection-refresh]")?.disabled, "next page missing");
  assert.equal(requests.at(-1).get("before"), "2");
  assert.equal(requests.at(-1).get("include_non_public"), "true");
  assert.match(document.querySelector(".connection-summary").textContent, /观测连接 2/g);
  const beforeRefresh = requests.length;
  document.querySelector("[data-connection-refresh]").click();
  await waitFor(() => requests.length > beforeRefresh && !document.querySelector("[data-connection-refresh]").disabled, "refresh did not complete");
  assert.equal(requests.at(-1).get("before"), "2", "refresh should retain the current page");
  assert.equal(requests.at(-1).get("agent_id"), "alpha", "refresh should retain the node filter");
  assert.match(document.querySelector(".connection-pagination").textContent, /第 2 页/);
  document.querySelector("[data-connection-previous]").click();
  await waitFor(() => document.querySelector(".connection-pagination")?.textContent.includes("第 1 页") && !document.querySelector("[data-connection-refresh]")?.disabled, "previous page missing");
  assert.ok(!requests.at(-1).has("before"));
  const beforeRecent = requests.length;
  document.querySelector("[data-connection-recent]").click();
  await waitFor(() => requests.length > beforeRecent && !document.querySelector('[type="submit"]').disabled, "recent query missing");
  assert.ok(!requests.at(-1).has("include_non_public"));
  assert.equal(document.querySelector('[name="include_non_public"]').value, "");
  const beforeAll = requests.length;
  document.querySelector('[data-connection-agent=""]').click();
  await waitFor(() => requests.length > beforeAll && !document.querySelector('[type="submit"]').disabled, "all nodes request missing");
  assert.ok(!requests.at(-1).has("agent_id"));
  assert.ok(document.querySelector('[data-connection-agent=""]').classList.contains("active"));
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "connection page overflows viewport");

}
