import { bindDialogBackdrop, closeRetiringDialog } from "./popup.js";
import { diagnosticError } from "./errors.js";
import { bindCodeEditors } from "./code-editor.js";

export async function openTaskEditRetry(ctx, id, refresh, trigger) {
  const { api, state, esc, notify, confirmAction } = ctx;
  const dialog = document.createElement("dialog");
  dialog.className = "config-inbound-dialog task-retry-dialog";
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
    if (saving || confirming) return false;
    confirming = true;
    try {
      if (dirty && !(await confirmAction("修改尚未保存，确定放弃？", "放弃修改"))) return false;
      dispose();
      return true;
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
    const language = config.engine === "mihomo" ? "YAML" : "JSON";
    const filename = language === "YAML" ? "config.yaml" : "config.json";
    dialog.querySelector("[data-body]").innerHTML = `
      <section class="task-retry-context"><div><strong>${esc(config.name)}</strong><small>当前 v${config.version} · 原任务 v${task.config_version}</small></div>${config.agent_id ? `<a class="button" data-preset-link href="#live-config?agent=${encodeURIComponent(config.agent_id)}&engine=${encodeURIComponent(config.engine)}">在预设表单中编辑</a>` : ""}</section>
      ${task.error ? `<details class="task-retry-error" open><summary>上次失败原因</summary><pre class="task-error">${esc(diagnosticError(task.error))}</pre></details>` : ""}
      <form class="task-retry-form" data-retry-form>
        <section class="code-workspace" data-code-editor data-code-language="${language}">
          <header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>${filename}</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status aria-live="polite">当前配置</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header>
          <div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input spellcheck="false" required aria-label="重试配置源码">${esc(config.content)}</textarea></div>
          <footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite">保存后仍会由节点内核校验。</span></span><div><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button><button class="button code-format" type="button" data-code-format>格式化配置</button></div></footer>
        </section>
        <p class="alert error" role="alert" data-error hidden></p>
        <footer class="task-retry-actions"><button type="button" class="button" data-cancel>取消</button><button type="submit" class="button primary">保存并重试${label}</button></footer>
      </form>`;
    const form = dialog.querySelector("form");
    const content = form.elements.namedItem("content");
    content.oninput = () => { dirty = content.value !== config.content; };
    form.querySelector("[data-cancel]").onclick = close;
    bindCodeEditors(dialog);
    dialog.querySelector("[data-preset-link]")?.addEventListener("click", async event => {
      event.preventDefault();
      const href = event.currentTarget.getAttribute("href");
      if (await close()) location.hash = href;
    });
    form.onsubmit = async event => {
      event.preventDefault();
      if (saving || !form.reportValidity()) return;
      saving = true;
      form.setAttribute("aria-busy", "true");
      content.readOnly = true;
      const controls = [...form.querySelectorAll("button")];
      controls.forEach(control => { control.disabled = true; });
      dialog.querySelector("[data-close]").disabled = true;
      const button = form.querySelector('[type="submit"]');
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
        errorBox.tabIndex = -1;
        errorBox.focus();
      } finally {
        saving = false;
        form.removeAttribute("aria-busy");
        content.readOnly = false;
        controls.forEach(control => { control.disabled = false; });
        form.querySelector("[data-code-reset]").disabled = !dirty;
        dialog.querySelector("[data-close]").disabled = false;
        button.textContent = `保存并重试${label}`;
      }
    };
    content.focus();
  } catch (error) {
    if (!dialog.isConnected || controller.signal.aborted) return;
    dialog.querySelector("[data-body]").innerHTML = `<p class="alert error" role="alert">${esc(error.message)}</p>`;
  }
}
