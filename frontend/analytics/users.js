import { userPermissions, defaultPermissions, userActionRestriction, parseSharedPorts, sharedLimitBytes, sharedLimitGiB, formatSharedPorts } from "../modules/user-model.js";
import { node, button, actions, stack, grid, section, table, badge, field, muted, list, pathID, notice } from "./ui.js";
import { date } from "./operations.js";

export async function usersPage(ctx) {
  let users = list(await ctx.api("/users"));
  const results = node("div");
  const query = field("搜索用户", "query", "", { type: "search", placeholder: "用户名或显示名称" });
  async function refresh() { users = list(await ctx.api("/users")); if (ctx.current()) draw(); }
  function edit(user = null) {
    const checks = userPermissions.map(([permission, label]) => field(label, `permission:${permission}`,
      user ? user.permissions?.includes(permission) : defaultPermissions.includes(permission), { type: "checkbox" }));
    const role = field("角色", "role", user?.role || "user", { options: [["user", "普通用户"], ["admin", "管理员"]] });
    const permissionSection = section("账号权限", node("div", { className: "a-card-body a-grid" }, checks));
    const roleInput = role.querySelector("select");
    const sync = () => { ctx.visible(permissionSection, roleInput.value !== "admin"); };
    roleInput.addEventListener("change", sync);
    sync();
    const form = ctx.form([
      grid(field("用户名", "username", user?.username || "", { required: true, readonly: Boolean(user), maxlength: 64, pattern: "[A-Za-z0-9._\\-]+" }),
        field("显示名称", "display_name", user?.display_name || "", { maxlength: 100 })),
      role, field("密码", "password", "", { type: "password", autocomplete: "new-password", required: !user,
        hint: user ? "留空保持当前密码" : "设置登录密码" }),
      permissionSection,
    ], user ? "保存用户" : "创建用户", async (values, element) => {
      if (user?.role === "admin" && values.role !== "admin") {
        const reason = userActionRestriction(user, ctx.session, "demote");
        if (reason) throw new Error(reason);
      }
      const payload = {
        display_name: values.display_name, role: values.role, password: values.password,
        permissions: values.role === "admin" ? [] : userPermissions.filter(([permission]) => element.elements.namedItem(`permission:${permission}`).checked).map(([permission]) => permission),
        ...(!user ? { username: values.username, agent_isolation: false } : {}),
      };
      await ctx.api(user ? `/users/${pathID(user.id)}` : "/users", { method: user ? "PUT" : "POST", body: payload });
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
  async function allocation(user) {
    let access = await ctx.api(`/users/${pathID(user.id)}/agent-access`);
    const directory = list(await ctx.api("/agent-directory"));
    if (!ctx.current()) return;
    const isolation = field("使用共享节点范围", "isolated", access.isolated, { type: "checkbox" });
    const rows = node("div", { className: "a-stack" });
    const entries = list(access.shares).map(share => ({ ...share, engines: [...share.engines] }));
    function drawRows() {
      rows.replaceChildren(...entries.map((share, index) => {
        const nodeField = field("节点", `node:${index}`, share.agent_id, { options: directory.map(agent => [agent.id, agent.name]), disabled: Boolean(share.id), required: true });
        const ports = field("授权端口", `ports:${index}`, formatSharedPorts(share.ports), { hint: "单个端口或范围，0 表示不限端口" });
        const limit = field("总额度（GiB）", `quota:${index}`, sharedLimitGiB(share.limit_bytes), { type: "number", min: 0, step: "any", required: true });
        const domains = field("HTTP-01 域名", `domains:${index}`, (share.http_domains || []).join("\n"), { multiline: 2 });
        const enabled = field("启用共享", `enabled:${index}`, share.enabled !== false, { type: "checkbox" });
        const reinvite = field("重新邀请", `reinvite:${index}`, false, { type: "checkbox" });
        const engines = ["mihomo", "xray", "sing-box", "ss-rust"].map(engine =>
          field(engine, `engine:${index}:${engine}`, share.engines.includes(engine), { type: "checkbox" }));
        return section(directory.find(agent => agent.id === share.agent_id)?.name || "共享资源",
          node("div", { className: "a-card-body" }, stack(nodeField, grid(ports, limit), domains, actions(engines), actions(enabled, reinvite))),
          badge(({ accepted: "已接受", pending: "待接受", rejected: "已拒绝" })[share.status] || "待发送"));
      }));
    }
    drawRows();
    const add = button("添加共享", () => {
      try { capture(); } catch (error) { ctx.announce(error.message, "error"); return; }
      entries.push({ agent_id: directory[0]?.id || "", engines: [], ports: [], limit_bytes: 0, enabled: true });
      drawRows();
      ctx.setDirty(true);
    }, { disabled: !directory.length });
    const form = ctx.form([
      isolation, rows, actions(add),
    ], "保存资源范围", async () => {
      capture();
      const shares = entries.map(share => ({
        agent_id: share.agent_id, engines: share.engines, ports: share.ports, limit_bytes: share.limit_bytes,
        http_domains: share.http_domains, enabled: share.enabled, reinvite: share.reinvite,
      }));
      const saved = await ctx.api(`/users/${pathID(user.id)}/agent-access`, { method: "PUT", body: {
        revision: access.revision, isolated: form.elements.isolated.checked, shares,
      } });
      access = saved;
      ctx.setDirty(false);
      box.close();
      ctx.announce("资源范围已保存");
    });
    function capture() {
      const value = name => form.elements.namedItem(name);
      entries.forEach((share, index) => {
        share.agent_id = value(`node:${index}`).value;
        share.ports = parseSharedPorts(value(`ports:${index}`).value);
        share.limit_bytes = sharedLimitBytes(value(`quota:${index}`).value);
        share.http_domains = value(`domains:${index}`).value.split(/[\s,，]+/).filter(Boolean);
        share.enabled = value(`enabled:${index}`).checked;
        share.reinvite = value(`reinvite:${index}`).checked;
        share.engines = ["mihomo", "xray", "sing-box", "ss-rust"].filter(engine => value(`engine:${index}:${engine}`).checked);
        if (share.enabled && !share.engines.length) throw new Error("请至少分配一个内核。");
      });
    }
    let changed = false;
    form.addEventListener("input", () => { changed = true; ctx.setDirty(true); });
    const box = ctx.dialog(`${user.username} · 共享资源`, form, { wide: true });
    box.onRequestClose = async () => {
      if (changed && !await ctx.confirm("放弃未保存的资源范围更改？", "放弃更改")) return;
      ctx.setDirty(false);
      box.close();
    };
  }
  function draw() {
    const text = query.querySelector("input").value.toLowerCase();
    results.replaceChildren(table(["用户名", "显示名称", "角色", "状态", "最近登录", "操作"], users.filter(user =>
      [user.username, user.display_name].join(" ").toLowerCase().includes(text)).map(user => [
      user.username, user.display_name || "—", user.role === "admin" ? "管理员" : "普通用户",
      badge(user.disabled ? "已停用" : "正常", user.disabled ? "warn" : "ok"), date(user.last_login_at),
      actions(button("编辑", () => edit(user)),
        user.role !== "admin" && button("共享资源", event => void ctx.mutate(event.currentTarget, () => allocation(user))),
        button(user.disabled ? "启用" : "停用", async event => {
          const control = event.currentTarget;
          const reason = !user.disabled && userActionRestriction(user, ctx.session, "disable");
          if (reason) { ctx.announce(reason, "error"); return; }
          if (!await ctx.confirm(`确定${user.disabled ? "启用" : "停用"}账号 ${user.username}？`, user.disabled ? "启用用户" : "停用用户") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/users/${pathID(user.id)}`, { method: "PUT", body: { disabled: !user.disabled } });
            await refresh();
          });
        }),
        button("永久删除", async event => {
          const control = event.currentTarget;
          const reason = userActionRestriction(user, ctx.session, "delete");
          if (reason) { ctx.announce(reason, "error"); return; }
          if (!await ctx.confirm(`永久删除 ${user.username}？账号与私有数据将删除，自有节点和主机资源转入管理员管理。`, "永久删除用户")) return;
          if (!await ctx.confirm("确认永久删除？此操作无法恢复。", "确认永久删除") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/users/${pathID(user.id)}/purge`, { method: "POST" });
            ctx.announce("用户已永久删除");
            await refresh();
          });
        }, { tone: "danger" })),
    ]), { label: "用户" }));
  }
  draw();
  query.querySelector("input").addEventListener("input", draw);
  return stack(section(null, node("div", { className: "a-card-body" }, query)),
    section(null, results, button("创建用户", () => edit(), { tone: "primary" })));
}
