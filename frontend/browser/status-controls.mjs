import { agentPresenceMarkup } from "../modules/agent-presence.js";
import { serviceStatusInfo, serviceRuntimeStatus } from "../modules/service-status.js";
import { assert, waitFor } from "./assertions.mjs";

export async function testStatusControls({ testAPI }, preview = false) {
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
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    const reference = getComputedStyle(pills[0]);
    const colors = new Map();
    const markers = new Map();
    for (const pill of pills) {
      const style = getComputedStyle(pill);
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
      const mark = [marker.height, marker.borderStyle, marker.borderWidth, marker.borderColor, marker.backgroundColor].join("|");
      if (markers.has(tone)) assert.equal(mark, markers.get(tone), `${theme}: ${tone} markers differ between renderers`);
      else markers.set(tone, mark);
    }
    for (const pill of card.querySelectorAll(".agent-presence, .engine-state")) {
      const style = getComputedStyle(pill);
      for (const property of geometry) assert.equal(style[property], reference[property], `overview override changed ${property}`);
    }
  }
  document.documentElement.dataset.theme = originalTheme;
  if (preview) {
    document.querySelector(".workspace-main").replaceChildren(board);
  } else {
    board.remove();
  }
}
