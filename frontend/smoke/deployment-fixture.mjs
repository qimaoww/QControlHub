import { installConfigPages } from "../modules/configs.js";
// Install one shared test session, then restore it after every ordered case.
export function createDeploymentFixture() {
  const previousDocument = globalThis.document;
  const previousFormData = globalThis.FormData;
  const previousSetTimeout = globalThis.setTimeout;
  const previousHistory = globalThis.history;
  const previousLocation = globalThis.location;
  globalThis.location ??= { hash: "" };
  globalThis.location.hash = "#agent-config";
  const replacedRoutes = [];
  globalThis.history = {replaceState:(_state,_title,path)=>replacedRoutes.push(path)};

  class FakeForm {
    before(element) { this.accountingSummary = element; }
    constructor(elements = {}) {
      this.isConnected = true;
      this.dataset = {};
      this.footer = { prepend: element => { this.saveStatus = element; } };
      this.querySelector = selector => selector === ".code-workspace>footer" ? this.footer : null;
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
    get elements() {
      const values = [...this._el.values()];
      values.namedItem = (n) => this._el.get(n) ?? null;
      return values;
    }
    setAttribute() {}
    removeAttribute() {}
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
    features: ["managed-config-read-v1"],
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
        path = path.replace(/\?view=status$/, "");
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

  const installForms = (ctx, forms = {}, lists = {}) => {
    globalThis.document = {
      querySelector: (sel) => forms[sel] ?? null,
      querySelectorAll: selector => lists[selector] || [],
      getElementById: () => null,
      createElement: () => {
        const attributes = new Map();
        return {
          className: "", type: "", dataset: {}, textContent: "",
          setAttribute: (name, value) => attributes.set(name, value),
          getAttribute: name => attributes.get(name) ?? null,
          removeAttribute: name => attributes.delete(name),
          remove() {}, append() {}, before() {}, addEventListener() {},
          parentElement: { classList: { contains: () => false }, append() {} },
        };
      },
    };
    return installConfigPages(ctx);
  };

  const drain = async () => {
    for (let i = 0; i < 300; i++) await new Promise((r) => setImmediate(r));
  };

  return {
    FakeForm, deferred, AGENT_ID, ENGINE, KEY, OLD_CONTENT, NEW_CONTENT, makeAgent, workspaceWithConfig, emptyWorkspace, buildContext, installForms, drain, replacedRoutes,
    restore() {
      if (previousHistory === undefined) delete globalThis.history;
      else globalThis.history = previousHistory;
      if (previousLocation === undefined) delete globalThis.location;
      else globalThis.location = previousLocation;
      if (previousSetTimeout === undefined) delete globalThis.setTimeout;
      else globalThis.setTimeout = previousSetTimeout;
      if (previousFormData === undefined) delete globalThis.FormData;
      else globalThis.FormData = previousFormData;
      if (previousDocument === undefined) delete globalThis.document;
      else globalThis.document = previousDocument;
    },
  };
}
