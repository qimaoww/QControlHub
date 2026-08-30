import { bindEvent } from "./refresh.js";

export function installSettings(ctx) {
  const { api, state, esc, can, shell, notify, applyUIFontScale } = ctx;
  let settingsRequest = 0;

  const options = (selected, values) => values
    .map(([value, label]) => `<option value="${esc(value)}" ${String(value) === String(selected) ? "selected" : ""}>${esc(label)}</option>`)
    .join("");
  const field = (name, label, control, hint = "") => `<label class="settings-field"><span>${label}</span>${control}${hint ? `<small>${hint}</small>` : ""}</label>`;
  const select = (item, name, label, values, disabled, hint = "") => field(name, label, `<select name="${name}" ${disabled}>${options(item[name], values)}</select>`, hint);
  const toggle = (item, name, label, hint, disabled) => `<label class="settings-toggle"><span><b>${label}</b><small>${hint}</small></span><input type="checkbox" name="${name}" ${item[name] ? "checked" : ""} ${disabled}></label>`;
  const section = (id, number, title, copy, body) => `<section class="settings-section" id="${id}"><header><span class="settings-section-number">${number}</span><div><h3>${title}</h3><p>${copy}</p></div></header>${body}</section>`;

  async function settings() {
    const request = ++settingsRequest;
    const [item, deployment] = await Promise.all([api("/settings"), api("/settings/deployment")]);
    if (request !== settingsRequest || state.route !== "settings") return;
    state.data.settings = item;
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
        ${section("settings-basic", "01", "基础设置", "面板显示和操作默认值。", `<div class="settings-grid">
          ${field("panel_name", "面板名称", `<input name="panel_name" value="${esc(item.panel_name)}" maxlength="40" required ${disabled}>`)}
          ${field("panel_description", "面板说明", `<input name="panel_description" value="${esc(item.panel_description)}" maxlength="120" ${disabled}>`)}
          ${select(item, "time_zone", "时间区域", [["browser", "跟随浏览器"], ["Asia/Shanghai", "Asia/Shanghai"], ["UTC", "UTC"]], disabled)}
          ${select(item, "time_display", "时间显示", [["absolute-relative", "绝对时间 + 相对时间"], ["absolute", "仅绝对时间"]], disabled)}
          ${select(item, "ui_font_scale", "界面字号", [[90, "小 · 90%"], [100, "中 · 100%"], [110, "大 · 110%"]], disabled, "所有页面统一缩放；保存后立即生效")}
          ${select(item, "task_page_size", "任务默认显示数量", [[50, "50 条"], [100, "100 条"], [500, "500 条"]], disabled)}
          ${select(item, "default_config_editor", "配置编辑器默认模式", [["structured", "结构化表单"], ["source", "源文件"]], disabled)}
        </div>`)}
        ${section("settings-runtime", "02", "任务与同步", "策略保存后，在线 Agent 自动重连并立即获取新配置，不会重启内核。", `<div class="settings-subsection"><h4>节点上报策略</h4><div class="settings-grid settings-grid-three">
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
        ${section("settings-data", "03", "数据与日志", "Agent 本地缓存与 PostgreSQL 历史数据分别控制。", `<div class="settings-subsection settings-local-log"><h4>Agent 本地内核日志</h4><p>限制每个节点的易失性内核日志空间；可继续调小到 1 MiB。systemd 使用独立 journal 总容量，OpenRC 使用受控文件轮转。</p><div class="settings-grid">
          ${select(item, "agent_core_log_max_mib", "单节点容量上限", [[1, "1 MiB"], [2, "2 MiB"], [4, "4 MiB"], [8, "8 MiB"], [16, "16 MiB"], [32, "32 MiB"], [64, "64 MiB"], [128, "128 MiB"]], disabled)}
          ${select(item, "agent_core_log_rotate_count", "旧文件保留数量", [[0, "0（仅当前日志）"], [1, "1 个"], [2, "2 个"], [3, "3 个"], [5, "5 个"]], disabled)}
        </div></div><div class="settings-subsection"><h4>PostgreSQL 数据保留</h4><div class="settings-grid settings-grid-three">
          ${select(item, "core_log_minimum_level", "内核日志最低级别", [["debug", "调试及以上"], ["info", "信息及以上"], ["warning", "警告及以上"], ["error", "错误及以上"], ["critical", "仅严重错误"], ["off", "停止保存新日志"]], disabled)}
          ${select(item, "core_log_retention_days", "内核日志保留", [[1, "1 天"], [3, "3 天"], [7, "7 天"], [14, "14 天"], [30, "30 天"]], disabled, "每小时按时间清理；不是固定条数")}
          ${select(item, "metric_retention_days", "指标历史保留", [[7, "7 天"], [14, "14 天"], [30, "30 天"]], disabled)}
          ${select(item, "audit_retention_days", "审计记录保留", [[0, "永久"], [30, "30 天"], [90, "90 天"], [180, "180 天"]], disabled)}
          ${select(item, "task_retention_days", "任务记录保留", [[0, "永久"], [30, "30 天"], [90, "90 天"], [180, "180 天"]], disabled)}
          ${select(item, "config_revision_retention", "每份配置保留版本", [[0, "全部"], [50, "最近 50 个"], [100, "最近 100 个"]], disabled)}
        </div></div>`)}
        ${section("settings-notify", "04", "事件通知", "Webhook 地址由控制面调用，签名密钥继续通过部署环境配置。", `<div class="settings-grid one-column">${field("webhook_url", "Webhook 地址", `<input name="webhook_url" type="url" value="${esc(item.webhook_url || "")}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub" ${disabled}>`)}</div><div class="settings-toggle-list">
          ${toggle(item, "notify_task_failed", "任务失败", "部署、校验或服务操作失败", disabled)}
          ${toggle(item, "notify_agent_offline", "节点离线", "超过离线判定时间", disabled)}
          ${toggle(item, "notify_agent_online", "节点恢复在线", "离线节点重新连接", disabled)}
          ${toggle(item, "notify_traffic_quota", "流量配额事件", "端口达到配额并触发阻断", disabled)}
        </div>`)}
        ${section("settings-deployment", "05", "部署状态", "高风险密钥、数据库地址和代理信任范围只读展示，仍由部署环境管理。", `<div class="settings-deployment-grid"><div><h4>安全状态</h4><ul class="settings-health-list">${securityRows}<li><span><b>可信代理</b><small>已配置 ${esc(deployment.trusted_proxy_count)} 条网段</small></span><em>${esc(deployment.trusted_proxy_count)}</em></li></ul></div><div class="settings-version-card"><header><h4>组件版本</h4><button class="button small" type="button" data-check-update>检查更新</button></header><dl><div><dt>Control Plane</dt><dd><code>${esc(deployment.control_plane_version || "unknown")}</code><span>当前</span></dd></div><div><dt>QAgent 安装包</dt><dd><code>${esc(deployment.agent_package_version || "unknown")}</code><span>当前</span></dd></div></dl><p data-update-result>尚未检查 GHCR latest；只检查，不会自动升级。</p></div></div>`)}
        ${writable ? `<footer class="settings-savebar"><div class="settings-savebar-copy"><b data-save-title>所有更改已保存</b><small><span class="settings-saved-state" data-settings-state>已保存 · v${esc(item.revision)}</span> 修改任一选项后可统一保存。</small></div><button class="button primary" type="submit" data-save-settings disabled>保存更改</button></footer>` : `<p class="settings-hint"><span class="settings-saved-state" data-settings-state>已保存 · v${esc(item.revision)}</span> 当前账号仅可查看设置。</p>`}
      </form>
    </div>`, "系统设置");

    const form = document.querySelector("#settings-form");
    const saveButton = form?.querySelector("[data-save-settings]");
    const stateBadge = document.querySelector("[data-settings-state]");
    const saveTitle = form?.querySelector("[data-save-title]");
    const markDirty = () => {
      if (!saveButton) return;
      saveButton.disabled = false;
      stateBadge.textContent = "有未保存更改";
      stateBadge.classList.add("dirty");
      if (saveTitle) saveTitle.textContent = "有未保存更改";
    };
    bindEvent(form, "input", markDirty);
    bindEvent(form, "change", markDirty);

    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      if (!writable) return;
      const data = new FormData(form);
      const number = (name) => Number(data.get(name));
      const body = {
        revision: item.revision,
        panel_name: data.get("panel_name"), panel_description: data.get("panel_description"),
        time_zone: data.get("time_zone"), time_display: data.get("time_display"), ui_font_scale: number("ui_font_scale"), default_config_editor: data.get("default_config_editor"),
        task_page_size: number("task_page_size"), task_poll_interval_ms: number("task_poll_interval_ms"),
        agent_heartbeat_interval_seconds: number("agent_heartbeat_interval_seconds"), agent_metrics_interval_seconds: number("agent_metrics_interval_seconds"),
        agent_offline_threshold_seconds: number("agent_offline_threshold_seconds"), task_stale_timeout_seconds: number("task_stale_timeout_seconds"),
        install_task_stale_timeout_seconds: number("install_task_stale_timeout_seconds"), task_max_attempts: number("task_max_attempts"),
        public_ip_probe_interval_seconds: number("public_ip_probe_interval_seconds"), core_log_minimum_level: data.get("core_log_minimum_level"),
        core_log_retention_days: number("core_log_retention_days"), agent_core_log_max_mib: number("agent_core_log_max_mib"),
        agent_core_log_rotate_count: number("agent_core_log_rotate_count"), metric_retention_days: number("metric_retention_days"),
        audit_retention_days: number("audit_retention_days"), task_retention_days: number("task_retention_days"),
        config_revision_retention: number("config_revision_retention"), webhook_url: data.get("webhook_url"),
        notify_task_failed: data.has("notify_task_failed"), notify_agent_offline: data.has("notify_agent_offline"),
        notify_agent_online: data.has("notify_agent_online"), notify_traffic_quota: data.has("notify_traffic_quota"),
      };
      saveButton.disabled = true;
      try {
        const saved = await api("/settings", { method: "PUT", body: JSON.stringify(body) });
        state.data.settings = saved;
        item.revision = saved.revision;
        applyUIFontScale?.(saved.ui_font_scale);
        stateBadge.textContent = `已保存 · v${saved.revision}`;
        stateBadge.classList.remove("dirty");
        if (saveTitle) saveTitle.textContent = "所有更改已保存";
        notify("设置已保存；运行策略变更会由在线 Agent 自动应用");
      } catch (error) {
        saveButton.disabled = false;
        notify(error.message, "error");
      }
    });

    bindEvent(document.querySelector("[data-check-update]"), "click", async (event) => {
      const button = event.currentTarget;
      const output = document.querySelector("[data-update-result]");
      button.disabled = true;
      output.textContent = "正在检查 GitHub 最新正式版…";
      try {
        const result = await api("/settings/check-update", { method: "POST" });
        if (!result.comparable) {
          output.innerHTML = `GHCR latest 当前为 <a href="${esc(result.release_url)}" target="_blank" rel="noopener">${esc(result.latest_version)}</a>；当前构建 ${esc(result.current_control_plane)} 不是可比较的提交或版本号。`;
        } else if (result.update_available) {
          output.innerHTML = `GHCR latest 已更新为 <a href="${esc(result.release_url)}" target="_blank" rel="noopener">${esc(result.latest_version)}</a>，请审核变更后再升级。`;
        } else {
          output.textContent = `已是 GHCR latest 对应版本（${result.latest_version}）。`;
        }
      } catch (error) {
        output.textContent = `检查失败：${error.message}`;
      } finally {
        button.disabled = false;
      }
    });
  }

  return settings;
}
