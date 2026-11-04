import { subStoreSelectionPayload, subStoreAddressChoices } from "../modules/substore-model.js";
import { node, button, actions, stack, section, table, badge, field, muted, list, pathID, query, notice } from "./ui.js";
import { date } from "./operations.js";

export async function substorePage(ctx) {
  let resource = await ctx.api("/substore-sync");
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
  function markDirty() { dirty = true; ctx.setDirty(true); }
  function targetForm(target = null) {
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
    const profiles = list(resource.profiles);
    const selectTarget = field("同步组", "target", resource.target_id || "", {
      options: [["", "选择同步组"], ...list(resource.targets).map(item => [item.id, item.display_name || item.subscription_name])],
    });
    selectTarget.querySelector("select").addEventListener("change", async event => {
      const control = event.target, next = control.value;
      if (dirty && !await ctx.confirm("当前选择尚未保存，确定切换同步组？", "切换同步组")) {
        control.value = resource.target_id || "";
        return;
      }
      await ctx.mutate(control, () => reload(next));
    });
    const tools = actions(writable && button("新建同步组", () => targetForm(), { tone: "primary" }),
      writable && target && button("组设置", () => targetForm(target)),
      writable && target && button("移除关联", async event => {
        const control = event.currentTarget;
        if (!await ctx.confirm("移除面板中的同步关系？Sub-Store 远端组会保留。", "移除关联") || !ctx.current()) return;
        await ctx.mutate(control, async () => {
          await ctx.api(`/substore-sync/targets/${pathID(target.id)}`, { method: "DELETE" });
          await reload("");
        });
      }, { tone: "danger" }));
    const search = field("搜索节点", "search", "", { type: "search" });
    const rows = node("div");
    function drawRows() {
      const q = search.querySelector("input").value.toLowerCase();
      rows.replaceChildren(table(["选择", "节点", "内核 / 入站", "同步名称", "地址", "状态"], profiles.filter(profile =>
        [profile.agent_name, profile.default_name, profile.profile_tag, profile.protocol].join(" ").toLowerCase().includes(q)).map(profile => {
        const unavailable = !profile.available || (target?.sync_format === "mihomo" && profile.mihomo_error);
        const selected = field("同步", "selected", profile.selected, { type: "checkbox", disabled: !writable || !target || Boolean(unavailable) });
        selected.querySelector("input").addEventListener("change", event => { profile.selected = event.target.checked; markDirty(); });
        const name = field("名称", "name", profile.custom_name || profile.default_name || "", { maxlength: 100, disabled: !writable || !target || Boolean(unavailable) });
        name.querySelector("input").addEventListener("input", event => { profile.custom_name = event.target.value; markDirty(); });
        const address = field("地址", "address", profile.address_mode || "auto", { options: subStoreAddressChoices(profile).map(item => [item.value, item.label]),
          disabled: !writable || !target || Boolean(unavailable) });
        address.querySelector("select").addEventListener("change", event => { profile.address_mode = event.target.value; markDirty(); });
        return [selected, profile.agent_name || profile.agent_id, stack(badge(profile.engine), muted(profile.profile_tag)),
          name, address, unavailable ? notice(profile.mihomo_error || profile.unavailable_reason || "源配置不可用", "warn") : badge("可同步", "ok")];
      }), { label: "同步节点" }));
    }
    drawRows();
    search.querySelector("input").addEventListener("input", drawRows);
    const save = writable && button("保存所选节点", event => void ctx.mutate(event.currentTarget, async () => {
      if (!target) return;
      await ctx.api("/substore-sync/selections", { method: "PUT", body: { target_id: target.id, selections: subStoreSelectionPayload(profiles) } });
      dirty = false;
      ctx.setDirty(false);
      ctx.announce("同步选择已保存");
    }), { disabled: !target });
    const sync = writable && button("立即同步", async event => {
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
    root.replaceChildren(
      section("Sub-Store", node("div", { className: "a-card-body" }, stack(selectTarget,
        muted(resource.settings?.endpoint_hint || "尚未配置连接"),
        target && muted(target.last_synced_at ? `上次同步 · ${date(target.last_synced_at)}` : "尚未同步"),
        actions(writable && button("连接设置", () => {
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
      section("同步节点", stack(node("div", { className: "a-card-body" }, search), rows,
        node("div", { className: "a-card-body" }, actions(save, sync)))));
  }
  draw();
  return root;
}
