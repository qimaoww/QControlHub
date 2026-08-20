export function installSettings(ctx) {
  const { api, state, esc, date, can, shell, notify, confirmAction } = ctx;
async function settings() {
  const [item, auditLogs] = await Promise.all([
    api("/settings"),
    api("/audit?limit=100"),
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
  };
  const auditRows = auditLogs
    .map(
      (entry) =>
        `<li><time>${date(entry.acted_at)}</time><span class="audit-action">${esc(auditAction[entry.action] || entry.action)}</span>${entry.target ? `<code>${esc(entry.target)}</code>` : ""}${entry.detail ? `<small>${esc(entry.detail)}</small>` : ""}<em>${esc(entry.remote_ip || "-")}</em></li>`,
    )
    .join("");
  const readOnly = can("admin") ? "" : "disabled";
  shell(
    `<div class="settings-workspace"><header class="settings-hero"><h2>系统设置</h2></header><form class="settings-form" id="settings-form"><section class="settings-section" id="identity"><header><span class="settings-section-number">01</span><h3>面板标识</h3></header><div class="settings-grid"><label class="settings-field"><span>面板名称</span><input name="panel_name" value="${esc(item.panel_name)}" maxlength="40" required autocomplete="organization" ${readOnly}></label><label class="settings-field"><span>面板说明</span><input name="panel_description" value="${esc(item.panel_description)}" maxlength="120" ${readOnly}></label></div></section><section class="settings-section" id="defaults"><header><span class="settings-section-number">02</span><h3>操作默认值</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务默认显示数量</span><select name="task_page_size" ${readOnly}>${[50, 100, 500].map((value) => `<option value="${value}" ${value === item.task_page_size ? "selected" : ""}>${value} 条</option>`).join("")}</select></label></div></section><section class="settings-section" id="synchronization"><header><span class="settings-section-number">03</span><h3>状态同步</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>任务状态刷新频率</span><select name="task_poll_interval_ms" ${readOnly}>${[600, 1000, 2000, 5000].map((value) => `<option value="${value}" ${value === item.task_poll_interval_ms ? "selected" : ""}>${value < 1000 ? "0.6 秒" : `${value / 1000} 秒`}</option>`).join("")}</select></label></div></section><section class="settings-section" id="notifications"><header><span class="settings-section-number">04</span><h3>事件通知</h3></header><div class="settings-grid one-column"><label class="settings-field"><span>Webhook 地址</span><input name="webhook_url" type="url" value="${esc(item.webhook_url)}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub（留空禁用）" autocomplete="off" spellcheck="false" ${readOnly}></label><p class="settings-hint">任务失败、节点离线或恢复在线时，控制面会向该地址 POST 带 <code>X-QControlHub-Signature</code> HMAC-SHA256 签名的 JSON 事件（通过 <code>QCH_WEBHOOK_SECRET</code> 签名），可对接钉钉 / 企业微信自定义机器人或自建接收端。</p></div></section>${auditRows ? `<section class="settings-section" id="audit"><header><span class="settings-section-number">05</span><h3>最近操作</h3></header><ol class="audit-list">${auditRows}</ol></section>` : ""}${can("admin") ? '<footer class="settings-savebar"><div class="settings-savebar-copy"><b>保存设置</b><small>修改后的设置只在保存后生效。</small></div><div><button class="button" type="button" data-reset-settings>恢复默认值</button><button class="button primary" type="submit">保存设置</button></div></footer>' : '<p class="settings-hint">当前为只读角色，仅可查看设置与操作记录。</p>'}</form></div>`,
    "系统设置",
  );
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
}
  return settings;
}
