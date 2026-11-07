import assert from "node:assert/strict";

import { listen as bindEvent, patch as reconcileView } from "./analytics/live-dom.js";
import { createPoller } from "./modules/poller.js";

class FakeText {
  constructor(data, ownerDocument) {
    this.nodeType = 3;
    this.data = data;
    this.ownerDocument = ownerDocument;
    this.parentNode = null;
  }
  get nodeName() { return "#text"; }
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
  get nodeName() { return this.tagName; }
  hasAttribute(name) { return this.attributeMap.has(name); }
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
      if (value === "*") return true;
      if (value.toUpperCase() === this.tagName) return true;
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
const currentRoot = element("div", {}, currentMain);
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
);

const reconciled = reconcileView(currentRoot, freshRoot);
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
assert.equal(currentMain.scrollTop, 420, "workspace scroll survives");
assert.equal(currentList.scrollTop, 155, "inner scroll survives");
assert.deepEqual([fakeWindow.scrollX, fakeWindow.scrollY], [14, 640]);
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

// Native route cancellation, pointer refresh deferral, modal ownership and
// drag cleanup run on actual pages in browser/native-motion.mjs and
// browser/native-ordering.mjs. The poller above remains an active shared model.
