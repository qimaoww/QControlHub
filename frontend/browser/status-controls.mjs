import { agentPresenceMarkup } from "../modules/agent-presence.js";
import { serviceStatusInfo, serviceRuntimeStatus } from "../modules/service-status.js";
import { assert, waitFor } from "./assertions.mjs";

const statusSelector = ".agent-presence, .engine-state, .live-engine-status, .status-label, .traffic-policy-status";
export const settlePaint = async () => {
  const animations = [...document.querySelectorAll(statusSelector)].flatMap(pill => {
    getComputedStyle(pill).color; // Flush theme changes before collecting transitions.
    return pill.getAnimations();
  });
  await Promise.race([
    Promise.all(animations.map(animation => animation.finished.catch(() => {}))),
    new Promise(resolve => setTimeout(resolve, 1000)),
  ]);
};
const canvas = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
const luminance = color => {
  canvas.fillStyle = color;
  canvas.fillRect(0, 0, 1, 1);
  const [r, g, b] = [...canvas.getImageData(0, 0, 1, 1).data].map(value => {
    const channel = value / 255;
    return channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4;
  });
  return .2126 * r + .7152 * g + .0722 * b;
};
export function checkReadableLabel(pill) {
  const style = getComputedStyle(pill);
  const ink = luminance(style.color), background = luminance(style.backgroundColor);
  const contrast = (Math.max(ink, background) + .05) / (Math.min(ink, background) + .05);
  assert.ok(contrast >= 4.5, `${pill.textContent}: text contrast is ${contrast.toFixed(2)}:1`);
  const frame = pill.getBoundingClientRect();
  const dot = pill.querySelector("i");
  if (dot) {
    const marker = dot.getBoundingClientRect();
    assert.ok(Math.abs((marker.top + marker.bottom - frame.top - frame.bottom) / 2) <= .5,
      `${pill.textContent}: marker must stay centred in the label`);
  }
  const walker = document.createTreeWalker(pill, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) {
    if (!walker.currentNode.textContent.trim()) continue;
    const range = document.createRange();
    range.selectNodeContents(walker.currentNode);
    const lines = [...range.getClientRects()];
    assert.equal(lines.length, 1, `${pill.textContent}: status text wrapped`);
    assert.ok(lines[0].left >= frame.left - 1 && lines[0].right <= frame.right + 1,
      `${pill.textContent}: status text is clipped`);
  }
  return contrast;
}

export async function testStatusControls({ testAPI }, preview = false) {
  testAPI.tasks = ["pending", "running", "succeeded", "failed", "canceled"].map((status, index) => ({
    id: `status-task-${index}`, agent_id: "alpha", engine: "mihomo", action: "status", status,
    created_at: new Date(Date.now() - index * 60000).toISOString(),
  }));
  const card = await waitFor(() => document.querySelector('[data-agent-node="alpha"]'), "node card did not render");
  const service = () => card.querySelector('[data-core-service="mihomo"]');
  assert.equal(service().textContent, "运行中", "initial service rendering must not use task execution wording");
  const agent = testAPI.agents.find(item => item.id === "alpha");
  agent.runtime.mihomo.service_status = "inactive";
  await waitFor(() => service().textContent === "已停止", "service stop was not patched");
  assert.ok(service().closest(".engine-state").classList.contains("muted"), "a stopped service is not a failure");
  agent.status = "offline";
  agent.runtime.mihomo.service_status = "active";
  await waitFor(() => card.querySelector("[data-agent-presence]").dataset.agentPresence === "offline", "presence did not refresh");
  assert.equal(service().textContent, "状态未知", "offline service must not present stale running state as current");
  agent.status = "online";
  agent.runtime.mihomo.service_status = "running";
  await waitFor(() => service().textContent === "运行中", "service running alias did not refresh");
  assert.ok(service().closest(".engine-state").classList.contains("ok"));

  const engine = (status) => {
    const info = serviceStatusInfo(status);
    return `<span class="engine-state ${info.tone}" data-status-tone="${info.tone}"><i aria-hidden="true"></i><b>${info.label}</b></span>`;
  };
  const missing = serviceRuntimeStatus({});
  const board = document.createElement("section");
  board.dataset.statusControlBoard = "";
  board.innerHTML = `<style>
    [data-status-control-board]{max-width:900px;margin:0 auto;padding:24px;border:1px solid var(--line);border-radius:12px;background:var(--surface)}
    [data-status-control-board] h2{margin:0;font-size:20px}
    [data-status-control-board] p{margin:6px 0 22px;color:var(--muted);font-size:12px}
    [data-status-control-board] section{display:grid;grid-template-columns:110px minmax(0,1fr);gap:12px;align-items:center;padding:16px 0;border-top:1px solid var(--line)}
    [data-status-control-board] h3{margin:0;color:var(--ink-2);font-size:13px;font-weight:600}
    [data-status-control-board] .samples{display:flex;flex-wrap:wrap;align-items:center;gap:10px}
    @media(max-width:600px){[data-status-control-board]{padding:18px}[data-status-control-board] section{grid-template-columns:1fr}}
  </style><h2>统一状态控件</h2><p>连接、服务与任务使用同一套尺寸和样式，保留各自的状态语义。</p>
  <section><h3>节点连接</h3><div class="samples">${agentPresenceMarkup("online")}${agentPresenceMarkup("offline")}${agentPresenceMarkup("unknown")}</div></section>
  <section><h3>内核服务</h3><div class="samples">${["active", "inactive", "activating", "failed"].map(engine).join("")}<span class="engine-state muted" data-status-tone="muted"><i aria-hidden="true"></i><b>${missing.label}</b></span></div></section>
  <section><h3>配置页服务</h3><div class="samples">${["running", "stopped", "deactivating", "unknown"].map(status => {const info=serviceStatusInfo(status);return `<small class="live-engine-status ${info.state}" data-status-tone="${info.tone}">${info.label}</small>`;}).join("")}</div></section>
  <section><h3>任务执行</h3><div class="samples"><span class="status-label warn" data-status-tone="warn">准备中</span><span class="status-label warn" data-status-tone="warn">执行中</span><span class="status-label ok" data-status-tone="ok">成功</span><span class="status-label bad" data-status-tone="bad">失败</span><span class="status-label muted" data-status-tone="muted">已取消</span></div></section>
  <section class="page-traffic"><h3>流量监控</h3><div class="samples traffic-card-controls"><span class="traffic-policy-status ok" data-status-tone="ok"><i aria-hidden="true"></i>持续统计</span><span class="traffic-policy-status warn" data-status-tone="warn"><i aria-hidden="true"></i>等待同步</span><span class="traffic-policy-status bad" data-status-tone="bad"><i aria-hidden="true"></i>监控异常</span></div></section>
  <section><h3>业务状态</h3><div class="samples"><span class="status-label ok" data-status-tone="ok">已接受</span><span class="status-label muted" data-status-tone="muted">未配置</span><span class="status-label ip-quality-badge warn" data-status-tone="warn">等待检测</span></div></section>`;
  document.querySelector(".workspace-main").append(board);
  const pills = [...board.querySelectorAll(".agent-presence, .engine-state, .live-engine-status, .status-label, .traffic-policy-status")];
  const geometry = ["fontSize", "fontWeight", "lineHeight", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderTopWidth", "borderRadius", "minHeight", "boxShadow"];
  const originalTheme = document.documentElement.dataset.theme;
  let minimumContrast = Infinity;
  const themeColors = new Map();
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    await settlePaint();
    const reference = getComputedStyle(pills[0]);
    const colors = new Map();
    const markers = new Map();
    for (const pill of pills) {
      const style = getComputedStyle(pill);
      minimumContrast = Math.min(minimumContrast, checkReadableLabel(pill));
      if (matchMedia("(prefers-reduced-motion: reduce)").matches)
        assert.equal(style.transitionDuration, "0s", "reduced motion must disable status transitions");
      for (const property of geometry) assert.equal(style[property], reference[property], `${theme}: ${pill.className} has different ${property}`);
      const label = pill.querySelector("b");
      if (label) assert.equal(getComputedStyle(label).fontSize, reference.fontSize, "a page override shrank the status label");
      const tone = pill.dataset.statusTone || (pill.dataset.agentPresence === "online" ? "ok" : "muted");
      const color = `${style.color}|${style.backgroundColor}`;
      if (colors.has(tone)) assert.equal(color, colors.get(tone), `${theme}: ${tone} state colors differ between pages`);
      else colors.set(tone, color);
      const dot = pill.querySelector("i");
      const marker = dot ? getComputedStyle(dot) : getComputedStyle(pill, "::before");
      assert.equal(marker.width, "6px", "status marker size differs");
      assert.equal(marker.boxSizing, "border-box", "pseudo-element markers must include their border in the same 6px size");
      assert.equal(marker.boxShadow, "none", "status marker restored a pulse ring");
      const mark = [marker.height, marker.borderStyle, marker.borderWidth, marker.borderColor, marker.backgroundColor, marker.translate].join("|");
      if (markers.has(tone)) assert.equal(mark, markers.get(tone), `${theme}: ${tone} markers differ between renderers`);
      else markers.set(tone, mark);
    }
    themeColors.set(theme, colors);
    for (const pill of card.querySelectorAll(".agent-presence, .engine-state")) {
      const style = getComputedStyle(pill);
      for (const property of geometry) assert.equal(style[property], reference[property], `overview override changed ${property}`);
    }
  }
  document.documentElement.dataset.theme = originalTheme;
  window.statusControlResult = { minimumContrast: Number(minimumContrast.toFixed(2)), views: ["node-overview", "state-matrix"] };
  if (preview) {
    document.querySelector(".workspace-main").replaceChildren(board);
  } else {
    board.remove();
    // Stress the real overview layout without changing state mapping or polling.
    const sample = card.cloneNode(true);
    sample.style.width = "320px";
    sample.querySelectorAll(".core-chip .engine-state b").forEach(label => { label.textContent = "检测到但不可迁移"; });
    document.querySelector(".workspace-main").append(sample);
    const root = document.documentElement;
    const originalScale = root.style.getPropertyValue("--ui-font-scale");
    try {
      for (const scale of ["1", "1.25"]) {
        root.style.setProperty("--ui-font-scale", scale);
        await settlePaint();
        for (const pill of sample.querySelectorAll(statusSelector)) checkReadableLabel(pill);
        assert.ok(sample.scrollWidth <= sample.clientWidth + 1, "long status labels overflow a narrow node card");
      }
    } finally {
      sample.remove();
      if (originalScale) root.style.setProperty("--ui-font-scale", originalScale);
      else root.style.removeProperty("--ui-font-scale");
    }
    // Compare real renderer output with the shared state matrix in both themes.
    for (const view of [
      { hash: "#settings-node-alpha", selector: ".core-runtime-name .engine-state" },
      { hash: "#tasks", selector: ".task-event-card .status-label" },
      { hash: "#traffic", selector: ".traffic-policy-status" },
    ]) {
      location.hash = view.hash;
      await waitFor(() => document.querySelector(view.selector), `${view.hash}: status view did not load`);
      for (const theme of ["light", "dark"]) for (const scale of ["1", "1.25"]) {
        root.dataset.theme = theme;
        root.style.setProperty("--ui-font-scale", scale);
        await settlePaint();
        for (const pill of document.querySelectorAll(statusSelector)) {
          if (!pill.getClientRects().length) continue;
          minimumContrast = Math.min(minimumContrast, checkReadableLabel(pill));
          const tone = pill.dataset.agentPresence === "online" || pill.classList.contains("ok") || pill.classList.contains("running") ? "ok"
            : pill.classList.contains("warn") || pill.classList.contains("pending") ? "warn"
            : pill.classList.contains("bad") || pill.classList.contains("failed") ? "bad" : "muted";
          const style = getComputedStyle(pill);
          assert.equal(`${style.color}|${style.backgroundColor}`, themeColors.get(theme).get(tone), `${view.hash}: theme colors differ`);
        }
        assert.ok(root.scrollWidth <= root.clientWidth + 1, `${view.hash}: status layout overflows`);
      }
      window.statusControlResult.views.push(view.hash);
      if (view.hash === "#tasks") {
        testAPI.tasks[0].status = "running";
        await waitFor(() => document.querySelector('[data-task-id="status-task-0"] .status-label')?.textContent === "执行中",
          "polling did not update task execution status");
        assert.ok(document.querySelector('[data-task-id="status-task-0"] .status-label').classList.contains("warn"),
          "running task must retain transition semantics");
      }
    }
    if (originalScale) root.style.setProperty("--ui-font-scale", originalScale);
    else root.style.removeProperty("--ui-font-scale");
    root.dataset.theme = originalTheme;
    window.statusControlResult.minimumContrast = Number(minimumContrast.toFixed(2));
  }
}
