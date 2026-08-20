export function installSettings(ctx) {
  const { api, state, esc, date, can, shell, notify, confirmAction } = ctx;
async function settings() {
  const [item, auditLogs, users] = await Promise.all([
    api("/settings"),
    api("/audit?limit=100"),
    can("users.manage") ? api("/users") : Promise.resolve([]),
  ]);
  state.data.settings = item;
  const auditAction = {
    "login.succeeded": "登录成功",
    "login.failed": "登录失败",
    "config.created": "创建配置",
    "config.updated": "更新配置",
    "config.deleted": "删除配置",
    "config.restored": "恢复修订",
    "agent_config.saved": "保存节点配置",
    "task.created": "提交任务",
    "task.canceled": "取消任务",
    "task.retried": "重试任务",
    "agent.deleted": "移除节点",
    "enrollment_token.created": "生成添加节点命令",
    "enrollment_token.revoked": "删除添加节点命令",
    "enrollment_token.deleted": "删除添加节点命令",
    "settings.saved": "更新设置",
    "user.created": "创建用户",
    "user.updated": "更新用户",
    "user.disabled": "停用用户",
  };
  const auditRows = auditLogs
    .map(
      (entry) =>
        `<li><time>${date(entry.acted_at)}</time><span class="audit-action">${esc(auditAction[entry.action] || entry.action)}</span>${entry.target ? `<code>${esc(entry.target)}</code>` : ""}${entry.detail ? `<small>${esc(entry.detail)}</small>` : ""}<em>${esc(entry.remote_ip || "-")}</em></li>`,
    )
    .join("");
  const readOnly = can("settings.manage") ? "" : "disabled";
  const roleName = { admin: "管理员", operator: "运维人员", auditor: "审计人员", readonly: "只读用户" };
  const userRows = users
    .map(
      (user) =>
        `<div class="panel-user-row" data-user-form="${esc(user.id)}"><div><strong>${esc(user.username)}</strong><small>${esc(user.display_name || "未设置姓名")} · ${user.disabled ? "已停用" : "正常"}</small></div><select name="role" aria-label="${esc(user.username)} 角色">${["admin", "operator", "auditor", "readonly"].map((role) => `<option value="${role}" ${user.role === role ? "selected" : ""}>${roleName[role]}</option>`).join("")}</select><input name="display_name" value="${esc(user.display_name || "")}" maxlength="100" placeholder="显示名称" aria-label="${esc(user.username)} 显示名称"><input name="password" type="password" minlength="12" maxlength="72" autocomplete="new-password" placeholder="留空不修改" aria-label="${esc(user.username)} 新密码"><label class="user-disabled-toggle"><input name="disabled" type="checkbox" ${user.disabled ? "checked" : ""} ${state.session.user_id === user.id ? "disabled" : ""}>停用</label><button class="button small" type="button" data-save-user="${esc(user.id)}">保存</button>${state.session.user_id === user.id ? "" : `<button class="button small danger-button" type="button" data-disable-user="${esc(user.id)}" ${user.disabled ? "disabled" : ""}>停用</button>`}</div>`,
    )
    .join("");
  const userSection = can("users.manage")
    ? `<section class="settings-section" id="users"><header><span class="settings-section-number">05</span><h3>用户管理</h3></header><p class="settings-hint">为团队成员创建独立账号，权限变更会立即使其现有会话失效。至少保留一个启用中的管理员。</p><div class="user-create-form" id="user-create-form"><input name="username" required maxlength="64" pattern="[A-Za-z0-9._\\-]+" placeholder="用户名"><input name="display_name" maxlength="100" placeholder="显示名称"><select name="role" aria-label="新用户角色"><option value="operator">运维人员</option><option value="auditor">审计人员</option><option value="readonly">只读用户</option><option value="admin">管理员</option></select><input name="password" type="password" required minlength="12" maxlength="72" autocomplete="new-password" placeholder="初始密码（至少 12 字节）"><button class="button primary" type="button" data-create-user>添加用户</button></div><div class="panel-user-list">${userRows || '<div class="empty compact"><strong>还没有个人用户</strong></div>'}</div></section>`
    : "";
  shell(
    `<div class="settings-workspace"><header class="settings-hero"><h2>系统设置</h2></header><form class="settings-form" id="settings-form"><section class="settings-section" id="identity"><header><span class="settings-section-number">01</span><h3>面板标识</h3></header><div class="settings-grid"><label class="settings-field"><span>面板名称</span><input name="panel_name" value="${esc(item.panel_name)}" maxlength="40" required autocomplete="organization" ${readOnly}></label><label class="settings-field"><span>面板说明</span><input name="panel_description" value="${esc(item.panel_description)}" maxlength="120" ${readOnly}></label></div></section><section class="settings-section" id="defaults"><header><span class="settings-section-number">02</span><h3>操作默认值</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务默认显示数量</span><select name="task_page_size" ${readOnly}>${[50, 100, 500].map((value) => `<option value="${value}" ${value === item.task_page_size ? "selected" : ""}>${value} 条</option>`).join("")}</select></label></div></section><section class="settings-section" id="synchronization"><header><span class="settings-section-number">03</span><h3>状态同步</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务状态刷新频率</span><select name="task_poll_interval_ms" ${readOnly}>${[600, 1000, 2000, 5000].map((value) => `<option value="${value}" ${value === item.task_poll_interval_ms ? "selected" : ""}>${value < 1000 ? "0.6 秒" : `${value / 1000} 秒`}</option>`).join("")}</select></label></div></section><section class="settings-section" id="notifications"><header><span class="settings-section-number">04</span><h3>事件通知</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>Webhook 地址</span><input name="webhook_url" type="url" value="${esc(item.webhook_url)}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub（留空禁用）" autocomplete="off" spellcheck="false" ${readOnly}></label><p class="settings-hint">任务失败、节点离线或恢复在线时，控制面会向该地址 POST 带 <code>X-QControlHub-Signature</code> HMAC-SHA256 签名的 JSON 事件（通过 <code>QCH_WEBHOOK_SECRET</code> 签名），可对接钉钉 / 企业微信自定义机器人或自建接收端。</p></div></section>${auditRows ? `<section class="settings-section" id="audit"><header><span class="settings-section-number">05</span><h3>最近操作</h3></header><ol class="audit-list">${auditRows}</ol></section>` : ""}${can("settings.manage") ? '<footer class="settings-savebar"><div class="settings-savebar-copy"><b>保存设置</b><small>修改后的设置只在保存后生效。</small></div><div><button class="button" type="button" data-reset-settings>恢复默认值</button><button class="button primary" type="submit">保存设置</button></div></footer>' : '<p class="settings-hint">当前为只读角色，仅可查看设置与操作记录。</p>'}</form></div>`,
    "系统设置",
  );
  if (userSection) {
    document.querySelector(".settings-savebar")?.insertAdjacentHTML("beforebegin", userSection);
  }
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
  document
    .querySelector("[data-reset-settings]")
    ?.addEventListener("click", async () => {
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
      await settings();
    });
  document.querySelector("[data-create-user]")?.addEventListener("click", async (event) => {
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
        }),
      });
      notify("用户已创建");
      await settings();
    } catch (error) {
      notify(error.message, "error");
    }
  });
  document.querySelectorAll("[data-save-user]").forEach((button) => {
    button.addEventListener("click", async () => {
      const formElement = button.closest("[data-user-form]");
      const value = (name) => formElement.querySelector(`[name="${name}"]`);
      const password = String(value("password")?.value || "");
      const body = {
        display_name: value("display_name")?.value || "",
        role: value("role")?.value || "",
        disabled: Boolean(value("disabled")?.checked),
      };
      if (password) body.password = password;
      try {
        await api(`/users/${encodeURIComponent(formElement.dataset.userForm)}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
        notify("用户已更新");
        await settings();
      } catch (error) {
        notify(error.message, "error");
      }
    });
  });
  document.querySelectorAll("[data-disable-user]").forEach((button) => {
    button.addEventListener("click", async () => {
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
