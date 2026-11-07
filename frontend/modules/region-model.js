// Preserve the panel's existing display policy for Taiwan in every view.
export function geoRegionDetails(value) {
  const code = String(value || "").trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return null;
  const flagCode = code === "TW" ? "CN" : code;
  let name = code === "TW" ? "中国台湾" : code;
  if (code !== "TW") {
    try {
      name = new Intl.DisplayNames(["zh-CN"], { type: "region" }).of(code) || code;
    } catch { /* Keep the code in browsers without DisplayNames. */ }
  }
  return { code, flagCode, name };
}

