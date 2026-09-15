import { bindEvent } from "./refresh.js";
export function createAccessControlDialog({ state, engineName, confirmAction }, { card, bind }) {
  return (entry, { trigger, isCurrent, onSaved }) => {
    const dialog = document.createElement("dialog");
    dialog.className = "config-access-dialog";
    dialog.setAttribute("aria-labelledby", "config-access-title");
    const header = document.createElement("header");
    header.className = "config-access-heading";
    header.innerHTML = '<div><h2 id="config-access-title">入站访问限制</h2><p></p></div><button type="button" class="config-access-close" data-access-close aria-label="关闭限制弹窗">×</button>';
    header.querySelector("p").textContent = entry.agent_name + " / " + engineName(entry.engine) + " / " + entry.tag + " · :" + entry.port;
    const body = document.createElement("div");
    body.innerHTML = card(entry);
    dialog.append(header, body);
    const note = document.createElement("footer");
    note.className = "config-access-note";
    note.innerHTML = '<span>修改后需保存并部署，规则才会在节点生效。</span><a href="#settings-cnip">CN IP 数据源设置 →</a>';
    dialog.append(note);
    bindEvent(note.querySelector("a"), "click", async event => {
      event.preventDefault();
      await close();
      if (!dialog.isConnected) location.hash = "#settings-cnip";
    });
    document.body.append(dialog);
    const signal = state.routeSignal;
    let confirming = false;
    const cleanup = () => {
      signal?.removeEventListener("abort", dispose);
      dialog.remove();
      if (trigger.isConnected) { trigger.setAttribute("aria-expanded", "false"); trigger.focus(); }
    };
    const dispose = () => { dialog.close(); cleanup(); };
    const close = async () => {
      if (confirming || dialog.querySelector('[data-access-control-form][data-busy="1"]')) return;
      confirming = true;
      try {
        if (dialog.querySelector(".is-dirty") && !(await confirmAction("当前入站有未保存的访问限制修改，确定放弃并关闭？", "放弃更改"))) return;
        dispose();
      } finally { confirming = false; }
    };
    bindEvent(dialog.querySelector("[data-access-close]"), "click", close);
    bindEvent(dialog, "cancel", event => { event.preventDefault(); void close(); });
    bindEvent(dialog, "click", event => {
      const rect = dialog.getBoundingClientRect();
      if (event.target === dialog && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) void close();
    });
    bindEvent(dialog, "close", cleanup);
    signal?.addEventListener("abort", dispose, { once: true });
    bind([entry], dialog, async result => { dispose(); await onSaved(result); }, () => isCurrent() && dialog.isConnected);
    trigger.setAttribute("aria-expanded", "true");
    dialog.showModal();
  };

}
