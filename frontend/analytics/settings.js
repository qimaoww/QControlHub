import { readRegion, loadingTable } from "./loading.js";
import { userPermissions } from "../modules/user-model.js";
import { node, button, actions, stack, grid, section, table, badge, field, code, muted, list, pathID, notice, copyText, link } from "./ui.js";
import { date } from "./operations.js";
import { siteBrandingSettings } from "./site-branding.js";

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
const engineIDs = ["mihomo", "xray", "sing-box", "ss-rust"];

export async function settingsPage(ctx) {
  let settings = {}, ready = false;
  const writable = ctx.can("settings.manage");
  let brandingDirty = false;
  const input = (name, label, attrs = {}) => field(label, name, settings[name] ?? "", { ...attrs, disabled: true });
  const numeric = numericFields.map(([name, label, options]) => input(name, label, { options }));
  const saveState = muted("正在读取设置…");
  saveState.setAttribute("role", "status");
  let baseline, phase = "idle";
  const content = [
    section("基础设置", node("div", { className: "a-card-body a-form-grid" },
      input("panel_name", "面板名称", { required: true, maxlength: 40 }),
      input("panel_description", "面板说明", { maxlength: 120 }),
      input("time_zone", "时间区域", { options: [["browser", "跟随浏览器"], ["Asia/Shanghai", "中国标准时间"], ["UTC", "UTC"]] }),
      input("time_display", "时间显示", { options: [["absolute-relative", "日期与相对时间"], ["absolute", "日期时间"]] }),
      input("ui_font_scale", "文字大小", { options: [[90, "较小"], [100, "标准"], [110, "较大"]] }),
      input("default_config_editor", "默认配置编辑器", { options: [["structured", "结构化编辑"], ["source", "源码编辑"]] }))),
    section("默认内核能力", node("div", { className: "a-card-body a-actions" },
      engineIDs.map(engine => field(engine, `engine:${engine}`, settings.default_agent_engines?.includes(engine), { type: "checkbox", disabled: !ready || !writable })))),
    section("节点上报", node("div", { className: "a-card-body a-stack" },
      muted("仅影响当前账号及自有节点，借用节点不受影响。"),
      node("div", { className: "a-form-grid a-settings-metrics" }, [2, 3, 4, 8].map(index => numeric[index])))),
    section("任务", node("div", { className: "a-card-body a-form-grid a-settings-metrics" }, [0, 1, 5, 6, 7].map(index => numeric[index]))),
    section("日志", node("div", { className: "a-card-body a-form-grid a-settings-metrics" },
      numeric.slice(9, 12), input("core_log_minimum_level", "日志最低级别", { options: [["debug", "调试"], ["info", "信息"], ["warning", "警告"], ["error", "错误"], ["critical", "严重错误"], ["off", "关闭"]] }),
      input("client_connection_retention_days", "客户端连接保留天数", { type: "number", min: 0, max: 3650 }))),
    section("数据清理", node("div", { className: "a-card-body a-form-grid a-settings-metrics" }, numeric.slice(12))),
    section("证书与地址库", node("div", { className: "a-card-body a-form-grid" },
      input("acme_email", "ACME 邮箱", { type: "email" }),
      field("大陆 IPv4 数据源", "cnip_ipv4_url", settings.cnip_source?.ipv4_url || "", { type: "url", disabled: !ready || !writable }),
      field("大陆 IPv6 数据源", "cnip_ipv6_url", settings.cnip_source?.ipv6_url || "", { type: "url", disabled: !ready || !writable }))),
    saveState,
  ];
  const form = ctx.form(content, "保存设置", async (values, element) => {
    if (!writable || !ready) return;
    const submitted = snapshot();
    const body = { ...settings, ...values, revision: settings.revision,
      default_agent_engines: engineIDs.filter(engine => element.elements.namedItem(`engine:${engine}`).checked),
      cnip_source: { ipv4_url: values.cnip_ipv4_url, ipv6_url: values.cnip_ipv6_url, format: "auto" },
    };
    delete body.cnip_ipv4_url;
    delete body.cnip_ipv6_url;
    for (const engine of engineIDs) delete body[`engine:${engine}`];
    for (const name of [...numericFields.map(([key]) => key), "ui_font_scale", "client_connection_retention_days"]) body[name] = Number(body[name]);
    phase = "saving"; sync();
    try {
      const saved = await ctx.api("/settings", { method: "PUT", body });
      if (!ctx.current()) return;
      settings = saved;
      baseline = submitted;
      phase = "idle"; sync();
      ctx.announce("设置已保存");
    } catch (error) {
      if (ctx.current()) { phase = "failed"; sync(); }
      throw error;
    }
  });
  const submit = form.querySelector('button[type="submit"]');
  form.id = "panel-settings-form";
  const snapshot = () => JSON.stringify([...form.elements].filter(control => control.name)
    .map(control => [control.name, control.type === "checkbox" ? control.checked : control.value]));
  function sync() {
    if (!ctx.current()) return;
    const changed = ready && writable && snapshot() !== baseline;
    ctx.setDirty(changed || brandingDirty);
    submit.disabled = !ready || !changed || phase === "saving" || form.getAttribute("aria-busy") === "true";
    saveState.textContent = !ready ? "正在读取设置…" : phase === "saving" ? "正在保存…" : phase === "failed" ? "保存失败 · 有未保存更改" : changed ? "有未保存更改" : "已保存";
    const heartbeat = form.elements.agent_heartbeat_interval_seconds, offline = form.elements.agent_offline_threshold_seconds;
    offline.setCustomValidity(Number(offline.value) < Number(heartbeat.value) * 3 ? "离线阈值不得少于心跳间隔的 3 倍。" : "");
  }
  baseline = snapshot();
  const changed = () => {
    if (phase !== "saving") phase = "idle";
    form.querySelector(':scope > .a-notice')?.replaceChildren();
    sync();
  };
  form.addEventListener("input", changed);
  form.addEventListener("change", changed);
  submit.addEventListener("mutationend", sync);
  const refreshState = notice('');
  const branding = ctx.session.role === "admin" && writable ? siteBrandingSettings(ctx, changed => { brandingDirty = changed; sync(); }) : null;
  let refreshSequence = 0;
  const refreshSettings = async () => {
    const sequence = ++refreshSequence;
    const latest = await ctx.api('/settings');
    if (!ctx.current() || sequence !== refreshSequence) return;
    if (ready && (snapshot() !== baseline || phase === 'saving')) {
      refreshState.textContent = latest.revision !== settings.revision ? '设置已由其他会话修改，当前草稿已保留。' : '';
      return;
    }
    settings = latest; ready = true;
    for (const control of [...form.elements].filter(control => control.name)) {
      const name = control.name;
      const value = name.startsWith('engine:') ? latest.default_agent_engines?.includes(name.slice(7))
        : name === 'cnip_ipv4_url' ? latest.cnip_source?.ipv4_url || ''
        : name === 'cnip_ipv6_url' ? latest.cnip_source?.ipv6_url || '' : latest[name];
      control.disabled = !writable;
      if (control.type === 'checkbox') control.checked = Boolean(value);
      else if (control.value !== String(value ?? '')) control.value = value ?? '';
    }
    baseline = snapshot(); phase = 'idle'; refreshState.textContent = ''; sync();
  };
  sync();
  if (!writable) submit.remove();
  else {
    submit.setAttribute("form", form.id);
    // The existing sticky page header keeps the real form submit reachable.
    ctx.pageActions(submit);
  }
  const updateResult = muted("尚未检查更新");
  updateResult.setAttribute("role", "status");
  const check = button("检查更新", event => void ctx.mutate(event.currentTarget, async () => {
    updateResult.textContent = "正在检查更新…";
    try {
      const result = await ctx.api("/settings/check-update", { method: "POST" });
      if (ctx.current()) {
        const message = result.update_available ? `发现更新：${result.latest_version}` : result.comparable ? `已是最新发行版：${result.latest_version}` : `当前可用发行版：${result.latest_version}`;
        updateResult.replaceChildren(message, ...(result.release_url ? [link("查看发行版", result.release_url)] : []));
      }
    } catch (error) {
      if (ctx.current()) updateResult.textContent = `检查失败：${error.message}`;
    }
  }));
  const deploymentTable = loadingTable(['项目', '状态'], '部署状态');
  const deploymentRead = readRegion(ctx, '部署状态', signal => ctx.api('/settings/deployment', { signal }), deployment => {
    const security = [['控制面安全传输', deployment.secure_transport], ['数据库 TLS 校验', deployment.database_tls_verified], ['配置加密', deployment.config_encryption_configured]];
    deploymentTable.updateRows([...security.map(([name, enabled]) => [name, badge(enabled ? '已配置' : '未配置', enabled ? 'ok' : 'warn')]),
      ['可信代理', `${deployment.trusted_proxy_count ?? 0} 条网段`], ['控制面发行版本', deployment.control_plane_version || '尚未上报'], ['QAgent 安装包版本', deployment.agent_package_version || '尚未上报']]);
  }, { view:deploymentTable, clear: () => deploymentTable.updateRows([]) });
  const settingsRead = readRegion(ctx, '面板设置', refreshSettings, () => {}, { clear() {
    ready = false; settings = {}; baseline = undefined;
    for (const control of [...form.elements].filter(control => control.name)) { control.disabled = true; if (control.type === 'checkbox') control.checked = false; else control.value = ''; }
    sync();
  } });
  const extra = stack(section("部署状态", node('div', { className: 'a-card-body' }, stack(deploymentRead.element, deploymentTable, updateResult, actions(check)))));
  if (writable) extra.append(section("API", node("div", { className: "a-card-body" }, link("管理 API Key", "#api"))));
  ctx.onRefresh(() => Promise.all([settingsRead.load(), deploymentRead.load(), branding?.refresh()]));
  void settingsRead.load(); void deploymentRead.load();
  return stack(branding?.element, settingsRead.element, form, refreshState, extra);
}
