import { subStoreSelectionPayload, subStoreAddressChoices, groupSubStoreProfiles, filterSubStoreProfiles } from "../modules/substore-model.js";
import { savedNodeOrder } from "../modules/node-order.js";
import { geoRegionDetails } from "../modules/region-model.js";
import { regionFlag } from "./node-region.js";
import { bindSelectionSurface, node, button, actions, stack, section, table, badge, field, muted, list, pathID, query, notice } from "./ui.js";
import { date } from "./operations.js";

export async function substorePage(ctx) {
  let resource = await ctx.api("/substore-sync");
  const agents = ctx.can("agents.read") ? list(await ctx.api("/agents")) : [];
  const regions = new Map(agents.map(agent => [agent.id, geoRegionDetails(agent.labels?.region_code || agent.region_code)]));
  const regionLoads = new Map();
  let order = savedNodeOrder();
  try { order = (await ctx.api("/preferences/node-order")).ids; } catch (error) { if (error.name === "AbortError") throw error; }
  const root = node("div", { className: "a-stack" });
  let serial = 0, dirty = false;
  const writable = ctx.can("settings.manage");
  async function reload(targetID = resource.target_id) {
    const request = ++serial;
    const value = await ctx.api(query("/substore-sync", { target_id: targetID }));
    if (!ctx.current() || serial !== request) return;
    resource = value;
    dirty = false;
    ctx.setDirty(false);
    draw();
  }
  let syncDraft = () => {};
  function markDirty() { syncDraft(); }
  function profileRegion(id) {
    const host = node("span", { "data-substore-region": id }, regionFlag(regions.get(id)));
    if (!regions.get(id) && ctx.can("agents.read")) {
      if (!regionLoads.has(id)) regionLoads.set(id, ctx.api(`/agents/${pathID(id)}/region`).then(value => {
        if (ctx.current()) regions.set(id, geoRegionDetails(value.country_code));
      }).catch(() => {}));
      void regionLoads.get(id).then(() => { if (ctx.current()) host.replaceChildren(regionFlag(regions.get(id))); });
    }
    return host;
  }
  function targetForm(target = null) {
    if (dirty) { ctx.announce("请先保存同步选择。", "error"); return; }
    const form = ctx.form([
      field("同步组名称", "display_name", target?.display_name || "", { required: true, maxlength: 100 }),
      field("同步格式", "sync_format", target?.sync_format || "url", { options: [["url", "URL / 原生格式"], ["mihomo", "Mihomo YAML"]] }),
      field("同步模式", "sync_mode", target?.sync_mode || "incremental", { options: [["incremental", "增量更新，保留远端其他节点"], ["managed", "完全托管，仅保留所选节点"]] }),
      target && field("同时修改远端组名", "rename_remote", false, { type: "checkbox" }),
    ], "保存同步组", async (values, element) => {
      const saved = await ctx.api(target ? `/substore-sync/targets/${pathID(target.id)}` : "/substore-sync/targets", {
        method: target ? "PUT" : "POST", body: { ...values, rename_remote: Boolean(element.elements.rename_remote?.checked) },
      });
      box.close();
      ctx.announce("同步组已保存");
      await reload(saved.id);
    });
    const box = ctx.dialog(target ? "编辑同步组" : "新建同步组", form);
  }
  function draw() {
    const target = list(resource.targets).find(item => item.id === resource.target_id);
    const profiles = groupSubStoreProfiles(list(resource.profiles), agents, order).flatMap(group => group.profiles);
    let baseline = JSON.stringify(subStoreSelectionPayload(profiles));
    const unavailable = profile => !profile.available || Boolean(target?.sync_format === "mihomo" && profile.mihomo_error);
    const selectTarget = field("同步组", "target", resource.target_id || "", {
      options: [["", "选择同步组"], ...list(resource.targets).map(item => [item.id, item.display_name || item.subscription_name])],
    });
    selectTarget.querySelector("select").addEventListener("change", async event => {
      const control = event.target, next = control.value;
      control.value = resource.target_id || "";
      if (dirty && !await ctx.confirm("当前选择尚未保存，确定切换同步组？", "切换同步组")) {
        control.value = resource.target_id || "";
        return;
      }
      await ctx.mutate(control, async () => {
        selectionFields.disabled = true;
        try { await reload(next); } finally { selectionFields.disabled = false; }
      });
    });
    const tools = actions(writable && button("新建同步组", () => targetForm(), { tone: "primary" }),
      writable && target && button("组设置", () => targetForm(target)),
      writable && target && button("移除关联", async event => {
        const control = event.currentTarget;
        if (dirty) { ctx.announce("请先保存同步选择。", "error"); return; }
        if (!await ctx.confirm("移除面板中的同步关系？Sub-Store 远端组会保留。", "移除关联") || !ctx.current()) return;
        await ctx.mutate(control, async () => {
          await ctx.api(`/substore-sync/targets/${pathID(target.id)}`, { method: "DELETE" });
          await reload("");
        });
      }, { tone: "danger" }));
    const search = field("搜索节点", "search", "", { type: "search" });
    const agentFilter = field("节点", "agent", "", { options: [["", "全部节点"], ...[...new Map(profiles.map(profile => [profile.agent_id, profile.agent_name || profile.agent_id]))]] });
    const rows = node("div"), selectionFields = node("fieldset", { className: "a-form-fields" }, rows);
    bindSelectionSurface(rows, { selector: 'tbody tr', preserveControls: true, signal: ctx.signal });
    let save, sync;
    syncDraft = () => {
      dirty = JSON.stringify(subStoreSelectionPayload(profiles)) !== baseline; ctx.setDirty(dirty);
      if (save) save.disabled = !target || !dirty;
      if (sync) {
        const selected = profiles.filter(profile => profile.selected);
        sync.disabled = !target || !resource.settings?.configured || dirty || selected.some(unavailable) || !(selected.length || target?.last_synced_at);
      }
    };
    function drawRows() {
      const q = search.querySelector("input").value.toLowerCase();
      const visible = filterSubStoreProfiles(profiles, agentFilter.querySelector("select").value, q);
      const content = table(["选择", "节点", "内核 / 入站", "同步名称", "地址", "状态"], visible.map(profile => {
        const blocked = unavailable(profile);
        const selected = field("同步", "selected", profile.selected, { type: "checkbox", disabled: !writable || !target || blocked });
        selected.querySelector("input").addEventListener("change", event => {
          profile.selected = event.target.checked;
          name.querySelector("input").disabled = address.querySelector("select").disabled = !profile.selected;
          showParameters();
          markDirty();
        });
        const name = field("名称", "name", profile.custom_name || profile.default_name || "", { maxlength: 100, disabled: !writable || !target || blocked || !profile.selected });
        name.querySelector("input").addEventListener("input", event => { profile.custom_name = event.target.value; markDirty(); });
        const address = field("地址", "address", profile.address_mode || "auto", { options: subStoreAddressChoices(profile).map(item => [item.value, item.label]),
          disabled: !writable || !target || blocked || !profile.selected });
        address.querySelector("select").addEventListener("change", event => { profile.address_mode = event.target.value; markDirty(); });
        const nameView = node("div"), addressView = node("div");
        function showParameters() {
          const editing = writable && target && !blocked && profile.selected;
          nameView.replaceChildren(editing ? name : muted(profile.custom_name || profile.default_name || "—"));
          addressView.replaceChildren(editing ? address : muted(subStoreAddressChoices(profile).find(item => item.value === (profile.address_mode || "auto"))?.label || "自动选择"));
        }
        showParameters();
        return [selected, stack(actions(profileRegion(profile.agent_id), node("strong", {}, profile.agent_name || profile.agent_id)),
          profile.engine === "ss-rust" && muted(profile.custom_name || profile.default_name || profile.profile_tag)), stack(actions(badge(profile.engine)), muted(profile.profile_tag)),
          nameView, addressView, blocked ? stack(notice(profile.mihomo_error || profile.unavailable_reason || "源配置不可用", "warn"),
            writable && target && profile.selected && button("移除选择", () => { profile.selected = false; markDirty(); drawRows(); search.querySelector("input").focus({ preventScroll: true }); })) : badge("可同步", "ok")];
      }), { label: "同步节点", columnWidths: [80, 260, 180, 320, 240, 220], rowKey: (_, index) => {
        const profile = visible[index]; return JSON.stringify([profile.agent_id,profile.engine,profile.profile_tag,profile.config_id || ""]);
      } });
      content.classList.add("a-probe-list");
      content.querySelectorAll('tbody tr').forEach(row => {
        const input = row.querySelector('input[type=checkbox]');
        if (input && !input.disabled) row.tabIndex = 0;
      });
      rows.replaceChildren(content);
    }
    drawRows();
    search.querySelector("input").addEventListener("input", drawRows);
    agentFilter.querySelector("select").addEventListener("change", drawRows);
    save = writable && button("保存所选节点", event => void ctx.mutate(event.currentTarget, async () => {
      if (!target) return;
      selectionFields.disabled = true;
      try {
        const selections = subStoreSelectionPayload(profiles);
        await ctx.api("/substore-sync/selections", { method: "PUT", body: { target_id: target.id, selections } });
        if (!ctx.current()) return;
        baseline = JSON.stringify(selections); syncDraft();
        ctx.announce("同步选择已保存");
        await reload(target.id);
      } finally { selectionFields.disabled = false; }
    }), { disabled: !target });
    sync = writable && button("立即同步", async event => {
      const control = event.currentTarget;
      if (dirty) { ctx.announce("请先保存同步选择。", "error"); return; }
      if (target.sync_mode === "managed" && !await ctx.confirm("完全托管同步会让远端组仅保留当前所选节点，确定同步？", "立即同步")) return;
      if (!ctx.current()) return;
      await ctx.mutate(control, async () => {
        const result = await ctx.api("/substore-sync/run", { method: "POST", body: { target_id: target.id } });
        ctx.announce(`已同步 ${result.node_count} 个节点`);
        await reload(target.id);
      });
    }, { tone: "primary", disabled: !target || !resource.settings?.configured });
    if (save) save.addEventListener("mutationend", () => syncDraft());
    if (sync) sync.addEventListener("mutationend", () => syncDraft());
    syncDraft();
    root.replaceChildren(
      section("Sub-Store", node("div", { className: "a-card-body" }, stack(selectTarget,
        muted(resource.settings?.endpoint_hint || "尚未配置连接"),
        target && muted(target.last_synced_at ? `上次同步 · ${date(target.last_synced_at)}` : "尚未同步"),
        actions(writable && button("连接设置", () => {
          if (dirty) { ctx.announce("请先保存同步选择。", "error"); return; }
          const form = ctx.form([field("后端地址", "endpoint_url", "", { type: "password", autocomplete: "new-password",
            required: !resource.settings?.configured, hint: resource.settings?.configured ? "留空保留当前地址" : "填写包含后端路径口令的地址" })], "保存连接", async values => {
            await ctx.api("/substore-sync/settings", { method: "PUT", body: values });
            box.close();
            ctx.announce("连接设置已保存");
            await reload();
          });
          const box = ctx.dialog("Sub-Store 连接设置", form);
        }), writable && button("测试连接", event => void ctx.mutate(event.currentTarget, async () => {
          await ctx.api("/substore-sync/test", { method: "POST" });
          ctx.announce("Sub-Store 连接正常");
        })), writable && button("关联远端现有组", event => void ctx.mutate(event.currentTarget, async () => {
          if (dirty) { ctx.announce("请先保存同步选择。", "error"); return; }
          const remote = list(await ctx.api("/substore-sync/remote-targets")).filter(item => !item.imported || item.subscription_name === target?.subscription_name);
          const form = ctx.form([
            field("远端组", "subscription_name", "", { required: true, options: [["", "选择远端组"], ...remote.map(item => [item.subscription_name, `${item.subscription_name} · ${item.node_count} 个节点`])] }),
            field("面板名称", "display_name", target?.display_name || "", { required: true, maxlength: 100 }),
            field("同步格式", "sync_format", target?.sync_format || "url", { options: [["url", "URL / 原生格式"], ["mihomo", "Mihomo YAML"]] }),
          ], target ? "更换关联组" : "加入同步组", async values => {
            const saved = await ctx.api(target ? `/substore-sync/targets/${pathID(target.id)}/remote` : "/substore-sync/targets/import", { method: "POST", body: values });
            box.close();
            await reload(saved.id);
          });
          const box = ctx.dialog("关联远端现有组", form);
        }))))), tools),
      section("同步节点", stack(node("div", { className: "a-card-body" }, actions(search, agentFilter)), selectionFields,
        node("div", { className: "a-card-body" }, actions(save, sync)))));
  }
  draw();
  return root;
}
