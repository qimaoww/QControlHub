import { assert, waitFor } from "./assertions.mjs";
export async function testAgentOverview({ testAPI }) {

await waitFor(() => document.querySelector(".node-card-grid"), "聚合页没有渲染节点卡片");
  const komariInline = await waitFor(
    () => {
      const inline = document.querySelector('[data-komari-link="alpha"]');
      return inline?.querySelector("[data-komari-traffic]")?.textContent ===
        "4.0 GB / 10.0 GB"
        ? inline
        : null;
    },
    "Komari 流量没有载入网络资源格",
  );
  assert.equal(komariInline.closest(".node-card-network") !== null, true);
  assert.equal(
    komariInline.querySelector("[data-komari-traffic]").textContent,
    "4.0 GB / 10.0 GB",
  );
  assert.match(
    komariInline.querySelector("[data-komari-cycle]").textContent,
    /\d{1,2}\.\d{1,2}–\d{1,2}\.\d{1,2}/,
  );
  assert.equal(Number(komariInline.querySelector("[data-komari-progress]").value), 40);
  const alphaAvatar = document.querySelector('[data-agent-node="alpha"] [data-region-avatar]');
  const alphaFlag = await waitFor(
    () => alphaAvatar.querySelector('img[src="/api/v1/region-flags/cn"]'),
    "节点地区没有渲染统一 SVG 旗帜",
  );
  await waitFor(() => alphaFlag.complete && alphaFlag.naturalWidth > 0, "节点地区 SVG 旗帜没有载入");
  assert.equal(alphaAvatar.textContent, "");
  assert.equal(alphaAvatar.classList.contains("has-region"), true);
  assert.equal(alphaAvatar.title, "中国台湾 (TW) · 点击选择国家/地区旗帜");
  assert.equal(alphaAvatar.getAttribute("aria-label"), "选择国家/地区旗帜：中国台湾 (TW)");
  // A node whose region the list already resolved renders its flag without a
  // per-node lookup; that is what keeps a node grid from costing one request
  // per card on a link where a single read takes about a second.
  await waitFor(() => {
    const flag = document.querySelector(
      '[data-agent-node="bravo"] [data-region-avatar] img[src="/api/v1/region-flags/sg"]',
    );
    return flag?.complete && flag.naturalWidth > 0 ? flag : null;
  }, "节点列表内联地区没有渲染旗帜");
  assert.equal(
    testAPI.calls.some(
      (call) => call.method === "GET" && call.path === "/agents/bravo/region",
    ),
    false,
    "列表已解析地区的节点不应再逐节点查询地区",
  );
  await waitFor(() => {
    const inline = document.querySelector('[data-komari-link="delta"]');
    return inline?.querySelector("[data-komari-traffic]")?.textContent ===
      "5.0 GB / 20.0 GB"
      ? inline
      : null;
  }, "节点列表内联 Komari 资源没有渲染月流量");
  assert.equal(
    testAPI.calls.some(
      (call) => call.method === "GET" && call.path === "/agents/delta/komari",
    ),
    false,
    "列表已内联 Komari 资源的节点不应再逐节点读取",
  );
  assert.equal(document.querySelector(".node-card-komari"), null, "Komari 不应再渲染为独立卡片");
  assert.ok(
    komariInline.closest(".node-card-network").querySelector("[data-metric-text=download-rate]"),
    "绑定 Komari UUID 后必须保留原实时网络内容",
  );
  assert.equal(
    komariInline.closest(".node-card-network").querySelector("[data-metric-text=download-total]"),
    null,
    "绑定 Komari UUID 后应只替换原累计网络内容",
  );
  assert.ok(
    document.querySelector('[data-agent-node="bravo"] .node-card-network [data-metric-text="download-rate"]'),
    "未绑定 Komari UUID 的节点必须保留原实时网络内容",
  );
  assert.ok(
    document.querySelector('[data-agent-node="bravo"] .node-card-network [data-metric-text="download-total"]'),
    "未绑定 Komari UUID 的节点必须保留原累计网络内容",
  );

}
