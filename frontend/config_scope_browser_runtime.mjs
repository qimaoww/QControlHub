import { installConfigPages } from "./modules/configs.js";
import { installSubStoreSync, subStoreSelectionPayload } from "./modules/substore-sync.js";

const assert = (value, message) => { if (!value) throw new Error(message); };
const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
})[char]);
const waitFor = async (condition, message) => {
  const deadline = performance.now() + 3000;
  while (!condition()) {
    if (performance.now() > deadline) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};

async function configFixture({ writable = true, executable = false, installed = true, saved = true, owned = false } = {}) {
  const content = "mixed-port: 21001\n# alice-private-workspace\n";
  const agent = { id: "shared", name: "共享主机", status: "online", os: "linux", arch: "amd64",
    can_manage: owned,
    capabilities: ["mihomo"], features: ["managed-config-read-v1"],
    runtime: { mihomo: { installed, existing_config_available: true } } };
  const state = { route: "live-config", navigationEpoch: 1, session: { role: "user", user_id: "alice" },
    data: { liveAgent: agent.id, liveEngine: "mihomo", liveSources: {
      "shared|mihomo": { content: "mixed-port: 21002\n# other-user-runtime-secret\n" },
    } } };
  let workspace = saved ? { id: "cfg_alice", version: 1, name: "Alice", engine: "mihomo", content } : null;
  const records = { calls: [], writes: [], tasks: [], notifications: [] };
  const archives = [1, 2].map(index => ({ id: `archive_${index}`, version: 1, name: `方案 ${index}`,
    engine: "mihomo", content: `mixed-port: ${22000 + index}\n` }));
  const permissions = new Set(["agents.read", "agent-config.read", "configs.read", ...(writable ? ["agent-config.write", "configs.write"] : []), ...(executable ? ["tasks.execute"] : [])]);
  const api = async (path, options = {}) => {
    records.calls.push(path);
    if (path === "/agents") return [agent];
    if (path.endsWith("/workspace")) return { config: workspace };
    if (path === "/configs") return archives;
    if (path.includes("/revisions")) return [];
    if (options.method === "PUT") {
      const input = JSON.parse(options.body);
      records.writes.push(input);
      workspace = { ...input, id: "cfg_alice", version: (workspace?.version || 0) + 1 };
      return workspace;
    }
    throw new Error(`unexpected configuration API: ${path}`);
  };
  const pages = installConfigPages({ state, api, optionalAPI: api, engines: ["mihomo"],
    can: permission => permissions.has(permission), esc, engineName: value => value, conciseVersion: () => "test",
    date: String, ago: String, bytes: String, confirmAction: async () => true,
    notify: message => records.notifications.push(message), bindCodeEditors: () => {},
    shell: markup => { document.body.innerHTML = markup; },
    submitTask: async task => { records.tasks.push(task); return { id: "fixture-task" }; },
  });
  await pages.liveConfig();
  return { state, pages, records, archives };
}

export async function testConfigScopeRuntime(preview = false) {
  if (preview) {
    await configFixture();
    return;
  }
  for (const options of [{}, { installed: false, saved: false }]) {
    const { records } = await configFixture(options);
    const editor = document.querySelector("[data-code-input]");
    assert(editor && !editor.readOnly, "private workspace must be editable without task permission or an installed core");
    assert(!editor.value.includes("other-user-runtime-secret"), "private editor inherited the shared host snapshot");
    assert(!document.querySelector("[data-read-current], [data-auto-read-current], [data-live-source]"), "private editor offered shared-host configuration reads");
    assert(document.querySelector('[data-live-intent="save"]'), "save-only users cannot save their configuration");
    assert(!document.querySelector('[data-live-intent="deploy"], [data-live-intent="validate"]'), "save-only user was offered task execution");
    editor.value = "mixed-port: 23001\n# saved-private-draft\n";
    document.querySelector('[data-live-intent="save"]').click();
    await waitFor(() => records.notifications.includes("个人配置已保存"), "personal save did not complete");
    assert(records.writes.length === 1 && records.tasks.length === 0, "save-only operation submitted a task");
    assert(records.writes[0].content.includes("saved-private-draft"), "personal save lost edited content");
  }
  await configFixture({ executable: true });
  assert(document.querySelector('[data-live-intent="save"]') && document.querySelector('[data-live-intent="deploy"]'), "authorized user must have separate save and deploy controls");
  assert(document.body.textContent.includes("不能覆盖其他用户"), "shared-host deployment boundary is not explained");
  const owned = await configFixture({ owned: true });
  assert(document.querySelector('[data-live-source="managed"]') && document.querySelector('[data-live-source="import"]'), "owner cannot explicitly read/import its host configuration");
  assert(!owned.records.calls.includes("/tasks") && !owned.records.tasks.length, "owner's personal editor auto-read a host snapshot");
  await configFixture({ writable: false });
  assert(document.querySelector("[data-code-input]").readOnly && !document.querySelector("[data-live-intent]"), "read-only workspace is writable");

  const { state, pages, records } = await configFixture();
  state.route = "archive-config";
  state.data.archiveConfigId = "foreign-stale-selection";
  await pages.archiveConfigs();
  assert(state.data.archiveConfigId === "archive_1", "stale foreign archive selection was retained");
  assert(!records.calls.includes("/templates"), "archive reader requested an unauthorized template list");
  state.data.archiveConfigId = "archive_2";
  await pages.archiveConfigs();
  assert(document.querySelector("[data-code-input]").value.includes("22002"), "second personal configuration cannot be selected");
}

async function subStoreFixture(manage = true) {
  const state = { route: "substore-sync", navigationEpoch: 1, data: {}, session: { role: "user", user_id: "alice" } };
  const targets = [
    { id: "url-group", display_name: "我的 URL 组", subscription_name: "Alice URL", sync_format: "url", sync_mode: "incremental" },
    { id: "mihomo-group", display_name: "我的 Mihomo 组", subscription_name: "Alice Mihomo", sync_format: "mihomo", sync_mode: "incremental" },
  ];
  const active = { agent_id: "shared", agent_name: "共享主机", agent_status: "online", config_id: "cfg_current",
    engine: "mihomo", profile_tag: "same-tag", protocol: "Shadowsocks 2022", port: 21001, default_name: "我的节点",
    available: true, addresses: [{ family: "ipv4", address: "198.51.100.10" }, { family: "ipv6", address: "2001:db8::10" }] };
  const stale = { ...active, config_id: "cfg_previous", default_name: "失效的旧配置", available: false, addresses: [] };
  const selections = new Map([["url-group", [{ ...stale, custom_name: stale.default_name, selected: true }]], ["mihomo-group", []]]);
  const calls = [];
  const api = async (path, options = {}) => {
    const input = options.body ? JSON.parse(options.body) : null;
    calls.push({ path, method: options.method || "GET", input });
    if (path === "/substore-sync/remote-targets") return [];
    if (path === "/substore-sync/selections") {
      selections.set(input.target_id, input.selections);
      return input.selections;
    }
    if (path.startsWith("/substore-sync/targets")) {
      const target = targets.find(item => path.endsWith(`/${item.id}`));
      if (target) {
        Object.assign(target, input);
        return { ...target };
      }
      const created = { ...input, id: `group-${targets.length}`, subscription_name: input.display_name };
      targets.push(created);
      selections.set(created.id, []);
      return created;
    }
    if (path.startsWith("/substore-sync")) {
      const id = new URL(path, location.origin).searchParams.get("target_id") || targets[0].id;
      const selected = selections.get(id) || [];
      const profiles = [active, ...(selected.some(item => item.config_id === stale.config_id) ? [stale] : [])].map(profile => {
        const selection = selected.find(item => item.config_id === profile.config_id);
        return { ...profile, ...selection, selected: Boolean(selection) };
      });
      return structuredClone({ settings: { configured: true, endpoint_hint: "https://substore.example/••••••" },
        targets, target_id: id, profiles, selections: subStoreSelectionPayload(profiles) });
    }
    throw new Error(`unexpected Sub-Store API ${path}`);
  };
  const render = installSubStoreSync({ state, api, can: permission => permission === "settings.manage" && manage,
    esc, engineName: value => value, notify: () => {},
    shell: markup => { document.body.innerHTML = markup; },
  });
  await render();
  return { state, render, targets, calls, active };
}

export async function testSubStoreScopeRuntime(preview = false) {
  const fixture = await subStoreFixture();
  if (preview) {
    document.querySelector("[data-substore-target-edit]").click();
    return;
  }
  document.querySelector("[data-substore-remove]").click();
  await waitFor(() => !document.querySelector("[data-substore-remove]"), "stale selection was not removed");
  document.querySelector("[data-substore-add]").click();
  await waitFor(() => document.querySelector("[data-substore-parameters-form]"), "current configuration selection did not render");
  let form = document.querySelector("[data-substore-parameters-form]");
  form.elements.custom_name.value = "我的双栈节点";
  form.elements.address_mode.value = "both";
  form.requestSubmit();
  await waitFor(() => fixture.calls.some(call => call.input?.selections?.[0]?.address_mode === "both"), "address mode was not saved");
  const selected = fixture.calls.filter(call => call.path === "/substore-sync/selections").at(-1).input.selections;
  assert(selected.length === 1 && selected[0].config_id === "cfg_current", "same-tag old/new configurations shared a selector key");
  await fixture.render();
  document.querySelector("[data-substore-target-edit]").click();
  form = document.querySelector("[data-substore-target-form]");
  const dialog = document.querySelector("[data-substore-target-dialog]");
  await Promise.all(dialog.getAnimations().map(animation => animation.finished));
  const body = dialog.querySelector(".traffic-edit-body");
  assert(body.scrollWidth <= body.clientWidth + 1, "sync group options overflow the dialog");
  const footer = form.querySelector("footer").getBoundingClientRect();
  assert(footer.bottom <= innerHeight && footer.bottom <= dialog.getBoundingClientRect().bottom, "sync group save controls are clipped");
  if (innerWidth <= 820) {
    const options = [...form.querySelectorAll('[name="sync_format"]')].map(input => input.closest("label").getBoundingClientRect());
    assert(options[1].top >= options[0].bottom, "narrow sync format options must stack");
  }
  assert(form.querySelector('[name="sync_format"][value="url"]').checked, "URL format was not restored");
  form.querySelector('[name="sync_format"][value="mihomo"]').click();
  form.requestSubmit();
  await waitFor(() => fixture.targets[0].sync_format === "mihomo" && !document.querySelector("[data-substore-target-dialog]").open, "Mihomo format did not persist");
  document.querySelector('[data-substore-target="mihomo-group"]').click();
  await waitFor(() => fixture.state.data.subStoreSync.target_id === "mihomo-group", "second sync group did not load");
  document.querySelector("[data-substore-target-edit]").click();
  form = document.querySelector("[data-substore-target-form]");
  assert(form.querySelector('[name="sync_format"][value="mihomo"]').checked, "per-group format was not restored");
  form.querySelector('[name="sync_format"][value="url"]').click();
  form.requestSubmit();
  await waitFor(() => fixture.targets[1].sync_format === "url" && !document.querySelector("[data-substore-target-dialog]").open, "URL format did not persist");
  assert(fixture.targets[0].sync_format === "mihomo", "changing a group changed another group's format");
  document.querySelector('[data-substore-target="url-group"]').click();
  await waitFor(() => fixture.state.data.subStoreSync.target_id === "url-group", "first group did not reload");
  fixture.active.mihomo_error = "Mihomo 不支持此安全选项，请选择 URL 格式";
  await fixture.render();
  assert(document.querySelector("[data-substore-select]").disabled && document.querySelector("[data-substore-run]").disabled, "unsupported Mihomo security options remained syncable");
  assert(document.body.textContent.includes("请选择 URL"), "unsupported format reason is hidden");
  const readonly = await subStoreFixture(false);
  assert(!document.querySelector("[data-substore-target-edit], [data-substore-add], [data-substore-remove], [data-substore-parameters-form]"), "read-only user can edit sync groups");
  assert([...document.querySelectorAll("[data-substore-select]")].every(input => input.disabled), "read-only selectors remain enabled");
  assert(readonly.calls.every(call => call.method === "GET"), "read-only rendering wrote sync settings");
}
