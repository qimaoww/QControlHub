import { assert, delay, waitFor } from "./assertions.mjs";
import { animateNodeCardDrop } from "../modules/agent-card-drag.js";
import { accountStorage } from "../modules/account-storage.js";

const key = (grip, value) => grip.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true }));
export const settled = () => waitFor(() => !document.querySelector(".card-move-ghost") &&
  !document.getAnimations().some(animation => animation.id.startsWith("qch-card") || animation.id === "qch-drop"), "card movement did not settle");
export const route = async (hash, selector) => {
  location.hash = hash;
  const grid = await waitFor(() => document.querySelector(selector) && !document.querySelector(".is-route-pending"), `missing ${hash}`);
  await waitFor(() => !document.getAnimations().some(animation => animation.id.startsWith("qch-route")), "route did not settle");
  await waitFor(() => !document.getAnimations().some(animation => animation.id.startsWith("qch")), "route content did not settle");
  return document.querySelector(selector);
};
const equalOrder = (actual, expected, message) => assert.equal(JSON.stringify(actual), JSON.stringify(expected), message);
const trafficPaint = (original, preview) => {
  for (const selector of [".traffic-card-header", ".traffic-card-heading", ".traffic-card-facts", ".traffic-card-port code", "footer"]) {
    const live = getComputedStyle(original.querySelector(selector)), copy = getComputedStyle(preview.querySelector(selector));
    for (const property of ["display", "padding", "fontSize", "lineHeight", "gap", "color", "backgroundColor"])
      assert.equal(copy[property], live[property], `traffic preview retains ${selector} ${property}`);
  }
};

// The runner dispatches real mouse/touch events and waits for each inspection
// acknowledgement before sending the next input. Timing cannot skip assertions.
export async function viewportScale(scale) {
  window.__cardMoveViewport = { action: "viewport", scale };
  await waitFor(() => window.__cardMoveViewport === null, "native viewport setup did not finish");
}

export async function gesture(start, points, release, inspect = async () => {}, waitForLanding = true) {
  const input = new AbortController();
  window.__cardMoveStep = -1;
  window.__cardMoveAck = -1;
  window.__cardMoveInput = { pressed: false, ended: false, cancelled: false, point: null };
  for (const type of ["pointerdown", "pointermove", "pointerup", "pointercancel"])
    document.addEventListener(type, event => {
      const delivered = window.__cardMoveInput;
      if (type === "pointerdown") delivered.pressed = true;
      if (type === "pointerup" || type === "pointercancel") delivered.ended = true;
      if (type === "pointercancel") delivered.cancelled = true;
      else delivered.point = { x: event.clientX, y: event.clientY };
    }, { capture: true, signal: input.signal });
  window.__cardMoveGesture = { start, points, release };
  try {
    for (let index = 0; index < points.length; index++) {
      await waitFor(() => window.__cardMoveStep === index, "native input step missing");
      await inspect(index, points[index]);
      window.__cardMoveAck = index;
    }
    await waitFor(() => window.__cardMoveGesture === null, "native input did not finish");
    if (waitForLanding) await settled();
  } finally {
    input.abort();
  }
}
export const geometry = (grip, target) => {
  const source = grip.closest(".node-card,.traffic-policy-card");
  // Keep both adjacent grips inside the viewport; holding a target at the edge
  // deliberately auto-scrolls and is covered by the separate edge gesture.
  const anchor = source.getBoundingClientRect().top < target.getBoundingClientRect().top ? source : target;
  if (matchMedia("(pointer:coarse)").matches) {
    anchor.scrollIntoView({ block: "start", behavior: "instant" });
    // Keep reversals clear of the top bar's auto-scroll band as well as the
    // bottom dock. Edge scrolling has its own stationary-pointer regression.
    document.scrollingElement.scrollTo({ top: scrollY + anchor.getBoundingClientRect().top - 160, behavior: "instant" });
  }
  else grip.scrollIntoView({ block: "center", behavior: "instant" });
  const box = grip.getBoundingClientRect(), card = source.getBoundingClientRect();
  const destination = target.getBoundingClientRect();
  const start = { x: box.left + box.width / 2, y: box.top + box.height / 2 };
  return { start, end: { x: start.x + destination.left - card.left, y: start.y + destination.top - card.top } };
};

export async function testCardMoveRuntime({ mode, testAPI }, preview) {
  if (preview) await new Promise(() => {});
  await waitFor(() => document.querySelector(".node-card-grid"), "nodes missing");
  const reduced = mode.endsWith("reduced"), mobile = mode.includes("mobile");
  assert.equal(matchMedia("(prefers-reduced-motion: reduce)").matches, reduced);
  // Row-height changes must begin at the last painted size rather than pop
  // when a shorter card moves out of a row stretched by a taller neighbour.
  const sizing = document.createElement("article");
  sizing.className = "node-card";
  sizing.style.cssText = "position:fixed;left:10px;top:100px;width:100px;height:100px";
  document.body.append(sizing);
  const beforeSize = new Map([[sizing, sizing.getBoundingClientRect()]]);
  sizing.style.width = "140px"; sizing.style.height = "180px";
  animateNodeCardDrop([sizing], beforeSize, { resize: true });
  if (!reduced) {
    const first = sizing.getBoundingClientRect();
    assert.ok(Math.abs(first.width - 100) < 2 && Math.abs(first.height - 100) < 2, "row resize starts at the painted bounds");
  }
  await settled();
  assert.equal(sizing.getBoundingClientRect().height, 180, "row resize settles at the actual layout size");
  sizing.remove();
  for (const theme of ["light", "dark"]) {
    localStorage.setItem("qcontrolhub-color-theme", theme);
    document.documentElement.dataset.theme = theme;
    for (const type of ["nodes", "traffic"]) {
      const selector = type === "nodes" ? ".node-card-grid" : ".traffic-policy-grid";
      const cardSelector = type === "nodes" ? ".node-card" : "[data-traffic-card-key]";
      const orderKey = `qcontrolhub:${type === "nodes" ? "node" : "traffic"}-card-order`;
      const hash = type === "nodes" ? "#node-settings" : "#traffic-all";
      const grid = await route(hash, selector);
      const cards = () => [...grid.querySelectorAll(cardSelector)];
      const ids = () => cards().map(card => type === "nodes" ? card.dataset.agentNode : card.dataset.trafficCardKey);
      const original = cards(), originalIds = ids(), grip = original[0].querySelector(".node-card-grip");
      assert.ok(grip.getAttribute("aria-describedby"), "grip needs shared instructions");
      assert.equal(grip.getAttribute("aria-pressed"), "false");
      const draft = original[0].querySelector('input[name="name"]');
      if (draft) draft.value = "unsaved card movement draft";
      const { start, end } = geometry(grip, original[1]);
      if (mobile) {
        const box = grip.getBoundingClientRect(), hit = getComputedStyle(grip, "::before");
        const width = box.width - parseFloat(hit.left) - parseFloat(hit.right);
        const height = box.height - parseFloat(hit.top) - parseFloat(hit.bottom);
        assert.ok(width >= 44 && height >= 44, "original grip has an expanded invisible touch target");
        // Start native touch outside the visible grip, after scrolling it into
        // view. The padded area must initiate dragging without card navigation.
        const offset = box.width / 2 + 6;
        start.x -= offset; end.x -= offset;
        assert.ok(grip.contains(document.elementFromPoint(start.x, start.y)), "expanded touch area belongs to the grip");
      }
      const sourceRect = original[0].getBoundingClientRect();
      const saved = accountStorage.getItem(orderKey);
      await gesture(start, [{ x: start.x + 2, y: start.y }, end, start, end], "drop", async (index, point) => {
        if (!index) {
          assert.equal(document.querySelector(".card-move-ghost"), null, "below-threshold input cannot pick up a card");
          return;
        }
        const ghost = document.querySelector(".card-move-ghost");
        assert.ok(ghost, `native input must create a preview: ${type}/${theme}/${index}; start ${start.x},${start.y}; target ${point.x},${point.y}`);
        assert.ok(ghost.inert && ghost.getAttribute("aria-hidden") === "true", "preview is excluded from focus and announcements");
        assert.equal(ghost.querySelector("[id],dialog"), null, "preview cannot duplicate IDs or dialogs");
        assert.equal(ghost.getAttribute(type === "nodes" ? "data-agent-node" : "data-traffic-card-key"), null);
        if (type === "nodes") assert.equal(ghost.querySelector(".agent-presence").dataset.agentPresence,
          original[0].querySelector(".agent-presence").dataset.agentPresence, "preview preserves presence artwork");
        else trafficPaint(original[0], ghost);
        const painted = ghost.getBoundingClientRect();
        const anchor = { x: start.x - sourceRect.left, y: start.y - sourceRect.top };
        const error = Math.hypot(painted.left + anchor.x - point.x, painted.top + anchor.y - point.y);
        assert.ok(error < 1, `grab point follows the pointer: ${type}/${theme}/${index}; error ${error}px`);
        assert.equal(accountStorage.getItem(orderKey), saved, "preview cannot write order preferences");
        const target = original[0];
        assert.equal(grid.querySelector(".drop-target"), target, "target-card border follows reversals");
        assert.equal(Number(target.style.order), index === 2 ? 0 : 1, `border placeholder occupies the preview insertion slot: ${type}/${theme}/${index}; point ${JSON.stringify(point)}; start ${JSON.stringify(start)}; source ${JSON.stringify(sourceRect)}; scroll ${scrollY}; slots ${JSON.stringify(cards().map(c => [c.style.order,c.getBoundingClientRect().top,c.offsetHeight]))}`);
        if (target) assert.ok(getComputedStyle(target).boxShadow.includes("inset") && getComputedStyle(target).boxShadow.includes("2px"), "destination uses the original traffic inset border in both themes");
        assert.equal(document.querySelector(".card-move-cue,.card-move-guide,.card-move-feedback"), null, "drag feedback uses borders without added labels");
        assert.equal(getComputedStyle(original[0]).opacity, "1", "destination border retains the original traffic-card strength");
        assert.equal(getComputedStyle(original[0]).borderRadius, "12px", "destination uses the original traffic-card radius");
        assert.equal(getComputedStyle(original[0].firstElementChild).opacity, "1", "destination keeps the original traffic target's opaque content");
        const cap = type === "nodes" ? ".node-card-head" : ".traffic-card-header";
        const fill = getComputedStyle(target.querySelector(cap)).backgroundColor;
        assert.equal(fill, getComputedStyle(target).backgroundColor, "header masks the inset like the original traffic target");
        assert.equal(getComputedStyle(ghost).scale, "none", "preview has no added pickup scale");
        assert.equal(getComputedStyle(ghost).opacity, "0.96", "preview keeps the original traffic-card opacity");
        if (reduced) assert.equal(ghost.getAnimations().length, 0, "reduced motion keeps cue and placement without animation");
        if (index === 3 && theme === "light") {
          const path = type === "nodes" ? "/agents" : "/traffic-policies";
          const before = testAPI.calls.filter(call => call.path === path).length;
          await waitFor(() => testAPI.calls.filter(call => call.path === path).length > before, "background poll did not run during drag");
          await delay(60);
          assert.equal(cards()[0], original[0], "refresh cannot replace or commit a card during drag");
          assert.equal(document.querySelector(".card-move-ghost"), ghost);
        }
      });
      assert.equal(cards()[1], original[0], `drop commits the preview position: ${type}/${theme}; ${ids().join(",")}; ${grid.parentElement.querySelector("[data-card-order-status]")?.textContent}`);
      assert.equal(document.activeElement, grip, "pointer drop keeps focus on the same grip");
      assert.ok(grid.parentElement.querySelector('[data-phase="saved"]'), "success is announced without a toast");
      assert.ok(cards().every(card => !card.style.order && !card.style.transform && !card.style.transition && !card.classList.contains("drop-target")), "temporary geometry and target border must be cleaned");

      // Direct arrows commit serially; Space opens a reversible keyboard preview.
      key(grip, "ArrowLeft"); key(grip, "ArrowRight"); key(grip, "ArrowLeft");
      await settled();
      equalOrder(ids(), originalIds, "rapid keyboard reversals must persist the latest order");
      key(grip, " ");
      for (let index = 1; index < cards().length; index++) key(grip, "ArrowRight");
      assert.equal(grip.getAttribute("aria-pressed"), "true");
      assert.equal(grid.querySelector(".drop-target"), original[0], "last-slot movement retains a destination border");
      key(grip, "Escape"); await settled();
      equalOrder(ids(), originalIds, "keyboard cancellation must restore the committed order");
      assert.equal(document.activeElement, grip);
      key(grip, " "); key(grip, "ArrowRight"); key(grip, " "); await settled();
      assert.equal(cards()[1], original[0], "keyboard pickup and drop commit the same slot");

      for (const release of ["escape", "cancel", "outside"]) {
        const before = ids(), positioned = geometry(grip, cards()[0]);
        const point = release === "outside" ? { x: 2, y: 2 } : positioned.end;
        await gesture(positioned.start, [point], release);
        assert.equal(location.hash, hash, `${release} cannot synthesize card navigation`);
        equalOrder(ids(), before, `${release} must preserve the last saved order`);
        assert.ok(grid.parentElement.querySelector('[data-phase="idle"]'), "cancellation is announced without a toast");
      }

      const beforeFailure = ids(), persisted = accountStorage.getItem(orderKey);
      const nativeSet = Storage.prototype.setItem;
      let writeAttempts = 0;
      if (type === "nodes") testAPI.failNodeOrder = true;
      Storage.prototype.setItem = function(key, value) {
        if (type === "traffic" && key.startsWith(orderKey)) { writeAttempts++; throw new DOMException("fixture quota exceeded", "QuotaExceededError"); }
        return nativeSet.call(this, key, value);
      };
      try {
        const positioned = geometry(grip, cards()[0]);
        assert.ok(grid.isConnected && grip.isConnected, `failure fixture must retain its grid: ${type}/${theme}/${location.hash}`);
        await gesture(positioned.start, [positioned.end], "drop", async () => {
          assert.ok(document.querySelector(".card-move-ghost"), `failure gesture must pick up: ${type}/${theme}/${location.hash}`);
          assert.equal(grid.querySelector(".drop-target"), grip.closest(cardSelector), `failure gesture keeps its placeholder: ${type}/${theme}`);
        });
        equalOrder(ids(), beforeFailure, "failed persistence must restore the last saved DOM order");
        assert.equal(accountStorage.getItem(orderKey), persisted);
        if (type === "traffic") assert.equal(writeAttempts, 1, "failed drop must attempt exactly one write");
        assert.ok(grid.parentElement.querySelector('[data-phase="error"]')?.textContent.includes("已恢复原位"), "storage failure needs explicit recovery feedback");
        assert.ok(document.querySelector('[data-spa-notice][role="alert"]'), "failed saves use the existing error surface");
        document.querySelector('[data-spa-notice] .notice-close').click();
      } finally { Storage.prototype.setItem = nativeSet; testAPI.failNodeOrder = false; }
      key(grip, "ArrowLeft"); await settled();
      equalOrder(ids(), originalIds, "moving after a failure must remain reliable");

      if (draft) assert.equal(draft.value, "unsaved card movement draft", "moving a card retains its internal form draft");

      // A fresh document reads the same account preference rather than DOM state.
      if (theme === "dark") {
        const frame = document.createElement("iframe");
        frame.style.cssText = "position:fixed;left:-2000px;width:1280px;height:900px";
        frame.src = `/agents-browser-smoke.html?mode=${mode}&preview=1${hash}`;
        document.body.append(frame);
        await waitFor(() => frame.contentDocument?.querySelector(selector), "fresh document did not load");
        const reloaded = [...frame.contentDocument.querySelectorAll(`${selector} > ${cardSelector}`)]
          .map(card => type === "nodes" ? card.dataset.agentNode : card.dataset.trafficCardKey);
        equalOrder(reloaded, ids(), "fresh document must restore saved order");
        frame.remove();
      }
    }
  }

  // Filtered traffic moves only the visible slots in the complete order.
  let grid = await route("#traffic-all", ".traffic-policy-grid");
  const keys = () => [...grid.querySelectorAll("[data-traffic-card-key]")].map(card => card.dataset.trafficCardKey);
  const all = keys(), filter = document.querySelector('[data-traffic-filter="engine"]');
  filter.value = "xray"; filter.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => document.querySelectorAll('.traffic-policy-grid > [data-traffic-card-key]').length === 2, "filtered traffic missing");
  grid = document.querySelector(".traffic-policy-grid");
  const visible = keys(); key(grid.querySelector(".traffic-card-grip"), "ArrowRight"); await settled();
  const visibleSet = new Set(visible), reversed = [...visible].reverse();
  let cursor = 0;
  equalOrder(JSON.parse(accountStorage.getItem("qcontrolhub:traffic-card-order")), all.map(id => visibleSet.has(id) ? reversed[cursor++] : id), "filtered movement must preserve hidden positions");
  filter.value = ""; filter.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => document.querySelectorAll('.traffic-policy-grid > [data-traffic-card-key]').length === all.length, "unfiltered traffic missing");
  await waitFor(() => !document.getAnimations().some(animation => animation.id.startsWith("qch")), "filter reveal did not settle");

  // Discovered listeners keep the original dashed traffic border while sharing
  // the destination inset and preserving their scoped traffic layout.
  grid = document.querySelector(".traffic-policy-grid");
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    const before = keys(), cards = [...grid.querySelectorAll("[data-traffic-card-key]")];
    const port = grid.querySelector(".traffic-configured-port"), grip = port.querySelector(".traffic-card-grip");
    const index = cards.indexOf(port), targetIndex = index + 1 < cards.length ? index + 1 : index - 1;
    const positioned = geometry(grip, cards[targetIndex]);
    await gesture(positioned.start, [positioned.end, positioned.start, positioned.end], "drop", async step => {
      const ghost = document.querySelector(".card-move-ghost");
      trafficPaint(port, ghost);
      assert.equal(getComputedStyle(ghost).borderTopStyle, "dashed", "discovered listener keeps its original traffic border");
      const target = port;
      assert.equal(grid.querySelector(".drop-target"), target, `discovered listener border follows the selected card: ${theme}/${step}; source ${index}, target ${targetIndex}, actual ${cards.indexOf(grid.querySelector(".drop-target"))}`);
      assert.equal(Number(port.style.order), step === 1 ? index : targetIndex, `listener selected slot ${theme}/${step}; pointer ${JSON.stringify(step === 1 ? positioned.start : positioned.end)}; slots ${JSON.stringify(cards.map(c => [c.style.order,c.offsetTop,c.offsetHeight]))}`);
      if (target) assert.ok(getComputedStyle(target).boxShadow.includes("inset"), "discovered listener destination shares the traffic inset border");
    });
    const expected = [...before]; expected.splice(index, 1); expected.splice(targetIndex, 0, before[index]);
    equalOrder(keys(), expected, "discovered listener drop saves the selected slot");
    const reversed = geometry(grip, grid.querySelectorAll("[data-traffic-card-key]")[index]);
    await gesture(reversed.start, [reversed.end], "escape");
    equalOrder(keys(), expected, "discovered listener cancellation retains the saved order");
    key(grip, targetIndex > index ? "ArrowLeft" : "ArrowRight"); await settled();
    equalOrder(keys(), before, "discovered listener keyboard move restores its original slot");
    assert.equal(document.querySelector(".card-move-layer"), null, "settled movement removes its viewport layer");
  }

  // A card's own dialog stays a DOM child of the card, so its presses still
  // bubble to the grid. They must remain native: no preview, no drop slot, no
  // saved order, and no preventDefault that would swallow text selection or a
  // dialog control. Presses on the card surface outside the dialog must still
  // arm the drag controller, which is covered right after the dialogs close.
  grid = document.querySelector(".traffic-policy-grid");
  const dialogBefore = keys(), dialogSaved = accountStorage.getItem("qcontrolhub:traffic-card-order");
  for (const [name, opener, openSelector, closer] of [
    ["quota", "[data-traffic-edit-open]", "[data-traffic-edit-dialog][open]", "[data-traffic-edit-close]"],
    ["status", "[data-traffic-status-open]", "[data-traffic-status-dialog][open]", "[data-traffic-status-close]"],
  ]) {
    const launcher = grid.querySelector(opener);
    assert.ok(launcher, `${name} dialog launcher missing from the traffic fixture`);
    launcher.click();
    const dialog = await waitFor(() => document.querySelector(openSelector), `${name} dialog did not open`);
    assert.ok(dialog.closest("[data-traffic-card-key]"), `${name} dialog must stay inside its card`);
    const frames = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await frames();
    for (const animation of dialog.getAnimations()) animation.finish();
    await frames();
    const heading = dialog.querySelector("header h2").getBoundingClientRect();
    assert.ok(heading.width > 20 && heading.height > 8, `${name} dialog header must be painted`);
    const press = { x: heading.left + 4, y: heading.top + heading.height / 2 }, presses = [];
    const record = event => { if (event.target.closest("dialog")) presses.push(event.defaultPrevented); };
    document.addEventListener("pointerdown", record);
    try {
      await gesture(press, [{ x: press.x + 40, y: press.y + 2 }], "drop", async () => {
        assert.equal(document.querySelector(".card-move-ghost"), null, `${name} dialog press cannot pick up a card`);
        assert.equal(grid.querySelector(".drop-target"), null, `${name} dialog press cannot claim a drop slot`);
      });
    } finally { document.removeEventListener("pointerdown", record); }
    assert.ok(presses.length && presses.every(prevented => prevented === false), `${name} dialog press keeps native selection and focus`);
    equalOrder(keys(), dialogBefore, `${name} dialog press cannot change the card order`);
    assert.equal(accountStorage.getItem("qcontrolhub:traffic-card-order"), dialogSaved, `${name} dialog press cannot save a card order`);
    assert.equal(document.body.classList.contains("card-moving"), false, `${name} dialog press cannot start a drag state`);
    dialog.querySelector(closer).click();
    await waitFor(() => !document.querySelector(openSelector), `${name} dialog did not close`);
  }
  // Mouse/pen presses on the card surface still arm the controller, so the
  // dialog exclusion is scoped to dialogs and pointer ordering keeps working.
  if (!mobile) {
    const dialogCard = grid.querySelector("[data-traffic-edit-dialog]")?.closest("[data-traffic-card-key]");
    assert.ok(dialogCard, "the quota dialog must belong to a traffic card");
    dialogCard.scrollIntoView({ block: "center", behavior: "instant" });
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const facts = dialogCard.querySelector(".traffic-card-facts")?.getBoundingClientRect();
    assert.ok(facts && facts.width > 24, "the dialog card must paint its facts block");
    const surface = { x: facts.left + 12, y: facts.top + Math.min(facts.height / 2, 20) }, surfacePresses = [];
    // The controller stops propagation while it arms, so keep the native event
    // and read defaultPrevented after dispatch.
    const capture = { capture: true }, recordArm = event => surfacePresses.push(event);
    document.addEventListener("pointerdown", recordArm, capture);
    try {
      await gesture(surface, [surface], "drop", async () => {
        assert.equal(document.querySelector(".card-move-ghost"), null, "a below-threshold card press cannot pick up a card");
      });
    } finally { document.removeEventListener("pointerdown", recordArm, capture); }
    assert.ok(surfacePresses.some(event => event.defaultPrevented), "card surface presses still belong to the drag controller");
    equalOrder(keys(), dialogBefore, "a below-threshold card press cannot change the card order");
    assert.equal(accountStorage.getItem("qcontrolhub:traffic-card-order"), dialogSaved, "a below-threshold card press cannot save a card order");
  }

  grid = await route("#node-settings", ".node-card-grid");
  const grip = grid.querySelector(".node-card-grip"), card = grip.closest(".node-card");
  const scroller = mobile ? document.scrollingElement : document.querySelector(".workspace-main");
  const positioned = geometry(grip, card), scrollBefore = scroller.scrollTop;
  await gesture(positioned.start, [{ x: positioned.start.x, y: innerHeight - (mobile ? 108 : 8) }], "escape", async () => {
    await waitFor(() => scroller.scrollTop > scrollBefore + 20, "dragging near the viewport edge must scroll");
  });
  if (mobile) {
    card.scrollIntoView({ block: "center", behavior: "instant" });
    const rect = card.getBoundingClientRect(), before = scrollY;
    const start = { x: rect.left + 80, y: Math.min(innerHeight - 150, rect.top + 240) };
    await gesture(start, [{ x: start.x, y: start.y - 150 }], "drop", async () => {
      assert.equal(document.querySelector(".card-move-ghost"), null, "card content remains available for page scrolling");
    });
    assert.ok(scrollY > before + 20, "normal touch outside the grip must scroll the page");
  }
  const beforeLeave = JSON.stringify(JSON.parse(accountStorage.getItem("qcontrolhub:node-card-order")));
  const leave = geometry(grip, grid.querySelectorAll(".node-card")[1]);
  await gesture(leave.start, [leave.end], "leave");
  assert.equal(document.querySelector(".card-move-ghost"), null, "route exit must remove all preview layers");
  assert.equal(document.querySelector(".card-move-layer"), null, "route exit must remove the viewport layer");
  assert.equal(document.body.classList.contains("card-moving"), false);
  assert.equal(JSON.stringify(JSON.parse(accountStorage.getItem("qcontrolhub:node-card-order"))), beforeLeave, "route exit cannot commit an unfinished drag");
  window.cardMovementResult = `${mobile ? "touch" : "mouse"}; light+dark; ${reduced ? "reduced" : "full"} motion; threshold/reversal/polling/keyboard/cancel/failure/reload/filter/dialog/edge/leave`;
}
