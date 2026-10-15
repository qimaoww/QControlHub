import { bindDialogBackdrop, closeRetiringDialog } from "./popup.js";
import { diagnosticError } from "./errors.js";

export async function openTaskEditRetry(ctx, id, refresh, trigger) {
  const { api, state, esc, notify, confirmAction } = ctx;
  const dialog = document.createElement("dialog");
  dialog.className = "config-inbound-dialog";
  dialog.setAttribute("aria-labelledby", "task-edit-retry-title");
  dialog.innerHTML = `<header class="config-inbound-heading"><div><h2 id="task-edit-retry-title">编辑后重试</h2><p>修改原任务关联配置，保存新版本后重新执行。</p></div><button type="button" class="config-access-close" aria-label="关闭弹窗" data-close>×</button></header><div class="config-inbound-body" data-body><p role="status">正在读取当前配置…</p></div>`;
  const controller = new AbortController();
  let saving = false, dirty = false, confirming = false;
  const dispose = () => {
    controller.abort();
    state.routeSignal?.removeEventListener("abort", dispose);
    closeRetiringDialog(dialog);
    dialog.remove();
    if (trigger?.isConnected) trigger.focus();
  };
  const close = async () => {
    if (saving || confirming) return;
    confirming = true;
    try {
      if (dirty && !(await confirmAction("修改尚未保存，确定放弃？", "放弃修改"))) return;
      dispose();
    } finally { confirming = false; }
  };
  dialog.querySelector("[data-close]").onclick = close;
  dialog.addEventListener("cancel", event => { event.preventDefault(); void close(); });
  bindDialogBackdrop(dialog, close);
  state.routeSignal?.addEventListener("abort", dispose, { once: true });
  document.body.append(dialog);
  dialog.showModal();
  try {
    const { task, config } = await api(`/tasks/${encodeURIComponent(id)}/retry-editor`, { signal: controller.signal });
    if (!dialog.isConnected) return;
    const label = task.action === "deploy" ? "部署" : "校验";
    dialog.querySelector("[data-body]").innerHTML = `<p>${esc(config.name)} · 当前 v${config.version}（原任务 v${task.config_version}）</p>${task.error ? `<details open><summary>上次失败原因</summary><pre class="task-error">${esc(diagnosticError(task.error))}</pre></details>` : ""}${config.agent_id ? `<p><a href="#live-config?agent=${encodeURIComponent(config.agent_id)}&engine=${encodeURIComponent(config.engine)}">打开预设表单修改 TLS、反代和端口</a></p>` : ""}<form data-retry-form><label>配置源码<textarea name="content" spellcheck="false" rows="20" required aria-label="重试配置源码" style="width:100%;font-family:var(--font-mono,monospace)">${esc(config.content)}</textarea></label><p role="alert" data-error hidden></p><div class="config-inbound-actions"><button type="button" class="button" data-cancel>取消</button><button type="submit" class="button primary">保存并重试${label}</button></div></form>`;
    const form = dialog.querySelector("form");
    const content = form.elements.namedItem("content");
    content.oninput = () => { dirty = content.value !== config.content; };
    form.querySelector("[data-cancel]").onclick = close;
    form.onsubmit = async event => {
      event.preventDefault();
      if (saving || !form.reportValidity()) return;
      saving = true;
      const button = form.querySelector('[type="submit"]');
      button.disabled = true;
      button.textContent = "正在保存并提交…";
      const errorBox = form.querySelector("[data-error]");
      errorBox.hidden = true;
      try {
        await api(`/tasks/${encodeURIComponent(id)}/edit-retry`, { method: "POST", body: JSON.stringify({ content: content.value, expected_version: config.version }), signal: controller.signal });
        dirty = false;
        notify(`已保存新版本并提交${label}任务`);
        dispose();
        await refresh();
      } catch (error) {
        if (!dialog.isConnected) return;
        errorBox.textContent = error.message;
        errorBox.hidden = false;
      } finally {
        saving = false;
        button.disabled = false;
        button.textContent = `保存并重试${label}`;
      }
    };
    content.focus();
  } catch (error) {
    if (!dialog.isConnected || controller.signal.aborted) return;
    dialog.querySelector("[data-body]").innerHTML = `<p role="alert">${esc(error.message)}</p>`;
  }
}
