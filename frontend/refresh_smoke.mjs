import assert from "node:assert/strict";

import {
  bindEvent,
  createInteractionGate,
  createPoller,
  createRefreshChannel,
  reconcileView,
} from "./modules/refresh.js";
import { animateNodeCardDrop, nodeCardDropIndex } from "./modules/agents.js";

class FakeText {
  constructor(data, ownerDocument) {
    this.nodeType = 3;
    this.data = data;
    this.ownerDocument = ownerDocument;
    this.parentNode = null;
  }
  cloneNode() {
    return new FakeText(this.data, this.ownerDocument);
  }
  remove() {
    this.parentNode?.removeChild(this);
  }
  replaceWith(replacement) {
    this.parentNode?.insertBefore(replacement, this);
    this.remove();
  }
}

class FakeElement {
  constructor(tagName, ownerDocument, attributes = {}) {
    this.nodeType = 1;
    this.tagName = tagName.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.parentNode = null;
    this.childNodes = [];
    this.attributeMap = new Map();
    this.scrollTop = 0;
    this.scrollLeft = 0;
    this.open = false;
    this.value = "";
    this.defaultValue = "";
    this.checked = false;
    this.defaultChecked = false;
    this.selected = false;
    this.defaultSelected = false;
    this.selectionStart = null;
    this.selectionEnd = null;
    this.selectionDirection = "none";
    this.readOnly = false;
    this.disabled = false;
    Object.entries(attributes).forEach(([name, value]) =>
      this.setAttribute(name, value),
    );
  }
  get attributes() {
    return [...this.attributeMap].map(([name, value]) => ({ name, value }));
  }
  get id() {
    return this.getAttribute("id") || "";
  }
  get name() {
    return this.getAttribute("name") || "";
  }
  get type() {
    return this.getAttribute("type") || "";
  }
  get children() {
    return this.childNodes.filter((child) => child.nodeType === 1);
  }
  get options() {
    return this.tagName === "SELECT" ? this.children : [];
  }
  get isConnected() {
    let current = this;
    while (current.parentNode) current = current.parentNode;
    return Boolean(current.connected);
  }
  setAttribute(name, value) {
    this.attributeMap.set(name, String(value));
  }
  getAttribute(name) {
    return this.attributeMap.get(name) ?? null;
  }
  removeAttribute(name) {
    this.attributeMap.delete(name);
  }
  append(...children) {
    children.forEach((child) => this.insertBefore(child, null));
  }
  insertBefore(child, reference) {
    child.parentNode?.removeChild(child);
    const index = reference == null ? this.childNodes.length : this.childNodes.indexOf(reference);
    this.childNodes.splice(index < 0 ? this.childNodes.length : index, 0, child);
    child.parentNode = this;
    return child;
  }
  removeChild(child) {
    const index = this.childNodes.indexOf(child);
    if (index >= 0) this.childNodes.splice(index, 1);
    child.parentNode = null;
  }
  remove() {
    this.parentNode?.removeChild(this);
  }
  replaceWith(replacement) {
    if (!this.parentNode) return;
    this.parentNode.insertBefore(replacement, this);
    this.remove();
  }
  cloneNode(deep = false) {
    const clone = new FakeElement(this.tagName, this.ownerDocument);
    this.attributes.forEach(({ name, value }) => clone.setAttribute(name, value));
    for (const property of [
      "open", "value", "defaultValue", "checked", "defaultChecked",
      "selected", "defaultSelected", "readOnly", "disabled",
    ])
      clone[property] = this[property];
    if (deep) this.childNodes.forEach((child) => clone.append(child.cloneNode(true)));
    return clone;
  }
  contains(candidate) {
    if (candidate === this) return true;
    return this.childNodes.some(
      (child) => child.nodeType === 1 && child.contains(candidate),
    );
  }
  matches(selector) {
    return selector.split(",").some((part) => {
      const value = part.trim();
      if (value === "[data-refresh-scroll]")
        return this.getAttribute("data-refresh-scroll") != null;
      if (value === ".workspace-main")
        return (this.getAttribute("class") || "").split(/\s+/).includes("workspace-main");
      return false;
    });
  }
  querySelectorAll(selector) {
    const result = [];
    const visit = (element) => {
      element.children.forEach((child) => {
        if (child.matches(selector)) result.push(child);
        visit(child);
      });
    };
    visit(this);
    return result;
  }
  closest(selector) {
    let current = this;
    while (current) {
      if (selector === "form" && current.tagName === "FORM") return current;
      current = current.parentNode;
    }
    return null;
  }
  focus() {
    this.ownerDocument.activeElement = this;
  }
  setSelectionRange(start, end, direction = "none") {
    this.selectionStart = start;
    this.selectionEnd = end;
    this.selectionDirection = direction;
  }
}

const fakeDocument = { activeElement: null };
const fakeWindow = {
  scrollX: 14,
  scrollY: 640,
  scrollTo(x, y) {
    this.scrollX = x;
    this.scrollY = y;
  },
};
const element = (tag, attributes, ...children) => {
  const node = new FakeElement(tag, fakeDocument, attributes);
  node.append(...children);
  return node;
};
const text = (value) => new FakeText(value, fakeDocument);

const currentInput = element("input", { name: "query", value: "server" });
currentInput.defaultValue = "server";
currentInput.value = "unsaved query";
currentInput.setSelectionRange(2, 8, "forward");
const currentAlpha = element("option", { value: "alpha" }, text("Alpha"));
currentAlpha.defaultSelected = true;
const currentBeta = element("option", { value: "beta" }, text("Beta"));
currentBeta.selected = true;
const currentSelect = element(
  "select",
  { name: "agent" },
  currentAlpha,
  currentBeta,
);
currentSelect.value = "beta";
const currentTab = element(
  "button",
  { "data-refresh-key": "tab-metrics", "aria-selected": "true" },
  text("Metrics"),
);
const currentStable = element("input", {
  name: "release_channel",
  type: "radio",
  value: "stable",
});
currentStable.defaultChecked = true;
const currentDevelopment = element("input", {
  name: "release_channel",
  type: "radio",
  value: "development",
});
currentDevelopment.checked = true;
const currentForm = element(
  "form",
  {},
  currentInput,
  currentSelect,
  currentTab,
  currentStable,
  currentDevelopment,
);
const currentDetails = element("details", {}, currentForm);
currentDetails.open = true;
const currentA = element(
  "article",
  { "data-refresh-key": "row-a" },
  text("old"),
  currentDetails,
);
const currentB = element("article", { "data-refresh-key": "row-b" }, text("remove"));
const currentList = element("section", { "data-refresh-scroll": "" }, currentA, currentB);
currentList.scrollTop = 155;
const currentMain = element("main", { class: "workspace-main" }, currentList);
currentMain.scrollTop = 420;
const currentDialog = element("dialog", { open: "" }, text("confirm"));
currentDialog.open = true;
const currentRoot = element("div", {}, currentMain, currentDialog);
currentRoot.connected = true;
currentInput.focus();

const freshInput = element("input", { name: "query", value: "server updated" });
freshInput.defaultValue = "server updated";
freshInput.value = "server updated";
const freshAlpha = element("option", { value: "alpha" }, text("Alpha"));
freshAlpha.selected = true;
freshAlpha.defaultSelected = true;
const freshBeta = element("option", { value: "beta" }, text("Beta"));
const freshSelect = element("select", { name: "agent" }, freshAlpha, freshBeta);
freshSelect.value = "alpha";
const freshTab = element(
  "button",
  { "data-refresh-key": "tab-metrics", "aria-selected": "true" },
  text("Metrics"),
);
const freshStable = element("input", {
  name: "release_channel",
  type: "radio",
  value: "stable",
});
freshStable.checked = true;
freshStable.defaultChecked = true;
const freshDevelopment = element("input", {
  name: "release_channel",
  type: "radio",
  value: "development",
});
const freshA = element(
  "article",
  { "data-refresh-key": "row-a" },
  text("new"),
  element(
    "details",
    {},
    element(
      "form",
      {},
      freshInput,
      freshSelect,
      freshTab,
      freshStable,
      freshDevelopment,
    ),
  ),
);
const freshC = element("article", { "data-refresh-key": "row-c" }, text("added"));
const freshRoot = element(
  "div",
  {},
  element(
    "main",
    { class: "workspace-main" },
    element("section", { "data-refresh-scroll": "" }, freshC, freshA),
  ),
  element("dialog", {}, text("confirm updated")),
);

const reconciliationMetrics = { inserted: 0, removed: 0, replaced: 0, updated: 0 };
const reconciled = reconcileView(currentRoot, freshRoot, {
  document: fakeDocument,
  window: fakeWindow,
  metrics: reconciliationMetrics,
});
assert.equal(reconciled, currentRoot, "same-route view root keeps identity");
assert.equal(currentMain, currentRoot.children[0], "workspace keeps identity");
assert.equal(currentA, currentList.children[1], "stable keyed row keeps identity");
assert.equal(currentB.isConnected, false, "deleted keyed data leaves the DOM");
assert.equal(currentList.children[0].getAttribute("data-refresh-key"), "row-c");
assert.equal(currentInput.value, "unsaved query", "unsaved form input survives");
assert.equal(currentSelect.value, "beta", "dirty select choice survives");
assert.equal(currentTab.isConnected, true, "selected tab keeps node identity");
assert.equal(currentTab.getAttribute("aria-selected"), "true", "selected tab stays active");
assert.equal(currentStable.isConnected, true, "first same-name option keeps identity");
assert.equal(currentDevelopment.isConnected, true, "second same-name option keeps identity");
assert.equal(currentDevelopment.checked, true, "dirty radio choice survives");
assert.equal(fakeDocument.activeElement, currentInput, "focused control survives");
assert.deepEqual(
  [currentInput.selectionStart, currentInput.selectionEnd, currentInput.selectionDirection],
  [2, 8, "forward"],
  "keyboard selection survives",
);
assert.equal(currentDetails.open, true, "details state survives");
assert.equal(currentDialog.open, true, "modal state survives");
assert.equal(currentDialog.childNodes[0].data, "confirm", "open modal content stays stable");
assert.equal(currentMain.scrollTop, 420, "workspace scroll survives");
assert.equal(currentList.scrollTop, 155, "inner scroll survives");
assert.deepEqual([fakeWindow.scrollX, fakeWindow.scrollY], [14, 640]);
assert.deepEqual(
  {
    inserted: reconciliationMetrics.inserted,
    removed: reconciliationMetrics.removed,
    replaced: reconciliationMetrics.replaced,
  },
  { inserted: 1, removed: 1, replaced: 0 },
  "keyed data changes insert/delete only and replace no stable nodes",
);

const switchedInput = element("input", { name: "content", value: "draft" });
switchedInput.defaultValue = "draft";
switchedInput.value = "unsaved draft";
const switchedMain = element(
  "main",
  { class: "workspace-main", "data-refresh-key": "workspace-a" },
  element("form", {}, switchedInput),
);
const switchedRoot = element("div", {}, switchedMain);
switchedRoot.connected = true;
const nextMain = element(
  "main",
  { class: "workspace-main", "data-refresh-key": "workspace-b" },
  element("form", {}, element("input", { name: "content", value: "record-b" })),
);
reconcileView(switchedRoot, element("div", {}, nextMain), {
  document: fakeDocument,
  window: fakeWindow,
});
assert.notEqual(
  switchedRoot.children[0],
  switchedMain,
  "an explicit data-object switch replaces the keyed workspace",
);
assert.equal(
  switchedRoot.children[0].children[0].children[0].getAttribute("value"),
  "record-b",
  "an explicit switch uses the selected record instead of stale draft state",
);

const eventTarget = {
  listeners: [],
  addEventListener(_type, listener) {
    this.listeners.push(listener);
  },
};
const eventCalls = [];
bindEvent(eventTarget, "click", () => eventCalls.push("old"));
bindEvent(eventTarget, "click", () => eventCalls.push("latest"));
assert.equal(eventTarget.listeners.length, 1, "rebinding keeps one listener");
eventTarget.listeners[0]({});
assert.deepEqual(eventCalls, ["latest"], "the stable listener uses the latest handler");

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
};
let routeScope = 1;
const channel = createRefreshChannel({ getScope: () => routeScope });
const older = deferred();
const newer = deferred();
const appliedValues = [];
let olderSignal;
const olderRun = channel.run((signal) => {
  olderSignal = signal;
  return older.promise;
}, (value) => appliedValues.push(value));
const newerRun = channel.run(() => newer.promise, (value) => appliedValues.push(value));
assert.equal(olderSignal.aborted, true, "newer refresh aborts the older request");
newer.resolve("newer");
assert.equal(await newerRun, true);
older.resolve("older");
assert.equal(await olderRun, false);
assert.deepEqual(appliedValues, ["newer"], "out-of-order data cannot overwrite latest");
const departed = deferred();
const departedRun = channel.run(() => departed.promise, (value) => appliedValues.push(value));
routeScope += 1;
departed.resolve("departed");
assert.equal(await departedRun, false);
assert.deepEqual(appliedValues, ["newer"], "a departed route rejects its old response");

const timers = new Map();
let nextTimer = 1;
const pendingRuns = [];
let pollRuns = 0;
let pollActive = true;
const poller = createPoller({
  run: () => {
    pollRuns += 1;
    const pending = deferred();
    pendingRuns.push(pending);
    return pending.promise;
  },
  isActive: () => pollActive,
  delay: () => 1000,
  setTimer: (callback) => {
    const id = nextTimer++;
    timers.set(id, callback);
    return id;
  },
  clearTimer: (id) => timers.delete(id),
});
poller.start();
poller.start();
assert.equal(timers.size, 1, "repeated starts keep one timer");
const scheduled = [...timers.values()][0];
timers.clear();
const firstPoll = scheduled();
await Promise.resolve();
const coalesced = poller.trigger();
poller.trigger();
assert.equal(pollRuns, 1, "concurrent refreshes share the active request");
pendingRuns[0].resolve();
await Promise.resolve();
assert.equal(pollRuns, 2, "concurrent refreshes coalesce to one latest rerun");
pendingRuns[1].resolve();
await Promise.all([firstPoll, coalesced]);
assert.equal(timers.size, 1, "a completed refresh owns one future timer");
pollActive = false;
poller.stop();
await poller.trigger();
assert.equal(timers.size, 0, "a departed route owns no timer");
assert.equal(pollRuns, 2, "a departed route starts no new request");

const interactionGate = createInteractionGate();
const releasePointer = interactionGate.begin();
let structuralRefreshes = 0;
let metricPatches = 0;
interactionGate.defer(() => {
  structuralRefreshes += 1;
}, "structure");
interactionGate.defer(() => {
  metricPatches += 1;
}, "metrics");
assert.equal(structuralRefreshes, 0, "pointerdown defers structural refresh");
assert.equal(metricPatches, 0, "pointerdown defers metric DOM patches");
assert.equal(interactionGate.pendingCount(), 2, "refresh types coalesce independently");
assert.equal(
  nodeCardDropIndex(
    [
      { left: 0, right: 100, top: 0, bottom: 100 },
      { left: 120, right: 220, top: 0, bottom: 100 },
      { left: 0, right: 100, top: 120, bottom: 220 },
    ],
    { x: 40, y: 170 },
  ),
  2,
  "pointer move keeps cross-row hit testing available while refresh waits",
);
const transitionListeners = new Map();
const animatedCard = {
  style: { transition: "", transform: "" },
  get offsetWidth() {
    return 100;
  },
  getBoundingClientRect: () => ({ left: 120, top: 0 }),
  addEventListener: (type, listener) => transitionListeners.set(type, listener),
  removeEventListener: (type, listener) => {
    if (transitionListeners.get(type) === listener) transitionListeners.delete(type);
  },
};
let frame;
const animationTimers = new Map();
let animationTimerID = 1;
animateNodeCardDrop(
  [animatedCard],
  new Map([[animatedCard, { left: 20, top: 0 }]]),
  {
    requestFrame: (callback) => {
      frame = callback;
      return 1;
    },
    cancelFrame: () => {},
    setTimer: (callback) => {
      const id = animationTimerID++;
      animationTimers.set(id, callback);
      return id;
    },
    clearTimer: (id) => animationTimers.delete(id),
    onSettled: releasePointer,
  },
);
assert.equal(structuralRefreshes, 0, "drop keeps refresh deferred during FLIP");
frame();
transitionListeners.get("transitionend")({
  target: animatedCard,
  propertyName: "transform",
});
assert.equal(structuralRefreshes, 1, "FLIP settlement applies one merged refresh");
assert.equal(metricPatches, 1, "FLIP settlement applies the latest metric patch");
assert.equal(animatedCard.style.transform, "", "FLIP cleanup completes before refresh");
assert.equal(animationTimers.size, 0, "FLIP fallback timer is cleared");

const canceledGate = createInteractionGate();
const releaseCanceledInteraction = canceledGate.begin();
let canceledRefreshes = 0;
canceledGate.defer(() => {
  canceledRefreshes += 1;
});
canceledGate.cancel();
releaseCanceledInteraction();
assert.equal(canceledRefreshes, 0, "route departure discards stale interaction work");
assert.equal(canceledGate.activeCount(), 0, "route departure releases interaction state");
