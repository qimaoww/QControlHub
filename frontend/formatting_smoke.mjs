import assert from "node:assert/strict";
import { bytes, actionName } from "./analytics/operations.js";
import { applyAppearance, formatDate } from "./analytics/appearance.js";

const normalize = value => value.replaceAll("\u00a0", " ");
for (const [value, expected] of [[0,"0 B"],[1024,"1 KB"],[1024 ** 2,"1 MB"],[-1,"0 B"],[null,"0 B"]])
  assert.equal(normalize(bytes(value)), expected);
// Reused formatters must preserve rounding, grouping and non-finite handling.
for (const value of [undefined, NaN, Infinity, -Infinity, 0.49, 999.9, 1023.99, 1024.1, 1024 ** 2 * 3.14159, Number.MAX_SAFE_INTEGER]) {
  const amount = Math.max(0, Number(value) || 0);
  const exponent = amount ? Math.max(0, Math.min(4, Math.floor(Math.log(amount) / Math.log(1024)))) : 0;
  assert.equal(bytes(value), `${(amount / 1024 ** exponent).toLocaleString("zh-CN", { maximumFractionDigits: exponent ? 1 : 0 })}\u00a0${["B", "KB", "MB", "GB", "TB"][exponent]}`);
}
const previousDocument = globalThis.document;
globalThis.document = { documentElement: { style: {} } };
const previousTZ = process.env.TZ;
try {
  const dates = ["2026-01-01T00:00:00Z", "2026-07-01T12:34:56Z", "2026-11-07T01:02:03Z"];
  for (const time_zone of ["Asia/Hong_Kong", "America/New_York", "UTC", "browser", "invalid-zone", "Asia/Hong_Kong"]) {
    applyAppearance({ time_zone });
    for (let repeat = 0; repeat < 3; repeat++) for (const value of dates) {
      let expected;
      try { expected = new Date(value).toLocaleString("zh-CN", { hour12: false, timeZone: time_zone === "browser" ? undefined : time_zone }); }
      catch { expected = new Date(value).toLocaleString("zh-CN", { hour12: false }); }
      assert.equal(formatDate(value), expected);
    }
  }
  for (const invalid of [null, undefined, "", "bad-date", "1970-01-01T00:00:00Z"]) assert.equal(formatDate(invalid), "—");
  applyAppearance({ time_zone: "browser" });
  for (const timezone of ["Asia/Hong_Kong", "America/New_York", "UTC"]) {
    process.env.TZ = timezone;
    for (const value of dates) assert.equal(formatDate(value), new Date(value).toLocaleString("zh-CN", { hour12: false }));
  }
  applyAppearance({ time_zone: "UTC", time_display: "absolute-relative" });
  const now = Date.now;
  Date.now = () => Date.parse("2026-11-07T01:00:00Z");
  try {
    for (const delta of [-86400, -3600, -60, -1, 0, 1, 60, 3600, 86400]) {
      const value = Date.now() + delta * 1000;
      const [seconds, unit] = Math.abs(delta) < 60 ? [1, "second"] : Math.abs(delta) < 3600 ? [60, "minute"] : Math.abs(delta) < 86400 ? [3600, "hour"] : [86400, "day"];
      assert.equal(formatDate(value), `${new Date(value).toLocaleString("zh-CN", { hour12: false, timeZone: "UTC" })} · ${new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" }).format(Math.round(delta / seconds), unit)}`);
    }
  } finally { Date.now = now; }
  applyAppearance({});
} finally {
  globalThis.document = previousDocument;
  if (previousTZ === undefined) delete process.env.TZ; else process.env.TZ = previousTZ;
}
assert.equal(actionName("read-managed-config"), "读取托管配置");
assert.equal(actionName("configure-tcp"), "配置 TCP");
assert.equal(actionName("new_action"), "new_action");
// Native appearance/date/account-reset cases live in shell_modules_smoke.
// DOM text escaping and service badges are exercised on actual native nodes;
// chart history/legend tests use the real SVG in node-task-lifecycle modes.
console.log("Native display scalar smoke passed");
