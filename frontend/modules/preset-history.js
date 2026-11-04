import { recordDate } from "./record-display.js";
import { bindEvent } from "./refresh.js";
import { updateFeedback } from "./presence-motion.js";
import { xrayMutableAPIServices } from "./xray-api.js";

export function bindPresetHistory(ctx, { history, config, isCurrent, read, editor, drafts, onSaved, status }) {
  if (!history || !ctx.can("configs.read")) return;
  const { api, esc, date, can, confirmAction, notify } = ctx;
  const body = history.querySelector("[data-revision-body]");
  const current = () => history.isConnected && isCurrent();
  let loading = false, loaded = false, selection = 0;
  const load = async () => {
    if (!history.open || loading || loaded) return;
    loading = true;
    updateFeedback(body, "正在加载配置历史…");
    try {
      const path = `/configs/${encodeURIComponent(config.id)}/revisions?limit=50`;
      const revisions = await read(path);
      if (!current()) return;
      body.innerHTML = `<nav aria-label="配置保存历史">${revisions.map(revision => `<a href="#revisions" data-revision="${revision.version}" class="${revision.version === config.version ? "current" : ""}"><i></i><span><b>${esc(recordDate(revision.updated_at, date))}</b><strong>${esc(revision.name)}</strong><small>${revision.version === config.version ? "当前配置" : "历史记录"}</small></span></a>`).join("")}</nav><div class="timeline-placeholder" data-history-preview role="status">${revisions.length ? "选择历史记录" : "暂无历史记录"}</div>`;
      loaded = true;
      body.querySelectorAll("[data-revision]").forEach(link => bindEvent(link, "click", async event => {
        event.preventDefault();
        if (!current() || editor.savePending) return;
        const request = ++selection;
        const preview = body.querySelector("[data-history-preview]");
        body.querySelectorAll("[data-revision]").forEach(item => {
          item.classList.toggle("active", item === link);
          item.setAttribute("aria-current", String(item === link));
        });
        preview.className = "timeline-placeholder";
        updateFeedback(preview, "正在加载历史配置…");
        try {
          const revision = await api(`/configs/${encodeURIComponent(config.id)}/revisions/${Number(link.dataset.revision)}`);
          if (!current() || request !== selection) return;
          preview.className = "timeline-preview";
          preview.innerHTML = `<header><div><b>${esc(revision.name)}</b><small>${esc(recordDate(revision.updated_at, date))}</small></div>${revision.version === config.version ? '<span class="status-label ok">当前配置</span>' : ""}</header><textarea readonly aria-label="历史配置内容">${esc(revision.content)}</textarea>${can("configs.restore") && revision.version !== config.version ? '<button class="button" type="button" data-restore-revision>恢复此档案</button>' : ""}<p class="validation-note">恢复仅保存配置，部署后才会生效。</p><div role="alert" data-restore-error></div>`;
          const button = preview.querySelector("[data-restore-revision]");
          bindEvent(button, "click", async () => {
            if (!current() || request !== selection || editor.savePending) return;
            // Freeze selection and other editor mutations through confirmation
            // and commit. Every continuation is tied to this mounted workspace.
            editor.savePending = true;
            const pending = {};
            const sessionData = ctx.state.data;
            editor.historyRestore = pending;
            button.disabled = true;
            editor.syncControls();
            let restored;
            try {
              const mutableServices = xrayMutableAPIServices(config.engine, revision.content);
              const migration = mutableServices.length ? ` 同时仅关闭 ${mutableServices.join("、")}，保留统计、日志及其他 API 配置。` : "";
              if (!(await confirmAction(`确定恢复 ${recordDate(revision.updated_at, date)} 的配置？将放弃当前草稿并按本次保存时间新增档案，部署后才会生效。${migration}`, "恢复配置"))) return;
              if (!current() || request !== selection) return;
              restored = await api(`/configs/${encodeURIComponent(config.id)}/revisions/${revision.version}/restore`, {
                method: "POST", body: JSON.stringify({ expected_version: config.version,
                  ...(mutableServices.length ? {disable_mutable_api:true} : {}) }),
              });
              if (!current()) return;
              drafts().clear(`${config.agent_id}|${config.engine}|`);
              status.saveOperation({ key:`${config.agent_id}|${config.engine}`, message:"历史配置已恢复并保存，尚未部署。", tone:"success" });
            } catch (error) {
              if (current()) updateFeedback(preview.querySelector("[data-restore-error]"), error.status === 409
                ? "配置已变化，未恢复。请关闭历史并重新加载配置后重试。" : `恢复失败：${error.message}`);
            } finally {
              // A switched account owns a different lifecycle record.
              if (ctx.state.data === sessionData && editor.historyRestore === pending) {
                editor.savePending = false;
                delete editor.historyRestore;
                button.disabled = false;
                editor.syncControls();
              }
            }
            if (!restored || !current()) return;
            try { await onSaved(restored); }
            catch (error) { notify(`配置已恢复并保存，刷新失败：${error.message}。请重新加载配置。`, "error"); }
          });
        } catch (error) {
          if (current() && request === selection) updateFeedback(preview, `预览失败：${error.message}。请重新选择记录。`);
        }
      }));
    } catch (error) {
      if (current()) updateFeedback(body, `配置历史加载失败：${error.message}；重新展开可重试。`);
    } finally { loading = false; }
  };
  bindEvent(history, "toggle", load);
  void load();
}
