// Appearance belongs to this runtime and is reset with its authenticated session.
let preferences = {};
export function applyAppearance(settings = {}) {
  preferences = settings;
  const scale = [90, 100, 110].includes(Number(settings.ui_font_scale)) ? Number(settings.ui_font_scale) : 100;
  document.documentElement.style.fontSize = `${scale}%`;
}
export function formatDate(value) {
  const timestamp = new Date(value).getTime();
  if (!value || !Number.isFinite(timestamp) || timestamp <= 0) return "—";
  const timeZone = preferences.time_zone && preferences.time_zone !== "browser" ? preferences.time_zone : undefined;
  let absolute;
  try { absolute = new Date(timestamp).toLocaleString("zh-CN", { hour12: false, timeZone }); }
  catch { absolute = new Date(timestamp).toLocaleString("zh-CN", { hour12: false }); }
  if (preferences.time_display !== "absolute-relative") return absolute;
  const elapsed = (timestamp - Date.now()) / 1000;
  const [seconds, unit] = Math.abs(elapsed) < 60 ? [1, "second"] : Math.abs(elapsed) < 3600 ? [60, "minute"] : Math.abs(elapsed) < 86400 ? [3600, "hour"] : [86400, "day"];
  return `${absolute} · ${new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" }).format(Math.round(elapsed / seconds), unit)}`;
}
