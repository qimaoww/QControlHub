import { bindEvent } from "./refresh.js";
import { bindDialogBackdrop, closeRetiringDialog } from "./popup.js";

// Reading the deployed file never replaces the draft in the configuration
// editor. Runtime status and lifecycle controls belong to node management.
export function bindSharedInstanceRead({state, esc, engineName, notify, bindCodeEditors}, {agent, engine, root, requestSnapshot}) {
  const button = root?.querySelector("[data-instance-read]");
  if (agent.can_manage !== false || !button) return;
  const data = state.data, epoch = state.navigationEpoch;
  const current = () => data === state.data && epoch === state.navigationEpoch && root.isConnected;
  let busy = false, dialog;
  button.disabled = agent.status !== "online" || !agent.runtime?.[engine]?.instance?.deployed_version ||
    !(agent.features || []).includes("shared-core-lifecycle-v1");
  bindEvent(button, "click", async () => {
    if (busy || button.disabled || !current()) return;
    busy = true; button.disabled = true;
    try {
      const snapshot = await requestSnapshot(agent, engine, "read-managed-config", {isCurrent:current});
      if (!snapshot || !current()) return;
      dialog = document.createElement("dialog");
      dialog.className = "config-inbound-dialog";
      dialog.setAttribute("aria-label", "本人实例实际配置");
      dialog.innerHTML = `<header class="config-inbound-heading"><div><h2>已部署实例文件</h2><p>${esc(engineName(engine))} · 只读</p></div><button class="config-access-close" type="button" aria-label="关闭弹窗">×</button></header><div class="config-inbound-body"><section class="code-workspace" data-code-editor><div class="code-editor-frame"><aside class="code-gutter" data-line-numbers aria-hidden="true"></aside><textarea class="code-editor-input" data-code-input readonly aria-label="本人实例实际配置" spellcheck="false"></textarea></div></section></div>`;
      dialog.querySelector("textarea").value = snapshot.content;
      bindCodeEditors?.(dialog);
      const close = () => { closeRetiringDialog(dialog); dialog.remove(); dialog = null; };
      bindEvent(dialog.querySelector("button"), "click", close);
      bindEvent(dialog, "cancel", event => { event.preventDefault(); close(); });
      bindDialogBackdrop(dialog, close);
      document.body.append(dialog); dialog.showModal();
    } catch (error) { if (current()) notify(error.message, "error"); }
    finally { busy = false; if (current()) button.disabled = false; }
  });
  state.routeSignal?.addEventListener("abort", () => { if (dialog) { dialog.close(); dialog.remove(); dialog = null; } }, {once:true});
}
