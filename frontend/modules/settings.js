import { bindEvent } from "./refresh.js";

export function installSettings(ctx) {
  const { api, state, esc, date, can, shell, notify, confirmAction } = ctx;
  let settingsRequest = 0;
  async function settings({ settings: preloadedSettings, replaceForm = false } = {}) {
    const request = ++settingsRequest;
    const [item, users] = await Promise.all([
      preloadedSettings || api("/settings"),
      can("users.manage") ? api("/users") : Promise.resolve([]),
    ]);
    if (request !== settingsRequest || state.route !== "settings") return;
  state.data.settings = item;
  const readOnly = can("settings.manage") ? "" : "disabled";
  const roleName = { admin: "管理员", user: "用户" };
  const permissionGroups = [
    ["查看", [["overview.read", "查看总览"], ["agents.read", "查看节点"], ["deployments.read", "查看部署记录"], ["client-access.read", "查看客户端配置"], ["catalogs.read", "查看内核目录"], ["agent-config.read", "读取节点配置"], ["configs.read", "查看配置档案"], ["tasks.read", "查看任务记录"], ["core-logs.read", "查看内核日志"], ["settings.read", "查看系统设置"], ["audit.read", "查看审计记录"], ["metrics.read", "查看指标"], ["traffic.read", "查看端口流量"]]],
    ["操作", [["agent-config.write", "编辑节点配置"], ["configs.write", "管理配置档案"], ["tasks.execute", "执行节点任务"], ["traffic.manage", "管理流量配额"], ["templates.read", "查看配置模板"], ["templates.write", "管理配置模板"]]],
    ["管理", [["agents.manage", "管理节点身份"], ["enrollment.manage", "添加节点"], ["configs.delete", "删除配置档案"], ["configs.restore", "恢复配置版本"], ["templates.delete", "删除配置模板"], ["settings.manage", "修改系统设置"], ["users.manage", "管理用户"]]],
  ];
  const permissionEditor = (permissions, options = {}) => {
    const selected = new Set(permissions || []);
    const disabled = options.disabled ? "disabled" : "";
    const prefix = options.prefix || "";
    return `<div class="permission-editor" data-permission-editor="${esc(prefix)}"><div class="permission-editor-head"><b>细分权限</b><small>${options.disabled ? "管理员默认拥有全部权限" : "只授予完成工作所需的权限"}</small></div><div class="permission-groups">${permissionGroups.map(([group, values]) => `<section><h4>${group}</h4><div>${values.map(([key, label]) => `<label><input type="checkbox" name="${esc(prefix)}permissions" value="${esc(key)}" ${selected.has(key) ? "checked" : ""} ${disabled}><span>${label}</span></label>`).join("")}</div></section>`).join("")}</div></div>`;
  };
  const userRows = users
    .map(
      (user) =>
        `<article class="panel-user-row" data-user-form="${esc(user.id)}"><header><span class="user-avatar">${esc((user.display_name || user.username).slice(0, 1).toUpperCase())}</span><div><strong>${esc(user.display_name || user.username)}</strong><small>${esc(user.username)} · ${user.disabled ? "已停用" : "正常"}</small></div><em class="user-role-badge">${user.role === "admin" ? "管理员" : "用户"}</em></header><div class="user-edit-grid"><label>身份<select name="role" data-user-role aria-label="${esc(user.username)} 身份"><option value="user" ${user.role === "user" ? "selected" : ""}>用户</option><option value="admin" ${user.role === "admin" ? "selected" : ""}>管理员</option></select></label><label>显示名称<input name="display_name" value="${esc(user.display_name || "")}" maxlength="100" placeholder="显示名称"></label><label>修改密码<input name="password" type="password" minlength="12" maxlength="72" autocomplete="new-password" placeholder="留空不修改"></label><label class="user-disabled-toggle"><span>账号状态</span><span><input name="disabled" type="checkbox" ${user.disabled ? "checked" : ""} ${state.session.user_id === user.id ? "disabled" : ""}> ${user.disabled ? "已停用" : "正常"}</span></label></div>${permissionEditor(user.permissions, { disabled: user.role === "admin", prefix: "user-" })}<footer><small>${user.role === "admin" ? "管理员可访问全部功能" : `${(user.permissions || []).length} 项权限已授权`}</small><button class="button small" type="button" data-save-user="${esc(user.id)}">保存修改</button></footer></article>`,
    )
    .join("");
  const userSection = can("users.manage")
    ? `<section class="settings-section" id="users"><header><span class="settings-section-number">06</span><div><h3>用户管理</h3><p>身份只有管理员和用户，具体可见和可执行的功能由权限决定。</p></div></header><div class="user-create-form" id="user-create-form"><div class="user-create-fields"><label>用户名<input name="username" required maxlength="64" pattern="[A-Za-z0-9._\\-]+" placeholder="例如 ops-user"></label><label>显示名称<input name="display_name" maxlength="100" placeholder="例如 张三"></label><label>身份<select name="role" data-create-role aria-label="新用户身份"><option value="user">用户</option><option value="admin">管理员</option></select></label><label>初始密码<input name="password" type="password" required minlength="12" maxlength="72" autocomplete="new-password" placeholder="至少 12 个字符"></label></div>${permissionEditor([], { prefix: "create-" })}<div class="user-create-actions"><span>创建后仍可随时调整权限</span><button class="button primary" type="button" data-create-user>添加用户</button></div></div><div class="panel-user-list">${userRows || '<div class="empty compact"><strong>还没有个人用户</strong><span>使用上方表单添加第一个用户</span></div>'}</div></section>`
    : "";
  shell(
    `<div class="settings-workspace"><header class="settings-hero"><h2>系统设置</h2></header><form class="settings-form" id="settings-form"><section class="settings-section" id="identity"><header><span class="settings-section-number">01</span><h3>面板标识</h3></header><div class="settings-grid"><label class="settings-field"><span>面板名称</span><input name="panel_name" value="${esc(item.panel_name)}" maxlength="40" required autocomplete="organization" ${readOnly}></label><label class="settings-field"><span>面板说明</span><input name="panel_description" value="${esc(item.panel_description)}" maxlength="120" ${readOnly}></label></div></section><section class="settings-section" id="defaults"><header><span class="settings-section-number">02</span><h3>操作默认值</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务默认显示数量</span><select name="task_page_size" ${readOnly}>${[50, 100, 500].map((value) => `<option value="${value}" ${value === item.task_page_size ? "selected" : ""}>${value} 条</option>`).join("")}</select></label></div></section><section class="settings-section" id="synchronization"><header><span class="settings-section-number">03</span><h3>状态同步</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务状态刷新频率</span><select name="task_poll_interval_ms" ${readOnly}>${[600, 1000, 2000, 5000].map((value) => `<option value="${value}" ${value === item.task_poll_interval_ms ? "selected" : ""}>${value < 1000 ? "0.6 秒" : `${value / 1000} 秒`}</option>`).join("")}</select></label></div></section><section class="settings-section" id="notifications"><header><span class="settings-section-number">04</span><h3>事件通知</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>Webhook 地址</span><input name="webhook_url" type="url" value="${esc(item.webhook_url)}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub（留空禁用）" autocomplete="off" spellcheck="false" ${readOnly}></label><p class="settings-hint">任务失败、节点离线或恢复在线时，控制面会向该地址 POST 带 <code>X-QControlHub-Signature</code> HMAC-SHA256 签名的 JSON 事件（通过 <code>QCH_WEBHOOK_SECRET</code> 签名），可对接钉钉 / 企业微信自定义机器人或自建接收端。</p></div></section>${can("settings.manage") ? '<footer class="settings-savebar"><div class="settings-savebar-copy"><b>保存设置</b><small>修改后的设置只在保存后生效。</small></div><div><button class="button" type="button" data-reset-settings>恢复默认值</button><button class="button primary" type="submit">保存设置</button></div></footer>' : '<p class="settings-hint">当前为只读角色，仅可查看设置。</p>'}</form></div>`,
    "系统设置",
  );
  if (replaceForm) {
    const form = document.querySelector("#settings-form");
    if (form) {
      form.elements.panel_name.value = item.panel_name;
      form.elements.panel_description.value = item.panel_description;
      form.elements.task_page_size.value = String(item.task_page_size);
      form.elements.task_poll_interval_ms.value = String(item.task_poll_interval_ms);
      form.elements.webhook_url.value = item.webhook_url || "";
    }
  }
  if (userSection) {
    // 用户管理是设置页的第五个区块，与左侧目录保持一致。
    const userSectionMarkup = userSection.replace('class="settings-section-number">06', 'class="settings-section-number">05');
    document.querySelector(".settings-savebar")?.insertAdjacentHTML("beforebegin", userSectionMarkup);
  }
  const togglePermissionEditor = (select, editor) => {
    if (!select || !editor) return;
    const admin = select.value === "admin";
    editor.querySelectorAll("input[type=checkbox]").forEach((input) => {
      input.disabled = admin;
      if (admin) input.checked = false;
    });
    editor.querySelector(".permission-editor-head small")?.replaceChildren(
      document.createTextNode(admin ? "管理员默认拥有全部权限" : "只授予完成工作所需的权限"),
    );
  };
  document.querySelectorAll("[data-user-role]").forEach((select) => {
    bindEvent(select, "change", () => togglePermissionEditor(select, select.closest("[data-user-form]")?.querySelector("[data-permission-editor]")));
  });
  const createRole = document.querySelector("[data-create-role]");
  bindEvent(createRole, "change", () => togglePermissionEditor(createRole, document.querySelector("#user-create-form [data-permission-editor]")));
  const checkedPermissions = (root, prefix) => [...(root?.querySelectorAll(`input[name="${prefix}permissions"]`) || [])].filter((input) => input.checked && !input.disabled).map((input) => input.value);
  document.querySelector("#settings-form").onsubmit = async (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      await api("/settings", {
        method: "PUT",
        body: JSON.stringify({
          panel_name: form.get("panel_name"),
          panel_description: form.get("panel_description"),
          task_page_size: Number(form.get("task_page_size")),
          task_poll_interval_ms: Number(form.get("task_poll_interval_ms")),
          webhook_url: form.get("webhook_url"),
        }),
      });
      notify("设置已保存");
    } catch (error) {
      notify(error.message, "error");
    }
  };
  bindEvent(document.querySelector("[data-reset-settings]"), "click", async () => {
      if (!(await confirmAction("确定恢复系统默认设置？", "恢复默认值"))) return;
      await api("/settings", {
        method: "PUT",
        body: JSON.stringify({
          panel_name: "QControlHub",
          panel_description: "可信远程编排",
          task_page_size: 100,
          task_poll_interval_ms: 600,
          webhook_url: "",
        }),
      });
      await settings({ replaceForm: true });
  });
  bindEvent(document.querySelector("[data-create-user]"), "click", async (event) => {
    const container = document.querySelector("#user-create-form");
    const value = (name) => container.querySelector(`[name="${name}"]`)?.value || "";
    try {
      await api("/users", {
        method: "POST",
        body: JSON.stringify({
          username: value("username"),
          display_name: value("display_name"),
          role: value("role"),
          password: value("password"),
          permissions: checkedPermissions(container, "create-"),
        }),
      });
      notify("用户已创建");
      container.querySelectorAll("input").forEach((input) => {
        if (input.type === "checkbox" || input.type === "radio")
          input.checked = input.defaultChecked;
        else input.value = input.defaultValue;
      });
      container.querySelectorAll("select").forEach((select) => {
        select.value = [...select.options].find((option) => option.defaultSelected)?.value || "";
      });
      await settings();
    } catch (error) {
      notify(error.message, "error");
    }
  });
  document.querySelectorAll("[data-save-user]").forEach((button) => {
    bindEvent(button, "click", async () => {
      const formElement = button.closest("[data-user-form]");
      const value = (name) => formElement.querySelector(`[name="${name}"]`);
      const password = String(value("password")?.value || "");
      const body = {
        display_name: value("display_name")?.value || "",
        role: value("role")?.value || "",
        disabled: Boolean(value("disabled")?.checked),
        permissions: checkedPermissions(formElement, "user-"),
      };
      if (password) body.password = password;
      try {
        await api(`/users/${encodeURIComponent(formElement.dataset.userForm)}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
        notify("用户已更新");
        if (value("password")) {
          value("password").value = "";
          value("password").defaultValue = "";
        }
        await settings();
      } catch (error) {
        notify(error.message, "error");
      }
    });
  });
  document.querySelectorAll("[data-disable-user]").forEach((button) => {
    bindEvent(button, "click", async () => {
      if (!(await confirmAction("停用后该用户将立即退出所有会话，确定继续？", "停用用户"))) return;
      try {
        await api(`/users/${encodeURIComponent(button.dataset.disableUser)}`, { method: "DELETE" });
        notify("用户已停用");
        await settings();
      } catch (error) {
        notify(error.message, "error");
      }
    });
  });
}
  return settings;
}
