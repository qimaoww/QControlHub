import { geoRegionDetails } from "./regions.js";

export function connectionDateInput(value) {
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

export function defaultConnectionFilters(now = Date.now()) {
  return { date: connectionDateInput(now).slice(0, 10), include_non_public: "" };
}

export function connectionQuery(filters, cursor = "") {
  const params = new URLSearchParams({ group_by: "ip" });
  for (const key of ["agent_id", "engine", "client_ip", "include_non_public"]) {
    if (filters[key]) params.set(key, filters[key]);
  }
  let since, until;
  if (filters.date !== undefined) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(filters.date)) throw new Error("请选择有效的查询日期");
    since = new Date(`${filters.date}T00:00:00`);
    if (!Number.isFinite(+since) || connectionDateInput(since).slice(0, 10) !== filters.date)
      throw new Error("请选择有效的查询日期");
    until = new Date(since);
    until.setDate(until.getDate() + 1);
  } else {
    since = new Date(filters.since);
    until = new Date(filters.until);
  }
  if (!Number.isFinite(+since) || !Number.isFinite(+until) || until <= since || until - since > 7 * 86400000)
    throw new Error("请选择有效的时间范围，单次最多查询 7 天");
  params.set("since", since.toISOString());
  params.set("until", until.toISOString());
  if (cursor) params.set("cursor", cursor);
  return params;
}

export function connectionSourceLabel(source) {
  if (!source.updated_at) return "等待内核日志";
  return { ok: "已读取内核日志", partial: "日志不完整", unavailable: "日志不可用" }[source.status] || "等待内核日志";
}

export function connectionSourceDetail(source) {
  if (!source.updated_at) return "面板尚无可读取的内核日志，请检查内核日志是否开启。";
  return "来源 IP 已从内核日志提取入库，可按日期查询历史记录。";
}

export function connectionLocationLabel(location) {
  if (location?.non_public) return "非公网";
  const region = geoRegionDetails(location?.country_code);
  if (!region) return "—";
  return region.code === "CN" && location.province ? `${region.name} · ${location.province}` : region.name;
}
