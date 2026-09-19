export const ipQualityFeature = "ip-quality-v1";

export function ipQualityToday(now = new Date()) {
  return [now.getFullYear(), String(now.getMonth() + 1).padStart(2, "0"), String(now.getDate()).padStart(2, "0")].join("-");
}

export function validIPQualityDate(value) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value || "")) return false;
  const date = new Date(`${value}T12:00:00`);
  return Number.isFinite(date.getTime()) && ipQualityToday(date) === value;
}

export function nextIPQualityDay(value, offset) {
  if (!validIPQualityDate(value)) return "";
  const date = new Date(`${value}T12:00:00`);
  date.setDate(date.getDate() + offset);
  return ipQualityToday(date);
}

export function ipQualityValue(value) {
  if (value == null || typeof value === "object") return "未知";
  if (value === true) return "是";
  if (value === false) return "否";
  const text = String(value).trim();
  return !text || /^(null|undefined|n\/a)$/i.test(text) ? "未知" : text;
}

export function ipQualityEntries(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? Object.entries(value) : [];
}

export function ipQualitySummary(records = []) {
  return {
    total: records.length,
    succeeded: records.filter((record) => record.status === "succeeded").length,
    running: records.filter((record) => ["pending", "running"].includes(record.status)).length,
    failed: records.filter((record) => ["failed", "canceled"].includes(record.status)).length,
  };
}

export function ipQualityBlockReason(agent, record) {
  if (!agent?.features?.includes(ipQualityFeature)) return "请先升级 Agent";
  if (agent.status !== "online") return "节点离线";
  if (["pending", "running"].includes(record?.status)) return "检测任务进行中";
  return "";
}
