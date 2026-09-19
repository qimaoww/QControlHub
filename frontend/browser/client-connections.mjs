import { assert, waitFor } from "./assertions.mjs";

export async function testClientConnectionsRuntime(preview = false) {
  const fallback = window.fetch.bind(window), requests = [];
  const now = new Date().toISOString();
  const row = { id: 2, agent_id: "alpha", agent_name: "Alpha · 东京", engine: "xray", protocol: "vless", inbound: "vless-443", transport: "tcp", client_ip: "2001:db8::8", location: { country_code: "CN", country: "China", province: "广东" }, client_port: 52000, local_ip: "192.0.2.1", local_port: 443, first_seen: now, last_seen: now };
  window.fetch = async (input, options) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.href);
    if (url.pathname === "/api/v1/client-connections") {
      requests.push(url.searchParams);
      const older = url.searchParams.has("before");
      return new Response(JSON.stringify({
        records: [{ ...row, id: older ? 1 : 2, client_port: older ? 51000 : 52000 }],
        ips: 1, flows: 2, next_before: older ? undefined : 2,
        sources: [{ agent_id: "alpha", agent_name: row.agent_name, updated_at: now, status: "partial", detail: "UDP: conntrack unavailable or incomplete", truncated: false }, { agent_id: "bravo", agent_name: "Bravo · 新加坡", status: "unsupported" }],
        timeline: Array.from({ length: 24 }, (_, i) => ({ time: new Date(Date.now() - (23 - i) * 3600000).toISOString(), flows: i % 3 === 0 ? 1 : 2, ips: 1 })),
      }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    return fallback(input, options);
  };
  location.hash = "#client-connections";
  await import("../app.js");
  await waitFor(() => document.querySelector("tbody")?.textContent.includes("52000"), "connection page did not load");
  assert.match(document.querySelector("tbody").textContent, /中国 · 广东/);
  assert.ok(document.querySelector('a[href="#client-connections"]'), "connection navigation missing");
  assert.ok(document.body.classList.contains("no-context"), "connection page must not show the configuration sidebar");
  assert.match(document.querySelector(".connection-summary").textContent, /观测连接 2/g);
  assert.ok(document.querySelector('.connection-sources [title*="conntrack"]'));
  assert.equal(document.querySelectorAll(".client-connections p").length, 0, "connection page should not contain explanatory paragraphs");
  const form = document.querySelector("[data-connection-filters]");
  form.elements.client_ip.value = "2001:db8::8";
  document.querySelector(".connection-advanced").open = true;
  form.elements.inbound.value = "vless-443";
  form.elements.port.value = "443";
  const beforeQuery = requests.length;
  form.requestSubmit();
  await waitFor(() => requests.length > beforeQuery && !document.querySelector('[type="submit"]').disabled, "filter request missing");
  assert.equal(requests.at(-1).get("client_ip"), "2001:db8::8");
  assert.equal(requests.at(-1).get("inbound"), "vless-443");
  assert.equal(requests.at(-1).get("port"), "443");
  document.querySelector("[data-connection-next]").click();
  await waitFor(() => document.querySelector("tbody")?.textContent.includes("51000"), "next page missing");
  assert.equal(requests.at(-1).get("before"), "2");
  assert.match(document.querySelector(".connection-summary").textContent, /观测连接 2/g);
  document.querySelector("[data-connection-previous]").click();
  await waitFor(() => document.querySelector("tbody")?.textContent.includes("52000"), "previous page missing");
  assert.ok(!requests.at(-1).has("before"));
  const bucket = document.querySelector("[data-connection-bucket]");
  bucket.value = "day";
  const beforeBucket = requests.length;
  bucket.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => requests.length > beforeBucket && !document.querySelector('[type="submit"]').disabled, "time bucket change missing");
  assert.equal(requests.at(-1).get("bucket"), "day");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "connection page overflows viewport");

}
