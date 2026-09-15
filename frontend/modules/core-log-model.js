const visibleLevel = (level) => {
  if (["error", "critical"].includes(level)) return "error";
  if (level === "warning") return "warning";
  return "info";
};

export function filterCoreLogEntries(entries, filters = {}) {
  const keyword = String(filters.q || "").trim().toLowerCase();
  return (entries || []).filter((entry) => {
    if (filters.engine && entry.engine !== filters.engine) return false;
    if (filters.level && visibleLevel(entry.level) !== filters.level) return false;
    return !keyword || String(entry.message || "").toLowerCase().includes(keyword);
  });
}

export function coreLogFilterCounts(entries, engines = []) {
  const engine = Object.fromEntries(engines.map((value) => [value, 0]));
  const level = { info: 0, warning: 0, error: 0 };
  (entries || []).forEach((entry) => {
    if (Object.hasOwn(engine, entry.engine)) engine[entry.engine] += 1;
    level[visibleLevel(entry.level)] += 1;
  });
  return { total: (entries || []).length, engine, level };
}

export const levelName = (value) =>
    ({ debug: "调试", info: "信息", warning: "警告", error: "错误", critical: "严重" })[value] || value;
export const storagePolicyName = (value) =>
    ({
      debug: "保存全部级别",
      info: "保存信息及以上",
      warning: "保存警告及以上",
      error: "保存错误及以上",
      critical: "仅保存严重错误",
      off: "已停止保存新日志",
    })[value] || "保存策略不可见";
