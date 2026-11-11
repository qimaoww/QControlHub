import { bindPreferenceFields } from "./preferences.js";
import { readRegion, loadingTable } from "./loading.js";
import { editUserAllocations } from "./user-allocations.js";
import { userPermissions, defaultPermissions, userActionRestriction } from "../modules/user-model.js";
import { filterbar, node, button, actions, stack, grid, section, table, badge, field, muted, list, pathID, notice } from "./ui.js";
import { date } from "./operations.js";
import { patch } from "./live-dom.js";

export async function usersPage(ctx) {
  let users = [], loaded = false;
  const results = node("div", {}, loadingTable(["用户名", "显示名称", "角色", "状态", "最近登录", "操作"], "用户"));
  const query = field("搜索用户", "query", "", { type: "search", placeholder: "用户名或显示名称" });
  let refreshSequence = 0;
  async function refresh() { const sequence = ++refreshSequence, result = list(await ctx.api("/users")); if (ctx.current() && sequence === refreshSequence) { users = result; loaded = true; draw(); } }
  const pendingActions = new Set();
  async function accountAction(user, control, run) {
    if (!ctx.current() || control.disabled || pendingActions.has(user.id)) return;
    pendingActions.add(user.id); control.disabled = true;
    try { await run(); }
    catch (error) { if (ctx.current() && error.name !== "AbortError") ctx.announce(error.message, "error"); }
    finally { pendingActions.delete(user.id); if (ctx.current()) draw(); }
  }
  function edit(user = null) {
    const checks = userPermissions.map(([permission, label]) => field(label, `permission:${permission}`,
      user ? user.permissions?.includes(permission) : defaultPermissions.includes(permission), { type: "checkbox" }));
    const restriction = user?.role === "admin" ? userActionRestriction(user, ctx.session, "demote") : "";
    const role = field("角色", "role", user?.role || "user", { options: [["user", "普通用户"], ["admin", "管理员"]], disabled: Boolean(restriction), hint: restriction });
    const permissionSection = node("details", { className: "a-disclosure" }, node("summary", {}, "账号权限"), node("div", { className: "a-disclosure-body a-grid" }, checks));
    const roleInput = role.querySelector("select");
    const sync = () => { ctx.visible(permissionSection, roleInput.value !== "admin"); };
    roleInput.addEventListener("change", sync);
    sync();
    const form = ctx.form([
      grid(field("用户名", "username", user?.username || "", { required: true, readonly: Boolean(user), maxlength: 64, pattern: "[A-Za-z0-9._\\-]+" }),
        field("显示名称", "display_name", user?.display_name || "", { maxlength: 100 })),
      role, field("密码", "password", "", { type: "password", autocomplete: "new-password", required: !user, minlength: 12, maxlength: 72,
        hint: user ? "留空保持当前密码" : "设置登录密码" }),
      permissionSection,
    ], user ? "保存用户" : "创建用户", async (values, element) => {
      const selectedRole = restriction ? user.role : values.role;
      if (user?.role === "admin" && selectedRole !== "admin") {
        await refresh();
        if (!ctx.current()) return;
        const latest = users.find(item => item.id === user.id);
        if (!latest) throw new Error('用户已不存在');
        const reason = userActionRestriction(latest, ctx.session, "demote");
        if (reason) throw new Error(reason);
      }
      const payload = {
        display_name: values.display_name, role: selectedRole, password: values.password,
        permissions: selectedRole === "admin" ? [] : userPermissions.filter(([permission]) => element.elements.namedItem(`permission:${permission}`).checked).map(([permission]) => permission),
        ...(!user ? { username: values.username, agent_isolation: selectedRole !== "admin" } : {}),
      };
      await ctx.api(user ? `/users/${pathID(user.id)}` : "/users", { method: user ? "PUT" : "POST", body: payload });
      if (!ctx.current()) return;
      ctx.setDirty(false);
      box.close();
      ctx.announce(user ? "用户已保存" : "用户已创建");
      await refresh();
    });
    form.addEventListener("input", () => ctx.setDirty(true));
    let changed = false;
    form.addEventListener("input", () => { changed = true; });
    const box = ctx.dialog(user ? `编辑 ${user.username}` : "创建用户", form, { wide: true });
    box.onRequestClose = async () => {
      if (changed && !await ctx.confirm("放弃未保存的用户更改？", "放弃更改")) return;
      ctx.setDirty(false);
      box.close();
    };
  }
  function draw() {
    if (!loaded) return;
    const text = query.querySelector("input").value.toLowerCase();
    const visible = users.filter(user =>
      [user.username, user.display_name].join(" ").toLowerCase().includes(text));
    const fresh = table(["用户名", "显示名称", "角色", "状态", "最近登录", "操作"], visible.map(user => Object.assign([
      user.username, user.display_name || "—", user.role === "admin" ? "管理员" : "普通用户",
      badge(user.disabled ? "已停用" : "正常", user.disabled ? "warn" : "ok"), date(user.last_login_at),
      actions(button("编辑", () => edit(user), { disabled: pendingActions.has(user.id) }),
        user.role !== "admin" && button("共享资源", () => editUserAllocations(ctx, user), { disabled: pendingActions.has(user.id) }),
        button(user.disabled ? "启用" : "停用", event => void accountAction(user, event.currentTarget, async () => {
          const reason = !user.disabled && userActionRestriction(user, ctx.session, "disable");
          if (reason) throw new Error(reason);
          if (!await ctx.confirm(`确定${user.disabled ? "启用" : "停用"}账号 ${user.username}？`, user.disabled ? "启用用户" : "停用用户") || !ctx.current()) return;
          await ctx.mutate(null, async () => {
            await refresh();
            if (!ctx.current()) return;
            const latest = users.find(item => item.id === user.id);
            if (!latest || latest.disabled !== user.disabled) return;
            const reason = !latest.disabled && userActionRestriction(latest, ctx.session, 'disable');
            if (reason) throw new Error(reason);
            try { await ctx.api(`/users/${pathID(user.id)}`, { method: "PUT", body: { disabled: !latest.disabled } }); }
            catch (error) { try { await refresh(); } catch {} throw error; }
            if (ctx.current()) await refresh();
          });
        }), { disabled: pendingActions.has(user.id) || !user.disabled && Boolean(userActionRestriction(user, ctx.session, "disable")), title: !user.disabled ? userActionRestriction(user, ctx.session, "disable") : "" }),
        button("永久删除", event => void accountAction(user, event.currentTarget, async () => {
          const reason = userActionRestriction(user, ctx.session, "delete");
          if (reason) throw new Error(reason);
          if (!await ctx.confirm(`永久删除 ${user.username}？账号与私有数据将删除，自有节点和主机资源转入管理员管理。`, "永久删除用户") || !ctx.current()) return;
          if (!await ctx.confirm("确认永久删除？此操作无法恢复。", "确认永久删除") || !ctx.current()) return;
          await ctx.mutate(null, async () => {
            await refresh();
            if (!ctx.current()) return;
            const latest = users.find(item => item.id === user.id);
            if (!latest) return;
            const reason = userActionRestriction(latest, ctx.session, 'delete');
            if (reason) throw new Error(reason);
            try { await ctx.api(`/users/${pathID(user.id)}/purge`, { method: "POST" }); }
            catch (error) { try { await refresh(); } catch {} throw error; }
            if (!ctx.current()) return;
            ctx.state.data.userAllocationDrafts?.delete(user.id);
            ctx.state.data.userAllocationPending?.delete(user.id);
            ctx.announce("用户已永久删除");
            await refresh();
          });
        }), { tone: "danger", disabled: pendingActions.has(user.id) || Boolean(userActionRestriction(user, ctx.session, "delete")), title: userActionRestriction(user, ctx.session, "delete") })),
    ], { id: user.id })), { label: "用户", rowKey: row => row.id });
    if (results.firstChild) patch(results.firstChild, fresh);
    else results.append(fresh);
  }
  const loading = readRegion(ctx, '用户', signal => ctx.api('/users', { signal }), value => { users = list(value); loaded = true; draw(); }, { view: results, clear() { users = []; loaded = true; draw(); } });
  void loading.load();
  query.querySelector("input").addEventListener("input", draw);
  ctx.pageActions(button("创建用户", () => edit(), { tone: "primary" }));
  ctx.onRefresh(loading.load);
  bindPreferenceFields(ctx, "users", [query], { restore: draw });
  return stack(filterbar(query), loading.element,
    section(null, results));
}
