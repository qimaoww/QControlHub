export const GiB = 1024 ** 3;
export const userPermissions = [
  ["overview.read", "总览"], ["agents.read", "节点查看"], ["metrics.read", "性能指标"],
  ["panel-metrics.read", "面板主机指标"],
  ["agent-config.read", "配置查看"], ["agent-config.write", "配置编辑"], ["catalogs.read", "内核预设"],
  ["configs.read", "配置存档"], ["configs.write", "存档编辑"], ["configs.restore", "版本恢复"],
  ["configs.delete", "存档删除"], ["deployments.read", "部署记录"], ["tasks.read", "任务查看"],
  ["tasks.execute", "部署与执行"], ["client-access.read", "客户端"], ["traffic.read", "流量查看"],
  ["settings.read", "设置查看"], ["settings.manage", "个人同步 / 设置"],
  ["templates.read", "模板查看"], ["templates.write", "模板编辑"], ["templates.delete", "模板删除"],
  ["agents.manage", "自有主机 / 共享管理"], ["traffic.manage", "自有节点配额"], ["enrollment.manage", "添加自有节点"],
  ["core-logs.read", "自有主机日志"], ["audit.read", "个人审计记录"],
];
// Panel-host counters are global infrastructure data, not a user's node
// metrics. Only an explicit administrator grant may expose them to a user.
export const defaultPermissions = userPermissions.map(([permission]) => permission)
  .filter((permission) => permission !== "panel-metrics.read");

export function parseSharedPorts(value) {
  const parts = String(value ?? "").trim().replace(/(\d)\s*-\s*(?=\d)/g, "$1-").split(/[\s,，]+/).filter(Boolean);
  if (parts.length === 1 && parts[0] === "0") return [0];
  if (parts.includes("0")) throw new Error("0 表示端口无限制，不能与其他端口或范围混用。");
  const ports = new Set();
  for (const part of parts) {
    const match = /^(\d+)(?:-(\d+))?$/.exec(part);
    if (!match) throw new Error("请填写单个端口或范围，如 21000-21100，多个用逗号分隔。");
    const start = Number(match[1]), end = Number(match[2] ?? match[1]);
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 1 || end > 65535 || start > end)
      throw new Error("端口须为 1–65535，范围起始端口不能大于结束端口。");
    if (start <= 10086 && end >= 10085) throw new Error("10085、10086 是保留端口，不能分配。");
    // Bound expansion before allocating or iterating through the range.
    if (ports.size + end - start + 1 > 256) throw new Error("最多分配 256 个端口，范围按实际端口数量计算。");
    for (let port = start; port <= end; port++) {
      if (ports.has(port)) throw new Error("端口不能重复，范围不能重叠。");
      ports.add(port);
    }
  }
  return [...ports].sort((left, right) => left - right);
}

export function formatSharedPorts(ports) {
  const sorted = [...(ports || [])].sort((left, right) => left - right);
  const ranges = [];
  for (let index = 0; index < sorted.length; index++) {
    const start = sorted[index];
    let end = start;
    while (sorted[index + 1] === end + 1) end = sorted[++index];
    ranges.push(start === end ? String(start) : `${start}-${end}`);
  }
  return ranges.join(", ");
}

export function sharedPortsLabel(ports) {
  return ports?.length === 1 && ports[0] === 0 ? "无限制" : formatSharedPorts(ports) || "未分配";
}

export function selectedSharedEngines(row) {
  const engines = [...row.querySelectorAll('[name="engines"]:checked')].map(input => input.value);
  if (row.querySelector('[name="enabled"]').checked && !engines.length)
    throw new Error("请至少分配一个内核。");
  return engines;
}

export function sharedLimitBytes(value) {
  const text = String(value ?? "").trim();
  const gib = Number(text);
  const bytes = Math.round(gib * GiB);
  if (!text || !Number.isFinite(gib) || gib < 0 || !Number.isSafeInteger(bytes) || (gib > 0 && bytes === 0))
    throw new Error("请输入有效额度；0 表示不限量。");
  return bytes;
}

export function sharedLimitGiB(bytes) {
  const gib = Number(bytes || 0) / GiB;
  for (let places = 0; places <= 10; places++) {
    const value = Number(gib.toFixed(places));
    if (Math.round(value * GiB) === Number(bytes || 0)) return String(value);
  }
  return String(gib);
}

export function agentShareStatus(share) {
  if (share.reinvite || (!share.id && !share.user_id)) return "待发送";
  if (!share.enabled) return "已撤销";
  return { pending: "待接受", accepted: "已接受", rejected: "已拒绝" }[share.status] || "待接受";
}

export function mergeUserAllocation(shares, allocation) {
  // The API replaces the full allocation set. Keep every untouched share,
  // including revoked reservations, without replaying invitation actions.
  const rows = (shares || []).map(share => ({
    agent_id: share.agent_id, enabled: share.enabled,
    engines: [...(share.engines || [])], ports: [...(share.ports || [])],
    limit_bytes: share.limit_bytes || 0,
  }));
  const index = rows.findIndex(row => row.agent_id === allocation.agent_id);
  if (index < 0) rows.push(allocation);
  else rows[index] = { ...rows[index], ...allocation };
  return rows;
}

export const usage = (bytes) => `${(Number(bytes || 0) / GiB).toLocaleString("zh-CN", { maximumFractionDigits: 2 })} GiB`;
