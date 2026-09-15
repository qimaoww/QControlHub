const themeStorageKey = "qcontrolhub-color-theme";

export function createShellAppearance(state) {
function storedTheme() {
  try {
    const value = localStorage.getItem(themeStorageKey);
    return value === "light" || value === "dark" ? value : "";
  } catch {
    return "";
  }
}

const uiFontScaleOptions = new Set([90, 100, 110]);
function applyUIFontScale(value = state.data.settings?.ui_font_scale) {
  const percent = Number(value);
  const normalized = uiFontScaleOptions.has(percent) ? percent : 100;
  document.documentElement.dataset.uiFontScale = String(normalized);
  document.documentElement.style.setProperty("--ui-font-scale", String(normalized / 100));
}

function applyTheme(theme = storedTheme() || (matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark")) {
  applyUIFontScale();
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  const nextLabel = theme === "light" ? "切换为深色主题" : "切换为浅色主题";
  document.querySelectorAll("[data-theme-toggle]").forEach((button) => {
    button.setAttribute("aria-label", nextLabel);
    button.setAttribute("title", nextLabel);
    const icon = button.querySelector("[data-theme-icon]");
    if (icon) icon.textContent = theme === "light" ? "☾" : "☀";
  });
}

function toggleTheme() {
  const next =
    document.documentElement.dataset.theme === "light" ? "dark" : "light";
  try {
    localStorage.setItem(themeStorageKey, next);
  } catch {}
  applyTheme(next);
}

  return { applyUIFontScale, applyTheme, toggleTheme };
}
