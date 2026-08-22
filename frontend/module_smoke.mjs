import assert from "node:assert/strict";

import {
  animateNodeCardDrop,
  clearNodeCardDragState,
  installAgents,
  nodeCardDropIndex,
} from "./modules/agents.js";
import { installClientAccess } from "./modules/client-access.js";
import { installConfigPages } from "./modules/configs.js";
import { installCoreLogs } from "./modules/core-logs.js";
import { installDashboard } from "./modules/dashboard.js";
import { installSettings } from "./modules/settings.js";
import { installTasks } from "./modules/tasks.js";
import { installTraffic } from "./modules/traffic.js";

const state = { data: {}, session: { role: "admin" } };
const noop = () => {};
const ctx = new Proxy(
  { state, engines: [], actions: [] },
  { get: (target, key) => target[key] ?? noop },
);

for (const install of [
  installAgents,
  installClientAccess,
  installConfigPages,
  installCoreLogs,
  installDashboard,
  installSettings,
  installTasks,
  installTraffic,
]) {
  const page = install(ctx);
  if (typeof page !== "function" && (typeof page !== "object" || !page)) {
    throw new TypeError(`${install.name} returned an invalid page module`);
  }
}

const cardSlots = [
  { left: 0, right: 100, top: 0, bottom: 160 },
  { left: 120, right: 220, top: 0, bottom: 160 },
  { left: 240, right: 340, top: 0, bottom: 160 },
  { left: 0, right: 100, top: 180, bottom: 340 },
  { left: 120, right: 220, top: 180, bottom: 340 },
];
const upperRightGripOffset = { x: 40, y: -60 };
const dropAt = (x, y) =>
  nodeCardDropIndex(cardSlots, { x, y }, upperRightGripOffset);

assert.equal(dropAt(90, 20), 0, "natural upper-half drop selects first slot");
assert.equal(dropAt(210, 20), 1, "natural upper-half drop selects middle slot");
assert.equal(dropAt(330, 20), 2, "natural upper-half drop selects last slot");
assert.equal(dropAt(90, 200), 3, "drop selection follows the actual grid row");
assert.equal(dropAt(210, 200), 4, "drop selection follows row direction");

const fakeCard = (left) => {
  const listeners = new Map();
  const classes = new Set();
  return {
    left,
    style: { transition: "", transform: "" },
    classList: {
      add: (...names) => names.forEach((name) => classes.add(name)),
      remove: (...names) => names.forEach((name) => classes.delete(name)),
      contains: (name) => classes.has(name),
    },
    forcedLayouts: 0,
    get offsetWidth() {
      this.forcedLayouts += 1;
      return 100;
    },
    getBoundingClientRect() {
      return { left: this.left, top: 0 };
    },
    addEventListener(type, listener) {
      listeners.set(type, listener);
    },
    removeEventListener(type, listener) {
      if (listeners.get(type) === listener) listeners.delete(type);
    },
    dispatchTransitionEnd() {
      listeners.get("transitionend")?.({
        target: this,
        propertyName: "transform",
      });
    },
    hasTransitionListener() {
      return listeners.has("transitionend");
    },
  };
};
const animationRuntime = () => {
  const frames = new Map();
  const timers = new Map();
  let nextID = 1;
  return {
    frames,
    timers,
    requestFrame(callback) {
      const id = nextID++;
      frames.set(id, callback);
      return id;
    },
    cancelFrame(id) {
      frames.delete(id);
    },
    setTimer(callback) {
      const id = nextID++;
      timers.set(id, callback);
      return id;
    },
    clearTimer(id) {
      timers.delete(id);
    },
    runFrame() {
      const pending = [...frames.values()];
      frames.clear();
      pending.forEach((callback) => callback());
    },
    runTimer() {
      const pending = [...timers.values()];
      timers.clear();
      pending.forEach((callback) => callback());
    },
  };
};

const landingCard = fakeCard(120);
const landingRuntime = animationRuntime();
const cancelLanding = animateNodeCardDrop(
  [landingCard],
  new Map([[landingCard, { left: 20, top: 0 }]]),
  landingRuntime,
);
assert.equal(landingCard.style.transition, "none");
assert.equal(landingCard.style.transform, "translate(-100px, 0px)");
assert.equal(landingCard.forcedLayouts, 1);
assert.equal(landingRuntime.frames.size, 1);
landingRuntime.runFrame();
assert.equal(landingCard.style.transition, "");
assert.equal(landingCard.style.transform, "");
assert.equal(landingRuntime.timers.size, 1);
assert.equal(landingCard.hasTransitionListener(), true);
landingCard.dispatchTransitionEnd();
assert.equal(landingRuntime.timers.size, 0);
assert.equal(landingCard.hasTransitionListener(), false);
assert.equal(landingCard.style.transition, "");
assert.equal(landingCard.style.transform, "");

const interruptedCard = fakeCard(120);
const interruptedRuntime = animationRuntime();
const interruptLanding = animateNodeCardDrop(
  [interruptedCard],
  new Map([[interruptedCard, { left: 20, top: 0 }]]),
  interruptedRuntime,
);
interruptLanding();
assert.equal(interruptedRuntime.frames.size, 0);
assert.equal(interruptedCard.hasTransitionListener(), false);
assert.equal(interruptedCard.style.transition, "");
assert.equal(interruptedCard.style.transform, "");
assert.equal(interruptedCard.forcedLayouts, 2);

const timedOutCard = fakeCard(120);
const timedOutRuntime = animationRuntime();
animateNodeCardDrop(
  [timedOutCard],
  new Map([[timedOutCard, { left: 20, top: 0 }]]),
  timedOutRuntime,
);
timedOutRuntime.runFrame();
timedOutRuntime.runTimer();
assert.equal(timedOutCard.hasTransitionListener(), false);
assert.equal(timedOutCard.style.transition, "");
assert.equal(timedOutCard.style.transform, "");
cancelLanding();

const releaseCard = fakeCard(0);
const releaseTarget = fakeCard(120);
releaseCard.classList.add("dragging");
releaseTarget.classList.add("drop-target");
releaseCard.style.order = "1";
releaseCard.style.transition = "none";
releaseCard.style.transform = "translate(-100px, 0px)";
let ghostRemoved = false;
const dragBody = fakeCard(0);
dragBody.classList.add("node-card-dragging");
const dragGrid = {
  querySelectorAll: () => [releaseCard, releaseTarget],
};
clearNodeCardDragState(
  dragGrid,
  { card: releaseCard, ghost: { remove: () => (ghostRemoved = true) } },
  { clearAnimationStyles: false, body: dragBody },
);
assert.equal(releaseCard.classList.contains("dragging"), false);
assert.equal(releaseTarget.classList.contains("drop-target"), false);
assert.equal(dragBody.classList.contains("node-card-dragging"), false);
assert.equal(ghostRemoved, true);
assert.equal(releaseCard.style.order, "");
assert.equal(releaseCard.style.transition, "none");
assert.equal(releaseCard.style.transform, "translate(-100px, 0px)");
clearNodeCardDragState(
  dragGrid,
  { card: releaseCard },
  { clearAnimationStyles: true, body: dragBody },
);
assert.equal(releaseCard.style.transition, "");
assert.equal(releaseCard.style.transform, "");

const previousDocument = globalThis.document;
const previousDetailsElement = globalThis.HTMLDetailsElement;
const previousCSS = globalThis.CSS;
class PresetWorkspace {
  constructor(agentID) {
    this.dataset = { agentNode: agentID };
    this.id = `preset-node-${agentID}`;
  }
  querySelector() {
    return null;
  }
  querySelectorAll() {
    return [];
  }
}

const presetEngines = ["mihomo", "xray", "sing-box", "ss-rust"];
const presetAgents = [
  {
    id: "alpha",
    name: "Alpha",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: presetEngines,
    runtime: Object.fromEntries(
      presetEngines.map((engine) => [
        engine,
        { installed: true, service_status: "active", version: "1.0.0" },
      ]),
    ),
  },
  {
    id: "beta",
    name: "Beta",
    os: "linux",
    arch: "arm64",
    status: "online",
    capabilities: presetEngines,
    runtime: Object.fromEntries(
      presetEngines.map((engine) => [
        engine,
        { installed: true, service_status: "active", version: "1.0.0" },
      ]),
    ),
  },
];
const presetState = {
  route: "agents",
  anchor: "agents",
  data: { selectedAgent: "" },
};
const presetLinks = presetAgents.map((agent) => ({
  dataset: { contextAgent: agent.id },
  href: `#node-${agent.id}`,
}));
let presetWorkspaces = [];
let presetMarkup = "";

globalThis.HTMLDetailsElement = class {};
globalThis.CSS = { escape: (value) => String(value) };
globalThis.document = {
  querySelector: () => null,
  querySelectorAll(selector) {
    if (selector === ".preset-node-workspace") return presetWorkspaces;
    if (selector === "[data-context-agent]") return presetLinks;
    if (
      selector ===
      ".preset-node-workspace, .machine-workspace, .node-operations-workspace"
    )
      return presetWorkspaces;
    return [];
  },
};

try {
  const presetShell = (markup) => {
    presetMarkup = markup;
    presetWorkspaces = [
      ...markup.matchAll(
        /<section class="preset-node-workspace[^>]*data-agent-node="([^"]+)"/g,
      ),
    ].map((match) => new PresetWorkspace(match[1]));
  };
  const presetCtx = new Proxy(
    {
      state: presetState,
      engines: presetEngines,
      api: async (path) => {
        if (path === "/agents") return presetAgents;
        if (path.endsWith("/configs")) return [];
        assert.fail(`unexpected preset smoke API path ${path}`);
      },
      optionalAPI: async () => null,
      can: (capability) => capability === "agent-config.read",
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      serviceStatusName: (value) => value,
      statusTone: (value) => value,
      conciseVersion: (_engine, value) => value,
      shell: presetShell,
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const { agents: renderPresetAgents } = installAgents(presetCtx);
  await renderPresetAgents();

  const assertFocusedPreset = (selected, excluded) => {
    assert.equal(presetState.route, "agents", "preset selection stays on agents");
    assert.deepEqual(
      presetWorkspaces.map((workspace) => workspace.id),
      [`preset-node-${selected}`],
      "only the selected preset workspace enters the main DOM",
    );
    assert.equal(
      presetMarkup.includes(`data-agent-node="${excluded}"`),
      false,
      "unselected node content stays out of the main DOM",
    );
    assert.equal(
      presetMarkup.includes('class="machine-workspace"'),
      false,
      "focused preset content has no node accordion",
    );
    assert.equal(
      presetMarkup.includes('class="machine-header"'),
      false,
      "focused preset content has no node summary header",
    );
    assert.equal(
      presetMarkup.includes('class="node-page-intro"'),
      false,
      "focused preset content has no redundant page introduction",
    );
    assert.equal(
      (presetMarkup.match(/<article class="service-card service-/g) || [])
        .length,
      presetEngines.length,
      "focused preset content keeps all engine cards",
    );
    assert.equal(
      (presetMarkup.match(new RegExp(`data-config="${selected}"`, "g")) || [])
        .length,
      presetEngines.length,
      "focused preset content keeps each engine configuration action",
    );
    assert.equal(presetMarkup.includes("节点内核"), true);
  };

  assert.equal(
    presetState.data.selectedAgent,
    "alpha",
    "first preset visit selects the first node",
  );
  assertFocusedPreset("alpha", "beta");
  assert.equal(presetState.route, "agents", "preset selection stays on agents");
  assert.deepEqual(
    presetLinks.map((link) => link.href),
    ["#preset-node-alpha", "#preset-node-beta"],
    "preset sidebar links target per-node preset anchors",
  );
  assert.equal(
    presetLinks.some((link) => link.href.startsWith("#settings-node-")),
    false,
    "preset sidebar never enters node settings",
  );

  presetState.anchor = "preset-node-beta";
  presetState.data.selectedAgent = "beta";
  await renderPresetAgents();
  assertFocusedPreset("beta", "alpha");
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
  if (previousDetailsElement === undefined) delete globalThis.HTMLDetailsElement;
  else globalThis.HTMLDetailsElement = previousDetailsElement;
  if (previousCSS === undefined) delete globalThis.CSS;
  else globalThis.CSS = previousCSS;
}
