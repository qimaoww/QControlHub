import { assert, delay, waitFor } from "./assertions.mjs";
import { createAgentEnrollment } from "../modules/agent-enrollment.js";
import { bindConfigMenu } from "../modules/config-menu.js";
import { cancelPopupEntrance } from "../modules/popup.js";

const settle = async () => { await delay(320); };
const escape = element => element.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
const pointer = (element, type, x, y) => element.dispatchEvent(new PointerEvent(type, { bubbles: true, clientX: x, clientY: y }));
const click = (element, x, y) => element.dispatchEvent(new MouseEvent("click", { bubbles: true, clientX: x, clientY: y }));
const scrimGesture = (dialog, inside = false) => {
  const rect = dialog.getBoundingClientRect();
  pointer(dialog, "pointerdown", inside ? rect.left + 12 : -10, inside ? rect.top + 12 : -10);
  click(dialog, -10, -10);
};

export async function testPopupRuntime({ mode, testAPI }, preview) {
  const reduced = mode.endsWith("reduced");
  const root = await waitFor(() => document.querySelector(".desktop-app"), "shell missing");
  const launcher = await waitFor(() => document.querySelector("[data-open-enrollment]"), "enrollment launcher missing");
  await settle();
  if (preview) { launcher.focus(); launcher.click(); await new Promise(() => {}); }
  const overflow = document.body.style.overflow;
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    launcher.focus(); launcher.click();
    const wrap = await waitFor(() => document.querySelector(".enrollment-backdrop"), "enrollment modal missing");
    const card = wrap.querySelector("[role=dialog]");
    assert.equal(wrap.getAnimations().length, 0, "scrim must not fade its child content");
    assert.equal(root.inert, true);
    assert.ok(wrap.contains(document.activeElement), "modal takes focus immediately");
    if (reduced) assert.equal(card.getAnimations().length, 0);
    await settle();
    const bounds = card.getBoundingClientRect();
    assert.ok(bounds.left >= -1 && bounds.right <= innerWidth + 1 && bounds.top >= -1 && bounds.bottom <= innerHeight + 1);
    pointer(card, "pointerdown", bounds.left + 12, bounds.top + 12); click(wrap, 0, 0);
    assert.ok(wrap.isConnected, "a drag from modal content must not dismiss it");
    escape(document.activeElement);
    assert.equal(wrap.isConnected, false);
    assert.equal(root.inert, false);
    assert.equal(document.body.style.overflow, overflow);
    assert.equal(document.activeElement, launcher, "Escape restores launcher focus");
  }
  for (let i = 0; i < 3; i++) {
    launcher.focus(); launcher.click();
    const wrap = document.querySelector(".enrollment-backdrop");
    pointer(wrap, "pointerdown", 0, 0); click(wrap, 0, 0);
    assert.equal(document.querySelector(".modal-backdrop"), null, "rapid scrim close leaves no modal");
  }
  launcher.focus(); launcher.click();
  location.hash = "#traffic";
  const quotaTrigger = await waitFor(() => document.querySelector("[data-traffic-edit-open]"), "traffic missing");
  assert.equal(document.querySelector(".modal-backdrop"), null, "route departure clears custom modal");
  assert.equal(root.inert, false);
  assert.equal(document.body.style.overflow, overflow);
  quotaTrigger.focus(); quotaTrigger.click();
  const quota = document.querySelector("[data-traffic-edit-dialog][open]");
  await settle();
  scrimGesture(quota, true);
  assert.ok(quota.open, "native content-to-scrim drag keeps dialog open");
  click(quota, quota.getBoundingClientRect().left + 8, quota.getBoundingClientRect().top + 8);
  assert.ok(quota.open, "native dialog padding is not a backdrop");
  scrimGesture(quota);
  assert.equal(quota.open, false);
  assert.equal(document.activeElement, quotaTrigger, "native close restores focus");
  for (let i = 0; i < 3; i++) { quotaTrigger.click(); quota.querySelector("[data-traffic-edit-close]").click(); }
  quotaTrigger.click();
  cancelPopupEntrance(quota);
  assert.equal(quota.getAnimations().filter(a => a.animationName === "qch-dialog-enter").length, 0,
    "restoring a native modal never replays entrance");
  quota.close();
  await settle();

  // Real configuration menu handlers, including a same-DOM refresh/rebind.
  const host = document.createElement("div");
  host.innerHTML = '<details class="config-inbound-menu"><summary>Menu</summary><div role="menu"><button role="menuitem">Action</button></div></details><button data-outside-popup>Outside</button>';
  document.querySelector(".workspace-main").append(host);
  const menu = host.querySelector("details"), summary = menu.querySelector("summary"), content = menu.querySelector("[role=menu]");
  bindConfigMenu(menu); summary.focus(); summary.click(); await delay(25);
  assert.ok(menu.open);
  assert.equal(menu.getAnimations().length, 0, "menu launcher must not fade with the panel");
  assert.equal(summary.getAttribute("aria-expanded"), "true");
  if (!reduced) assert.ok(content.getAnimations().some(a => a.id === "qch-disclosure"));
  bindConfigMenu(menu);
  assert.ok(menu.open, "rebind retains an open configuration menu");
  menu.querySelector("button").focus(); escape(document.activeElement);
  assert.equal(menu.open, false); assert.equal(document.activeElement, summary);
  summary.click(); await delay(25);
  pointer(document.body, "pointerdown", 0, 0);
  assert.equal(menu.open, false);
  summary.focus(); summary.click(); await delay(25);
  host.querySelector("[data-outside-popup]").focus(); assert.equal(menu.open, false, "focus leaving menu dismisses it");
  host.remove();
  const more = document.querySelector(".mobile-account-menu");
  if (mode.includes("mobile")) {
    more.querySelector("summary").focus(); more.open = true; await delay(25);
    more.querySelector("a").focus(); escape(document.activeElement);
    assert.equal(more.open, false);
    assert.equal(document.activeElement, more.querySelector("summary"));
  }

  location.hash = "#dashboard";
  const month = await waitFor(() => document.querySelector(".dashboard-month-picker"), "dashboard month picker missing");
  await settle();
  const monthSummary = month.querySelector("summary");
  monthSummary.focus(); monthSummary.click(); await delay(25);
  assert.ok(month.open);
  assert.equal(month.getAnimations().length, 0);
  month.querySelector("button").focus(); escape(document.activeElement);
  assert.equal(month.open, false); assert.equal(document.activeElement, monthSummary);

  // Late production API responses must not resurrect a closed popup.
  location.hash = "#node-settings";
  const nextLauncher = await waitFor(() => document.querySelector("[data-open-enrollment]"), "nodes missing after popup checks");
  let release;
  testAPI.enrollmentGate = new Promise(resolve => { release = resolve; });
  nextLauncher.focus(); nextLauncher.click();
  const pendingWrap = document.querySelector(".enrollment-backdrop");
  pendingWrap.querySelector("input").value = "pending";
  pendingWrap.querySelector("form").requestSubmit();
  await waitFor(() => testAPI.lastEnrollmentRequest?.name === "pending", "pending enrollment request missing");
  const submits = testAPI.calls.filter(call => call.path === "/enrollment-tokens" && call.method === "POST").length;
  pendingWrap.querySelector("form").requestSubmit(); await delay(15);
  assert.equal(testAPI.calls.filter(call => call.path === "/enrollment-tokens" && call.method === "POST").length, submits,
    "pending enrollment does not submit twice");
  escape(document.activeElement); testAPI.enrollmentGate = null; release(); await delay(40);
  assert.equal(document.querySelector(".modal-backdrop"), null, "closed pending modal stays closed");
  testAPI.enrollmentGate = new Promise(resolve => { release = resolve; });
  nextLauncher.focus(); nextLauncher.click();
  const refreshingWrap = document.querySelector(".enrollment-backdrop");
  refreshingWrap.querySelector("input").value = "refresh-pending";
  refreshingWrap.querySelector("form").requestSubmit();
  await waitFor(() => testAPI.lastEnrollmentRequest?.name === "refresh-pending", "refresh enrollment request missing");
  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await waitFor(() => !document.querySelector(".workspace-main[aria-busy]"), "same-route refresh did not settle");
  assert.ok(refreshingWrap.isConnected, "same-route refresh retains the pending popup");
  testAPI.enrollmentGate = null; release();
  await waitFor(() => document.querySelector(".deploy-command-input"), "same-route refresh cannot lose a pending command response");
  escape(document.activeElement);

  // The directory request and external-removal lifecycle are isolated here.
  let directoryRelease;
  const state = { data: {}, navigationEpoch: 1 };
  const enrollment = createAgentEnrollment({ state, esc: value => String(value), engineName: value => value,
    notify: () => {}, refreshAgentPage: async () => {}, api: () => new Promise(resolve => { directoryRelease = resolve; }) });
  const directory = enrollment.showAgentDirectoryDialog();
  state.navigationEpoch++; directoryRelease([]); await directory;
  assert.equal(document.querySelector(".modal-backdrop"), null, "late directory response stays closed after navigation");
  const supersededDirectory = enrollment.showAgentDirectoryDialog();
  enrollment.showCommand("fixture command");
  directoryRelease([]); await supersededDirectory;
  assert.ok(document.querySelector(".deploy-command-input"), "late directory response cannot replace a newer command popup");
  document.querySelector(".modal-backdrop").remove(); await delay(25);
  assert.equal(root.inert, false, "external removal restores background interaction");
  assert.equal(document.body.style.overflow, overflow);
  await settle();
  assert.equal(document.querySelector("dialog:modal,.modal-backdrop"), null);
  if (reduced) assert.equal(document.getAnimations().length, 0);
}
