import { installAccessControl } from "./access-control.js";

// The server identifies saved restrictions by node, engine, tag and port.
// Never infer identity from a display label or apply them over a source draft.
export async function bindConfigRestrictions(ctx) {
  const { form, files, agent, engine, saved, sourceMode, state, api, notify, onSaved } = ctx;
  if (!form || sourceMode === "import") return;
  const input = form.querySelector("[data-code-input]");
  if (!input) return;
  const baseline = input.value, data = state.data, epoch = state.navigationEpoch;
  const current = () => data === state.data && epoch === state.navigationEpoch && form.isConnected &&
    state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine;
  const access = installAccessControl(ctx);
  let navigation = form.querySelector(".config-file-buttons"), selected = null, busy = false;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "button config-access-trigger";
  button.dataset.configAccessOpen = "";
  button.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 4.5 6v5c0 4.6 3.1 7.9 7.5 10 4.4-2.1 7.5-5.4 7.5-10V6zM8.5 12h7"/></svg>限制';
  button.setAttribute("aria-haspopup", "dialog");
  button.setAttribute("aria-expanded", "false");
  const target = () => files ? files.selectedInbound() : selected;
  const dirty = () => files ? files.dirty() : input.value !== baseline;
  const update = () => {
    button.disabled = busy || !target();
    button.title = target() ? "设置当前入站的访问限制" : "请先选择一个入站配置";
  };
  if (!navigation) {
    navigation = document.createElement("nav");
    navigation.className = "config-file-buttons";
    navigation.setAttribute("aria-label", "选择限制入站");
    form.querySelector(".code-editor-toolbar").before(navigation);
    // YAML and native ssserver configs remain whole-file editors. Use the
    // server's parsed saved inbounds for their restriction selection.
    try {
      const entries = await api("/access-controls");
      if (!current()) return;
      entries.filter(entry => entry.agent_id === agent.id && entry.engine === engine).forEach(entry => {
        const tab = document.createElement("button");
        tab.type = "button"; tab.className = "config-file-button";
        tab.dataset.accessInbound = entry.tag;
        const name = document.createElement("b"), detail = document.createElement("small");
        name.textContent = entry.tag; detail.textContent = (entry.kind || engine) + " · " + entry.port;
        tab.append(name, detail);
        tab.setAttribute("aria-pressed", "false");
        tab.onclick = () => {
          selected = entry;
          navigation.querySelectorAll("[data-access-inbound]").forEach(other => {
            other.classList.toggle("active", other === tab);
            other.setAttribute("aria-pressed", String(other === tab));
          });
          update();
        };
        navigation.append(tab);
      });
    } catch (error) {
      if (current() && error.name !== "AbortError") notify("读取入站限制失败：" + error.message, "error");
    }
  }
  if (!current()) return;
  navigation.append(button);
  input.addEventListener("input", update);
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
}
