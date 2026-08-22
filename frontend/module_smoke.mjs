import assert from "node:assert/strict";

import {
  animateNodeCardDrop,
  clearNodeCardDragState,
  installAgents,
  nodeCardDropIndex,
} from "./modules/agents.js";
import { installClientAccess } from "./modules/client-access.js";
import {
  bindServerPlanRegeneration,
  installConfigPages,
} from "./modules/configs.js";
import { installCoreLogs } from "./modules/core-logs.js";
import { installDashboard } from "./modules/dashboard.js";
import { installSettings } from "./modules/settings.js";
import { installTasks } from "./modules/tasks.js";
import { installTraffic } from "./modules/traffic.js";
import { createLatestRenderScheduler } from "./modules/render-scheduler.js";

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

let requestedRoute = "dashboard";
let releaseFirstRender;
const firstRenderGate = new Promise((resolve) => {
  releaseFirstRender = resolve;
});
const renderedRoutes = [];
let activeRenders = 0;
let maximumActiveRenders = 0;
const scheduleRender = createLatestRenderScheduler(async () => {
  const route = requestedRoute;
  renderedRoutes.push(route);
  activeRenders += 1;
  maximumActiveRenders = Math.max(maximumActiveRenders, activeRenders);
  if (renderedRoutes.length === 1) await firstRenderGate;
  activeRenders -= 1;
});
const firstRender = scheduleRender();
requestedRoute = "node-settings";
scheduleRender();
requestedRoute = "tasks";
scheduleRender();
releaseFirstRender();
await firstRender;
assert.deepEqual(
  renderedRoutes,
  ["dashboard", "tasks"],
  "in-flight navigation coalesces to one render of the latest route",
);
assert.equal(maximumActiveRenders, 1, "route renders never overlap");

const previousFormData = globalThis.FormData;
const planControls = Object.fromEntries(
  Object.entries({
    operation: "modify",
    tag: "unsaved-tag",
    listen: "127.0.0.1",
    port: "24443",
    username: "unsaved-user",
    credential: "unsaved-credential",
    secondary_credential: "unsaved-secondary",
    method: "2022-blake3-aes-128-gcm",
    transport: "grpc",
    transport_path: "unsaved-service",
    tls_enabled: "1",
    certificate_path: "/unsaved/certificate.pem",
    private_key_path: "/unsaved/private-key.pem",
    reality_enabled: "0",
    reality_private_key: "",
    reality_public_key: "",
    reality_short_id: "",
    reality_server_name: "unsaved.example.test",
  }).map(([name, value]) => [name, { name, value }]),
);
const planFormListeners = new Map();
const planForm = {
  isConnected: true,
  elements: {
    namedItem(name) {
      return planControls[name] || null;
    },
  },
  addEventListener(type, listener) {
    const listeners = planFormListeners.get(type) || [];
    listeners.push(listener);
    planFormListeners.set(type, listeners);
  },
  async dispatch(type) {
    await Promise.all(
      (planFormListeners.get(type) || []).map((listener) =>
        listener({ currentTarget: this }),
      ),
    );
  },
};
const fakePlanButton = (textContent, dataset) => {
  const listeners = [];
  const attributes = new Map();
  return {
    attributes,
    dataset,
    disabled: false,
    textContent,
    addEventListener(type, listener) {
      if (type === "click") listeners.push(listener);
    },
    setAttribute(name, value) {
      attributes.set(name, value);
    },
    removeAttribute(name) {
      attributes.delete(name);
    },
    dispatchClick() {
      return Promise.all(
        listeners.map((listener) =>
          listener({ currentTarget: this, preventDefault: noop }),
        ),
      );
    },
  };
};
const planButton = fakePlanButton("重新生成参数", { regenerate: "all" });
const credentialPlanButton = fakePlanButton("生成", {
  regenerate: "credential",
  regenerateSuccess: "凭据已生成",
});
const realityKeyPlanButton = fakePlanButton("生成密钥对", {
  regenerate: "reality_private_key,reality_public_key",
  regenerateSuccess: "Reality 密钥对已生成",
});
const deferredPlan = () => {
  let resolve;
  let reject;
  const promise = new Promise((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
};
const planRequests = [];
const planNotifications = [];
const appliedPlans = [];
const pageState = {
  route: "#agent-config",
  node: "node-current",
  engine: "ss-rust",
  tab: "transport",
  expanded: true,
  scrollY: 720,
};
globalThis.FormData = class {
  constructor(form) {
    this.form = form;
  }

  get(name) {
    return this.form.elements.namedItem(name)?.value ?? null;
  }
};

try {
  bindServerPlanRegeneration({
    form: planForm,
    buttons: [planButton, credentialPlanButton, realityKeyPlanButton],
    api: async (path, options) => {
      const pending = deferredPlan();
      planRequests.push({ path, options, pending });
      return pending.promise;
    },
    base: "/agents/node-current/configs/ss-rust",
    protocol: {
      key: "ss2022",
      requires_tls: false,
      uses_reality: false,
    },
    report: (...message) => planNotifications.push(message),
    onApplied: (plan) => appliedPlans.push(plan),
  });

  const firstClick = planButton.dispatchClick();
  assert.equal(planButton.disabled, true, "regeneration disables its button");
  assert.equal(credentialPlanButton.disabled, true);
  assert.equal(planButton.textContent, "生成中…");
  assert.equal(planButton.attributes.get("aria-busy"), "true");
  assert.equal(planRequests.length, 1);
  assert.equal(
    planRequests[0].path,
    "/agents/node-current/configs/ss-rust/plans",
    "regeneration stays scoped to the selected node and engine",
  );
  const firstPayload = JSON.parse(planRequests[0].options.body);
  assert.equal(
    firstPayload.input.method,
    "2022-blake3-aes-128-gcm",
    "regeneration uses the current unsaved method",
  );
  assert.equal(firstPayload.input.transport, "grpc");
  assert.equal(firstPayload.input.listen, "127.0.0.1");
  assert.equal(firstPayload.input.certificate_path, "/unsaved/certificate.pem");
  planRequests[0].pending.resolve({
    tag: "regenerated-tag",
    port: 35555,
    username: "regenerated-user",
    credential: "regenerated-credential",
    secondary_credential: "regenerated-secondary",
    transport_path: "regenerated-service",
    method: "server-default-method",
    transport: "websocket",
    listen: "0.0.0.0",
    certificate_path: "/server/default.pem",
  });
  await firstClick;
  assert.equal(planControls.tag.value, "regenerated-tag");
  assert.equal(planControls.port.value, "35555");
  assert.equal(planControls.credential.value, "regenerated-credential");
  assert.equal(
    planControls.method.value,
    "2022-blake3-aes-128-gcm",
    "local updates preserve current selections",
  );
  assert.equal(planControls.transport.value, "grpc");
  assert.equal(planControls.listen.value, "127.0.0.1");
  assert.equal(planControls.operation.value, "modify");
  assert.deepEqual(pageState, {
    route: "#agent-config",
    node: "node-current",
    engine: "ss-rust",
    tab: "transport",
    expanded: true,
    scrollY: 720,
  });
  assert.equal(appliedPlans.length, 1);
  assert.equal(planButton.disabled, false);
  assert.equal(planButton.textContent, "重新生成参数");
  assert.equal(planButton.attributes.has("aria-busy"), false);

  const fullTag = planControls.tag.value;
  const fullPort = planControls.port.value;
  const credentialClick = credentialPlanButton.dispatchClick();
  assert.equal(planButton.disabled, true);
  assert.equal(credentialPlanButton.textContent, "生成中…");
  planRequests[1].pending.resolve({
    tag: "must-not-apply",
    port: 38888,
    credential: "credential-only-result",
  });
  await credentialClick;
  assert.equal(
    planControls.credential.value,
    "credential-only-result",
    "a field action updates its requested result",
  );
  assert.equal(planControls.tag.value, fullTag);
  assert.equal(planControls.port.value, fullPort);
  assert.deepEqual(planNotifications.at(-1), ["凭据已生成"]);
  assert.equal(credentialPlanButton.textContent, "生成");

  const previousShortID = planControls.reality_short_id.value;
  const realityKeyClick = realityKeyPlanButton.dispatchClick();
  planRequests[2].pending.resolve({
    reality_private_key: "regenerated-private-key",
    reality_public_key: "regenerated-public-key",
    reality_short_id: "must-not-apply",
  });
  await realityKeyClick;
  assert.equal(
    planControls.reality_private_key.value,
    "regenerated-private-key",
  );
  assert.equal(planControls.reality_public_key.value, "regenerated-public-key");
  assert.equal(
    planControls.reality_short_id.value,
    previousShortID,
    "Reality key generation updates the pair without changing Short ID",
  );

  const failedCredential = planControls.credential.value;
  const failedClick = planButton.dispatchClick();
  planRequests[3].pending.reject(new Error("generation unavailable"));
  await failedClick;
  assert.equal(
    planControls.credential.value,
    failedCredential,
    "a failed request preserves the current form",
  );
  assert.deepEqual(planNotifications.at(-1), [
    "生成参数失败：generation unavailable",
    "error",
  ]);
  assert.equal(planButton.disabled, false);

  const olderClick = planButton.dispatchClick();
  planControls.method.value = "2022-blake3-aes-256-gcm";
  await planForm.dispatch("change");
  const newerClick = planButton.dispatchClick();
  assert.equal(planRequests.length, 6, "rapid clicks can be ordered safely");
  assert.equal(
    JSON.parse(planRequests[5].options.body).input.method,
    "2022-blake3-aes-256-gcm",
    "the newer request captures the newer unsaved selection",
  );
  planRequests[5].pending.resolve({
    tag: "newer-tag",
    port: 36666,
    credential: "newer-credential",
  });
  await newerClick;
  assert.equal(planControls.credential.value, "newer-credential");
  assert.equal(
    planButton.disabled,
    true,
    "the button stays disabled until every pending request settles",
  );
  planRequests[4].pending.resolve({
    tag: "older-tag",
    port: 37777,
    credential: "older-credential",
  });
  await olderClick;
  assert.equal(
    planControls.credential.value,
    "newer-credential",
    "an out-of-order response cannot overwrite the newer result",
  );
  assert.equal(planControls.method.value, "2022-blake3-aes-256-gcm");
  assert.equal(planButton.disabled, false);
} finally {
  if (previousFormData === undefined) delete globalThis.FormData;
  else globalThis.FormData = previousFormData;
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
      can: (capability) =>
        capability === "agent-config.read" || capability === "overview.read",
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
  await renderPresetAgents({ overview: { agents: 2, agents_online: 2 } });

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
  await renderPresetAgents({ overview: { agents: 2, agents_online: 2 } });
  assertFocusedPreset("beta", "alpha");
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
  if (previousDetailsElement === undefined) delete globalThis.HTMLDetailsElement;
  else globalThis.HTMLDetailsElement = previousDetailsElement;
  if (previousCSS === undefined) delete globalThis.CSS;
  else globalThis.CSS = previousCSS;
}

const routeDataDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const dashboardCalls = [];
  const dashboardState = { route: "dashboard", data: {} };
  const renderDashboard = installDashboard(
    new Proxy(
      {
        state: dashboardState,
        api: async (path) => {
          dashboardCalls.push(path);
          if (path === "/agents" || path.startsWith("/tasks?")) return [];
          assert.fail(`unexpected dashboard preload API path ${path}`);
        },
        shell: noop,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await renderDashboard({
    overview: { agents: 3, agents_online: 3, tasks_pending: 0 },
  });
  assert.deepEqual(
    dashboardCalls,
    ["/agents", "/tasks?limit=7"],
    "dashboard reuses the route bootstrap overview",
  );

  const taskCalls = [];
  const taskTimers = new Map();
  let nextTaskTimer = 1;
  let taskNow = 1_000;
  const taskState = { route: "tasks", data: {} };
  const renderTasks = installTasks(
    new Proxy(
      {
        state: taskState,
        actions: [],
        api: async (path) => {
          taskCalls.push(path);
          if (path === "/agents" || path.startsWith("/tasks?")) return [];
          if (path === "/settings") return { task_poll_interval_ms: 600 };
          assert.fail(`unexpected task polling API path ${path}`);
        },
        shell: noop,
        setTimer: (callback) => {
          const id = nextTaskTimer++;
          taskTimers.set(id, callback);
          return id;
        },
        clearTimer: (id) => taskTimers.delete(id),
        now: () => taskNow,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await renderTasks({ settings: { task_poll_interval_ms: 600 } });
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    0,
    "initial task render reuses bootstrap settings",
  );
  assert.equal(taskTimers.size, 1, "task page schedules one polling timer");
  const poll = [...taskTimers.values()][0];
  taskTimers.clear();
  await poll();
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    0,
    "background task polling does not refetch unchanged settings",
  );
  assert.equal(
    taskCalls.filter((path) => path.startsWith("/tasks?")).length,
    2,
    "each task refresh issues one task request",
  );
  assert.equal(
    taskCalls.filter((path) => path === "/agents").length,
    2,
    "each task refresh issues one agent request",
  );
  assert.equal(taskTimers.size, 1, "task polling keeps a single timer");
  const expiredPoll = [...taskTimers.values()][0];
  taskTimers.clear();
  taskNow += 30_001;
  await expiredPoll();
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    1,
    "task polling refreshes cached settings after the bounded interval",
  );
  assert.equal(taskTimers.size, 1, "expired settings refresh keeps one timer");
  taskTimers.clear();
} finally {
  if (routeDataDocument === undefined) delete globalThis.document;
  else globalThis.document = routeDataDocument;
}

const accessEntries = [
  {
    agent_id: "alpha",
    agent_name: "Alpha node",
    engine: "mihomo",
    address: "alpha.example.test",
    source: "test",
    profiles: [
      {
        tag: "alpha-in",
        protocol: "test",
        profile: { format: "URI", uri: "test-alpha", fields: [] },
      },
    ],
  },
  {
    agent_id: "beta",
    agent_name: "Beta node",
    engine: "xray",
    address: "beta.example.test",
    source: "test",
    profiles: [
      {
        tag: "beta-in",
        protocol: "test",
        profile: { format: "URI", uri: "test-beta", fields: [] },
      },
    ],
  },
];
const accessAgents = [
  { id: "alpha", name: "Alpha node", labels: {}, status: "online" },
  { id: "beta", name: "Beta node", labels: {}, status: "online" },
];
const accessState = {
  route: "client-access",
  data: { accessAgent: "beta", accessEngine: "", accessQuery: "" },
};
const accessSidebarLinks = [
  { dataset: { accessAgent: "" }, onclick: null },
  { dataset: { accessAgent: "alpha" }, onclick: null },
  { dataset: { accessAgent: "beta" }, onclick: null },
];
let accessMarkup = "";
globalThis.document = {
  querySelector: () => null,
  querySelectorAll(selector) {
    if (selector === "[data-access-agent]") return accessSidebarLinks;
    return [];
  },
};

try {
  const accessCtx = new Proxy(
    {
      state: accessState,
      engines: ["mihomo", "xray"],
      api: async (path) => {
        if (path === "/client-access") return accessEntries;
        if (path === "/agents") return accessAgents;
        assert.fail(`unexpected client access smoke API path ${path}`);
      },
      can: (capability) => capability === "agents.read",
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      short: (value) => value,
      shell: (markup) => {
        accessMarkup = markup;
      },
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const renderClientAccess = installClientAccess(accessCtx);
  await renderClientAccess();

  assert.equal(accessMarkup.includes("Alpha node"), false);
  assert.equal(accessMarkup.includes("Beta node"), true);
  assert.equal(accessMarkup.includes("data-filter-agent"), false);
  assert.equal(accessMarkup.includes("按节点筛选"), false);
  assert.equal(accessMarkup.includes('data-filter-engine=""'), true);
  assert.equal(accessMarkup.includes("按内核筛选"), true);
  assert.equal(accessMarkup.includes("client-access-results-head"), true);
  assert.equal(
    accessSidebarLinks.every((link) => typeof link.onclick === "function"),
    true,
    "context sidebar remains the executable node filter",
  );

  await accessSidebarLinks[0].onclick({ preventDefault: noop });
  assert.equal(accessState.data.accessAgent, "");
  assert.equal(accessMarkup.includes("Alpha node"), true);
  assert.equal(accessMarkup.includes("Beta node"), true);

  accessState.data.accessEngine = "xray";
  await renderClientAccess();
  assert.equal(accessMarkup.includes("Alpha node"), false);
  assert.equal(accessMarkup.includes("Beta node"), true);
} finally {
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
