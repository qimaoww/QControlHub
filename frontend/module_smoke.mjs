import assert from "node:assert/strict";
import "./refresh_smoke.mjs";

import {
  animateNodeCardDrop,
  clearNodeCardDragState,
  coreSourceForInstall,
  developmentSourceVisible,
  formatHostPort,
  installAgents,
  nodeCardDropIndex,
  publicAddressRows,
  updatePublicIPDisplays,
} from "./modules/agents.js";
import { coreSourceLabel, coreSourceName } from "./modules/tasks.js";
import { installClientAccess } from "./modules/client-access.js";
import { ConfigFormatError, formatConfigContent } from "./modules/code-format.js";
import {
  bindServerPlanRegeneration,
  installConfigPages,
} from "./modules/configs.js";
import { installCoreLogs } from "./modules/core-logs.js";
import { installDashboard } from "./modules/dashboard.js";
import { installSettings } from "./modules/settings.js";
import { installTasks } from "./modules/tasks.js";
import { installTraffic } from "./modules/traffic.js";
import { createLatestRenderScheduler } from "./modules/refresh.js";

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

// Manual-config deploy-cache regression tests (round 5).
// Tests drive actual production form-submit handlers with controlled
// async timing to verify:
// A. Route abort on monitor -> reconcile sees running -> recovery poller
//    detects succeeded -> cache invalidated -> fresh read fires.
// B. Field deploy failure does NOT invalidate (real handler).
// C. Validate-only preserves cache and records no pending.
// D. Different agent|engine keys do not cross-contaminate.
// E. ABA: old deploy A succeeds while B is pending -> cache IS
//    invalidated (node changed!), B's pending NOT cleared by A.
// F. Live-config editor deploy while on page -> auto-converges,
//    editorReadCount >= 1 proves fresh read fired.
// G. Source-config deploy success path uses same terminal mechanism.
{
  const previousDocument = globalThis.document;
  const previousFormData = globalThis.FormData;
  const previousSetTimeout = globalThis.setTimeout;
  globalThis.location ??= { hash: "" };

  class FakeForm {
    constructor(elements = {}) {
      this.isConnected = true;
      this.querySelector = () => null;
      this.querySelectorAll = () => [];
      this._el = new Map(
        Object.entries(elements).map(([k, v]) => [
          k,
          { name: k, value: v, type: "text", hidden: false,
            addEventListener() {}, replaceWith() {},
            parentElement: { classList: { contains: () => false }, append() {} } },
        ]),
      );
      this._l = new Map();
    }
    get elements() { return { namedItem: (n) => this._el.get(n) ?? null }; }
    addEventListener(type, listener) {
      const list = this._l.get(type) || [];
      list.push(listener);
      this._l.set(type, list);
    }
    async dispatchSubmit(ds = {}) {
      await Promise.all(
        (this._l.get("submit") || []).map((fn) =>
          fn({ currentTarget: this, preventDefault() {},
               submitter: { dataset: ds } }),
        ),
      );
    }
  }

  const deferred = () => {
    let resolve, reject;
    return { promise: new Promise((res, rej) => { resolve = res; reject = rej; }),
             resolve, reject };
  };

  globalThis.setTimeout = (cb) => { cb(); return 1; };
  globalThis.FormData = class {
    constructor(f) { this.f = f; }
    get(n) { return this.f.elements.namedItem(n)?.value ?? null; }
  };

  const AGENT_ID = "test-agent";
  const ENGINE = "xray";
  const KEY = `${AGENT_ID}|${ENGINE}`;
  const OLD_CONTENT = '{"inbounds":[{"tag":"old-inbound"}]}';
  const NEW_CONTENT = '{"inbounds":[]}';

  const makeAgent = (id = AGENT_ID) => ({
    id, name: `Agent-${id}`, os: "linux", arch: "amd64",
    status: "online", capabilities: [ENGINE],
    runtime: { [ENGINE]: { installed: true } },
  });

  const workspaceWithConfig = (overrides = {}) => ({
    config: { id: "cfg-1", version: 1, name: "cfg", description: "", ...overrides },
    inbounds: [{ tag: "old-inbound", listen: "0.0.0.0", port: 443 }],
    protocols: [{ key: "ss2022", badge: "SS", name: "SS 2022", docs: "",
      methods: [], transports: ["raw"], default_port: 443 }],
    catalog: { fields: [{ key: "log", label: "Log", kind: "object", docs: "" }],
      name: "", format: "JSON", topic_count: 0, topic_groups: [] },
    present_fields: {},
  });

  const emptyWorkspace = {
    config: null, inbounds: [],
    protocols: [{ key: "ss2022", badge: "SS", name: "SS 2022", docs: "",
      methods: [], transports: ["raw"], default_port: 443 }],
    catalog: { fields: [], name: "", format: "JSON", topic_count: 0,
      topic_groups: [] }, present_fields: {},
  };

  const buildContext = (testState, handlers) => {
    const apiLog = [];
    let markup = "";
    const noopFn = () => {};
    const match = (method, path) => {
      for (const [key, value] of Object.entries(handlers)) {
        if (key.startsWith(method + " ")) {
          try {
            if (new RegExp(`^${key.slice(method.length + 1)}$`).test(path))
              return value;
          } catch {}
        }
      }
      return undefined;
    };
    const ctx = new Proxy({
      state: testState, engines: [ENGINE],
      api: async (path, options = {}) => {
        const method = options.method || "GET";
        apiLog.push({ method, path });
        const h = match(method, path);
        if (h === undefined) throw new Error(`unmocked ${method} ${path}`);
        return typeof h === "function" ? h(options, path) : h;
      },
      optionalAPI: async () => null,
      can: () => true, esc: (v) => String(v ?? ""),
      engineName: (v) => v, conciseVersion: (_e, v) => v,
      notify: noopFn, confirmAction: async () => true,
      shell: (m) => { markup = m; },
      submitTask: async (payload) => {
        const h = match("POST", "/tasks");
        if (h === undefined) return null;
        return typeof h === "function"
          ? h({ body: JSON.stringify(payload) }, "/tasks") : h;
      },
      bindCodeEditors: noopFn,
    }, { get: (t, k) => t[k] ?? noopFn });
    return { ctx, apiLog, markup: () => markup };
  };

  const installForms = (ctx, forms = {}) => {
    globalThis.document = {
      querySelector: (sel) => forms[sel] ?? null,
      querySelectorAll: () => [],
      createElement: () => ({
        className: "", type: "", dataset: {}, textContent: "",
        setAttribute() {}, removeAttribute() {}, append() {}, addEventListener() {},
        parentElement: { classList: { contains: () => false }, append() {} },
      }),
    };
    return installConfigPages(ctx);
  };

  const drain = async () => {
    for (let i = 0; i < 300; i++) await new Promise((r) => setImmediate(r));
  };

  // --- Test A: Route abort on monitor -> reconcile sees running ->
  // recovery poller detects succeeded -> cache invalidated -> fresh read.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let freshReadCount = 0;

    const planForm = new FakeForm({
      operation: "delete", tag: "old-inbound", listen: "0.0.0.0",
      port: "443", username: "", credential: "", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": () => ({
        config: { id: "cfg-new", version: 2 },
        task: { id: "deploy-t1", status: "pending" },
      }),
      "GET /tasks/deploy-t1": (() => {
        let calls = 0;
        return () => {
          calls += 1;
          if (calls <= 1) throw new DOMException("route abort", "AbortError");
          return { status: "succeeded", id: "deploy-t1" };
        };
      })(),
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "read-config") {
          freshReadCount += 1;
          return { id: `read-${freshReadCount}` };
        }
        return { id: "other" };
      },
      "GET /tasks/read-\\d+": (_o, p) => ({ status: "succeeded", id: p.split("/").pop() }),
      "GET /tasks/read-\\d+/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#server-plan-form": planForm });
    state.data.inboundTag = "old-inbound";
    await pages.agentConfig();
    await planForm.dispatchSubmit({ planIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "deploy-t1",
      "[A] pending recorded despite monitor abort");

    state.route = "live-config";
    await pages.liveConfig();
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[A] pending cleared after recovery poller saw succeeded");
    assert.ok(freshReadCount >= 1,
      "[A] fresh read fired after convergence");
    if (state.data.liveSources?.[KEY]?.content)
      assert.notEqual(state.data.liveSources[KEY].content, OLD_CONTENT,
        "[A] old content not persisted after convergence");
  }

  // --- Test B: Field deploy failure does NOT invalidate (real handler).
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let failPollCount = 0;
    let fieldPostCalled = false;

    const fieldForm = new FakeForm({ mutation: "modify", fragment: '{"level":"info"}' });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig({ version: 3, name: "existing-cfg" }),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/log": { present: true, fragment: "{}" },
      "POST /agents/test-agent/configs/xray/fields/log": () => {
        fieldPostCalled = true;
        return { config: { version: 4 }, task: { id: "field-deploy-fail", status: "pending" } };
      },
      "GET /tasks/field-deploy-fail": () => {
        failPollCount += 1;
        return failPollCount <= 2
          ? { status: "running", id: "field-deploy-fail" }
          : { status: "failed", id: "field-deploy-fail", error: "node down" };
      },
    });

    const pages = installForms(ctx, { "#field-form": fieldForm });
    await pages.agentConfig();
    await fieldForm.dispatchSubmit({ fieldIntent: "deploy" });
    await drain();

    assert.ok(fieldPostCalled, "[B] field POST was called");
    assert.ok(failPollCount > 0, "[B] deploy terminal was polled");
    assert.equal(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[B] cache NOT invalidated after deploy failed");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[B] pending entry cleared after failed terminal");
  }

  // --- Test C: Validate-only preserves cache and records no pending.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };

    const planForm = new FakeForm({
      operation: "modify", tag: "t", listen: "127.0.0.1", port: "8443",
      username: "u", credential: "c", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": {
        config: { version: 2 }, task: { id: "v-only-task" } },
    });

    installForms(ctx, { "#server-plan-form": planForm });
    const p = installConfigPages(ctx);
    await p.agentConfig();
    await planForm.dispatchSubmit({ planIntent: "validate" });
    await drain();

    assert.equal(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[C] validate-only preserves cached content");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[C] no pending deploy recorded for validate-only");
  }

  // --- Test D: Different agent|engine keys do not cross-contaminate.
  {
    const OTHER_AGENT = "other-agent";
    const OTHER_KEY = `${OTHER_AGENT}|${ENGINE}`;
    const state = {
      route: "live-config",
      data: {
        agents: [makeAgent(), makeAgent(OTHER_AGENT)],
        agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: {
          [KEY]: { content: OLD_CONTENT, reading: false },
          [OTHER_KEY]: { content: '{"other":true}', reading: false },
        },
        pendingDeployTasks: { [KEY]: { taskId: "dep-a" } },
      },
      session: { role: "admin" },
    };

    const { ctx } = buildContext(state, {
      "GET /agents": () => state.data.agents,
      "GET /agents/test-agent/configs/xray/workspace": emptyWorkspace,
      "GET /tasks/dep-a": { status: "succeeded", id: "dep-a" },
      "POST /tasks": { id: "r-x" },
      "GET /tasks/r-x": { status: "succeeded", id: "r-x" },
      "GET /tasks/r-x/config-snapshot": { content: NEW_CONTENT },
    });

    installForms(ctx, {});
    const pages = installConfigPages(ctx);
    await pages.liveConfig();
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[D] pending deploy cleared for target agent");
    assert.notEqual(state.data.liveSources?.[KEY]?.content, OLD_CONTENT,
      "[D] target agent's old content replaced after reconciliation");
    assert.equal(state.data.liveSources?.[OTHER_KEY]?.content, '{"other":true}',
      "[D] other agent's cache NOT affected");
    assert.equal(state.data.pendingDeployTasks?.[OTHER_KEY], undefined,
      "[D] other agent has no pending to clear (was never set)");
  }

  // --- Test E (ABA): Old deploy A succeeds -> cache invalidated.
  // Then B overwrites pending; B fails later -> only B's pending cleared.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };

    const gateA = deferred();
    const gateB = deferred();
    let editorReadCountE = 0;


    const planForm = new FakeForm({
      operation: "modify", tag: "t", listen: "0.0.0.0", port: "443",
      username: "", credential: "", secondary_credential: "",
      method: "", transport: "raw", transport_path: "",
      tls_enabled: "0", certificate_path: "", private_key_path: "",
      reality_enabled: "0", reality_private_key: "", reality_public_key: "",
      reality_short_id: "", reality_server_name: "",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/.*": { present: false, fragment: "" },
      "POST /agents/test-agent/configs/xray/server-inbounds": () => ({
        config: { id: "cfg-a", version: 2 },
        task: { id: "dep-A", status: "pending" },
      }),
      "GET /tasks/dep-A": () => gateA.promise,
      "GET /tasks/dep-B": (() => {
        let bCalls = 0;
        return async () => {
          bCalls += 1;
          if (bCalls <= 1) return { status: "running", id: "dep-B" };
          return await gateB.promise;
        };
      })(),
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "read-config") {
          editorReadCountE += 1;
          return { id: "read-e1" };
        }
        return { id: "other-e" };
      },
      "GET /tasks/read-e1": { status: "succeeded", id: "read-e1" },
      "GET /tasks/read-e1/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#server-plan-form": planForm });
    await pages.agentConfig();

    // Submit deploy A via form handler (starts monitor A).
    await planForm.dispatchSubmit({ planIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "dep-A",
      "[E] dep-A recorded as pending");

    // Simulate deploy B overwriting A in the pending map
    // (happens when a second mutation fires before A completes).
    state.data.pendingDeployTasks[KEY] = { taskId: "dep-B" };

    // Release gate A -> old dep-A succeeds.
    gateA.resolve({ status: "succeeded", id: "dep-A" });
    await drain();

    // Cache IS invalidated because A succeeded and changed the node file.
    assert.equal(state.data.liveSources?.[KEY], undefined,
      "[E] cache invalidated by old deploy A's success");

    // Pending still holds dep-B (A did NOT clear it via CAS).
    assert.equal(state.data.pendingDeployTasks?.[KEY]?.taskId, "dep-B",
      "[E] newer dep-B pending NOT cleared by old dep-A completion");

    // Step 4: Start B's recovery poller by entering live-config.
    // reconcilePendingDeploy sees dep-B running -> starts recovery poller.
    state.route = "live-config";
    await pages.liveConfig();
    await drain();

    // Step 5: Release gate B -> dep-B fails.
    gateB.resolve({ status: "failed", id: "dep-B", error: "conflict" });
    await drain();

    // Assert: B's failure cleared its own pending record.
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[E] dep-B pending cleared after B's own terminal failure");

    // Strict: post-A fresh read completed and cached NEW_CONTENT.
    assert.ok(editorReadCountE >= 1,
      "[E] post-A fresh read-config fired");
    assert.equal(state.data.liveSources?.[KEY]?.content, NEW_CONTENT,
      "[E] cache contains post-deploy content (not OLD_CONTENT) after B failed");
  }


  // --- Test F: Live-config editor deploy -> terminal success ->
  // pending cleanup + cache invalidation + guarded re-render.
  {
    const state = {
      route: "live-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveAgent: AGENT_ID, liveEngine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let editorReadCount = 0;

    const liveForm = new FakeForm({
      content: NEW_CONTENT, name: "e", description: "d", version: "1",
    });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": emptyWorkspace,
      "PUT /agents/test-agent/configs/xray": { config: { id: "lc", version: 2 } },
      "POST /tasks": (options) => {
        const body = JSON.parse(options?.body || "{}");
        if (body.action === "deploy") return { id: "lc-deploy-task" };
        editorReadCount += 1;
        return { id: `editor-read-${editorReadCount}` };
      },
      "GET /tasks/lc-deploy-task": () => ({
        status: "succeeded", id: "lc-deploy-task",
      }),
      "GET /tasks/editor-read-\\d+": (_o, p) => ({
        status: "succeeded", id: p.split("/").pop(),
      }),
      "GET /tasks/editor-read-\\d+/config-snapshot": { content: NEW_CONTENT },
    });

    const pages = installForms(ctx, { "#live-config-form": liveForm });
    await pages.liveConfig();
    await liveForm.dispatchSubmit({ liveIntent: "deploy" });
    await drain();

    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[F] pending cleared after editor deploy succeeded");
    assert.ok(editorReadCount >= 1,
      "[F] fresh read-config fired after editor deploy succeeded");
    assert.equal(state.data.liveSources?.[KEY]?.content, NEW_CONTENT,
      "[F] cache contains post-deploy content after convergence");
  }

  // --- Test G: Source-config deploy success uses same terminal mechanism.
  {
    const state = {
      route: "agent-config",
      data: {
        agents: [makeAgent()], agentId: AGENT_ID, engine: ENGINE,
        liveSources: { [KEY]: { content: OLD_CONTENT, reading: false } },
      },
      session: { role: "admin" },
    };
    let srcPollCount = 0;

    const srcForm = new FakeForm({ name: "s", description: "d", content: NEW_CONTENT });

    const { ctx } = buildContext(state, {
      "GET /agents": () => [makeAgent()],
      "GET /agents/test-agent/configs/xray/workspace": workspaceWithConfig(),
      "POST /agents/test-agent/configs/xray/plans": {
        protocol: "ss2022", listen: "0.0.0.0", port: 443, transport: "raw" },
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/xray/fields/log": { present: false, fragment: "" },
      "PUT /agents/test-agent/configs/xray": { config: { id: "sc", version: 2 } },
      "POST /tasks": { id: "src-deploy-task" },
      "GET /tasks/src-deploy-task": () => {
        srcPollCount += 1;
        return { status: "succeeded", id: "src-deploy-task" };
      },
    });

    const pages = installForms(ctx, { "#source-config-form": srcForm });
    await pages.agentConfig();
    await srcForm.dispatchSubmit({ sourceIntent: "deploy" });
    await drain();

    assert.ok(srcPollCount > 0,
      "[G] source-config deploy terminal was checked");
    assert.equal(state.data.pendingDeployTasks?.[KEY], undefined,
      "[G] source-config pending cleared after deploy succeeded");
    assert.equal(state.data.liveSources?.[KEY], undefined,
      "[G] source-config cache invalidated after deploy succeeded");
  }

  if (previousSetTimeout === undefined) delete globalThis.setTimeout;
  else globalThis.setTimeout = previousSetTimeout;
  if (previousFormData === undefined) delete globalThis.FormData;
  else globalThis.FormData = previousFormData;
  if (previousDocument === undefined) delete globalThis.document;
  else globalThis.document = previousDocument;
}
assert.equal(developmentSourceVisible("mihomo", "development"), true, "mihomo development shows source choice");
assert.equal(developmentSourceVisible("mihomo", "stable"), false, "stable hides source choice");
assert.equal(developmentSourceVisible("xray", "development"), false, "non-mihomo hides source choice");
assert.equal(coreSourceForInstall("mihomo", "development", "mirror"), "mirror", "mirror carries through");
assert.equal(coreSourceForInstall("mihomo", "development", ""), "official", "omitted source defaults to official");
assert.equal(coreSourceForInstall("mihomo", "stable", "mirror"), undefined, "stable omits source");
assert.equal(coreSourceForInstall("xray", "development", "mirror"), undefined, "non-mihomo omits source");
assert.equal(coreSourceName("mirror"), "vernesong/mihomo 镜像（第三方）", "mirror audited label");
assert.equal(coreSourceName("official"), "MetaCubeX/mihomo 官方", "official audited label");
assert.equal(coreSourceName(""), "", "no source has no label");
assert.equal(coreSourceLabel("mihomo", "development", ""), "MetaCubeX/mihomo 官方", "omitted mihomo development audits as official");
assert.equal(coreSourceLabel("mihomo", "development", "official"), "MetaCubeX/mihomo 官方", "explicit official audits as official");
assert.equal(coreSourceLabel("mihomo", "development", "mirror"), "vernesong/mihomo 镜像（第三方）", "mirror audits as third party");
assert.equal(coreSourceLabel("mihomo", "stable", ""), "", "stable has no source label");
assert.equal(coreSourceLabel("xray", "development", ""), "", "non-mihomo has no source label");

const formattedJson = formatConfigContent(
  '{"proxies":[],"mode":"rule","port":7890,"enabled":true,"empty":null}',
  "JSON",
);
assert.equal(
  formattedJson,
  '{\n  "proxies": [],\n  "mode": "rule",\n  "port": 7890,\n  "enabled": true,\n  "empty": null\n}\n',
  "JSON format applies two-space indentation, preserves order and types, and ends with one newline",
);
assert.equal(
  formatConfigContent('[1,{"nested":[2,3]}]', "JSON"),
  '[\n  1,\n  {\n    "nested": [\n      2,\n      3\n    ]\n  }\n]\n',
  "JSON format keeps array order and nesting",
);
assert.ok(
  formatConfigContent('{"a":1,\n\n\n "b":2}', "JSON").startsWith('{\n  "a": 1'),
  "JSON format is syntax-aware and idempotent across whitespace",
);
assert.throws(
  () => formatConfigContent('{"a":1,}', "JSON"),
  ConfigFormatError,
  "JSON with a trailing comma fails closed",
);
assert.throws(
  () => formatConfigContent('{"a":1} // sing-box comment', "JSON"),
  ConfigFormatError,
  "sing-box extended JSON comments fail closed",
);
assert.throws(
  () =>
    formatConfigContent(
      '{"a":1,"a":2}',
      "JSON",
    ),
  ConfigFormatError,
  "duplicate JSON keys fail closed",
);
assert.equal(
  formatConfigContent('{"big":9007199254740993}', "JSON"),
  '{\n  "big": 9007199254740993\n}\n',
  "unsafe integers are preserved verbatim, never rounded",
);
assert.equal(
  formatConfigContent('{"x":1e400}', "JSON"),
  '{\n  "x": 1e400\n}\n',
  "overflowing exponents are preserved verbatim",
);
assert.equal(
  formatConfigContent('{"x":9.007199254740993e15}', "JSON"),
  '{\n  "x": 9.007199254740993e15\n}\n',
  "high-precision decimals keep their full token",
);
assert.equal(
  formatConfigContent('{"x":0.100000000000000005}', "JSON"),
  '{\n  "x": 0.100000000000000005\n}\n',
  "high-precision decimal literals are not collapsed",
);
assert.equal(
  formatConfigContent('{"x":-0}', "JSON"),
  '{\n  "x": -0\n}\n',
  "negative zero keeps its sign",
);
assert.equal(
  formatConfigContent('{"10":"ten","2":"two","a":1}', "JSON"),
  '{\n  "10": "ten",\n  "2": "two",\n  "a": 1\n}\n',
  "object member order is preserved, including integer-like keys",
);
assert.equal(
  formatConfigContent('{"a\\u0041":1,"b\\n":2}', "JSON"),
  '{\n  "a\\u0041": 1,\n  "b\\n": 2\n}\n',
  "escaped object keys and string escapes are preserved",
);
assert.throws(
  () =>
    formatConfigContent(
      "mixed-port: 7890\n# keep me\nproxies: []\n",
      "YAML",
    ),
  ConfigFormatError,
  "Mihomo YAML with comments fails closed without a comment-preserving parser",
);
assert.throws(
  () => formatConfigContent("   ", "JSON"),
  ConfigFormatError,
  "empty content fails closed",
);

let requestedRoute = "dashboard";
let releaseFirstRender;
const firstRenderGate = new Promise((resolve) => {
  releaseFirstRender = resolve;
});
const renderedRoutes = [];
let activeRenders = 0;
let maximumActiveRenders = 0;
let canceledRenders = 0;
const scheduleRender = createLatestRenderScheduler(
  async () => {
    const route = requestedRoute;
    renderedRoutes.push(route);
    activeRenders += 1;
    maximumActiveRenders = Math.max(maximumActiveRenders, activeRenders);
    if (renderedRoutes.length === 1) await firstRenderGate;
    activeRenders -= 1;
  },
  { cancelActive: () => (canceledRenders += 1) },
);
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
assert.equal(canceledRenders, 2, "new navigation cancels the stale route work");

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
  {
    id: "gamma",
    name: "Gamma",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: presetEngines,
    features: ["mihomo-development-source-v1"],
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
      presetMarkup.includes("data-development-source"),
      true,
      "preset version drawer exposes the Mihomo development source fieldset",
    );
    assert.equal(
      presetMarkup.includes('value="mirror" disabled'),
      true,
      "legacy Agent mirror stays disabled",
    );
    assert.equal(
      presetMarkup.includes("source-upgrade-note"),
      true,
      "legacy Agent shows the upgrade-source explanation",
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
    ["#preset-node-alpha", "#preset-node-beta", "#preset-node-gamma"],
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

  presetState.anchor = "preset-node-gamma";
  presetState.data.selectedAgent = "gamma";
  await renderPresetAgents({ overview: { agents: 3, agents_online: 3 } });
  assert.equal(
    presetWorkspaces.map((workspace) => workspace.id)[0],
    "preset-node-gamma",
    "feature-capable preset node is selected",
  );
  assert.equal(
    presetMarkup.includes('value="mirror" disabled'),
    false,
    "source-capable Agent mirror is not disabled",
  );
  assert.equal(
    presetMarkup.includes("source-upgrade-note"),
    false,
    "source-capable Agent hides the upgrade-source explanation",
  );
  assert.equal(
    presetMarkup.includes('value="mirror"'),
    true,
    "source-capable Agent exposes the mirror option",
  );
  assert.equal(
    presetMarkup.includes("data-development-source"),
    true,
    "source-capable Agent still exposes the source fieldset",
  );
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

const pollingDocument = globalThis.document;
globalThis.document = {
  hidden: false,
  activeElement: null,
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const coreTimers = new Map();
  let nextCoreTimer = 1;
  let coreRequests = 0;
  let coreRenders = 0;
  let coreMarkup = "";
  let coreFailure = false;
  const coreState = {
    route: "core-logs",
    navigationEpoch: 1,
    data: {},
  };
  const renderCoreLogs = installCoreLogs({
    state: coreState,
    engines: ["mihomo"],
    can: () => true,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    date: (value) => value,
    api: async (path) => {
      coreRequests += 1;
      if (path.startsWith("/core-logs?") && coreFailure)
        throw new Error("temporary log failure");
      if (path.startsWith("/core-logs?"))
        return [{ id: 1, agent_id: "alpha", engine: "mihomo", level: "info", message: "ready", logged_at: "now" }];
      if (path === "/agents") return [{ id: "alpha", name: "Alpha" }];
      assert.fail(`unexpected core-log polling path ${path}`);
    },
    shell: (markup) => {
      coreMarkup = markup;
      coreRenders += 1;
    },
    setTimer: (callback) => {
      const id = nextCoreTimer++;
      coreTimers.set(id, callback);
      return id;
    },
    clearTimer: (id) => coreTimers.delete(id),
  });
  await renderCoreLogs();
  assert.equal(coreRequests, 2, "log refresh uses two parallel data requests");
  assert.equal(coreRenders, 1);
  assert.equal(coreTimers.size, 1, "log polling owns one timer");
  assert.equal(coreMarkup.includes('data-refresh-key="core-log-1"'), true);
  const corePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await corePoll();
  assert.equal(coreRequests, 4, "one log poll issues one request per data source");
  assert.equal(coreRenders, 2);
  assert.equal(coreTimers.size, 1, "log polling reschedules exactly one timer");
  coreFailure = true;
  const failedCorePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await failedCorePoll();
  assert.equal(coreRenders, 2, "a log error preserves the current view");
  assert.equal(coreTimers.size, 1, "a log error keeps recovery polling alive");
  coreFailure = false;
  const recoveredCorePoll = [...coreTimers.values()][0];
  coreTimers.clear();
  await recoveredCorePoll();
  assert.equal(coreRenders, 3, "log polling recovers without clearing state");
  assert.equal(coreTimers.size, 1);

  const trafficTimers = new Map();
  let nextTrafficTimer = 1;
  let trafficRequests = 0;
  let trafficRenders = 0;
  let trafficMarkup = "";
  const trafficState = {
    route: "traffic",
    navigationEpoch: 1,
    anchor: "traffic",
    data: {},
  };
  const renderTraffic = installTraffic({
    state: trafficState,
    can: () => false,
    esc: (value) => String(value ?? ""),
    engineName: (value) => value,
    bytes: (value) => `${value || 0} B`,
    rate: (value) => `${value || 0} B/s`,
    percent: (used, limit) => (limit ? Number(used || 0) * 100 / Number(limit) : 0),
    ago: () => "刚刚",
    api: async (path) => {
      trafficRequests += 1;
      if (path === "/agents")
        return [{ id: "alpha", name: "Alpha", features: ["port-traffic-v1"], capabilities: ["mihomo"] }];
      if (path === "/traffic-policies")
        return [{ id: "policy-a", agent_id: "alpha", engine: "mihomo", name: "Primary", port: 443, protocol: "tcp", cycle: "monthly", cycle_anchor: "2026-01-01T00:00:00Z", used_bytes: 10, limit_bytes: 100, received_bytes: 6, sent_bytes: 4, receive_bps: 1, send_bps: 1, enforcement_available: true, last_reported_at: "now" }];
      assert.fail(`unexpected traffic polling path ${path}`);
    },
    shell: (markup) => {
      trafficMarkup = markup;
      trafficRenders += 1;
    },
    setTimer: (callback) => {
      const id = nextTrafficTimer++;
      trafficTimers.set(id, callback);
      return id;
    },
    clearTimer: (id) => trafficTimers.delete(id),
  });
  await renderTraffic();
  assert.equal(trafficRequests, 2, "traffic refresh uses two parallel requests");
  assert.equal(trafficRenders, 1);
  assert.equal(trafficTimers.size, 1, "traffic polling owns one timer");
  assert.equal(
    trafficMarkup.includes('data-refresh-key="traffic-policy-policy-a"'),
    true,
  );
  const trafficPoll = [...trafficTimers.values()][0];
  trafficTimers.clear();
  await trafficPoll();
  assert.equal(trafficRequests, 4);
  assert.equal(trafficRenders, 2);
  assert.equal(trafficTimers.size, 1, "traffic polling reschedules one timer");

  let metricRequests = 0;
  const metricState = {
    route: "node-settings",
    navigationEpoch: 1,
    data: {},
  };
  const { pollAgentMetrics } = installAgents(
    new Proxy(
      {
        state: metricState,
        api: async (path) => {
          assert.equal(path, "/agents");
          metricRequests += 1;
          return ["alpha", "beta", "gamma"].map((id) => ({
            id,
            status: "online",
            metrics: {},
            runtime: {},
          }));
        },
        can: (capability) => capability === "metrics.read",
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  globalThis.document.hidden = true;
  await pollAgentMetrics();
  clearTimeout(metricState.agentPollTimer);
  assert.equal(metricRequests, 0, "hidden node page defers metrics requests");
  globalThis.document.hidden = false;
  await pollAgentMetrics();
  clearTimeout(metricState.agentPollTimer);
  assert.equal(metricRequests, 1, "three-node metrics patch uses one fleet request");
} finally {
  if (pollingDocument === undefined) delete globalThis.document;
  else globalThis.document = pollingDocument;
}

// The preset page compacts physical DOM only, never the install/version drawer.
{
  const compactDocument = globalThis.document;
  const { compactPresetPage: runCompactPresetPage } = installAgents(
    new Proxy(
      { state: { route: "agents", data: {} } },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const counts = {
    enrollment: 0,
    batch: 0,
    summary: 0,
    state: 0,
    inspector: 0,
    footer: 0,
    unavailable: 0,
    upgrade: 0,
    batchLabel: 0,
    drawer: 0,
    toggle: 0,
  };
  const tracked = (name) => ({
    remove: () => {
      counts[name] += 1;
    },
  });
  const workspace = {
    dataset: { agentNode: "gamma" },
    querySelector(selector) {
      if (selector === ".machine-resource-summary") return tracked("summary");
      if (selector === ".machine-state") return tracked("state");
      if (selector === ".node-inspector") return tracked("inspector");
      if (selector === ".machine-footer") return tracked("footer");
      if (selector === ".runtime-drawer") return tracked("drawer");
      if (selector === ".service-version-toggle") return tracked("toggle");
      return null;
    },
    querySelectorAll(selector) {
      if (selector === ".service-management-unavailable, [data-upgrade-agent]") {
        return [tracked("unavailable"), tracked("upgrade")];
      }
      if (selector === "[data-batch-checkbox]") {
        const label = tracked("batchLabel");
        return [{ closest: () => label }];
      }
      return [];
    },
  };
  globalThis.document = {
    querySelector(selector) {
      if (selector === "#enrollment") return tracked("enrollment");
      if (selector === "#batch-form") return tracked("batch");
      return null;
    },
    querySelectorAll(selector) {
      if (selector === ".preset-node-workspace") return [workspace];
      return [];
    },
  };
  try {
    runCompactPresetPage();
    assert.equal(counts.drawer, 0, "compact keeps the preset version drawer");
    assert.equal(counts.toggle, 0, "compact keeps the preset version toggle");
    assert.equal(counts.enrollment, 1, "compact removes the enrollment sheet");
    assert.equal(counts.batch, 1, "compact removes the batch form");
    assert.equal(counts.summary, 1, "compact removes the resource summary");
    assert.equal(counts.state, 1, "compact removes the machine state");
    assert.equal(counts.inspector, 1, "compact removes the node inspector");
    assert.equal(counts.footer, 1, "compact removes the machine footer");
    assert.equal(counts.unavailable, 1, "compact removes unavailable-service block");
    assert.equal(counts.upgrade, 1, "compact removes the upgrade-agent block");
    assert.equal(counts.batchLabel, 1, "compact removes the batch checkbox label");
  } finally {
    if (compactDocument === undefined) delete globalThis.document;
    else globalThis.document = compactDocument;
  }
}

// The preset version form binds the real reveal and source-carrying payload path.
{
  const presetDomDocument = globalThis.document;
  const presetDomFormData = globalThis.FormData;
  const presetDomDetails = globalThis.HTMLDetailsElement;
  const presetDomCSS = globalThis.CSS;

  const radio = (name, value, checked = false) => {
    const listeners = [];
    return {
      name,
      value,
      checked,
      addEventListener(type, listener) {
        if (type === "change") listeners.push(listener);
      },
      fireChange() {
        listeners.forEach((listener) => listener({ target: this }));
      },
    };
  };
  const checkedOf = (radios) => radios.find((item) => item.checked);

  const makeVersionForm = ({ agent, canMirror }) => {
    const channels = ["stable", "development", "custom"].map((value, index) =>
      radio("release_channel", value, index === 0),
    );
    const sources = ["official", "mirror"].map((value) =>
      radio("core_source", value, value === "official"),
    );
    const fieldset = { hidden: true };
    const form = {
      dataset: { versionEngine: "mihomo", versionAgent: agent.id },
      elements: {
        namedItem(name) {
          if (name === "release_channel") {
            return { value: checkedOf(channels).value };
          }
          if (name === "core_source") {
            return { value: checkedOf(sources).value };
          }
          if (name === "custom_version") return { value: "" };
          return null;
        },
      },
      querySelector(selector) {
        if (selector === ".custom-version-field") return null;
        if (selector === "[data-development-source]") return fieldset;
        if (selector === 'input[name="release_channel"]:checked') {
          return checkedOf(channels);
        }
        return null;
      },
      querySelectorAll(selector) {
        if (selector === 'input[name="release_channel"]') return channels;
        return [];
      },
      setChannel(value) {
        const current = channels.find((item) => item.value === value);
        channels.forEach((item) => {
          item.checked = item === current;
        });
        current.fireChange();
      },
      setSource(value) {
        sources.forEach((item) => {
          item.checked = item.value === value;
        });
      },
      fieldset,
    };
    return form;
  };

  const agent = {
    id: "gamma",
    name: "Gamma",
    os: "linux",
    arch: "amd64",
    status: "online",
    capabilities: ["mihomo"],
    features: ["mihomo-development-source-v1"],
    runtime: {
      mihomo: { installed: true, service_status: "active", version: "1.0.0" },
    },
  };
  const versionForm = makeVersionForm({ agent, canMirror: true });
  const taskBodies = [];

  globalThis.HTMLDetailsElement = class {};
  globalThis.CSS = { escape: (value) => String(value) };
  globalThis.FormData = class {
    constructor(form) {
      this.form = form;
    }
    get(name) {
      return this.form.elements.namedItem(name)?.value ?? null;
    }
  };
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll(selector) {
      if (selector === ".core-version-form") return [versionForm];
      if (selector === ".preset-node-workspace") {
        return [
          {
            dataset: { agentNode: "gamma" },
            querySelector: () => null,
            querySelectorAll: () => [],
          },
        ];
      }
      if (
        selector ===
        ".preset-node-workspace, .machine-workspace, .node-operations-workspace"
      ) {
        return [
          {
            dataset: { agentNode: "gamma" },
            querySelector: () => null,
            querySelectorAll: () => [],
          },
        ];
      }
      return [];
    },
  };

  try {
    const state = {
      route: "agents",
      anchor: "agents",
      data: { selectedAgent: "gamma" },
    };
    const ctx = new Proxy(
      {
        state,
        engines: ["mihomo"],
        api: async (path, options) => {
          if (path === "/agents") return [agent];
          if (path === "/deployments") return [];
          if (path === "/client-access") return [];
          if (path === "/overview") return { agents: 1, agents_online: 1 };
          if (path === "/agents/gamma/configs") return [];
          if (path === "/tasks") {
            taskBodies.push(options?.body);
            return { id: "task-1" };
          }
          assert.fail(`unexpected preset interaction API path ${path}`);
        },
        optionalAPI: async () => null,
        can: () => true,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        serviceStatusName: (value) => value,
        statusTone: (value) => value,
        conciseVersion: (_engine, value) => value,
        confirmAction: async () => true,
        shell: noop,
      },
      { get: (target, key) => target[key] ?? noop },
    );
    const { agents: renderPresetInteraction } = installAgents(ctx);
    await renderPresetInteraction({ overview: { agents: 1, agents_online: 1 } });

    assert.equal(
      versionForm.fieldset.hidden,
      true,
      "stable channel keeps the development source fieldset hidden",
    );
    versionForm.setChannel("development");
    assert.equal(
      versionForm.fieldset.hidden,
      false,
      "dev channel reveals the development source fieldset",
    );
    versionForm.setChannel("stable");
    assert.equal(
      versionForm.fieldset.hidden,
      true,
      "returning to stable hides the development source fieldset",
    );

    versionForm.setChannel("development");
    versionForm.setSource("official");
    await versionForm.onsubmit({ preventDefault: noop });
    let payload = JSON.parse(taskBodies.at(-1));
    assert.equal(payload.engine, "mihomo", "payload targets mihomo");
    assert.equal(payload.core_version, "development", "payload carries dev channel");
    assert.equal(
      payload.core_source,
      "official",
      "explicit official carries through the payload",
    );

    versionForm.setSource("mirror");
    await versionForm.onsubmit({ preventDefault: noop });
    payload = JSON.parse(taskBodies.at(-1));
    assert.equal(
      payload.core_source,
      "mirror",
      "feature-capable mirror carries through the payload",
    );
  } finally {
    if (presetDomDocument === undefined) delete globalThis.document;
    else globalThis.document = presetDomDocument;
    if (presetDomFormData === undefined) delete globalThis.FormData;
    else globalThis.FormData = presetDomFormData;
    if (presetDomDetails === undefined) delete globalThis.HTMLDetailsElement;
    else globalThis.HTMLDetailsElement = presetDomDetails;
    if (presetDomCSS === undefined) delete globalThis.CSS;
    else globalThis.CSS = presetDomCSS;
  }
}

// The manual config code editor formats JSON in place and fails closed on
// YAML/comments/lossy or readonly content, without re-rendering the page.
{
  const formatDocument = globalThis.document;
  const makeTextNode = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      hidden: false,
      value: "",
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
  };
  const makeCodeInput = (initialValue, { readOnly = false } = {}) => {
    const listeners = new Map();
    const input = {
      value: initialValue,
      readOnly,
      disabled: false,
      selectionStart: initialValue.length,
      selectionEnd: initialValue.length,
      scrollTop: 0,
      scrollLeft: 0,
      classList: { toggle() {} },
      setAttribute() {},
      dispatchEvent() {},
      focus() {},
      closest: () => ({
        querySelectorAll: () => [],
        addEventListener: () => {},
      }),
      setSelectionRange(start, end) {
        input.selectionStart = start;
        input.selectionEnd = end;
      },
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
    return input;
  };
  const buildEditor = (initialValue, language, readOnly = false) => {
    const input = makeCodeInput(initialValue, { readOnly });
    const gutter = makeTextNode();
    const byteLabel = makeTextNode();
    const position = makeTextNode();
    const status = makeTextNode();
    const statusDot = makeTextNode();
    const validation = makeTextNode();
    const reset = makeTextNode();
    const format = makeTextNode();
    const editor = {
      dataset: {
        codeLanguage: language,
        codeMaxBytes: "2097152",
        dirty: "0",
        codeValid: "1",
      },
      querySelector(selector) {
        if (selector === "[data-code-input]") return input;
        if (selector === "[data-line-numbers]") return gutter;
        if (selector === "[data-code-bytes]") return byteLabel;
        if (selector === "[data-code-position]") return position;
        if (selector === "[data-code-status]") return status;
        if (selector === "[data-code-status-dot]") return statusDot;
        if (selector === "[data-code-validation]") return validation;
        if (selector === "[data-code-reset]") return reset;
        if (selector === "[data-code-format]") return format;
        return null;
      },
      input,
      gutter,
      byteLabel,
      position,
      status,
      statusDot,
      validation,
      reset,
      format,
    };
    return editor;
  };

  const { bindCodeEditors } = installAgents(
    new Proxy(
      {
        state: { route: "live-config", data: {} },
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );

  const unformatted = '{"tag":"demo","port":443,"tls":{"enabled":true}}';
  const editor = buildEditor(unformatted, "JSON");
  const readonlyEditor = buildEditor(unformatted, "JSON", true);
  const brokenEditor = buildEditor('{"tag":"demo",}', "JSON");
  const largeEditor = buildEditor(
    JSON.stringify(Array(840001).fill(0)),
    "JSON",
  );
  const deepEditor = buildEditor("[".repeat(600000), "JSON");
  let editorQueryCalls = 0;

  globalThis.document = {
    querySelector: () => null,
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") {
        editorQueryCalls += 1;
        return [editor, readonlyEditor, brokenEditor, largeEditor, deepEditor];
      }
      return [];
    },
  };

  try {
    bindCodeEditors();
    assert.equal(
      editorQueryCalls,
      1,
      "binding queries the editor list exactly once",
    );
    assert.equal(
      editor.dataset.dirty,
      "0",
      "unmodified editor starts clean",
    );

    editor.format.dispatch("click", {});
    assert.equal(
      editorQueryCalls,
      1,
      "formatting does not re-query or re-render the editor page",
    );
    assert.equal(
      editor.input.value,
      '{\n  "tag": "demo",\n  "port": 443,\n  "tls": {\n    "enabled": true\n  }\n}\n',
      "JSON is formatted in place with two-space indentation and a final newline",
    );
    assert.equal(editor.dataset.dirty, "1", "formatting marks the editor dirty");
    assert.equal(editor.reset.disabled, false, "reset is enabled after formatting");
    assert.equal(
      editor.input.value.includes("  \"tag\": \"demo\""),
      true,
      "two-space indentation applied",
    );

    const firstSnapshot = editor.input.value;
    editor.format.dispatch("click", {});
    assert.equal(
      editor.input.value,
      firstSnapshot,
      "repeated formatting is idempotent",
    );

    readonlyEditor.format.dispatch("click", {});
    assert.equal(
      readonlyEditor.input.value,
      unformatted,
      "readonly editor is never rewritten",
    );
    assert.equal(
      readonlyEditor.dataset.dirty,
      "0",
      "readonly editor stays clean",
    );

    brokenEditor.format.dispatch("click", {});
    assert.equal(
      brokenEditor.input.value,
      '{"tag":"demo",}',
      "syntax-error content is preserved on failure",
    );
    assert.equal(
      brokenEditor.dataset.dirty,
      "0",
      "failure keeps the dirty baseline unchanged",
    );
    assert.equal(
      /无法安全格式化|JSON 语法错误/.test(brokenEditor.validation.textContent),
      true,
      "failure surfaces a local, explicit message",
    );
    assert.equal(
      brokenEditor.statusDot.style.background,
      "var(--red)",
      "failure marks the status dot red",
    );

    const deepSnapshot = deepEditor.input.value;
    deepEditor.format.dispatch("click", {});
    assert.equal(
      deepEditor.input.value,
      deepSnapshot,
      "over-deep content is preserved on failure",
    );
    assert.equal(
      deepEditor.dataset.dirty,
      "0",
      "over-deep failure keeps the dirty baseline unchanged",
    );
    assert.equal(
      deepEditor.validation.textContent,
      "当前内容无法安全格式化。",
      "internal formatter errors fall back to a generic local message",
    );
    assert.equal(
      deepEditor.statusDot.style.background,
      "var(--red)",
      "over-deep failure marks the status dot red",
    );

    const largeSnapshot = largeEditor.input.value;
    largeEditor.format.dispatch("click", {});
    assert.equal(
      largeEditor.input.value,
      largeSnapshot,
      "over-limit formatted output keeps the original text",
    );
    assert.equal(
      largeEditor.dataset.dirty,
      "0",
      "over-limit result keeps the dirty baseline unchanged",
    );
    assert.equal(
      /超过 2 MiB 上限/.test(largeEditor.validation.textContent),
      true,
      "over-limit result shows a local error without submitting",
    );
    assert.equal(
      largeEditor.statusDot.style.background,
      "var(--red)",
      "over-limit result marks the status dot red",
    );
  } finally {
    if (formatDocument === undefined) delete globalThis.document;
    else globalThis.document = formatDocument;
  }
}

// The archive new-config engine selector syncs the source editor language,
// file/language labels, and formatter on change without rebuilding the DOM.
{
  const archiveDocument = globalThis.document;
  const archiveEvent = globalThis.Event;
  globalThis.Event = class {
    constructor(type, options) {
      this.type = type;
      this.bubbles = options?.bubbles;
    }
  };

  const makeTarget = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      hidden: false,
      value: "",
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      _listeners: listeners,
    };
  };
  const makeInput = (initialValue) => {
    const listeners = new Map();
    const input = {
      value: initialValue,
      readOnly: false,
      disabled: false,
      selectionStart: initialValue.length,
      selectionEnd: initialValue.length,
      scrollTop: 0,
      scrollLeft: 0,
      classList: { toggle() {} },
      setAttribute() {},
      focus() {},
      closest: () => ({
        querySelectorAll: () => [],
        addEventListener: () => {},
      }),
      setSelectionRange(start, end) {
        input.selectionStart = start;
        input.selectionEnd = end;
      },
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
      dispatchEvent(event) {
        input.dispatch(event?.type || "input", event);
      },
    };
    return input;
  };
  const makeArchiveEditor = (initialValue, engine) => {
    const input = makeInput(initialValue);
    const gutter = makeTarget();
    const byteLabel = makeTarget();
    const position = makeTarget();
    const status = makeTarget();
    const statusDot = makeTarget();
    const validation = makeTarget();
    const reset = makeTarget();
    const format = makeTarget();
    const languageLabel = makeTarget();
    const fileLabel = makeTarget();
    const editor = {
      dataset: {
        codeLanguage: engine === "mihomo" ? "YAML" : "JSON",
        codeMaxBytes: "2097152",
        dirty: "0",
        codeValid: "1",
      },
      querySelector(selector) {
        if (selector === "[data-code-input]") return input;
        if (selector === "[data-line-numbers]") return gutter;
        if (selector === "[data-code-bytes]") return byteLabel;
        if (selector === "[data-code-position]") return position;
        if (selector === "[data-code-status]") return status;
        if (selector === "[data-code-status-dot]") return statusDot;
        if (selector === "[data-code-validation]") return validation;
        if (selector === "[data-code-reset]") return reset;
        if (selector === "[data-code-format]") return format;
        if (selector === ".code-language") return languageLabel;
        if (selector === ".code-file-meta b") return fileLabel;
        return null;
      },
      input,
      gutter,
      byteLabel,
      position,
      status,
      statusDot,
      validation,
      reset,
      format,
      languageLabel,
      fileLabel,
    };
    return editor;
  };

  const engineSelect = makeTarget();
  engineSelect.value = "mihomo";
  const archiveForm = {
    querySelector(selector) {
      if (selector === 'select[name="engine"]') return engineSelect;
      if (selector === "[data-code-editor]") return archiveEditor;
      return null;
    },
    addEventListener() {},
  };
  const archiveEditor = makeArchiveEditor(
    "mixed-port: 7890\nproxies: []\n",
    "mihomo",
  );

  globalThis.document = {
    querySelector(selector) {
      if (selector === "#archive-form") return archiveForm;
      if (selector === "[data-code-editor]") return archiveEditor;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") return [archiveEditor];
      return [];
    },
  };

  const archiveState = { route: "archive-config", data: { newConfig: true } };
  const { bindCodeEditors } = installAgents(
    new Proxy(
      {
        state: { route: "archive-config", data: {} },
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const { archiveConfigs } = installConfigPages(
    new Proxy(
      {
        state: archiveState,
        engines: ["mihomo", "xray", "sing-box", "ss-rust"],
        api: async () => [],
        optionalAPI: async () => null,
        can: () => true,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        ago: () => "now",
        date: () => "now",
        conciseVersion: () => "1.0.0",
        bytes: () => "0 B",
        confirmAction: async () => true,
        notify: () => {},
        shell: () => {},
        submitTask: async () => {},
        bindCodeEditors,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );

  try {
    await archiveConfigs();
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "YAML",
      "new archive config starts with YAML editor language",
    );

    engineSelect.value = "xray";
    engineSelect.dispatch("change", {});
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "JSON",
      "engine switch updates the editor language to JSON",
    );
    assert.equal(
      archiveEditor.languageLabel.textContent,
      "JSON",
      "engine switch updates the language badge",
    );
    assert.equal(
      archiveEditor.fileLabel.textContent,
      "config.json",
      "engine switch updates the file name label",
    );

    archiveEditor.input.value = '{"a":1,"b":[2,3]}';
    archiveEditor.format.dispatch("click", {});
    assert.equal(
      archiveEditor.input.value,
      '{\n  "a": 1,\n  "b": [\n    2,\n    3\n  ]\n}\n',
      "formatter uses the switched JSON language without rebuilding the editor",
    );
    assert.equal(
      archiveEditor.dataset.dirty,
      "1",
      "engine switch preserves dirty state after formatting",
    );

    engineSelect.value = "mihomo";
    engineSelect.dispatch("change", {});
    assert.equal(
      archiveEditor.dataset.codeLanguage,
      "YAML",
      "engine switch back updates the editor language to YAML",
    );
    const beforeYaml = archiveEditor.input.value;
    archiveEditor.format.dispatch("click", {});
    assert.equal(
      archiveEditor.input.value,
      beforeYaml,
      "YAML fail-closed keeps the original editor text",
    );
    assert.equal(
      /无法安全格式化|保留原文/.test(archiveEditor.validation.textContent),
      true,
      "YAML fail-closed shows a local message",
    );
  } finally {
    if (archiveDocument === undefined) delete globalThis.document;
    else globalThis.document = archiveDocument;
    if (archiveEvent === undefined) delete globalThis.Event;
    else globalThis.Event = archiveEvent;
  }
}

// A non-operator archive new-config never renders the format action and does
// not bind an engine-sync handler, so the readonly snapshot is untouched.
{
  const readonlyDocument = globalThis.document;
  const readonlyEvent = globalThis.Event;
  globalThis.Event = class {
    constructor(type, options) {
      this.type = type;
      this.bubbles = options?.bubbles;
    }
  };
  const makeTarget = () => {
    const listeners = new Map();
    return {
      textContent: "",
      style: {},
      disabled: false,
      list: listeners,
      addEventListener(type, listener) {
        const list = listeners.get(type) || [];
        list.push(listener);
        listeners.set(type, list);
      },
      dispatch(type, event = {}) {
        (listeners.get(type) || []).forEach((listener) => listener(event));
      },
    };
  };
  const engine = makeTarget();
  engine.value = "mihomo";
  engine.disabled = true;
  const form = {
    querySelector(selector) {
      if (selector === 'select[name="engine"]') return engine;
      return null;
    },
    addEventListener() {},
  };
  const editor = {
    dataset: {
      codeLanguage: "YAML",
      codeMaxBytes: "2097152",
      dirty: "0",
      codeValid: "1",
    },
    querySelector(selector) {
      if (selector === "[data-code-format]") return null;
      if (selector === "[data-code-input]")
        return {
          value: "",
          readOnly: true,
          disabled: false,
          selectionStart: 0,
          selectionEnd: 0,
          scrollTop: 0,
          scrollLeft: 0,
          classList: { toggle() {} },
          setAttribute() {},
          focus() {},
          closest: () => ({ querySelectorAll: () => [], addEventListener: () => {} }),
          setSelectionRange() {},
          addEventListener() {},
          dispatchEvent() {},
        };
      if (selector === "[data-line-numbers]")
        return { textContent: "", scrollTop: 0 };
      return null;
    },
  };
  globalThis.document = {
    querySelector(selector) {
      if (selector === "#archive-form") return form;
      if (selector === "[data-code-editor]") return editor;
      return null;
    },
    querySelectorAll(selector) {
      if (selector === "[data-code-editor]") return [editor];
      return [];
    },
  };
  const { bindCodeEditors } = installAgents(
    new Proxy(
      { state: { route: "archive-config", data: {} } },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  const { archiveConfigs } = installConfigPages(
    new Proxy(
      {
        state: { route: "archive-config", data: { newConfig: true } },
        engines: ["mihomo", "xray"],
        api: async () => [],
        optionalAPI: async () => null,
        can: () => false,
        esc: (value) => String(value ?? ""),
        engineName: (value) => value,
        ago: () => "now",
        date: () => "now",
        conciseVersion: () => "1.0.0",
        bytes: () => "0 B",
        confirmAction: async () => true,
        notify: () => {},
        shell: () => {},
        submitTask: async () => {},
        bindCodeEditors,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  try {
    await archiveConfigs();
    assert.equal(
      engine.disabled,
      true,
      "readonly archive keeps the engine selector disabled",
    );
    assert.equal(
      editor.querySelector("[data-code-format]"),
      null,
      "readonly archive has no format action",
    );
    assert.equal(
      (engine.list.get("change") || []).length,
      0,
      "readonly archive does not bind an engine-sync change handler",
    );
  } finally {
    if (readonlyDocument === undefined) delete globalThis.document;
    else globalThis.document = readonlyDocument;
    if (readonlyEvent === undefined) delete globalThis.Event;
    else globalThis.Event = readonlyEvent;
  }
}

// Dual-stack public address display regressions.
const ipRows = publicAddressRows({
  observed_public_ip: "93.184.216.34",
  public_ipv4: "198.35.26.96",
  public_ipv6: "2606:4700:4700::1111",
});
assert.equal(ipRows.length, 2);
assert.equal(ipRows[0].value, "198.35.26.96");
assert.equal(ipRows[0].source, "公网探测");
assert.equal(ipRows[0].ok, true);
assert.equal(ipRows[1].value, "2606:4700:4700::1111");

const fallbackRows = publicAddressRows({ observed_public_ip: "93.184.216.34" });
assert.equal(fallbackRows[0].value, "93.184.216.34");
assert.equal(fallbackRows[0].source, "控制面观测");
assert.equal(fallbackRows[1].value, "");
assert.equal(fallbackRows[1].ok, false);

assert.equal(
  formatHostPort("2606:4700:4700::1111", "443"),
  "[2606:4700:4700::1111]:443",
);
assert.equal(
  formatHostPort("[2606:4700:4700::1111]", "443"),
  "[2606:4700:4700::1111]:443",
);
assert.equal(formatHostPort("[::1]", "443"), "[::1]:443");
assert.equal(formatHostPort("93.184.216.34", "443"), "93.184.216.34:443");
assert.equal(formatHostPort("node.example.com", "8443"), "node.example.com:8443");
assert.equal(formatHostPort("", "443"), "");

{
  const codeV4 = { textContent: "" };
  const copyV4 = {
    dataset: { copyIp: "" },
    hidden: true,
    title: "",
    setAttribute() {},
  };
  const lineV4 = {
    querySelector(sel) {
      if (sel === "code") return codeV4;
      if (sel === "[data-copy-ip]") return copyV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? lineV4 : null;
    },
  };
  const root = {
    dataset: {},
    querySelector() { return null; },
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, { public_ipv4: "198.35.26.10", public_ipv6: "" });
  assert.equal(codeV4.textContent, "198.35.26.10");
  assert.equal(copyV4.dataset.copyIp, "198.35.26.10");
  assert.equal(copyV4.hidden, false);
}

{
  const code = { textContent: "" };
  const small = { textContent: "" };
  const line = {
    querySelector(sel) {
      if (sel === "code") return code;
      if (sel === "small") return small;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-public-ips"; } },
    querySelector(sel) {
      return sel === '.public-ip-row[data-ip-family="v6"]' ? line : null;
    },
  };
  const root = {
    dataset: {},
    querySelector() { return null; },
    querySelectorAll(sel) {
      return sel === ".node-card-ips, .node-public-ips" ? [container] : [];
    },
  };
  updatePublicIPDisplays(root, { public_ipv6: "2606:4700:4700::1111" });
  assert.equal(code.textContent, "2606:4700:4700::1111");
  assert.equal(small.textContent, "公网探测");
}

// Runtime smoke: a normal node-settings overview render must not throw
// ReferenceError for esc inside cardIPRow, and must emit the probe rows.
{
  const previousDocument = globalThis.document;
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  globalThis.setTimeout = () => 0;
  globalThis.clearTimeout = () => {};
  globalThis.document = {
    querySelector: () => null,
    querySelectorAll: () => [],
  };
  try {
  const overviewState = {
    route: "node-settings",
    navigationEpoch: 1,
    anchor: "node-settings",
    data: {},
  };
  let overviewMarkup = "";
  const overviewAgents = [
    {
      id: "alpha",
      name: "Alpha",
      os: "linux",
      arch: "amd64",
      status: "online",
      version: "1.2.3",
      capabilities: ["mihomo"],
      features: [],
      metrics: {
        public_ipv4: "198.35.26.96",
        public_ipv6: "",
        collected_at: "now",
      },
      runtime: {
        mihomo: {
          installed: true,
          version: "1.19.0",
          service_status: "running",
        },
      },
      last_seen: "now",
      enrolled_at: "now",
    },
  ];
  const overviewCtx = new Proxy(
    {
      state: overviewState,
      engines: ["mihomo"],
      api: async (path) => {
        if (path === "/agents") return overviewAgents;
        if (path === "/overview") return { agents: 1, agents_online: 1 };
        if (path === "/enrollment-tokens") return [];
        if (path.endsWith("/configs")) return [];
        if (path === "/deployments" || path === "/client-access") return [];
        return [];
      },
      optionalAPI: async () => null,
      can: () => true,
      esc: (value) => String(value ?? ""),
      engineName: (value) => value,
      serviceStatusName: (value) => value,
      statusTone: (value) => value,
      conciseVersion: (_engine, value) => value,
      ago: () => "刚刚",
      heartbeat: () => "刚刚",
      bytes: (value) => `${value || 0} B`,
      percent: (used, limit) => (limit ? Number(used || 0) / limit : 0),
      rate: (value) => `${value || 0} B/s`,
      actionName: (value) => value,
      serviceActionDisabled: () => false,
      trafficChart: () => "",
      renderConfigDiff: () => "",
      notify: () => {},
      confirmAction: () => {},
      short: (value) => value,
      date: (value) => value,
      shell: (markup) => {
        overviewMarkup = markup;
      },
    },
    { get: (target, key) => target[key] ?? noop },
  );
  const { nodeSettings: renderOverview } = installAgents(overviewCtx);
  await renderOverview(false, { overview: { agents: 1, agents_online: 1 } });
  assert.equal(
    overviewMarkup.includes('class="node-card-ips"'),
    true,
    "node-settings overview card renders the public-address strip",
  );
  assert.equal(
    overviewMarkup.includes("198.35.26.96"),
    true,
    "node-settings card prints the probed IPv4 address",
  );
  assert.equal(
    overviewMarkup.includes("data-copy-ip"),
    true,
    "node-settings card keeps a copy button",
  );
  } finally {
    if (previousSetTimeout === undefined) delete globalThis.setTimeout;
    else globalThis.setTimeout = previousSetTimeout;
    if (previousClearTimeout === undefined) delete globalThis.clearTimeout;
    else globalThis.clearTimeout = previousClearTimeout;
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}

// Runtime smoke: the real metrics patch path (pollAgentMetrics ->
// updateAgentMetrics -> updatePublicIPDisplays) updates the card rows and
// copy-button state instead of only the direct helper.
{
  const previousDocument = globalThis.document;
  const previousCSS = globalThis.CSS;
  const previousSetTimeout = globalThis.setTimeout;
  const previousClearTimeout = globalThis.clearTimeout;
  globalThis.setTimeout = () => 0;
  globalThis.clearTimeout = () => {};
  globalThis.CSS = { escape: (value) => String(value) };
  const metricState = {
    route: "node-settings",
    navigationEpoch: 1,
    data: {},
  };
  const codeV4 = { textContent: "" };
  const copyV4 = {
    dataset: { copyIp: "" },
    hidden: true,
    title: "",
    setAttribute() {},
  };
  const lineV4 = {
    querySelector(sel) {
      if (sel === "code") return codeV4;
      if (sel === "[data-copy-ip]") return copyV4;
      return null;
    },
    classList: { toggle() {} },
  };
  const container = {
    classList: { contains(cls) { return cls === "node-card-ips"; } },
    querySelector(sel) {
      return sel === '.card-ip-row[data-ip-family="v4"]' ? lineV4 : null;
    },
  };
  const root = {
    dataset: { available: "0" },
    querySelector() { return null; },
    querySelectorAll(sel) {
      if (sel === ".node-card-ips, .node-public-ips") return [container];
      return [];
    },
  };
  globalThis.document = {
    hidden: false,
    querySelector(sel) {
      return sel.includes("data-agent-metrics") ? root : null;
    },
    querySelectorAll() { return []; },
  };
  try {
    const { updateAgentMetrics } = installAgents(
      new Proxy(
        {
          state: metricState,
          engines: [],
          api: async () => [],
          optionalAPI: async () => null,
          can: () => true,
          esc: (value) => String(value ?? ""),
          engineName: (value) => value,
          serviceStatusName: (value) => value,
          statusTone: (value) => value,
          conciseVersion: (_engine, value) => value,
          ago: () => "刚刚",
          heartbeat: () => "刚刚",
          bytes: (value) => `${value || 0} B`,
          percent: (used, limit) => (limit ? Number(used || 0) / limit : 0),
          rate: (value) => `${value || 0} B/s`,
          actionName: (value) => value,
          serviceActionDisabled: () => false,
          trafficChart: () => "",
          renderConfigDiff: () => "",
          notify: () => {},
          confirmAction: () => {},
          short: (value) => value,
          date: (value) => value,
        },
        { get: (target, key) => target[key] ?? noop },
      ),
    );
    updateAgentMetrics({
      id: "alpha",
      status: "online",
      metrics: { public_ipv4: "198.35.26.10", public_ipv6: "" },
      runtime: {},
      version: "1.2.3",
      last_seen: "now",
    });
    assert.equal(codeV4.textContent, "198.35.26.10");
    assert.equal(copyV4.dataset.copyIp, "198.35.26.10");
    assert.equal(copyV4.hidden, false);
  } finally {
    if (previousSetTimeout === undefined) delete globalThis.setTimeout;
    else globalThis.setTimeout = previousSetTimeout;
    if (previousClearTimeout === undefined) delete globalThis.clearTimeout;
    else globalThis.clearTimeout = previousClearTimeout;
    if (previousCSS === undefined) delete globalThis.CSS;
    else globalThis.CSS = previousCSS;
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}
