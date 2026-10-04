import { assert, waitFor } from "./assertions.mjs";

export function nodeCardFixture(onlineAgent) {
  const GiB = 1024 ** 3;
  const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
  const names = ["VMShell HK", "Zouter HK", "DataWave HK", "Catixs HK", "legendvps SG", "Datawave SG", "VMISS US — a very long node name that must not push the status away", "AKKO SJC"];
  return names.map((name, index) => {
    const id = ["alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"][index];
    const now = new Date().toISOString();
    const quota = index === 0 || index === 5;
    return {
      ...onlineAgent(id), name, os: "Debian", region_code: index < 4 ? "HK" : index < 6 ? "SG" : "US",
      version: "fd969c20da8699177cf061ce5c249c9e76f1cfe2", capabilities: engines,
      status: index === 7 ? "offline" : "online", can_manage: index !== 7,
      last_seen: index === 7 ? new Date(Date.now() - 3600000).toISOString() : now,
      labels: quota ? { komari_uuid: `komari-${id}` } : {},
      ...(quota ? { komari: { uuid: `komari-${id}`, traffic_used_available: index === 0, traffic_used: 322.8 * GiB, traffic_limit: 1000 * GiB, traffic_reset_day: 11 } } : {}),
      runtime: Object.fromEntries(engines.map((engine, slot) => [engine, {
        installed: (index + slot) % 5 !== 3,
        service_status: (index + slot) % 4 === 1 ? "inactive" : "active",
      }])),
      metrics: index === 6 ? {} : {
        collected_at: index === 7 ? new Date(Date.now() - 3600000).toISOString() : now,
        cpu_available: true, cpu_percent: [15.5, 3.3, 4.3, 0, 78, 96, 0, 4.6][index],
        memory_available: true, memory_used_bytes: (126.2 + index * 26) * 1024 ** 2, memory_total_bytes: GiB,
        disk_available: true, disk_used_bytes: (2.5 + index / 3) * GiB, disk_total_bytes: 9.7 * GiB,
        network_available: true, network_rx_bps: index === 0 ? 0 : 164 * 1024, network_tx_bps: index === 0 ? 0 : 156 * 1024,
        network_rx_bytes: 288.6 * GiB, network_tx_bytes: 279.7 * GiB,
        // Public DNS literals exercise address validation without contacting them.
        public_ipv4: index % 2 === 0 ? "8.8.8.8" : "9.9.9.9", public_ipv4_source: "agent-config",
        ...(index % 3 ? { public_ipv6: "2001:4860:4860:0000:0000:0000:0000:8888", public_ipv6_source: "agent-config" } : {}),
      },
    };
  });
}

export async function testNodeCardLayout({ testAPI }, preview) {
  const cards = await waitFor(() => {
    const cards = [...document.querySelectorAll(".node-card-overview")];
    return cards.length === 8 && cards;
  }, "node overview did not load");
  const grid = document.querySelector(".node-card-grid");
  const alpha = cards[0];
  await waitFor(() => alpha.querySelector("[data-komari-traffic]")?.textContent === "322.8 GB / 1000.0 GB", "inline quota did not render");
  await waitFor(() => cards[5].querySelector('[data-metric-progress="cpu"]').dataset.level === "danger", "usage thresholds did not refresh");
  await Promise.all(document.getAnimations().filter(animation => animation.effect?.getTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {})));
  for (const card of cards) {
    const frame = card.getBoundingClientRect();
    assert.ok(card.scrollWidth <= card.clientWidth + 1, "node card overflows horizontally");
    for (const selector of [".node-card-head", ".node-card-ips", ".node-card-resources", ".node-card-cores", ".node-card-foot"]) {
      const bounds = card.querySelector(selector).getBoundingClientRect();
      assert.ok(bounds.left >= frame.left && bounds.right <= frame.right, `${selector} escapes the card`);
    }
    assert.equal(card.querySelectorAll(".node-card-resource").length, 3);
    assert.equal(getComputedStyle(card.querySelector(".node-card-resources")).gridTemplateColumns.split(" ").length, 3);
    assert.equal(getComputedStyle(card.querySelector(".node-card-cores")).gridTemplateColumns.split(" ").length, 4);
    assert.equal(card.querySelectorAll(".core-chip").length, 4);
  }
  assert.ok(grid.scrollWidth <= grid.clientWidth + 1, "node grid overflows horizontally");
  assert.ok(alpha.getBoundingClientRect().height <= 365, "overview card has excessive vertical spacing");
  if (innerWidth >= 1900) {
    assert.equal(getComputedStyle(grid).gridTemplateColumns.split(" ").length, 5, "wide screens should show five compact node cards");
  }
  const firstRow = cards.filter(card => Math.abs(card.offsetTop - alpha.offsetTop) < 2);
  for (const selector of [".node-card-resources", ".node-card-network", ".node-card-cores", ".node-card-foot"]) {
    const top = alpha.querySelector(selector).getBoundingClientRect().top;
    assert.ok(firstRow.every(card => Math.abs(card.querySelector(selector).getBoundingClientRect().top - top) < 2), `${selector} must align across mixed address and quota states`);
  }
  const missing = cards[6];
  assert.equal(missing.querySelector('[data-metric-text="memory-used"]').textContent, "等待采集");
  assert.equal(missing.querySelectorAll(".card-ip-row:not([hidden])").length, 0);
  assert.equal(missing.querySelector(".node-card-title strong").title, testAPI.agents[6].name);
  assert.equal(cards[7].dataset.state, "offline");
  assert.equal(cards[7].querySelector(".node-card-open").textContent, "查看节点");
  assert.equal(cards[3].querySelector('[data-metric-progress="cpu"]').value, 0);
  assert.ok(alpha.querySelector("[data-metric-text=download-rate]"), "quota must not replace live rates");
  assert.ok(cards[1].querySelector("[data-metric-text=download-total]"), "unlinked cards need cumulative totals");
  const root = document.documentElement;
  const originalScale = root.style.getPropertyValue("--ui-font-scale");
  root.style.setProperty("--ui-font-scale", "1.25");
  for (const card of cards) {
    for (const value of card.querySelectorAll(".node-card-resource strong, .node-card-resource small, .node-card-rates strong")) {
      assert.ok(value.scrollWidth <= value.clientWidth + 1, "resource values are clipped at a larger font size");
    }
  }
  if (originalScale) root.style.setProperty("--ui-font-scale", originalScale);
  else root.style.removeProperty("--ui-font-scale");
  const originalTheme = root.dataset.theme;
  for (const theme of ["light", "dark"]) {
    root.dataset.theme = theme;
    const style = getComputedStyle(alpha);
    assert.equal(getComputedStyle(alpha.querySelector(".core-chip .engine-badge")).borderTopWidth, "0px", "engine labels should not restore nested badge borders");
    assert.ok(parseFloat(getComputedStyle(alpha.querySelector(".node-card-resource strong")).fontSize) > parseFloat(getComputedStyle(alpha.querySelector(".node-card-resource > span")).fontSize), "resource values need a stronger hierarchy than labels");
    assert.notEqual(style.getPropertyValue("--ink"), style.getPropertyValue("--surface"), "theme must retain readable ink");
  }
  root.dataset.theme = originalTheme;
  if (preview) return;

  const copy = alpha.querySelector("[data-copy-ip]");
  const hash = location.hash;
  copy.click();
  await waitFor(() => copy.classList.contains("copied"), "copy feedback did not appear");
  assert.equal(location.hash, hash, "copy must not navigate away from the card grid");

  const metrics = testAPI.agents[0].metrics;
  metrics.memory_used_bytes = 512 * 1024 ** 2;
  metrics.memory_total_bytes = 2 * 1024 ** 3;
  metrics.disk_used_bytes = 3 * 1024 ** 3;
  metrics.disk_total_bytes = 12 * 1024 ** 3;
  metrics.cpu_percent = 95;
  await waitFor(() => alpha.querySelector('[data-metric-text="memory-used"]').textContent === "512.0 MB", "split memory value did not update");
  assert.equal(alpha.querySelector('[data-metric-text="memory-capacity"]').textContent, "共 2.0 GB");
  assert.equal(alpha.querySelector('[data-metric-text="disk-used"]').textContent, "3.0 GB");
  assert.equal(alpha.querySelector('[data-metric-text="disk-capacity"]').textContent, "共 12.0 GB");
  assert.equal(alpha.querySelector('[data-metric-progress="cpu"]').dataset.level, "danger");
  metrics.memory_available = false;
  await waitFor(() => alpha.querySelector('[data-metric-text="memory-used"]').textContent === "不可用", "unavailable metric retained a live value");
  assert.equal(alpha.querySelector('[data-metric-text="memory-capacity"]').textContent, "—");
  assert.equal(alpha.querySelector('[data-metric-progress="memory"]').value, 0);
}
