import { assert, delay, waitFor } from "./assertions.mjs";
import { animateMotion, cancelMotion } from "../modules/motion.js";
import { reconcileView } from "../modules/refresh.js";
import { createShellFeedback } from "../modules/shell-feedback.js";

const animations = () => document.getAnimations().filter(a => a.playState === "running" || a.playState === "pending");
const key = (grip, value) => grip.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true }));
const route = async (hash, selector) => {
  location.hash = hash;
  return waitFor(() => !document.querySelector(".is-route-pending") && document.querySelector(selector), `route missing: ${hash}`);
};
const gesture = async (grip, target, action = "drop") => {
  grip.scrollIntoView({ block: "center", behavior: "instant" });
  grip.focus();
  const start = grip.getBoundingClientRect(), card = grip.closest(".node-card,.traffic-policy-card").getBoundingClientRect();
  const box = target.getBoundingClientRect();
  const x = start.left + start.width / 2, y = start.top + start.height / 2;
  window.__motionGesture = { action, start: { x, y }, end: {
    x: box.left + box.width / 2 + x - (card.left + card.width / 2),
    y: box.top + box.height / 2 + y - (card.top + card.height / 2),
  } };
  await waitFor(() => window.__motionGesture === null, "browser pointer gesture did not run");
};
const settled = async () => waitFor(() => !animations().some(a => a.id.startsWith("qch-")), "native motion did not settle");

export async function testMotionRuntime({ mode, testAPI }, preview) {
  const reduced = mode.endsWith("reduced");
  if (!preview) assert.equal(matchMedia("(prefers-reduced-motion: reduce)").matches, reduced);
  const grid = await waitFor(() => document.querySelector(".node-card-grid"), "nodes missing");
  if (preview) await new Promise(() => {});
  assert.equal([...grid.querySelectorAll(".node-card")].some(card => card.getAnimations().length), false,
    "route entrance must not animate nested cards");
  await settled();
  if (reduced) assert.equal(animations().length, 0, "reduced motion disables CSS and JS entrances");
  const originalCards = [...grid.querySelectorAll(".node-card")];
  await gesture(originalCards[0].querySelector(".node-card-grip"), originalCards[1]);
  await settled();
  assert.equal(grid.querySelectorAll(".node-card")[1], originalCards[0], "pointer drop commits the selected slot");
  assert.equal(document.querySelector(".node-card-ghost"), null, "pointer drop removes the ghost");
  const orderAfterDrop = [...grid.querySelectorAll(".node-card")];
  await gesture(orderAfterDrop[0].querySelector(".node-card-grip"), orderAfterDrop[1], "cancel");
  assert.equal(grid.querySelector(".node-card"), orderAfterDrop[0], "Escape cancels pointer drag without committing");
  assert.equal(document.querySelector(".node-card-ghost"), null);
  const grip = grid.querySelector(".node-card-grip");
  const card = grip.closest(".node-card");
  grip.focus();
  key(grip, "ArrowRight");
  key(grip, "ArrowLeft");
  key(grip, "ArrowRight");
  await settled();
  assert.equal(grid.querySelectorAll(".node-card")[1], card, "rapid keyboard reorder must commit the final order");
  assert.equal(document.activeElement, grip, "reordering retains focus");
  assert.ok([...grid.querySelectorAll(".node-card")].every(item => !item.style.transform && !item.style.transition));

  const dock = document.querySelector(".app-dock"), mobile = mode.startsWith("motion-mobile");
  const menu = dock.querySelector(".mobile-account-menu");
  if (mobile) menu.open = true;
  const link = dock.querySelector(mobile ? '.mobile-account-menu a[href="#settings"]' : '.dock-tools a[href="#settings"]');
  link.focus(); link.click();
  await waitFor(() => document.querySelector("#settings-form"), "settings missing");
  assert.equal(document.querySelector(".app-dock"), dock, "dock DOM survives navigation");
  assert.equal(document.activeElement, mobile ? menu.querySelector("summary") : link, "dock focus survives navigation");
  await settled();
  const form = document.querySelector("#settings-form"), input = form.elements.panel_name;
  input.value = "unsaved motion draft";
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.focus(); input.setSelectionRange(2, 6);
  const main = document.querySelector(".workspace-main");
  main.scrollTop = 140;
  const scroll = main.scrollTop, reads = testAPI.calls.length;
  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await waitFor(() => testAPI.calls.length > reads && main.getAttribute("aria-busy") !== "true", "same-page refresh missing");
  assert.equal(document.querySelector("#settings-form"), form, "same-page refresh retains the form");
  assert.equal(document.activeElement, input, "same-page refresh retains keyboard focus");
  assert.equal(input.value, "unsaved motion draft");
  assert.equal(form.querySelector("[data-save-settings]").disabled, false, "retained draft keeps save available");
  assert.equal(input.selectionStart, 2); assert.equal(input.selectionEnd, 6);
  assert.equal(main.scrollTop, scroll, "same-page refresh retains scrolling");
  assert.equal(main.getAnimations().length, 0, "unchanged route does not replay entrance");
  const before = input.getBoundingClientRect(); input.blur(); input.focus();
  const after = input.getBoundingClientRect();
  assert.equal(after.top, before.top, "input focus must not move its hit area");
  assert.equal(after.left, before.left);

  // Save feedback retains edits made while the request is in flight.
  let release;
  testAPI.settingsGate = new Promise(resolve => { release = resolve; });
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  assert.equal(form.getAttribute("aria-busy"), "true");
  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await delay(30);
  assert.equal(form.getAttribute("aria-busy"), "true", "refresh cannot replace an in-flight save");
  input.value = "new edit during save";
  input.dispatchEvent(new Event("input", { bubbles: true }));
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  testAPI.settingsGate = null; release();
  await waitFor(() => form.getAttribute("aria-busy") !== "true", "save did not clean up busy state");
  assert.equal(input.value, "new edit during save");
  assert.match(document.querySelector("[data-settings-state]").textContent, /未保存/);
  assert.equal(form.querySelector("[data-save-settings]").disabled, false);

  // Confirmations settle even when superseded or closed externally.
  const feedbackState = {}, feedback = createShellFeedback(feedbackState);
  feedback.bindConfirmationDialog();
  const first = feedback.confirmAction("first"), second = feedback.confirmAction("second");
  assert.equal(await first, false);
  await delay(20);
  assert.ok(feedbackState.confirmOpen, "queued close event must not cancel a reopened dialog");
  document.querySelector("[data-confirm-dialog]").close();
  assert.equal(await second, false, "external close must settle the promise");
  for (let i = 0; i < 4; i++) {
    const pending = feedback.confirmAction("rapid toggle");
    document.querySelector("[data-confirm-cancel]").click();
    assert.equal(await pending, false);
  }
  feedback.notify("stable result");
  const notice = document.querySelector("[data-spa-notice]");
  await settled();
  feedback.notify("stable result");
  assert.equal(notice.getAnimations().length, 0, "identical feedback does not replay");
  const currentMain = document.querySelector(".workspace-main");
  const freshMain = currentMain.cloneNode(true); freshMain.querySelector("[data-spa-notice]").remove();
  reconcileView(currentMain, freshMain);
  assert.equal(document.querySelector("[data-spa-notice]"), notice, "polling retains operation feedback");
  notice.querySelector(".notice-close").click();
  feedback.resetFeedback();
  assert.equal(document.querySelector("dialog[open]"), null, "rapid close leaves no modal behind");

  const traffic = await route("#traffic", ".traffic-policy-grid");
  const trafficGrip = traffic.querySelector(".traffic-card-grip"), trafficCard = trafficGrip.closest("[data-traffic-card-key]");
  trafficGrip.focus(); key(trafficGrip, "ArrowRight"); key(trafficGrip, "ArrowLeft");
  await settled();
  assert.equal(traffic.querySelector("[data-traffic-card-key]"), trafficCard);
  assert.equal(document.activeElement, trafficGrip);
  const trafficCards = [...traffic.querySelectorAll("[data-traffic-card-key]")];
  await gesture(trafficGrip, trafficCards[1], "leave");
  await waitFor(() => document.querySelector(".core-log-row"), "route departure did not render logs");
  assert.equal(document.querySelector(".traffic-card-ghost"), null);
  assert.equal(document.body.classList.contains("traffic-card-dragging"), false);
  await settled();
  const stream = document.querySelector(".core-log-stream");
  assert.equal(stream.getAnimations({ subtree: true }).length, 0, "logs do not replay entrance animations");

  // Reconciliation preserves editor selection and open disclosure state.
  const host = document.createElement("section");
  host.innerHTML = '<textarea name="editor">saved</textarea><details open><summary>result</summary><pre>first</pre></details>';
  document.querySelector(".workspace-main").append(host);
  const editor = host.querySelector("textarea");
  editor.value = "unsaved editor text"; editor.focus(); editor.setSelectionRange(3, 8);
  const fresh = host.cloneNode(true); fresh.querySelector("pre").textContent = "updated";
  reconcileView(host, fresh);
  assert.equal(editor.value, "unsaved editor text");
  assert.equal(editor.selectionStart, 3); assert.equal(editor.selectionEnd, 8);
  assert.equal(host.querySelector("details").open, true);
  host.remove();

  // Task polling updates output in place without replaying row entrances.
  testAPI.tasks = [{ id: "motion-task", agent_id: "alpha", engine: "mihomo", action: "validate", status: "succeeded",
    created_at: "2026-10-01T00:00:00Z", started_at: "2026-10-01T00:00:01Z", finished_at: "2026-10-01T00:00:02Z", output: "first result" }];
  const task = await route("#tasks", "[data-task-id]");
  await settled();
  const result = task.querySelector("[data-task-result]");
  result.querySelector("summary").focus(); result.open = true;
  await delay(20); await settled();
  testAPI.tasks[0].output = "updated result";
  document.querySelector("#refresh").click();
  await waitFor(() => task.textContent.includes("updated result"), "task refresh missing");
  assert.equal(document.querySelector("[data-task-id]"), task);
  assert.equal(result.open, true, "task output refresh retains open results");
  assert.equal(task.getAnimations({ subtree: true }).filter(a => a.id || a.animationName).length, 0, `task polling does not replay entrance: ${task.getAnimations({ subtree: true }).map(a => a.id || a.transitionProperty || a.animationName).join(",")}`);

  // Removed elements release native animations without finish events.
  const temporary = document.createElement("div"); document.body.append(temporary);
  animateMotion(temporary, [{ opacity: 0 }, { opacity: 1 }]);
  temporary.remove(); await delay(20);
  assert.equal(temporary.getAnimations().length, 0);
  cancelMotion();
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    const field = document.querySelector("#task-agent");
    field.focus();
    assert.ok(getComputedStyle(field).outlineStyle !== "none", "keyboard focus remains visible in both themes");
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "console must not overflow");
  }
  if (reduced) assert.equal(animations().length, 0, "reduced motion keeps loading/results readable without animation");
  assert.equal(document.querySelector(".is-route-pending"), null);
  assert.equal(document.querySelector(".modal-backdrop"), null);
  document.querySelector("#logout").click();
  const login = await waitFor(() => document.querySelector("#login-form"), "login missing after logout");
  login.elements.username.value = "motion-test";
  login.elements.token.value = "fixture-password-123";
  login.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => !document.querySelector("[data-login-error]").hidden, "login failure feedback missing");
  assert.equal(document.querySelector("#login-form"), login, "login failure must retain the form");
  assert.equal(login.elements.username.value, "motion-test");
  assert.equal(login.elements.token.value, "fixture-password-123");
  assert.equal(login.querySelector("button").disabled, false);
  await settled();
  if (reduced) assert.equal(animations().length, 0);
}
