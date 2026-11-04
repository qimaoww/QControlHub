import { themes, createThemePreference } from "../modules/theme-registry.js";
import { node, button, grid, stack, section, actions, badge, notice } from "./ui.js";

export async function themePage(ctx) {
  const model = createThemePreference({ api: (path, options) => ctx.api(path, {
    method: options?.method, body: options?.body ? JSON.parse(options.body) : undefined,
  }), isCurrent: ctx.current });
  await model.read();
  const status = notice("", "ok");
  const controls = [];
  const cards = themes.filter(theme => theme.available || theme.id === "analytics").map(theme => {
    const active = theme.id === "analytics";
    const preview = node("div", { className: `a-preview ${theme.id}`, "aria-hidden": "true" },
      node("span"), node("div", { className: "a-preview-main" }, [1, 2, 3, 4, 5].map(() => node("span"))));
    const select = button(active ? "正在使用" : "使用此主题", async () => {
      controls.forEach(control => { control.disabled = true; });
      status.className = "a-notice";
      status.textContent = "正在保存…";
      try {
        const saved = await ctx.switchTheme(() => model.save(theme.id));
        if (!ctx.current()) return;
        if (!saved) {
          status.textContent = "";
          controls.forEach(control => { control.disabled = control.dataset.themeID === "analytics"; });
          return;
        }
        status.textContent = "已保存";
        location.reload();
      } catch (error) {
        if (!ctx.current()) return;
        status.className = "a-notice error";
        status.textContent = error.message;
        if (error.status === 409) {
          try { await model.read(); } catch { /* Retry retains a valid baseline. */ }
        }
        controls.forEach(control => { control.disabled = control.dataset.themeID === "analytics"; });
      }
    }, { tone: active ? "" : "primary", disabled: active });
    select.dataset.themeID = theme.id;
    controls.push(select);
    return section(theme.name, node("div", { className: "a-card-body" }, stack(preview,
      node("p", { className: "a-muted" }, theme.description), actions(select))),
    active ? badge("当前主题") : null);
  });
  return stack(grid(cards), status);
}
