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

export function connectionSourceDetail(source) {
  if (!source.updated_at) return "升级 Agent 后等待首次心跳上报。";
  const details = String(source.detail || "").split("; ").filter(Boolean).map(detail => {
    if (detail === "no managed inbound listeners") return "未发现托管入站；请确认配置已部署。";
    if (detail === "ambiguous shared listening ports omitted") return "多个入站共用端口，无法确定归属的连接已跳过。";
    if (detail === "connection sample limit reached") return "本次达到 512 条采样上限，部分连接未纳入。";
    if (detail === "invalid connection sample") return "连接样本校验失败，请升级 Agent 后重试。";
    if (/conntrack/.test(detail)) {
      const family = detail.includes("ipv6") ? "IPv6 UDP" : detail.includes("ipv4") ? "IPv4 UDP" : "UDP";
      if (detail.includes("not installed")) return `${family} 未采集：缺少 conntrack，请通过 Agent 安装脚本更新以补齐依赖。`;
      if (detail.includes("permission denied")) return `${family} 未采集：缺少 CAP_NET_ADMIN 权限，请检查 Agent 服务或容器权限。`;
      if (detail.includes("timed out")) return `${family} 采集超时或中断，请检查节点负载后重试。`;
      if (detail.includes("output incomplete")) return `${family} 连接表读取不完整，已保留读取到的连接。`;
      return `${family} 采集受限：请检查 conntrack 是否安装、Agent 权限及内核连接跟踪支持。`;
    }
    const [scope, reason] = detail.split(": ");
    const reasons = {
      "configuration unavailable": "配置无法读取，请检查文件权限。",
      "listener discovery failed": "入站解析失败，请检查已部署配置。",
      "socket table unavailable": "系统连接表无法读取，请检查 /proc 挂载和权限。",
      "socket table incomplete": "系统连接表读取不完整，部分连接可能遗漏。",
      "local interfaces unavailable": "无法读取本机接口地址，请检查系统网络状态。",
    };
    return reasons[reason] ? `${scope}：${reasons[reason]}` : detail;
  });
  return details.join(" ") || (source.truncated ? "本次达到 512 条采样上限，部分连接未纳入。" : source.status === "ok" ? "已完成本次连接采样。" : "请升级 Agent 后查看详细采集原因。");
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
