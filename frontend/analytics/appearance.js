// Appearance belongs to this runtime and is reset with its authenticated session.
let preferences = {};
let dateFormatter, datePreference, dateBrowserZone, dateOffset, relativeFormatter;
const dateFields = { year: "numeric", month: "numeric", day: "numeric", hour: "numeric", minute: "numeric", second: "numeric", hour12: false };
export function applyAppearance(settings = {}) {
  preferences = settings;
  dateFormatter = relativeFormatter = undefined;
  const scale = [90, 100, 110].includes(Number(settings.ui_font_scale)) ? Number(settings.ui_font_scale) : 100;
  document.documentElement.style.fontSize = `${scale}%`;
}
export function formatDate(value) {
  const timestamp = new Date(value).getTime();
  if (!value || !Number.isFinite(timestamp) || timestamp <= 0) return "—";
  const timeZone = preferences.time_zone && preferences.time_zone !== "browser" ? preferences.time_zone : undefined;
  // Keep one formatter for the active preference. The native offset detects
  // browser time-zone changes without constructing another Intl formatter on
  // every row; the display contains no zone name, so equal offsets are equal.
  const offset = new Date(timestamp).getTimezoneOffset();
  if (!dateFormatter || datePreference !== timeZone || dateBrowserZone && dateOffset !== offset) {
    dateBrowserZone = !timeZone;
    try { dateFormatter = new Intl.DateTimeFormat("zh-CN", { ...dateFields, timeZone }); }
    catch { dateFormatter = new Intl.DateTimeFormat("zh-CN", dateFields); dateBrowserZone = true; }
    datePreference = timeZone; dateOffset = offset;
  }
  const absolute = dateFormatter.format(timestamp);
  if (preferences.time_display !== "absolute-relative") return absolute;
  const elapsed = (timestamp - Date.now()) / 1000;
  const [seconds, unit] = Math.abs(elapsed) < 60 ? [1, "second"] : Math.abs(elapsed) < 3600 ? [60, "minute"] : Math.abs(elapsed) < 86400 ? [3600, "hour"] : [86400, "day"];
  relativeFormatter ||= new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });
  return `${absolute} · ${relativeFormatter.format(Math.round(elapsed / seconds), unit)}`;
}
