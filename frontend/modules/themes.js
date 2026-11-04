import { themes, createThemePreference } from "./theme-registry.js";

// The classic theme owns this renderer. Independent themes consume the same
// preference model but render their own theme page.
export function installThemes({ api, state, shell, esc, notify }) {
  const session = () => state.session;
  let model = null;
  let account = null;
  return async function themePage() {
    if (account !== session()) {
      model?.reset();
      account = session();
      const bound = account;
      model = createThemePreference({ api, isCurrent: () => session() === bound });
    }
    const bound = account;
    const signal = state.routeSignal;
    const current = await model.read();
    if (session() !== bound || signal?.aborted) return;
    const active = "classic";
    shell(`<section class="theme-picker"><header><h1>主题</h1></header>
      <div class="theme-picker-grid">${themes.filter(theme => theme.available).map(theme => `<article class="theme-picker-card">
        <div class="theme-picker-preview" aria-hidden="true"><i></i><div><b></b><span></span><span></span><span></span><em></em></div></div>
        <div class="theme-picker-description"><h2>${esc(theme.name)}</h2><p>${esc(theme.description)}</p>
        <p class="theme-picker-current">${active === theme.id ? "当前主题" : ""}</p>
        <button type="button" class="button ${current.theme_id === theme.id ? "" : "primary"}" data-select-theme="${esc(theme.id)}" ${current.theme_id === theme.id ? "disabled" : ""}>${current.theme_id === theme.id ? "正在使用" : "使用此主题"}</button>
        </div>
      </article>`).join("")}</div>
      ${current.theme_id !== active ? '<p role="status">所选主题暂不可用，已使用经典主题。</p>' : ""}
      <p role="status" data-theme-status></p></section>`, "主题");
    document.querySelectorAll("[data-select-theme]").forEach(button => {
      button.onclick = async () => {
        if (button.disabled || session() !== bound || signal?.aborted) return;
        const buttons = [...document.querySelectorAll("[data-select-theme]")];
        const disabled = buttons.map(item => item.disabled);
        buttons.forEach(item => { item.disabled = true; });
        const status = document.querySelector("[data-theme-status]");
        status.textContent = "正在保存…";
        try {
          await model.save(button.dataset.selectTheme);
          if (session() !== bound || signal?.aborted) return;
          status.textContent = "已保存";
          // Preserve the authorized target hash and the existing cookie. This
          // page has no business editor, draft or other pending submission.
          location.reload();
        } catch (error) {
          if (session() !== bound || signal?.aborted || error.name === "AbortError") return;
          status.textContent = error.message;
          notify(error.message, "error");
          if (error.status === 409) {
            try { await model.read(); } catch { /* Keep the last valid choice. */ }
          }
          buttons.forEach((item, index) => { item.disabled = disabled[index]; });
        }
      };
    });
  };
}
