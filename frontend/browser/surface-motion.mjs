import { assert, delay, waitFor } from "./assertions.mjs";
import { cancelMotion, retireSurface } from "../modules/motion.js";
import { setVisible, updateFeedback } from "../modules/presence-motion.js";
import { closeRetiringDialog } from "../modules/popup.js";
import { reconcileView } from "../modules/refresh.js";
import { routeModuleNames } from "../modules/routes.js";

const animations = element => element.getAnimations({ subtree: true });
// Chromium's internal details-content pseudo is not enumerated by getAnimations.
// Inspect its actual paint state rather than confusing launcher focus transitions
// with content motion.
const disclosureStyle = element => getComputedStyle(element, "::details-content");
const dialogEntered = dialog => getComputedStyle(dialog).opacity === "1" && animations(dialog).length === 0;
const disclosureEntered = details => disclosureStyle(details).opacity === "1" && disclosureStyle(details).overflow === "visible";
export async function checkNativeExit(dialog, launcher, reduced) {
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    launcher.focus(); launcher.click();
    await waitFor(() => dialogEntered(dialog), "dialog entrance must finish before checking its full exit");
    dialog.close();
    assert.equal(dialog.open, false, "close commits business state immediately");
    assert.equal(document.querySelector("dialog:modal"), null);
    assert.equal(document.activeElement, launcher, "exit never postpones focus restoration");
    assert.equal(dialog.inert, true, "closed rendered dialog cannot receive keyboard focus");
    assert.equal(dialog.getAttribute("aria-hidden"), "true");
    assert.equal(getComputedStyle(dialog).pointerEvents, "none");
    if (reduced) {
      assert.equal(getComputedStyle(dialog).display, "none", "reduced-motion dialog closes without a paint tail");
      assert.equal(getComputedStyle(dialog, "::backdrop").transitionDuration, "0s");
    }
    const hit = document.elementFromPoint(innerWidth / 2, innerHeight / 2);
    assert.ok(hit && !dialog.contains(hit), "closed top layer and backdrop cannot intercept background clicks");
    if (!reduced) assert.ok(animations(dialog).some(a => a.transitionProperty === "opacity"), "native exit missing");
    const fresh = dialog.cloneNode(true);
    fresh.removeAttribute("inert"); fresh.removeAttribute("aria-hidden");
    reconcileView(dialog, fresh);
    assert.ok(dialog.inert && dialog.getAttribute("aria-hidden") === "true", "refresh preserves closed surface accessibility");
    // CI can schedule the first rendering frame after the timer has fired.
    // Observe the bounded final paint state instead of assuming wall-clock
    // time advances the browser's transition timeline at the same rate.
    await waitFor(() => getComputedStyle(dialog).display === "none" && animations(dialog).length === 0,
      "closed dialog must finish its exit without retained top-layer paint");
    assert.equal(getComputedStyle(dialog).display, "none");
    assert.equal(animations(dialog).length, 0);
    for (let i = 0; i < 4; i++) {
      launcher.click(); await delay(25);
      assert.equal(dialog.inert, false, "reopening restores native interaction");
      assert.equal(dialog.hasAttribute("aria-hidden"), false);
      dialog.close(); await delay(20);
    }
    launcher.click();
    await waitFor(() => dialogEntered(dialog), "rapid dialog reversal must finish its entrance");
    assert.ok(dialog.open && !dialog.inert);
    assert.equal(getComputedStyle(dialog).opacity, "1");
    assert.ok(getComputedStyle(dialog).translate.split(" ").every(value => Math.abs(Number.parseFloat(value)) < .01),
      "completed dialog has no residual displacement");
    dialog.close();
    await waitFor(() => getComputedStyle(dialog).display === "none" && animations(dialog).length === 0,
      "rapid dialog reversal must finish without retained top-layer paint");
    assert.equal(getComputedStyle(dialog).display, "none", "rapid reversal leaves no top-layer residue");
  }
}

export async function checkDisclosureMotion(_workspace, reduced) {
  // Keep injected regression fixtures outside server-owned polling markup.
  // Production menus and the route sweep below still use the actual workspace.
  const root = document.createElement("section"); document.body.append(root);
  const host = document.createElement("section");
  host.innerHTML = '<details class="runtime-drawer"><summary><b>Dynamic disclosure</b><i>+</i></summary><div><label>Draft <input value="keep"></label><p>Second child</p></div><pre>Third child</pre></details><button>After disclosure</button>';
  root.append(host);
  const details = host.querySelector("details"), summary = host.querySelector("summary"), input = host.querySelector("input");
  summary.focus(); summary.click(); await delay(35);
  assert.ok(details.open && details.hasAttribute("data-motion-disclosure"), "dynamically inserted disclosure participates");
  if (reduced) assert.equal(disclosureStyle(details).transitionDuration, "0s", "native disclosure pseudo obeys reduced motion");
  if (!reduced) assert.ok(Number(disclosureStyle(details).opacity) < 1, "disclosure entrance missing");
  await waitFor(() => disclosureEntered(details), "dynamic disclosure entrance must finish without clipping");
  input.value = "unsaved"; input.focus(); input.setSelectionRange(2, 4);
  const fresh = host.cloneNode(true);
  fresh.querySelector("details").removeAttribute("data-motion-disclosure");
  reconcileView(host, fresh);
  assert.equal(host.querySelector("details"), details);
  assert.ok(details.hasAttribute("data-motion-disclosure"), "refresh retains disclosure ownership");
  assert.equal(document.activeElement, input);
  assert.equal(input.value, "unsaved");
  assert.equal(input.selectionStart, 2); assert.equal(input.selectionEnd, 4);
  assert.equal(disclosureStyle(details).opacity, "1", "same-data refresh does not replay open content");
  summary.focus(); summary.click(); await delay(30);
  assert.equal(details.open, false);
  input.focus();
  assert.equal(document.activeElement, summary, "closing disclosure content cannot retain keyboard focus");
  assert.ok(input.closest("[inert][aria-hidden=true]"), "closing content leaves the accessibility tree immediately");
  const closedRefresh = host.cloneNode(true);
  closedRefresh.querySelectorAll("[data-motion-retired-content]").forEach(child => {
    child.removeAttribute("inert"); child.removeAttribute("aria-hidden"); child.removeAttribute("data-motion-retired-content");
  });
  reconcileView(host, closedRefresh);
  assert.ok(input.closest("[inert][aria-hidden=true]"), "refresh cannot re-enable closing disclosure content");
  assert.equal(getComputedStyle(input).pointerEvents, "none", "collapsed tail cannot intercept input");
  if (!reduced) assert.ok(Number.parseFloat(disclosureStyle(details).blockSize) > 0, "disclosure exit missing");
  summary.click(); await delay(25); summary.click(); await delay(25); summary.click();
  await waitFor(() => disclosureEntered(details), "disclosure reversal must finish without clipping");
  assert.ok(details.open);
  assert.equal(input.value, "unsaved");
  assert.equal(input.closest("[data-motion-retired-content]"), null, "reopening releases accessibility retirement");
  input.focus(); assert.equal(document.activeElement, input, "reopened content accepts focus");
  summary.focus();
  assert.equal(getComputedStyle(input).pointerEvents, "auto");
  assert.equal(disclosureStyle(details).opacity, "1", "reverse opening finishes without stale animation");
  assert.equal(disclosureStyle(details).overflow, "visible", "completed disclosure must not clip focus or nested menus");
  const rect = input.getBoundingClientRect();
  assert.ok(rect.height > 0 && rect.top >= details.getBoundingClientRect().top);
  summary.click();
  await waitFor(() => disclosureStyle(details).blockSize === "0px" &&
    ["", "hidden"].includes(disclosureStyle(details).contentVisibility), "disclosure exit must release its layout space");
  const final = disclosureStyle(details);
  assert.ok(["", "hidden"].includes(final.contentVisibility));
  assert.ok(["", "0px"].includes(final.blockSize));
  assert.ok(details.getBoundingClientRect().height <= summary.getBoundingClientRect().height + 4,
    "collapsed disclosure leaves no layout spacer");
  host.remove();

  const feedback = document.createElement("p"); feedback.setAttribute("role", "alert"); root.append(feedback);
  updateFeedback(feedback, "Request failed");
  assert.equal(feedback.textContent, "Request failed", "error text is available immediately");
  assert.equal(feedback.hidden, false);
  if (!reduced) assert.ok(animations(feedback).some(animation => ["qch-feedback", "qch-reveal"].includes(animation.id)));
  await waitFor(() => animations(feedback).length === 0, "inline feedback must settle");
  updateFeedback(feedback, "Request failed");
  assert.equal(animations(feedback).length, 0, "identical inline feedback never replays");
  updateFeedback(feedback, "");
  assert.ok(feedback.hidden);
  updateFeedback(feedback, "Request failed");
  await delay(15);
  if (!reduced) assert.ok(animations(feedback).some(animation => ["qch-feedback", "qch-reveal"].includes(animation.id)),
    `retry feedback is restored: connected=${feedback.isConnected}, hidden=${feedback.hidden}, opacity=${getComputedStyle(feedback).opacity}, preference=${matchMedia("(prefers-reduced-motion: reduce)").matches}`);
  feedback.remove(); await delay(10);
  assert.equal(animations(feedback).length, 0);

  // Lost finish events, removal and cancellation must still retire visuals.
  const tail = document.createElement("aside");
  tail.textContent = "Dismissed feedback"; root.append(tail);
  retireSurface(tail);
  const tailAnimations = tail.getAnimations();
  if (!reduced) assert.equal(tailAnimations.length, 1, "retired surface owns one exit animation");
  // Explicitly reach the endpoint with finish delivery disabled. Render-frame
  // scheduling must not decide whether we inspect the fill or the fallback.
  tailAnimations.forEach(animation => { animation.onfinish = null; animation.finish(); });
  assert.ok(tail.inert && tail.getAttribute("aria-hidden") === "true");
  if (!reduced) assert.ok(tail.isConnected, "missing finish delivery leaves cleanup to the bounded fallback");
  await waitFor(() => {
    if (tail.isConnected) assert.equal(getComputedStyle(tail).opacity, "0",
      "lost finish event must not flash the retired surface before fallback");
    return !tail.isConnected;
  }, "lost finish event uses bounded cleanup");
  assert.equal(tail.isConnected, false, "lost finish event uses bounded cleanup");
  const removed = document.createElement("aside"); root.append(removed);
  retireSurface(removed); removed.remove(); await delay(10);
  assert.equal(animations(removed).length, 0, "external removal cancels the tail");
  const canceled = document.createElement("aside"); root.append(canceled);
  retireSurface(canceled); cancelMotion(canceled);
  assert.equal(canceled.isConnected, false, "cancellation removes retired visual immediately");
  root.remove();
}

// Exercise shared presence through real paint, native validity and keyboard
// focus. All four popup modes run this against the generated production CSS.
export async function checkInteractionFeedback(reduced) {
  const host = document.createElement("section");
  host.style.cssText = "position:fixed;left:12px;top:100px;width:min(340px,calc(100vw - 24px));background:var(--surface);z-index:80";
  host.innerHTML = '<button title="Motion keyboard tip">Hint</button><form><label>Required value<input required></label><button>Submit</button></form><aside hidden>Conditional fields</aside><p role="alert" hidden></p><dialog><h2>Retiring editor</h2><p>Complete content</p><footer><button>Close</button></footer></dialog>';
  document.body.append(host);
  const hint = host.querySelector("button"), input = host.querySelector("input"), form = host.querySelector("form");
  hint.focus();
  const tip = await waitFor(() => document.querySelector('.interaction-tooltip[data-open]'), "keyboard hint must use the animated authored tooltip");
  assert.equal(tip.textContent, "Motion keyboard tip");
  assert.ok(!hint.hasAttribute("title"), "native title bubble cannot duplicate the authored hint");
  await waitFor(() => getComputedStyle(tip).opacity === "1", "tooltip entrance must finish");
  const refreshedHint = hint.cloneNode(true); refreshedHint.title = "Updated motion keyboard tip";
  reconcileView(hint, refreshedHint);
  await waitFor(() => tip.textContent === "Updated motion keyboard tip", "polling must update a visible hint without a second native bubble");
  assert.ok(!hint.hasAttribute("title"));
  input.focus();
  assert.ok(!tip.hasAttribute("data-open"), "blur commits tooltip dismissal");
  if (!reduced) assert.ok(tip.isConnected, "tooltip keeps its exit paint");
  hint.focus();
  await waitFor(() => document.querySelector('.interaction-tooltip[data-open]'), "rapid refocus restores the tooltip");
  if (!reduced) assert.equal(document.querySelector('.interaction-tooltip'), tip, "rapid refocus retains the same painted hint");
  input.focus();
  await waitFor(() => !tip.isConnected, "tooltip exit releases its top-layer element");
  assert.equal(hint.title, "Updated motion keyboard tip");
  form.querySelector("button").click();
  const error = host.querySelector(".interaction-validation");
  assert.ok(error && !error.hidden && error.textContent, "native constraint failure must provide a visible inline message");
  assert.equal(document.activeElement, input, "validation focuses the first invalid control");
  assert.equal(input.getAttribute("aria-errormessage"), error.id);
  await waitFor(() => error.getAnimations().length === 0, "validation entry must settle");
  const refreshedForm = form.cloneNode(true);
  refreshedForm.querySelector(".interaction-validation").remove();
  refreshedForm.querySelector("input").removeAttribute("data-motion-invalid");
  refreshedForm.querySelector("input").removeAttribute("aria-invalid");
  refreshedForm.querySelector("input").removeAttribute("aria-errormessage");
  reconcileView(form, refreshedForm);
  assert.equal(form.querySelector(".interaction-validation"), error, "polling must retain the active validation message");
  assert.equal(input.getAttribute("aria-errormessage"), error.id);
  input.value = "unsaved value"; input.setSelectionRange(2, 6);
  input.dispatchEvent(new Event("input", { bubbles: true }));
  assert.ok(!error.isConnected, "valid input clears the live message immediately");
  assert.equal(input.getAttribute("aria-invalid"), null);
  assert.equal(input.value, "unsaved value");
  assert.equal(input.selectionStart, 2); assert.equal(input.selectionEnd, 6);
  const field = host.querySelector("aside");
  setVisible(field, true);
  if (!reduced) await waitFor(() => field.getAnimations().some(a => a.currentTime > 70), "conditional entry must play");
  const opacity = Number(getComputedStyle(field).opacity);
  setVisible(field, false); await delay(reduced ? 0 : 25);
  setVisible(field, true);
  if (!reduced) assert.ok(Number(getComputedStyle(field).opacity) <= opacity + .05, "fast reverse cannot flash to full opacity");
  await waitFor(() => field.getAnimations().length === 0, "conditional reverse must settle");
  assert.equal(getComputedStyle(field).opacity, "1");
  const feedback = host.querySelector('[role="alert"]');
  updateFeedback(feedback, "Copied successfully");
  await waitFor(() => feedback.getAnimations().length === 0, "copy result must finish entry");
  updateFeedback(feedback, "");
  assert.ok(feedback.hidden && feedback.textContent === "", "prompt clear commits synchronously");
  if (!reduced) assert.ok(document.querySelector(".motion-exit-host"), "cleared prompt preserves its complete exit paint");
  const dialog = host.querySelector("dialog"); dialog.showModal();
  await delay(reduced ? 0 : 80);
  closeRetiringDialog(dialog); dialog.remove();
  assert.equal(document.querySelector("dialog:modal"), null, "destroyed modal releases native top-layer state immediately");
  if (!reduced) assert.ok(document.querySelector(".motion-exit-host"), "destroyed modal retains its complete panel and scrim exit");
  await waitFor(() => !document.querySelector(".motion-exit-host"), "retired fields/messages/dialogs must completely settle");
  if (reduced) assert.equal(host.getAnimations({ subtree: true }).length, 0, "all feedback stays static in reduced motion");
  host.remove();
}

export async function checkRouteCoverage(reduced) {
  const dock = document.querySelector(".app-dock");
  let count = 0;
  const playback = [];
  for (const theme of ["light", "dark"]) {
    document.documentElement.dataset.theme = theme;
    for (const route of Object.keys(routeModuleNames)) {
      const previousMain = document.querySelector(".workspace-main");
      location.hash = `#${route}`;
      await delay(0);
      const expected = ["agents", "agent-config"].includes(route) ? "live-config" : route;
      await waitFor(() => document.querySelector(`.page-${expected} .workspace-main:not([inert]):not([aria-busy=true])`),
        `route did not mount: ${route}`);
      const main = document.querySelector(".workspace-main");
      const samples = [];
      if (!reduced && main !== previousMain) {
        assert.ok(main.getAnimations().some(animation => animation.id === "qch-route"), `route missed its visible entrance: ${route}`);
        await new Promise(resolve => {
          const sample = () => {
            const moving = main.getAnimations({ subtree: true }).filter(animation => animation.id.startsWith("qch-route"));
            samples.push({ opacity: Number(getComputedStyle(main).opacity),
              displacement: Math.max(0, ...moving.map(animation => Math.abs(Number.parseFloat(
                getComputedStyle(animation.effect.target).translate.split(" ").at(-1)) || 0))) });
            if (moving.length) requestAnimationFrame(sample); else resolve();
          };
          requestAnimationFrame(sample);
        });
        assert.ok(samples.length >= 4, `route did not play a complete visible sequence: ${route}`);
        assert.ok(samples[0].opacity < .85, `route entrance finished before content was visible: ${route}`);
        assert.ok(samples.some(sample => sample.displacement >= 10), `route has no perceptible displacement: ${route}`);
        assert.equal(samples.at(-1).opacity, 1, `route final opacity is not restored: ${route}`);
      }
      playback.push({ route, theme, reduced, frames: samples.length });
      assert.equal(document.querySelector(".app-dock"), dock, `dock DOM changed on ${route}`);
      assert.equal(document.querySelector("dialog:modal,.modal-backdrop,.motion-exit-host"), null,
        `route retains a popup: ${route}`);
      assert.ok(document.querySelector(".workspace-main").textContent.trim(), `blank page: ${route}`);
      assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, `viewport overflow: ${route}`);
      document.querySelectorAll("details[open]:not([data-motion-disclosure])").forEach(details => {
        assert.equal(getComputedStyle(details, "::details-content").opacity, "1", `initial content is faded: ${route}`);
      });
      if (reduced) assert.equal(document.getAnimations().length, 0, `reduced motion still animates: ${route}`);
      count++;
    }
  }
  document.documentElement.dataset.motionCoveragePages = count;
  window.__motionCoveragePlayback = playback;
}

export async function checkCoreDisclosure(reduced) {
  location.hash = "#node-alpha";
  const launcher = await waitFor(() => document.querySelector("[data-open-version-form]"), "version launcher missing");
  await delay(330);
  const details = launcher.closest(".service-card").querySelector(".version-drawer");
  launcher.focus(); launcher.click(); await delay(35);
  assert.ok(details.open && details.hasAttribute("data-motion-disclosure"), "button-operated version drawer missed integration");
  await waitFor(() => getComputedStyle(details).overflow === "visible" && disclosureStyle(details).opacity === "1",
    "version drawer entrance must finish before measuring its expanded height");
  const expandedHeight = details.getBoundingClientRect().height;
  const summary = details.querySelector("summary"); summary.focus(); summary.click();
  assert.equal(document.activeElement, launcher, "closing a drawer with a hidden summary returns to its launcher");
  assert.equal(launcher.getAttribute("aria-expanded"), "false");
  if (!reduced) await waitFor(() => {
    const height = details.getBoundingClientRect().height;
    return height > 0 && height < expandedHeight;
  }, "whole version drawer including its summary must shrink");
  else await delay(25);
  if (!reduced) assert.notEqual(getComputedStyle(details).display, "none", "version drawer exit was cut off by display:none");
  assert.equal(getComputedStyle(details).pointerEvents, "none");
  launcher.click(); await delay(35); summary.click(); await delay(35); launcher.click();
  await waitFor(() => getComputedStyle(details).overflow === "visible" && disclosureStyle(details).opacity === "1",
    "version drawer reversal must finish without clipping");
  assert.ok(details.open && details.getBoundingClientRect().height > summary.getBoundingClientRect().height);
  summary.click();
  await waitFor(() => getComputedStyle(details).display === "none", "version drawer exit must release its layout space");
  assert.equal(getComputedStyle(details).display, "none");
  assert.equal(document.activeElement, launcher);

  const workspace = launcher.closest(".node-operations-workspace");
  const agentTab = workspace.querySelector('[data-node-tab="agent"]');
  const coresTab = workspace.querySelector('[data-node-tab="cores"]');
  const panel = workspace.querySelector('[data-node-panel="agent"]');
  agentTab.focus(); agentTab.click();
  await waitFor(() => getComputedStyle(panel).opacity === "1", "tab entrance must reach full opacity");
  const input = panel.querySelector("input");
  if (input) { input.value = "unsaved tab motion draft"; input.focus(); }
  coresTab.click();
  assert.ok(panel.hidden && panel.inert && panel.getAttribute("aria-hidden") === "true", "outgoing tab retires interaction immediately");
  if (!reduced) assert.notEqual(getComputedStyle(panel).display, "none", "tab exit must remain painted");
  else assert.equal(getComputedStyle(panel).display, "none");
  const refreshed = workspace.cloneNode(true);
  reconcileView(workspace, refreshed);
  assert.ok(panel.inert && panel.hidden, "refresh must preserve outgoing tab ownership");
  agentTab.click(); await delay(25); coresTab.click(); await delay(25); agentTab.click();
  await waitFor(() => getComputedStyle(panel).opacity === "1" && !panel.hidden, "rapid tab reversal must finish");
  assert.equal(panel.inert, false);
  assert.equal(panel.hasAttribute("aria-hidden"), false);
  if (input) assert.equal(input.value, "unsaved tab motion draft", "tab switching must retain unsubmitted inputs");
  coresTab.click();
  await waitFor(() => getComputedStyle(panel).display === "none", "tab exit must remove its paint tail");
}
