import { installConfigPages } from "./modules/configs.js";
import { reconcileView } from "./modules/refresh.js";

const assert = (condition, message) => { if (!condition) throw new Error(message); };
const waitFor = async (condition, message) => {
  const deadline = performance.now() + 4000;
  while (performance.now() < deadline) {
    if (condition()) return;
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  throw new Error(message);
};
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};
const esc = value => String(value ?? "").replace(/[&<>"']/g, char =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);

async function fixture(engine, readOnly = false, actual) {
  const protocols = actual ? [actual.protocol] : engine === "xray" ? [
    { key: "vless", name: "VLESS Reality", uses_reality: true },
    { key: "vless-xhttp-reality", name: "XHTTP Reality", uses_reality: true, transport_config: true, transports: ["xhttp"] },
    { key: "vless-enc-tcp-reality-vision", name: "VLESS Encryption", uses_reality: true, uses_vless_encryption: true },
  ] : [{ key: "ss2022", name: "Shadowsocks 2022", ignores_username: true, methods: ["2022-blake3-aes-256-gcm"] }];
  protocols.forEach(protocol => Object.assign(protocol, { badge: protocol.key, transports: protocol.transports || ["raw"] }));
  const agent = { id: "node", name: "预设测试节点", status: "online", capabilities: [engine], runtime: { [engine]: { installed: true } } };
  const state = { route: "agent-config", data: { agentId: agent.id, engine,
    // Simulate returning from an installation with an out-of-date node cache.
    agents: [{ ...agent, runtime: { [engine]: { installed: false } } }], protocol: "unsupported-old-engine-protocol" } };
  const test = { calls: [], notifications: [], confirmed: true, saved: null, inbounds: [], taskState: "succeeded", fieldGate: null, saveGate: null };
  let serial = 0;
  const plan = protocol => actual ? { ...structuredClone(actual.plan), tag:`in-${++serial}`, port:21000+serial } : ({ protocol, tag: `in-${++serial}`, listen: "0.0.0.0", port: 21000 + serial,
    username: "test", credential: "123e4567-e89b-42d3-a456-426614174000", method: "2022-blake3-aes-256gcm",
    transport: protocol.includes("xhttp") ? "xhttp" : "raw", transport_path: "/test",
    reality_enabled: engine === "xray", reality_server_name: "www.amazon.com", reality_short_id: "1234567890abcdef",
    reality_public_key: "test-public", reality_private_key: "test-private", reality_min_client_ver: "0.0.0",
    vless_decryption: "test-decryption", vless_encryption: "test-encryption" });
  const api = async (path, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined;
    test.calls.push({ path, method: options.method || "GET", body });
    path = path.replace(/\?view=status$/, "");
    if (path.endsWith("/workspace")) {
      if (test.workspaceFailure) throw new Error("模拟页面刷新失败");
      return { agent, config: test.saved, inbounds: structuredClone(test.inbounds), protocols,
      catalog: { name: engine, format: engine === "mihomo" ? "YAML" : "JSON", fields: [
        { key: "log", label: "日志", kind: "object", scope: "global" },
        { key: "mode", label: "转发模式", kind: "string", scope: "override" },
      ], topic_groups: [] }, reality_presets: ["www.amazon.com"], present_fields: {} };
    }
    if (path.endsWith("/plans")) {
      if (test.planFailure) throw new Error("生成参数暂不可用");
      return { ...body.input, ...plan(body.protocol) };
    }
    if (path.includes("/revisions")) {
      if (test.historyGate) await test.historyGate;
      return [test.saved];
    }
    if (options.method === "POST" && path.endsWith("/source")) {
      assert(body.version === test.saved.version, "source submitted a stale revision");
      if (test.taskFailure) throw Object.assign(new Error("模拟任务提交失败"), {status:400});
      test.saved = { ...test.saved, ...body, version: test.saved.version + 1 };
      return { config: test.saved, task: { id: `task-${test.saved.version}`, status: "pending" } };
    }
    if (options.method === "POST" && (path.endsWith("/server-inbounds") || path.includes("/fields/"))) {
      if (test.saveGate) await test.saveGate;
      if (test.saveFailure) throw Object.assign(new Error("模拟保存失败，请重试"), {status:400});
      assert(body.expected_version === (test.saved?.version || 0), "submitted a stale config revision");
      if (path.endsWith("/server-inbounds")) {
        if (body.operation === "add") {
          assert(body.original_tag === "", "adding a port carried the old inbound identity");
          test.inbounds.push(body.input);
        } else {
          test.inbounds = test.inbounds.filter(input => input.tag !== body.original_tag);
          if (body.operation !== "delete") test.inbounds.push(body.input);
        }
      }
      test.saved = { id: "cfg", name: "preset", engine, version: (test.saved?.version || 0) + 1, content: "{}" };
      return { config: test.saved, task: { id: `task-${test.saved.version}`, status: "pending" } };
    }
    if (path.includes("/fields/")) {
      if (test.fieldGate) await test.fieldGate;
      if (test.fieldFailure) throw new Error("字段读取失败");
      return { present: true, fragment: "{}" };
    }
    if (path === "/tasks" && options.method === "POST") {
      if (test.taskFailure) throw new Error("模拟任务提交失败");
      return { id: `task-${test.saved.version}`, status: "pending" };
    }
    if (path.startsWith("/tasks/")) return { id: path.split("/").at(-1), status: test.taskState, error: test.taskState === "failed" ? "内核校验失败" : "" };
    if (options.method === "PUT") {
      assert(body.version === test.saved.version, "source submitted a stale revision");
      test.saved = { ...test.saved, ...body, version: test.saved.version + 1 };
      return test.saved;
    }
    throw new Error(`unexpected API ${path}`);
  };
  document.body.className = "app-body page-agent-config";
  document.body.innerHTML = '<main class="workspace-main"></main>';
  const pages = installConfigPages({ state, api, optionalAPI: api, engines: [engine], esc,
    can: permission => !readOnly || permission.endsWith(".read"), engineName: String, date: String, ago: String,
    confirmAction: async () => { test.confirmCount = (test.confirmCount || 0) + 1; return test.confirmed; },
    notify: message => test.notifications.push(message), bindCodeEditors: () => {},
    shell: (html, _title, { viewKey }) => {
      const template = document.createElement("template");
      template.innerHTML = `<main class="workspace-main" data-refresh-key="${viewKey}">${html}</main>`;
      const previous = document.querySelector("main");
      if (previous.dataset.refreshKey === viewKey) reconcileView(previous, template.content.firstElementChild);
      else previous.replaceWith(template.content.firstElementChild);
    },
  });
  await pages.agentConfig();
  test.pages = pages;
  test.state = state;
  window.presetFixture = test;
  return test;
}

export async function testPresetsRuntime(preview = false) {
  if (preview) {
    await fixture(new URLSearchParams(location.search).get("engine") || "xray");
    return;
  }
  const form = () => document.querySelector("#server-plan-form");
  const control = name => form().elements.namedItem(name);
  const button = intent => form().querySelector(`[data-plan-intent="${intent}"]`);
  const edit = (name, value) => { control(name).value = value; control(name).dispatchEvent(new Event("input", { bubbles: true })); };
  const submissions = test => test.calls.filter(call => call.method === "POST" && call.path.endsWith("/server-inbounds"));
  const errors = [];
  const onError = event => errors.push(String(event.reason || event.message));
  window.addEventListener("unhandledrejection", onError);
  window.addEventListener("error", onError);
  for (const engine of ["xray", "mihomo", "sing-box", "ss-rust"]) {
    const test = await fixture(engine);
    assert(!button("deploy").disabled, `${engine}: cached installation state disabled deployment`);
    if (engine === "xray") {
      edit("reality_min_client_ver", "bad-version");
      button("deploy").click();
      assert(!document.querySelector("#security").hidden && document.activeElement === control("reality_min_client_ver"), "invalid hidden Xray field was not revealed and focused");
      assert(submissions(test).length === 0, "invalid Xray form submitted a request");
      edit("reality_min_client_ver", "0.0.0");
    }
    const save = deferred();
    const fields = deferred();
    test.saveGate = save.promise;
    test.fieldGate = fields.promise;
    const started = performance.now();
    const submittedButton = button("deploy");
    submittedButton.click();
    submittedButton.click();
    assert(form().getAttribute("aria-busy") === "true" && submittedButton.textContent.includes("正在保存"), `${engine}: no immediate submission feedback`);
    assert(submissions(test).length === 1, `${engine}: duplicate submission`);
    save.resolve();
    await waitFor(() => control("operation").value === "modify" && !button("deploy").disabled, `${engine}: saved form did not recover while fields were slow`);
    assert(performance.now() - started < 1000, `${engine}: saving waited for auxiliary fields`);
    assert(!test.calls.some(call => call.path.includes("/revisions")), "collapsed history blocked the primary form");
    fields.resolve();
    await waitFor(() => document.querySelector("#field-form"), "field editor did not finish loading");
    const regenerated = form().querySelectorAll("[data-regenerate]").length;
    await test.pages.agentConfig();
    await waitFor(() => document.querySelector("#field-form"), "reload did not finish");
    assert(form().querySelectorAll("[data-regenerate]").length === regenerated, "render duplicated generator buttons");
    const originalTag = control("tag").value;
    edit("operation", "add"); edit("tag", "second"); edit("port", "22001");
    button("validate").click();
    await waitFor(() => test.saved.version === 2 && control("operation").value === "modify", "add did not reset to modify");
    assert(test.inbounds.some(input => input.tag === originalTag), "adding removed the original inbound");
    test.saveFailure = true;
    button("validate").click();
    await waitFor(() => !button("validate").disabled, "failed save left buttons disabled");
    assert(document.querySelector("[data-preset-status]").textContent.includes("模拟保存失败"), "save failure was invisible");
    test.saveFailure = false;
    // Deletion must use the saved identity even when a hidden draft is invalid.
    edit("operation", "delete"); edit("credential", "");
    test.confirmed = false;
    const before = submissions(test).length;
    button("validate").click();
    await waitFor(() => !button("validate").disabled, "cancel did not release submission state");
    assert(submissions(test).length === before, "cancelled deletion wrote data");
    test.confirmed = true;
    button("validate").click();
    await waitFor(() => test.saved.version === 3 && control("operation").value === "add", "deletion did not return to a fresh draft");
    assert(!test.inbounds.some(input => input.tag === "second"), "deleted inbound is still present");
    assert(test.confirmCount === 2, "deletion confirmation count is wrong");
    await waitFor(() => document.querySelector("#field-form"), "field editor unavailable after delete");
    document.querySelector('#field-form [data-field-intent="validate"]').click();
    await waitFor(() => test.saved.version === 4 && document.querySelector('#field-form [data-field-intent="validate"]')?.disabled === false, "field save failed");
    document.querySelector('#source-config-form [data-source-intent="validate"]').click();
    await waitFor(() => test.saved.version === 5 && document.querySelector('#source-config-form [data-source-intent="validate"]')?.disabled === false, "source save failed");
    test.taskFailure = true;
    document.querySelector('#source-config-form [data-source-intent="deploy"]').click();
    await waitFor(() => document.querySelector("[data-preset-status]").textContent.includes("模拟任务提交失败"), "atomic source failure was not reported");
    assert(test.saved.version === 5 && !button("deploy").disabled, "failed source submission changed config or blocked retry");
    test.taskFailure = false;
    document.querySelector('#source-config-form [data-source-intent="deploy"]').click();
    await waitFor(() => test.saved.version === 6 && document.querySelector('#source-config-form [data-source-intent="deploy"]')?.disabled === false, "source save retry used stale version");
    test.workspaceFailure = true;
    button("deploy").click();
    await waitFor(() => document.querySelector("[data-preset-status] button"), "failed post-save refresh has no recovery action");
    assert(test.saved.version === 7 && button("deploy").disabled && !form().inert, "failed refresh left a frozen or stale editable form");
    test.workspaceFailure = false;
    document.querySelector("[data-preset-status] button").click();
    await waitFor(() => !button("deploy").disabled, "reload did not recover saved form");
    if (engine === "xray") {
      for (const protocol of ["vless-xhttp-reality", "vless-enc-tcp-reality-vision"]) {
        document.querySelector(`[data-protocol="${protocol}"]`).click();
        await waitFor(() => document.querySelector(`[data-protocol="${protocol}"].active`), "Xray protocol did not switch");
        assert(form().checkValidity(), `${protocol}: generated form is invalid`);
        button("validate").click();
        await waitFor(() => control("operation").value === "modify" && !button("validate").disabled, `${protocol}: save did not complete`);
        assert(submissions(test).at(-1).body.input.protocol === protocol, "wrong protocol payload");
      }
      test.fieldFailure = true;
      await test.pages.agentConfig();
      await waitFor(() => document.querySelector("#advanced").textContent.includes("字段读取失败"), "field error missing");
      assert(!button("deploy").disabled, "field failure disabled primary save");
      test.state.route = "tasks";
    }
  }
  const catalogResponse = await fetch("/assets/preset-plans.json");
  assert(catalogResponse.ok, "real preset test catalog failed to load");
  const catalog = await catalogResponse.json();
  const covered = new Set();
  for (const actual of catalog) {
    const test = await fixture(actual.engine, false, actual);
    covered.add(actual.engine);
    const invalid = [...form().elements].filter(input => input.willValidate && !input.validity.valid).map(input => input.name);
    assert(invalid.length === 0, `${actual.engine}/${actual.protocol.key}: generated defaults blocked by ${invalid.join(", ")}`);
    button("validate").click();
    await waitFor(() => test.saved?.version === 1 && !button("validate").disabled, `${actual.engine}/${actual.protocol.key}: saving real preset defaults stalled`);
    assert(submissions(test)[0].body.input.protocol === actual.protocol.key, "real preset submitted a different protocol");
  }
  assert(covered.size === 4 && catalog.length >= 33, "not all engine presets were covered");
  await fixture("xray", true);
  assert(button("deploy").disabled && form().querySelector("[data-regenerate]").disabled, "read-only user can submit mutations");
  assert(errors.length === 0, `uncaught browser errors: ${errors.join("; ")}`);
  window.removeEventListener("unhandledrejection", onError);
  window.removeEventListener("error", onError);
}
