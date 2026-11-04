import { userPermissions } from "../modules/user-model.js";
import { node, button, actions, stack, grid, section, table, badge, field, code, muted, list, pathID, notice } from "./ui.js";
import { date } from "./operations.js";

const numericFields = [
  ["task_page_size", "任务每页条数", [50, 100, 500]],
  ["task_poll_interval_ms", "任务刷新间隔（毫秒）", [600, 1000, 2000, 5000]],
  ["agent_heartbeat_interval_seconds", "节点心跳间隔（秒）", [10, 15, 30]],
  ["agent_metrics_interval_seconds", "指标上报间隔（秒）", [1, 5, 15, 30]],
  ["agent_offline_threshold_seconds", "离线阈值（秒）", [45, 60, 90, 180]],
  ["task_stale_timeout_seconds", "任务超时（秒）", [60, 120, 300, 600]],
  ["install_task_stale_timeout_seconds", "安装任务超时（秒）", [180, 360, 600, 900]],
  ["task_max_attempts", "最大尝试次数", [1, 3, 5]],
  ["public_ip_probe_interval_seconds", "公网地址探测间隔（秒）", [300, 900, 3600]],
  ["core_log_retention_days", "内核日志保留天数", [1, 3, 7, 14, 30]],
  ["agent_core_log_max_mib", "节点单个日志上限（MiB）", [1, 2, 4, 8, 16, 32, 64, 128]],
  ["agent_core_log_rotate_count", "节点日志轮转份数", [0, 1, 2, 3, 5]],
  ["metric_retention_days", "指标保留天数", [7, 14, 30]],
  ["audit_retention_days", "审计保留天数", [[0, "不清理"], 30, 90, 180]],
  ["task_retention_days", "任务保留天数", [[0, "不清理"], 30, 90, 180]],
  ["config_revision_retention", "配置历史保留份数", [[0, "不清理"], 50, 100]],
];
const toggles = [["notify_task_failed", "任务失败"], ["notify_agent_offline", "节点离线"], ["notify_agent_online", "节点上线"], ["notify_traffic_quota", "流量超额"]];
const engineIDs = ["mihomo", "xray", "sing-box", "ss-rust"];

export async function settingsPage(ctx) {
  let [settings, deployment] = await Promise.all([ctx.api("/settings"), ctx.api("/settings/deployment")]);
  const writable = ctx.can("settings.manage");
  const input = (name, label, attrs = {}) => field(label, name, settings[name], { ...attrs, disabled: !writable });
  const numeric = numericFields.map(([name, label, options]) => input(name, label, { options }));
  const saveState = notice("", "ok");
  const content = [
    section("基础设置", node("div", { className: "a-card-body a-form-grid" },
      input("panel_name", "面板名称", { required: true, maxlength: 40 }),
      input("panel_description", "面板说明", { maxlength: 120 }),
      input("time_zone", "时间区域", { options: [["browser", "跟随浏览器"], ["Asia/Shanghai", "中国标准时间"], ["UTC", "UTC"]] }),
      input("time_display", "时间显示", { options: [["absolute-relative", "日期与相对时间"], ["absolute", "日期时间"]] }),
      input("ui_font_scale", "文字大小", { options: [[90, "较小"], [100, "标准"], [110, "较大"]] }),
      input("default_config_editor", "默认配置编辑器", { options: [["structured", "结构化编辑"], ["source", "源码编辑"]] }))),
    section("默认内核能力", node("div", { className: "a-card-body a-actions" },
      engineIDs.map(engine => field(engine, `engine:${engine}`, settings.default_agent_engines?.includes(engine), { type: "checkbox", disabled: !writable })))),
    section("运行与数据保留", node("div", { className: "a-card-body a-form-grid" },
      numeric, input("core_log_minimum_level", "日志最低级别", { options: [["debug", "调试"], ["info", "信息"], ["warning", "警告"], ["error", "错误"], ["critical", "严重错误"], ["off", "关闭"]] }),
      input("client_connection_retention_days", "客户端连接保留天数", { type: "number", min: 0, max: 3650 }))),
    section("证书与地址库", node("div", { className: "a-card-body a-form-grid" },
      input("acme_email", "ACME 邮箱", { type: "email" }),
      field("大陆 IPv4 数据源", "cnip_ipv4_url", settings.cnip_source?.ipv4_url || "", { type: "url", disabled: !writable }),
      field("大陆 IPv6 数据源", "cnip_ipv6_url", settings.cnip_source?.ipv6_url || "", { type: "url", disabled: !writable }))),
    section("通知", node("div", { className: "a-card-body a-stack" },
      input("webhook_url", "Webhook 地址", { type: "url", maxlength: 500 }),
      actions(toggles.map(([name, label]) => input(name, label, { type: "checkbox" }))))),
    section("Komari", node("div", { className: "a-card-body a-form-grid" },
      input("komari_url", "Komari 地址", { type: "url", maxlength: 500 }),
      field("API Key", "komari_api_key", "", { type: "password", autocomplete: "new-password", maxlength: 500, disabled: !writable, hint: "留空保留当前密钥" }),
      field("清除已存 API Key", "clear_komari_api_key", false, { type: "checkbox", disabled: !writable }))),
    saveState,
  ];
  const form = ctx.form(content, "保存设置", async (values, element) => {
    if (!writable) return;
    const body = { ...values, revision: settings.revision,
      default_agent_engines: engineIDs.filter(engine => element.elements.namedItem(`engine:${engine}`).checked),
      cnip_source: { ipv4_url: values.cnip_ipv4_url, ipv6_url: values.cnip_ipv6_url, format: "auto" },
      clear_komari_api_key: element.elements.clear_komari_api_key.checked,
    };
    delete body.cnip_ipv4_url;
    delete body.cnip_ipv6_url;
    for (const engine of engineIDs) delete body[`engine:${engine}`];
    for (const [name] of toggles) body[name] = element.elements.namedItem(name).checked;
    for (const name of [...numericFields.map(([key]) => key), "ui_font_scale", "client_connection_retention_days"]) body[name] = Number(body[name]);
    const saved = await ctx.api("/settings", { method: "PUT", body });
    if (!ctx.current()) return;
    settings = saved;
    ctx.setDirty(false);
    element.elements.komari_api_key.value = "";
    element.elements.clear_komari_api_key.checked = false;
    saveState.textContent = "已保存";
    ctx.announce("设置已保存");
  });
  form.addEventListener("input", () => ctx.setDirty(true));
  if (!writable) form.querySelector('button[type="submit"]').remove();
  const updateResult = node("div");
  const check = button("检查更新", event => void ctx.mutate(event.currentTarget, async () => {
    const result = await ctx.api("/settings/check-update", { method: "POST" });
    updateResult.replaceChildren(notice(result.update_available ? `发现更新：${result.latest_version}` : result.comparable ? `已是最新发行版：${result.latest_version}` : `当前可用发行版：${result.latest_version}`, "ok"));
  }));
  const security = [
    ["控制面安全传输", deployment.secure_transport], ["数据库 TLS 校验", deployment.database_tls_verified],
    ["配置加密", deployment.config_encryption_configured], ["Webhook 签名", deployment.webhook_signing_configured],
  ];
  const extra = stack(section("部署状态", node("div", { className: "a-card-body" }, stack(
    table(["项目", "状态"], security.map(([name, enabled]) => [name, badge(enabled ? "已配置" : "未配置", enabled ? "ok" : "warn")])),
    updateResult, actions(check)))));
  if (ctx.session.role === "admin" && writable) extra.append(await apiKeys(ctx));
  return stack(form, extra);
}

async function apiKeys(ctx) {
  let resource = await ctx.api("/settings/api-keys");
  const results = node("div");
  const labels = Object.fromEntries(userPermissions);
  async function load() { resource = await ctx.api("/settings/api-keys"); if (ctx.current()) draw(); }
  const body = key => ({ name: key.name, permissions: key.permissions, ip_allowlist: key.ip_allowlist,
    expires_at: key.expires_at, revision: key.revision, disabled: key.disabled });
  function edit(key = null) {
    const controls = list(resource.read_permissions).map(permission => field(labels[permission] || permission, `permission:${permission}`,
      key ? key.permissions.includes(permission) : true, { type: "checkbox" }));
    const localTime = key?.expires_at ? new Date(new Date(key.expires_at).getTime() - new Date(key.expires_at).getTimezoneOffset() * 60000).toISOString().slice(0, 16) : "";
    const form = ctx.form([
      field("名称", "name", key?.name || "", { required: true, maxlength: 100 }),
      field("到期时间", "expires_at", localTime, { type: "datetime-local", hint: "留空为永不过期" }),
      field("来源 IP / CIDR", "ip_allowlist", key?.ip_allowlist?.join("\n") || "", { multiline: 3, hint: "每行一个，留空不限制" }),
      section("读取权限", node("div", { className: "a-card-body a-grid" }, controls)),
    ], key ? "保存授权" : "创建 Key", async (values, element) => {
      const payload = { name: values.name, expires_at: values.expires_at === localTime ? key?.expires_at || null : values.expires_at ? new Date(values.expires_at).toISOString() : null,
        ip_allowlist: values.ip_allowlist.split(/[\s,，]+/).filter(Boolean),
        permissions: list(resource.read_permissions).filter(permission => element.elements.namedItem(`permission:${permission}`).checked),
        ...(key ? { revision: key.revision, disabled: key.disabled } : {}),
      };
      const saved = await ctx.api(key ? `/settings/api-keys/${pathID(key.id)}` : "/settings/api-keys", { method: key ? "PUT" : "POST", body: payload });
      box.close();
      if (!key && saved.secret) ctx.dialog("API Key 已创建", stack(muted("密钥仅显示一次，请妥善保存。"), code(saved.secret)));
      await load();
      ctx.announce(key ? "授权已保存" : "API Key 已创建");
    });
    const box = ctx.dialog(key ? "编辑 API Key" : "创建 API Key", form, { wide: true });
  }
  function draw() {
    results.replaceChildren(table(["名称", "前缀", "状态", "到期时间", "最近使用", "操作"], list(resource.items).map(key => [
      key.name, `${key.prefix}…`, badge(({ active: "有效", expired: "已过期", disabled: "已停用", revoked: "已撤销" })[key.status] || key.status),
      key.expires_at ? date(key.expires_at) : "永不过期", date(key.last_used_at),
      !key.revoked_at && actions(button("编辑授权", () => edit(key)),
        button(key.disabled ? "启用" : "停用", event => void ctx.mutate(event.currentTarget, async () => {
          await ctx.api(`/settings/api-keys/${pathID(key.id)}`, { method: "PUT", body: { ...body(key), disabled: !key.disabled } });
          await load();
        })),
        button("撤销", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`撤销“${key.name}”后无法恢复，确定撤销？`, "撤销 API Key") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/settings/api-keys/${pathID(key.id)}`, { method: "DELETE" });
            await load();
          });
        }, { tone: "danger" })),
    ]), { label: "API Key" }));
  }
  draw();
  return section("API Key", results, button("创建 Key", () => edit(), { tone: "primary" }));
}
