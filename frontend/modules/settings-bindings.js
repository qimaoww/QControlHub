import { bindEvent } from "./refresh.js";
import { selectedDefaultEngines } from "./engine-capabilities.js";
export function createSettingsBindings({ api, state, esc, notify, applyUIFontScale, invalidatePanelReads = () => {} }) {
  return ({ item, writable, accountData }) => {
    const form = document.querySelector("#settings-form");
    const saveButton = form?.querySelector("[data-save-settings]");
    const stateBadge = document.querySelector("[data-settings-state]");
    const markDirty = () => {
      if (!saveButton) return;
      saveButton.disabled = false;
      stateBadge.textContent = "有未保存更改";
      stateBadge.classList.add("dirty");
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
        cnip_source: { ipv4_url: String(data.get("cnip_ipv4_url") || "").trim(), ipv6_url: String(data.get("cnip_ipv6_url") || "").trim(), format: "auto" },
        default_agent_engines: selectedDefaultEngines(data),
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
        komari_url: data.get("komari_url"), komari_api_key: data.get("komari_api_key"),
        clear_komari_api_key: data.has("clear_komari_api_key"),
        notify_task_failed: data.has("notify_task_failed"), notify_agent_offline: data.has("notify_agent_offline"),
        notify_agent_online: data.has("notify_agent_online"), notify_traffic_quota: data.has("notify_traffic_quota"),
      };
      saveButton.disabled = true;
      try {
        const saved = await api("/settings", { method: "PUT", body: JSON.stringify(body) });
        if (state.data !== accountData) return;
        // Other routes read the panel settings from the shell cache.
        invalidatePanelReads("settings");
        state.data.settings = saved;
        item.revision = saved.revision;
        applyUIFontScale?.(saved.ui_font_scale);
        stateBadge.textContent = `已保存 · v${saved.revision}`;
        stateBadge.classList.remove("dirty");
        notify("设置已保存");
      } catch (error) {
        if (state.data !== accountData || error.name === "AbortError") return;
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
        if (state.data !== accountData) return;
        if (!result.comparable) {
          output.innerHTML = `GHCR latest 当前为 <a href="${esc(result.release_url)}" target="_blank" rel="noopener">${esc(result.latest_version)}</a>；当前构建 ${esc(result.current_control_plane)} 不是可比较的提交或版本号。`;
        } else if (result.update_available) {
          output.innerHTML = `GHCR latest 已更新为 <a href="${esc(result.release_url)}" target="_blank" rel="noopener">${esc(result.latest_version)}</a>，请审核变更后再升级。`;
        } else {
          output.textContent = `已是 GHCR latest 对应版本（${result.latest_version}）。`;
        }
      } catch (error) {
        if (state.data !== accountData || error.name === "AbortError") return;
        output.textContent = `检查失败：${error.message}`;
      } finally {
        button.disabled = false;
      }
    });
  };

}
