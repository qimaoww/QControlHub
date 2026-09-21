import { geoRegionDetails } from "./regions.js";

export function connectionDateInput(value) {
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

export function defaultConnectionFilters(now = Date.now()) {
  return { since: connectionDateInput(now - 86400000), until: connectionDateInput(now + 60000), include_non_public: "" };
}

export function connectionQuery(filters, before = "") {
  const params = new URLSearchParams({ group_by: "ip" });
  for (const key of ["agent_id", "engine", "client_ip", "include_non_public"]) {
    if (filters[key]) params.set(key, filters[key]);
  }
  const since = new Date(filters.since), until = new Date(filters.until);
  if (!Number.isFinite(+since) || !Number.isFinite(+until) || until <= since || until - since > 7 * 86400000)
    throw new Error("请选择有效的时间范围，单次最多查询 7 天");
  params.set("since", since.toISOString());
  params.set("until", until.toISOString());
  if (before) params.set("before", before);
  return params;
}

export function connectionSourceLabel(source) {
  if (!source.updated_at) return "等待内核日志";
  return { ok: "已读取内核日志", partial: "日志不完整", unavailable: "日志不可用" }[source.status] || "等待内核日志";
}

export function connectionSourceDetail(source) {
  if (!source.updated_at) return "面板尚无可读取的内核日志，请检查内核日志是否开启。";
  return "从面板保存的内核日志提取客户端来源 IP，不记录访问目标 IP。";
}

export function connectionLocationLabel(location) {
  if (location?.non_public) return "非公网";
  const region = geoRegionDetails(location?.country_code);
  if (!region) return "—";
  return region.code === "CN" && location.province ? `${region.name} · ${location.province}` : region.name;
}
