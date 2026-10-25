import { setVisible } from "./presence-motion.js";
import { createAccessControlController } from "./access-control-controller.js";

// The server identifies saved restrictions by node, engine, tag and port.
// Never infer identity from a display label or apply them over a source draft.
export async function bindConfigRestrictions(ctx) {
  const { form, files, agent, engine, saved, sourceMode, state, api, notify, onSaved, inbounds } = ctx;
  if (!form || sourceMode === "import") return;
  const input = form.querySelector("[data-code-input]");
  if (!input) return;
  const baseline = input.value, data = state.data, epoch = state.navigationEpoch;
  const current = () => data === state.data && epoch === state.navigationEpoch && form.isConnected &&
    state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine && state.data.liveConfigSource === sourceMode;
  const access = createAccessControlController(ctx);
  const navigation = form.querySelector("[data-config-object-tools]");
  let busy = false;
  if (!navigation) return;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "button config-access-trigger";
  button.dataset.configAccessOpen = "";
  button.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 4.5 6v5c0 4.6 3.1 7.9 7.5 10 4.4-2.1 7.5-5.4 7.5-10V6zM8.5 12h7"/></svg>限制';
  button.setAttribute("aria-haspopup", "dialog");
  button.setAttribute("aria-expanded", "false");
  const target = () => files?.selectedInbound();
  const dirty = () => files ? files.dirty() : input.value !== baseline;
  const update = () => {
    setVisible(button, target());
    button.disabled = busy || !target();
    button.title = target() ? "设置当前入站的访问限制" : "请先选择一个入站配置";
  };
  if (!current()) return;
  navigation.prepend(button);
  input.addEventListener("input", update);
  input.addEventListener("config-selection", update);
  update();
  button.onclick = async () => {
    if (busy || !target() || !current()) return;
    if (dirty()) { notify("配置源码有未保存修改，请先保存，再设置入站限制。", "error"); return; }
    const chosen = { ...target() };
    busy = true; update();
    try {
      const entries = await api("/access-controls");
      if (!current()) return;
      // Recheck after the read: editing/navigation may have continued meanwhile.
      if (dirty() || target()?.tag !== chosen.tag || target()?.port !== chosen.port) return;
      const entry = entries.find(item => item.agent_id === agent.id && item.engine === engine &&
        item.tag === chosen.tag && item.port === chosen.port);
      if (!entry || !saved?.version || entry.config_version !== saved.version) {
        notify("入站或配置版本已变化，请重新读取并保存配置后再设置限制。", "error"); return;
      }
      access.open(entry, { trigger: button, isCurrent: current, onSaved: result => onSaved(result, chosen) });
    } catch (error) {
      if (current() && error.name !== "AbortError") notify(error.message, "error");
    } finally { busy = false; if (current()) update(); }
  };
  return {
    selectedCommon: () => files?.selectedCommon(),
    selectedInbound: target,
    selectInbound(tag, port) { files?.selectInbound(tag, port);
    },
  };
}
