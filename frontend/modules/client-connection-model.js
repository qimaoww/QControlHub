import { geoRegionDetails } from "./regions.js";

export function connectionDateInput(value) {
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

export function defaultConnectionFilters(now = Date.now()) {
  return { since: connectionDateInput(now - 86400000), until: connectionDateInput(now + 60000), bucket: "hour", include_non_public: "" };
}

export function connectionQuery(filters, before = "") {
  const params = new URLSearchParams();
  for (const key of ["agent_id", "engine", "protocol", "inbound", "transport", "client_ip", "port", "bucket", "include_non_public"]) {
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

export function connectionSourceLabel(source, now = Date.now()) {
  if (!source.updated_at) return "尚未上报 · 请升级 Agent";
  if (now - new Date(source.updated_at).getTime() > 90000) return "采集已过期 · 节点可能离线";
  if (source.truncated) return "样本已截断";
  return { ok: "采集正常", partial: "部分采集", unavailable: "采集不可用" }[source.status] || "状态未知";
}

export function connectionAddress(ip, port) {
  return `${String(ip).includes(":") ? `[${ip}]` : ip}:${port}`;
}

export function connectionLocationLabel(location) {
  if (location?.non_public) return "非公网";
  const region = geoRegionDetails(location?.country_code);
  if (!region) return "—";
  return region.code === "CN" && location.province ? `${region.name} · ${location.province}` : region.name;
}
