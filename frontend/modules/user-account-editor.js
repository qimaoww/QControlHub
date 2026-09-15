import { bindEvent } from "./refresh.js";

import { userPermissions, defaultPermissions } from "./user-model.js";
export function createUserAccountEditor({ api, state, esc, notify }, { users, report }) {
  function editUser(user) {
    const dialog = document.querySelector("[data-user-dialog]");
    const selected = new Set(user ? user.permissions || [] : defaultPermissions);
    dialog.innerHTML = `<header><span class="traffic-edit-icon" aria-hidden="true">＋</span><div><h2 id="user-dialog-title">${user ? "编辑账号" : "新增用户"}</h2></div><button class="deploy-command-close" type="button" data-user-close aria-label="关闭">×</button></header>
      <form data-user-form><div class="traffic-edit-body">
        <label>用户名<input name="username" required maxlength="64" autocomplete="off" value="${esc(user?.username || "")}" ${user ? "disabled" : ""}></label>
        <label>显示名称<input name="display_name" maxlength="100" value="${esc(user?.display_name || "")}"></label>
        <label>${user ? "新密码（留空不改）" : "密码"}<input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="72" ${user ? "" : "required"}></label>
        <label>角色<select name="role"><option value="user" ${user?.role !== "admin" ? "selected" : ""}>普通用户</option><option value="admin" ${user?.role === "admin" ? "selected" : ""}>管理员</option></select></label>
        ${user ? `<label class="settings-toggle"><span><b>启用账号</b></span><input name="enabled" type="checkbox" ${user.disabled ? "" : "checked"} ${user.id === state.session.user_id ? "disabled" : ""}></label>` : '<p class="settings-hint" data-user-default-isolation>普通用户资源始终独立，可自行添加 Agent，或使用获共享的 Agent。</p>'}
        <details class="user-permissions" ${user?.role === "admin" ? "hidden" : ""}><summary>操作权限</summary><div>${userPermissions.map(([key, label]) => `<label><input type="checkbox" name="permission" value="${key}" ${selected.has(key) ? "checked" : ""}>${label}</label>`).join("")}</div><p class="settings-hint">共享节点仅限已分配内核、端口与额度。</p></details>
        <div class="alert error" data-user-error role="alert" hidden></div>
      </div><footer><button class="button" type="button" data-user-close>取消</button><button class="button primary" type="submit">保存账号</button></footer></form>`;
    dialog.querySelectorAll("[data-user-close]").forEach((button) => bindEvent(button, "click", () => dialog.close()));
    const form = dialog.querySelector("form");
    bindEvent(form.elements.role, "change", () => {
      dialog.querySelector(".user-permissions").hidden = form.elements.role.value === "admin";
      const hint = dialog.querySelector("[data-user-default-isolation]");
      if (hint) hint.hidden = form.elements.role.value === "admin";
    });
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      if (button.disabled) return;
      const data = new FormData(form);
      const body = { display_name: data.get("display_name"), role: data.get("role"), permissions: data.getAll("permission") };
      if (data.get("password")) body.password = data.get("password");
      if (user) body.disabled = user.id === state.session.user_id ? user.disabled : !data.has("enabled");
      else Object.assign(body, { username: data.get("username"), agent_isolation: body.role !== "admin" });
      button.disabled = true;
      try {
        const saved = await api(user ? `/users/${encodeURIComponent(user.id)}` : "/users", { method: user ? "PUT" : "POST", body: JSON.stringify(body) });
        if (!dialog.isConnected || state.route !== "users") return;
        dialog.close();
        state.data.userID = saved.id;
        await users();
        notify("账号已保存");
      } catch (error) { report(error, form); }
      finally { button.disabled = false; }
    });
    dialog.showModal();
  }

  return editUser;
}
