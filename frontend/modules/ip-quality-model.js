export const ipQualityFeature = "ip-quality-v2";
export const ipQualityLatest = "latest";

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

export function ipQualityAvailableDates(values = []) {
  const today = ipQualityToday();
  return [...new Set(values.filter((value) => validIPQualityDate(value) && value <= today))].sort().reverse();
}

export function ipQualityHistoryNeighbor(dates, selected, direction) {
  if (selected === ipQualityLatest) return direction < 0 ? dates[0] || "" : "";
  return direction < 0 ? dates.find((date) => date < selected) || ""
    : [...dates].reverse().find((date) => date > selected) || "";
}

export function nextIPQualityMonth(month, offset) {
  if (!validIPQualityDate(`${month}-01`)) return "";
  const date = new Date(`${month}-01T12:00:00`);
  date.setMonth(date.getMonth() + offset);
  return ipQualityToday(date).slice(0, 7);
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

export const ipQualityStatusNames = { succeeded: "已完成", pending: "等待执行", running: "检测中", failed: "检测失败", canceled: "已取消" };
export const ipQualityStatusTones = { succeeded: "good", pending: "pending", running: "pending", failed: "poor", canceled: "warning" };
const ipQualityDotTones = { succeeded: "ok", pending: "warn", running: "warn", failed: "bad", canceled: "bad" };

export function ipQualityStatusName(status) {
  return ipQualityStatusNames[status] || "状态未知";
}

export function ipQualityStatusTone(status) {
  return ipQualityStatusTones[status] || "unknown";
}

// The sidebar and the detail panel must agree on which node is shown, so one
// helper resolves both: every managed node in latest/today views, only nodes with a
// record on a historical day, and the first visible node when the remembered
// selection is no longer listed. The items carry no selection of their own, so
// the sidebar highlight always follows the live selected id.
export function ipQualityNodeList(agents = [], records = [], showAll, selectedID = "", missingNote = "", isLatest = false) {
  const recordMap = new Map(records.map((item) => [item.agent_id, item]));
  const visible = showAll ? agents : agents.filter((agent) => recordMap.has(agent.id));
  const selected = visible.find((agent) => agent.id === selectedID) || visible[0] || null;
  return {
    selected,
    items: visible.map((agent) => {
      const record = recordMap.get(agent.id);
      return {
        id: agent.id,
        name: agent.name,
        note: record ? ipQualityStatusName(record.status) : missingNote || ipQualityNodeNote(agent, isLatest),
        dot: ipQualityDotTones[record?.status] || "",
      };
    }),
  };
}

function ipQualityNodeNote(agent, isLatest) {
  if (!agent?.features?.includes(ipQualityFeature)) return "需升级 Agent";
  return isLatest ? "尚未检测" : "当天未检测";
}

export function ipQualityBlockReason(agent, record) {
  if (!agent?.features?.includes(ipQualityFeature)) return "请先升级 Agent";
  if (agent.status !== "online") return "节点离线";
  if (["pending", "running"].includes(record?.status)) return "检测任务进行中";
  return "";
}
