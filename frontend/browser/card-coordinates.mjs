import { assert, delay, waitFor } from "./assertions.mjs";
import { gesture, route, settled } from "./card-movement.mjs";

const cards = grid => [...grid.querySelectorAll(":scope > .node-card,:scope > .traffic-policy-card")];
const id = card => card.dataset.agentNode || card.dataset.trafficCardKey;
const order = grid => cards(grid).map(id);
const previewOrder = grid => cards(grid).sort((a, b) => Number(a.style.order) - Number(b.style.order)).map(id);
const equal = (a, b, message) => assert.equal(JSON.stringify(a), JSON.stringify(b), message);
const center = box => ({ x: box.left + box.width / 2, y: box.top + box.height / 2 });
const frames = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));

export async function testCardCoordinateRuntime({ mode }, preview) {
  if (preview) await new Promise(() => {});
  const mobile = mode.includes("mobile"), measurements = [];
  let pointer = null, pressed = null, pressedRect = null;
  const observe = event => {
    pointer = { x: event.clientX, y: event.clientY };
    if (event.type === "pointerdown") {
      pressed = pointer;
      pressedRect = event.target.closest(".node-card,.traffic-policy-card")?.getBoundingClientRect();
    }
  };
  document.addEventListener("pointerdown", observe, true);
  document.addEventListener("pointermove", observe, true);
  const run = async (grid, card, start, points, name, inspect = async () => {}, release = "drop", waitForLanding = true) => {
    const initial = order(grid);
    let expected = initial, samples = 0, maxError = 0, anchor;
    const sample = () => {
      const ghost = document.querySelector(".card-move-ghost");
      assert.ok(ghost, `${name}: real pointer must pick up; start ${JSON.stringify(start)}, pressed ${JSON.stringify(pressed)}, hit ${document.elementFromPoint(start.x,start.y)?.outerHTML.slice(0,300)}`);
      anchor ||= { x: pressed.x - pressedRect.left, y: pressed.y - pressedRect.top };
      const painted = ghost.getBoundingClientRect();
      const error = Math.hypot(painted.left + anchor.x - pointer.x, painted.top + anchor.y - pointer.y);
      maxError = Math.max(error, maxError); samples++;
      assert.ok(error < 1, `${name}: grab point drift ${error}px`);
      expected = previewOrder(grid);
    };
    const releasing = () => { if (document.querySelector(".card-move-ghost")) sample(); };
    document.addEventListener("pointerup", releasing, true);
    await gesture(start, points, release, async (index, point) => {
      sample();
      await inspect(index, point);
      await frames(); sample();
    }, waitForLanding, waitForLanding ? null : `[${card.dataset.agentNode ? "data-agent-node" : "data-traffic-card-key"}="${CSS.escape(id(card))}"] .node-card-grip`);
    document.removeEventListener("pointerup", releasing, true);
    equal(order(grid), release === "drop" ? expected : initial, `${name}: final order equals the last preview`);
    assert.equal(document.querySelector(".card-move-ghost,.card-move-layer,.card-move-active"), null, `${name}: cleans up`);
    assert.ok(cards(grid).every(c => !c.style.order && !c.style.transform), `${name}: clears temporary geometry`);
    measurements.push({ name, samples, maxGrabErrorPx: maxError, previewOrder: expected, finalOrder: order(grid), release });
  };

  const fixtureStyle = document.createElement("style");
  fixtureStyle.textContent = `body[data-card-coordinate-layout] :is(.node-card-grid,.traffic-policy-grid){grid-template-columns:var(--card-coordinate-columns)!important;align-items:start!important;scroll-margin-top:160px}
    body[data-card-coordinate-layout] :is(.node-card-grid>.node-card,.traffic-policy-grid>.traffic-policy-card){height:110px!important;min-height:0!important}
    body[data-card-coordinate-layout] :is(.node-card-grid>.node-card,.traffic-policy-grid>.traffic-policy-card):nth-child(3n){height:130px!important}`;
  document.head.append(fixtureStyle);
  for (const type of ["nodes", "traffic"]) {
    const grid = await route(type === "nodes" ? "#node-settings" : "#traffic-all", type === "nodes" ? ".node-card-grid" : ".traffic-policy-grid");
    await settled();
    const positions = mobile ? ["grip", "grip-top-left", "grip-bottom-right"] : ["center", "top-left", "top-right", "bottom-left", "bottom-right", "grip"];
    for (const [index, position] of positions.entries()) {
      document.documentElement.dataset.theme = index % 2 ? "dark" : "light";
      const list = cards(grid), card = list[0], target = list[1];
      card.scrollIntoView({ block: mobile ? "start" : "center", behavior: "instant" });
      const box = card.getBoundingClientRect(), handle = card.querySelector(".node-card-grip").getBoundingClientRect();
      let start = center(position.startsWith("grip") ? handle : box);
      if (position === "top-left") start = { x: box.left + 8, y: box.top + 8 };
      if (position === "top-right") start = { x: box.right - 8, y: box.top + 8 };
      if (position === "bottom-left") start = { x: box.left + 8, y: box.bottom - 8 };
      if (position === "bottom-right") start = { x: box.right - 8, y: box.bottom - 8 };
      if (position === "grip-top-left") start = { x: handle.left + 2, y: handle.top + 2 };
      if (position === "grip-bottom-right") start = { x: handle.right - 2, y: handle.bottom - 2 };
      const targetRect = target.getBoundingClientRect();
      const end = { x: targetRect.left + targetRect.width / 2, y: targetRect.top + 28 };
      const steps = index % 2 ? 1 : 8;
      const points = Array.from({ length: steps }, (_, n) => ({ x: start.x + (end.x - start.x) * (n + 1) / steps, y: start.y + (end.y - start.y) * (n + 1) / steps }));
      await run(grid, card, start, points, `${type}/${position}/${steps === 1 ? "fast" : "slow"}`);
    }

    // Compact variable-height cards expose row/column, first/last and gap
    // geometry in the viewport. Production handlers still receive native input.
    const prepare = async () => {
      document.body.dataset.cardCoordinateLayout = "compact";
      document.documentElement.style.setProperty("--card-coordinate-columns", mobile ? "repeat(2,minmax(0,1fr))" : "repeat(3,minmax(0,1fr))");
      const host = mobile ? document.scrollingElement : document.querySelector(".workspace-main");
      host.scrollTo({ top: host.scrollTop + grid.getBoundingClientRect().top - 160, behavior: "instant" });
      await frames();
    };
    await prepare();
    for (const reverse of [false, true, false]) {
      await prepare();
      const list = cards(grid), endIndex = 5;
      const card = reverse ? list[endIndex] : list[0], target = reverse ? list[0] : list[endIndex];
      const start = center(card.querySelector(".node-card-grip").getBoundingClientRect()), box = target.getBoundingClientRect();
      const end = { x: box.left + box.width / 2, y: box.top + 25 };
      await run(grid, card, start, [end, start, end], `${type}/cross-row/${reverse ? "first" : "forward"}`, async () => {
        assert.equal(grid.querySelector(".drop-target"), card, "the moving card owns the final-position border");
      });
    }
    await prepare();
    const list = cards(grid), card = list[0], target = list[1];
    let start = center(card.querySelector(".node-card-grip").getBoundingClientRect()), end = center(target.getBoundingClientRect());
    await run(grid, card, start, [end], `${type}/stationary-reflow`, async () => {
      const before = Number(card.style.order);
      document.documentElement.style.setProperty("--card-coordinate-columns", "1fr");
      await delay(500);
      assert.notEqual(Number(card.style.order), before, "stationary pointer re-hits new responsive slots");
      document.documentElement.style.setProperty("--card-coordinate-columns", mobile ? "repeat(2,minmax(0,1fr))" : "repeat(3,minmax(0,1fr))");
      await delay(500);
    });

    await prepare();
    for (let index = 0; index < 3; index++) {
      const quick = cards(grid)[index % 2];
      start = center(quick.querySelector(".node-card-grip").getBoundingClientRect());
      const box = grid.getBoundingClientRect();
      end = { x: index % 2 ? box.left + 30 : box.right - 30, y: box.top + 45 };
      if (Math.hypot(end.x - start.x, end.y - start.y) < 12) end.y += 30;
      await run(grid, quick, start, [end], `${type}/interrupt-landing/${index}`, async () => {}, "drop", false);
    }
    await settled();

    await prepare();
    const gapCard = cards(grid)[0];
    start = center(gapCard.querySelector(".node-card-grip").getBoundingClientRect());
    // On tall/mobile layouts auto-scroll is already covered; use the visible
    // row gap here to verify that small empty areas choose a stable neighbour.
    const second = cards(grid)[1].getBoundingClientRect();
    end = { x: (gapCard.getBoundingClientRect().right + second.left) / 2, y: second.top + 30 };
    await run(grid, gapCard, start, [end, { x: end.x + 2, y: end.y }, end], `${type}/empty-gap`);

    if (!mobile) {
      const zoomCard = cards(grid)[0];
      zoomCard.scrollIntoView({ block: "center", behavior: "instant" });
      start = center(zoomCard.querySelector(".node-card-grip").getBoundingClientRect());
      end = { x: start.x + 40, y: start.y + 40 };
      await run(grid, zoomCard, start, [end], `${type}/live-css-zoom`, async () => {
        document.body.style.zoom = "1.25"; await delay(120);
      }, "escape");
      document.body.style.zoom = "";
      await prepare();
      const sidebarCard = cards(grid)[0];
      start = center(sidebarCard.querySelector(".node-card-grip").getBoundingClientRect());
      end = { x: start.x + 20, y: start.y + 20 };
      await run(grid, sidebarCard, start, [end], `${type}/sidebar-reflow`, async () => {
        const workspace = document.querySelector(".workspace-main");
        workspace.style.marginLeft = "180px"; await delay(180);
        workspace.style.marginLeft = ""; await delay(180);
      }, "escape");
    }

    // A nested scroll container moves independently of its grid. Hold the
    // pointer stationary, scroll it manually, then let edge scrolling continue.
    await prepare();
    const panel = document.createElement("div");
    panel.style.cssText = "overflow:auto;max-height:360px;border:3px solid transparent";
    grid.before(panel); panel.append(grid); panel.scrollIntoView({ block: "start", behavior: "instant" });
    await frames();
    const scrolled = cards(grid)[0];
    start = center(scrolled.querySelector(".node-card-grip").getBoundingClientRect());
    const edge = panel.getBoundingClientRect();
    end = { x: start.x, y: Math.min(innerHeight - (mobile ? 108 : 8), edge.bottom - 12) };
    await run(grid, scrolled, start, [end], `${type}/nested-scroll`, async () => {
      panel.scrollTop += 70;
      const before = panel.scrollTop;
      await waitFor(() => panel.scrollTop > before + 15 || panel.scrollTop >= panel.scrollHeight - panel.clientHeight - 1, "nested auto-scroll did not advance");
      await frames();
    });
    panel.before(grid); panel.remove();
    delete document.body.dataset.cardCoordinateLayout;
  }
  if (!mobile) {
    const grid = await route("#node-settings", ".node-card-grid");
    const card = cards(grid)[0]; card.scrollIntoView({ block: "center", behavior: "instant" });
    const point = center(card.getBoundingClientRect()), href = card.getAttribute("href");
    await gesture(point, [{ x: point.x + 2, y: point.y }], "drop", () => {
      assert.equal(document.querySelector(".card-move-ghost"), null, "sub-threshold content click cannot drag");
    });
    await waitFor(() => location.hash === href, "sub-threshold content click must still navigate");
  }
  fixtureStyle.remove();
  document.documentElement.style.removeProperty("--card-coordinate-columns");
  document.removeEventListener("pointerdown", observe, true);
  document.removeEventListener("pointermove", observe, true);
  window.cardCoordinateMeasurements = { mobile, dpr: devicePixelRatio, viewport: [innerWidth, innerHeight], measurements };
  const max = Math.max(...measurements.map(m => m.maxGrabErrorPx));
  window.cardMovementResult = `coordinates ${measurements.length} gestures; max grab error ${max.toFixed(4)} CSS px; preview/final match; DPR ${devicePixelRatio}`;
}
