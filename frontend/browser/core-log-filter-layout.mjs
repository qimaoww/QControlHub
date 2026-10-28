import { assert } from "./assertions.mjs";
import { createCoreLogView } from "../modules/core-log-view.js";
import { createCoreLogBindings } from "../modules/core-log-bindings.js";

// Render the production toolbar before app startup, with isolated synthetic
// sources and records. No network, account preferences or live data are used.
export async function testCoreLogFilterLayout() {
  const app = document.querySelector("#app");
  const root = document.createElement("main");
  root.className = "workspace-main";
  root.style.fontSize = "var(--text-body)";
  app.replaceChildren(root);
  const bodyClass = document.body.className;
  const theme = document.documentElement.getAttribute("data-theme");
  document.body.className = "app-body page-core-logs";
  const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
  const names = { mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "SS Rust" };
  const agent = { id: "fixture", name: "Fixture", status: "online" };
  const state = { route: "core-logs", data: { coreLogAutoRefresh: false, coreLogPhase: "ready", coreLogFilters: {} } };
  const ctx = {
    state, engines, can: () => false, esc: String, engineName: engine => names[engine], date: String,
    shell: markup => { root.innerHTML = markup; },
  };
  const view = createCoreLogView(ctx);
  const render = () => {
    view(state.data.coreLogEntries || [], [agent], state.data.coreLogFilters);
    bind(state.data.coreLogFilters);
  };
  const bind = createCoreLogBindings(ctx, {
    renderCoreLogs: render, rememberSelection() {},
    coreLogs() { assert.fail("local filters must not fetch"); }, poller: { start() {}, stop() {} },
  });
  const sources = available => {
    state.data.coreLogSources = available.map(engine => ({ agent_id: agent.id, instance_id: "main", engine, status: "active" }));
  };
  const records = counts => {
    state.data.coreLogEntries = engines.flatMap((engine, index) => Array.from({ length: counts[index] }, (_, row) => ({
      id: index * 10000 + row, agent_id: agent.id, instance_id: "main", engine, level: "info",
      message: "Synthetic layout fixture", logged_at: "2026-10-28T00:00:00Z",
    })));
    render();
  };
  const buttons = () => [...root.querySelectorAll("[data-core-log-engine]")];
  const boxes = () => buttons().map(button => {
    const { x, y, width, height } = button.getBoundingClientRect();
    return [x, y, width, height];
  });
  const frame = () => new Promise(resolve => requestAnimationFrame(resolve));
  try {
    sources(engines);
    records([317, 6, 11, 1000]);
    assert.equal(buttons().map(button => button.querySelector("b").textContent).join(","), "1334,317,6,11,1000");
    for (const color of ["light", "dark"]) {
      document.documentElement.dataset.theme = color;
      for (const scale of [1, 1.5]) {
        root.style.setProperty("--ui-font-scale", scale);
        for (const width of [320, 390, 540, 680, 960, 1280, 1680]) {
          root.style.width = `${width}px`;
          await frame();
          const group = root.querySelector(".core-log-engine-filter>div").getBoundingClientRect();
          const geometry = boxes();
          for (const [index, button] of buttons().entries()) {
            const [x, y, w, h] = geometry[index];
            assert.ok(x >= group.left - 1 && x + w <= group.right + 1, "engine option escapes its container");
            assert.equal(h, geometry[0][3], "engine option heights differ");
            const name = button.querySelector("span");
            assert.ok(name.scrollWidth <= name.clientWidth + 1, `engine name must stay complete (${width}px, ${scale}x, ${color}, ${name.textContent}: ${name.clientWidth}/${name.scrollWidth})`);
            const label = name.getBoundingClientRect();
            const count = button.querySelector("b").getBoundingClientRect();
            assert.ok(label.right <= count.left && count.right <= x + w - 1 && label.left >= x, "engine label/count overlap or clip");
            assert.ok(Math.abs(label.top + label.height / 2 - count.top - count.height / 2) < 1, "label/count alignment differs");
            if (index && y > geometry[index - 1][1]) assert.ok(Math.abs(x - group.left - 5) < 1, "wrapped row must start at the leading edge");
          }
          const toolbar = root.querySelector(".core-log-filters").getBoundingClientRect();
          for (const control of root.querySelectorAll(".core-log-filter-fields>*,.core-log-filter-tools>*")) {
            const rect = control.getBoundingClientRect();
            assert.ok(rect.left >= toolbar.left && rect.right <= toolbar.right, "neighboring filter overflows");
          }
          const workspace = root.querySelector(".core-log-workspace");
          const em = parseFloat(getComputedStyle(workspace).fontSize);
          const fields = [...root.querySelector(".core-log-filter-fields").children];
          if (workspace.clientWidth >= 112 * em) {
            assert.ok(fields.every(field => Math.abs(field.getBoundingClientRect().top - fields[0].getBoundingClientRect().top) < 1), `wide toolbar fields must align (${width}px, ${scale}x, ${color})`);
          } else if (workspace.clientWidth >= 84 * em) {
            assert.ok(Math.abs(fields[2].getBoundingClientRect().top - fields[3].getBoundingClientRect().top) < 1, "medium toolbar engine/level fields must align");
          }
          const tools = [...root.querySelector(".core-log-filter-tools").children];
          if (workspace.clientWidth > 44 * em) {
            assert.ok(tools.every(tool => Math.abs(tool.getBoundingClientRect().bottom - tools[0].getBoundingClientRect().bottom) < 1), "search, limit and reset must align on one row");
            assert.ok(tools[0].getBoundingClientRect().width > tools[1].getBoundingClientRect().width && tools[0].getBoundingClientRect().width > tools[2].getBoundingClientRect().width, "search should stay wider than limit/reset controls");
            assert.ok(tools[0].getBoundingClientRect().width <= 24 * em + 1, "keyword search must stay compact on wide workspaces");
            assert.ok(tools[1].getBoundingClientRect().left - tools[0].getBoundingClientRect().right <= 13, "limit should remain beside keyword search");
          }
          const selected = buttons()[1];
          selected.click();
          selected.focus({ preventScroll: true });
          const selectedBoxes = boxes();
          for (const count of [0, 1, 9, 10, 99, 100, 999, 1000]) {
            const scroll = root.scrollTop;
            records([0, 0, 0, count]);
            await frame();
            assert.equal(document.activeElement, selected, "count updates must preserve the focused button");
            assert.equal(root.scrollTop, scroll, "count updates must preserve scroll position");
            assert.equal(state.data.coreLogFilters.engine, "mihomo");
            assert.equal(selected.getAttribute("aria-pressed"), "true");
            assert.equal(JSON.stringify(boxes()), JSON.stringify(selectedBoxes), "one-to-four-digit counts must not move or resize controls");
          }
          await Promise.all(selected.getAnimations().map(animation => animation.finished.catch(() => {})));
          const levelSelected = root.querySelector('[data-core-log-level][aria-pressed=true]');
          for (const property of ["border", "borderRadius", "boxShadow", "backgroundColor"]) {
            assert.equal(getComputedStyle(selected)[property], getComputedStyle(levelSelected)[property], `engine selection must reuse the existing button ${property}`);
          }
          for (const property of ["padding", "borderRadius", "fontWeight", "backgroundColor", "color"]) {
            assert.equal(getComputedStyle(selected.querySelector("b"))[property], getComputedStyle(levelSelected.querySelector("b"))[property], `engine counts must reuse the existing badge ${property}`);
          }
        }
      }
    }
    for (const available of [[], ["xray"], ["xray", "ss-rust"], engines]) {
      sources(available);
      state.data.coreLogFilters = {};
      records([0, 0, 0, 0]);
      assert.equal(buttons().map(button => button.value).join(","), ["", ...available].join(","), "options must follow authorized sources, including zero counts");
      buttons().at(-1).click();
      assert.equal(buttons().filter(button => button.getAttribute("aria-pressed") === "true").length, 1);
      root.querySelector("[data-reset-core-logs]").click();
      assert.equal(buttons()[0].getAttribute("aria-pressed"), "true");
    }
  } finally {
    app.replaceChildren();
    document.body.className = bodyClass;
    if (theme === null) document.documentElement.removeAttribute("data-theme");
    else document.documentElement.setAttribute("data-theme", theme);
  }
}
