import { engineCapabilityToggles } from "./engine-capabilities.js";
export function createSettingsView({ esc, can, shell }) {
  const options = (selected, values) => values
    .map(([value, label]) => `<option value="${esc(value)}" ${String(value) === String(selected) ? "selected" : ""}>${esc(label)}</option>`)
    .join("");
  const field = (name, label, control, hint = "") => `<label class="settings-field"><span>${label}</span>${control}${hint ? `<small>${hint}</small>` : ""}</label>`;
  const select = (item, name, label, values, disabled, hint = "") => field(name, label, `<select name="${name}" ${disabled}>${options(item[name], values)}</select>`, hint);
  const toggle = (item, name, label, hint, disabled) => `<label class="settings-toggle"><span><b>${label}</b>${hint ? `<small>${hint}</small>` : ""}</span><input type="checkbox" name="${name}" ${item[name] ? "checked" : ""} ${disabled}></label>`;
  const section = (id, number, title, copy, body) => `<section class="settings-section" id="${id}"><header><span class="settings-section-number">${number}</span><div><h3>${title}</h3>${copy ? `<p>${copy}</p>` : ""}</div></header>${body}</section>`;

  return (item, deployment) => {
    const writable = can("settings.manage");
    const disabled = writable ? "" : "disabled";
    const securityRows = [
      ["控制面传输", deployment.secure_transport, deployment.secure_transport ? "已启用 HTTPS / TLS 代理" : "未声明安全传输"],
      ["PostgreSQL 校验", deployment.database_tls_verified, deployment.database_tls_verified ? "远程连接要求 verify-full" : "允许不安全数据库连接"],
      ["配置加密", deployment.config_encryption_configured, deployment.config_encryption_configured ? "已配置静态加密密钥" : "未配置静态加密密钥"],
      ["Webhook 签名", deployment.webhook_signing_configured, deployment.webhook_signing_configured ? "已配置 HMAC 密钥" : "未配置签名密钥"],
    ].map(([label, healthy, copy]) => `<li><span><b>${label}</b><small>${copy}</small></span><em class="${healthy ? "ok" : "warn"}">${healthy ? "正常" : "需检查"}</em></li>`).join("");

    shell(`<div class="settings-workspace settings-overview">
      <form class="settings-form" id="settings-form">
        ${section("settings-engines", "01", "默认内核能力", "仅用于新 Agent；已有节点在“节点设置 → Agent”调整。全部关闭为纯监控节点。", engineCapabilityToggles(item.default_agent_engines ?? ["mihomo", "xray", "sing-box", "ss-rust"], { writable }))}
        ${section("settings-basic", "02", "基础设置", "", `<div class="settings-grid">
          ${field("panel_name", "面板名称", `<input name="panel_name" value="${esc(item.panel_name)}" maxlength="40" required ${disabled}>`)}
          ${field("panel_description", "面板说明", `<input name="panel_description" value="${esc(item.panel_description)}" maxlength="120" ${disabled}>`)}
          ${select(item, "time_zone", "时间区域", [["browser", "跟随浏览器"], ["Asia/Shanghai", "Asia/Shanghai"], ["UTC", "UTC"]], disabled)}
          ${select(item, "time_display", "时间显示", [["absolute-relative", "绝对时间 + 相对时间"], ["absolute", "仅绝对时间"]], disabled)}
          ${select(item, "ui_font_scale", "界面字号", [[90, "小"], [100, "正常"], [110, "大"]], disabled)}
          ${select(item, "task_page_size", "任务默认显示数量", [[50, "50 条"], [100, "100 条"], [500, "500 条"]], disabled)}
          ${select(item, "default_config_editor", "配置编辑器默认模式", [["structured", "结构化表单"], ["source", "源文件"]], disabled)}
        </div>`)}
        ${section("settings-runtime", "03", "任务与同步", "仅影响本账号及自有 Agent。保存后在线 Agent 重连，内核不重启；借用节点不受影响。", `<div class="settings-subsection"><h4>节点上报策略</h4><div class="settings-grid settings-grid-three">
          ${select(item, "agent_heartbeat_interval_seconds", "心跳间隔", [[10, "10 秒"], [15, "15 秒"], [30, "30 秒"]], disabled)}
          ${select(item, "agent_metrics_interval_seconds", "指标采集间隔", [[1, "1 秒"], [5, "5 秒"], [15, "15 秒"], [30, "30 秒"]], disabled)}
          ${select(item, "agent_offline_threshold_seconds", "离线判定时间", [[45, "45 秒"], [60, "60 秒"], [90, "90 秒"], [180, "180 秒"]], disabled, "不得少于心跳间隔的 3 倍")}
        </div></div><div class="settings-subsection"><h4>任务恢复与探测</h4><div class="settings-grid settings-grid-three">
          ${select(item, "task_poll_interval_ms", "页面任务刷新", [[600, "0.6 秒"], [1000, "1 秒"], [2000, "2 秒"], [5000, "5 秒"]], disabled)}
          ${select(item, "task_stale_timeout_seconds", "普通任务失联重排", [[60, "1 分钟"], [120, "2 分钟"], [300, "5 分钟"], [600, "10 分钟"]], disabled)}
          ${select(item, "install_task_stale_timeout_seconds", "安装任务失联重排", [[180, "3 分钟"], [360, "6 分钟"], [600, "10 分钟"], [900, "15 分钟"]], disabled)}
          ${select(item, "task_max_attempts", "最大尝试次数", [[1, "1 次"], [3, "3 次"], [5, "5 次"]], disabled)}
          ${select(item, "public_ip_probe_interval_seconds", "公网 IP 探测间隔", [[300, "5 分钟"], [900, "15 分钟"], [3600, "1 小时"]], disabled)}
        </div></div>`)}
        ${section("settings-data", "04", "数据与日志", "", `<div class="settings-subsection settings-local-log"><h4>Agent 内核日志传输缓存</h4><p>容量包含单节点全部日志来源及轮转文件；历史日志仅在面板保存。</p><div class="settings-grid">
          ${select(item, "agent_core_log_max_mib", "单节点容量上限", [[1, "1 MiB"], [2, "2 MiB"], [4, "4 MiB"], [8, "8 MiB"], [16, "16 MiB"], [32, "32 MiB"], [64, "64 MiB"], [128, "128 MiB"]], disabled)}
          ${select(item, "agent_core_log_rotate_count", "轮转缓存数量", [[0, "0（仅当前缓存）"], [1, "1 个"], [2, "2 个"], [3, "3 个"], [5, "5 个"]], disabled)}
        </div></div><div class="settings-subsection"><h4>PostgreSQL 数据保留</h4><div class="settings-grid settings-grid-three">
          ${select(item, "core_log_minimum_level", "内核日志最低级别", [["debug", "调试及以上"], ["info", "信息及以上"], ["warning", "警告及以上"], ["error", "错误及以上"], ["critical", "仅严重错误"], ["off", "停止保存新日志"]], disabled)}
          ${select(item, "core_log_retention_days", "内核日志保留", [[1, "1 天"], [3, "3 天"], [7, "7 天"], [14, "14 天"], [30, "30 天"]], disabled, "每小时清理过期日志，不按条数截断")}
          ${select(item, "metric_retention_days", "指标历史保留", [[7, "7 天"], [14, "14 天"], [30, "30 天"]], disabled)}
          ${select(item, "audit_retention_days", "审计记录保留", [[0, "永久"], [30, "30 天"], [90, "90 天"], [180, "180 天"]], disabled)}
          ${select(item, "task_retention_days", "任务记录保留", [[0, "永久"], [30, "30 天"], [90, "90 天"], [180, "180 天"]], disabled)}
          ${select(item, "config_revision_retention", "每份配置保留版本", [[0, "全部"], [50, "最近 50 个"], [100, "最近 100 个"]], disabled)}
        </div></div>`)}
        ${section("settings-notify", "05", "事件通知", "控制面调用 Webhook；签名密钥在部署环境配置。", `<div class="settings-grid one-column">${field("webhook_url", "Webhook 地址", `<input name="webhook_url" type="url" value="${esc(item.webhook_url || "")}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub" ${disabled}>`)}</div><div class="settings-toggle-list">
          ${toggle(item, "notify_task_failed", "任务失败", "部署、校验或服务操作失败", disabled)}
          ${toggle(item, "notify_agent_offline", "节点离线", "超过离线判定时间", disabled)}
          ${toggle(item, "notify_agent_online", "节点恢复在线", "", disabled)}
          ${toggle(item, "notify_traffic_quota", "流量配额事件", "端口达到配额并触发阻断", disabled)}
        </div>`)}
        ${section("settings-komari", "06", "Komari 联动", "在节点卡片显示流量周期、用量与额度。", `<div class="settings-grid one-column">${field("komari_url", "Komari 地址", `<input name="komari_url" type="url" value="${esc(item.komari_url || "")}" maxlength="500" placeholder="https://komari.example.com" ${disabled}>`, "站点根地址，不含 /api/nodes。")}${field("komari_api_key", "Komari API Key", `<input name="komari_api_key" type="password" value="" maxlength="500" autocomplete="new-password" placeholder="${item.komari_api_key ? "已配置，留空保持" : "可选"}" ${disabled}>`, "仅用于读取节点信息；已保存的密钥不回显。")}</div>${item.komari_api_key ? `<label class="settings-toggle"><span><b>清除 API Key</b><small>保存时删除密钥</small></span><input type="checkbox" name="clear_komari_api_key" ${disabled}></label>` : ""}<p class="settings-hint">在“节点设置”填写各节点的 Komari 服务器 UUID。</p>`)}
        ${section("settings-cnip", "07", "CN IP 数据源", "仅当前账号生效；下次配置校验或部署时应用。", `<div class="settings-grid one-column">${field("cnip_ipv4_url", "IPv4 数据源", `<input type="url" name="cnip_ipv4_url" value="${esc(item.cnip_source?.ipv4_url || "")}" placeholder="留空使用默认大陆 IPv4 源" maxlength="2000" ${disabled}>`)}${field("cnip_ipv6_url", "IPv6 数据源", `<input type="url" name="cnip_ipv6_url" value="${esc(item.cnip_source?.ipv6_url || "")}" placeholder="留空使用默认大陆 IPv6 源" maxlength="2000" ${disabled}>`)}</div><p class="settings-hint">填写 HTTPS 文件直链，双栈可共用地址。自动识别 TXT / DAT / SRS / MMDB；DAT / MMDB 提取 CN，SRS 仅接受纯 IP 规则。</p>`)}
        ${section("settings-deployment", "08", "部署状态", "只读；密钥、数据库与可信代理在部署环境管理。", `<div class="settings-deployment-grid"><div><h4>安全状态</h4><ul class="settings-health-list">${securityRows}<li><span><b>可信代理</b></span><em>${esc(deployment.trusted_proxy_count)} 条网段</em></li></ul></div><div class="settings-version-card"><header><h4>组件版本</h4><button class="button small" type="button" data-check-update>检查更新</button></header><dl><div><dt>Control Plane</dt><dd><code>${esc(deployment.control_plane_version || "unknown")}</code></dd></div><div><dt>QAgent 安装包</dt><dd><code>${esc(deployment.agent_package_version || "unknown")}</code></dd></div></dl><p data-update-result>尚未检查 GHCR latest；不会自动升级。</p></div></div>`)}
        ${writable ? `<footer class="settings-savebar"><div class="settings-savebar-copy"><b class="settings-saved-state" data-settings-state>已保存 · v${esc(item.revision)}</b></div><button class="button primary" type="submit" data-save-settings disabled>保存更改</button></footer>` : `<p class="settings-hint"><span class="settings-saved-state" data-settings-state>已保存 · v${esc(item.revision)}</span> 只读</p>`}
      </form>
    </div>`, "系统设置");

    return { writable };
  };
}
