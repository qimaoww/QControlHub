export const systemBBRFeature = "system-bbr-v1";
export const systemBBRActions = ["enable-bbr", "disable-bbr", "configure-tcp"];

export function validateTCPSelection(input, rules) {
  if (!Object.keys(input || {}).length) throw new Error("请至少勾选一个需要保存的参数");
  return Object.fromEntries(Object.entries(input).map(([key, raw]) => {
    const rule = rules.find((entry) => entry.key === key);
    const value = String(raw).trim().replace(/\s+/g, " ");
    if (!rule || new TextEncoder().encode(String(raw)).length > 100 || /[\r\n\x00]/.test(raw)) throw new Error(`不支持的参数或取值：${key}`);
    if (rule.choices?.length) {
      if (!rule.choices.includes(value)) throw new Error(`${rule.label}：请选择支持的选项`);
      return [key, value];
    }
    const values = value.split(" ");
    if (values.length !== (rule.tuple ? 3 : 1) || values.some((part, index) => !/^\d+$/.test(part) || Number(part) < (rule.min || 0) || Number(part) > rule.max || (index > 0 && Number(part) < Number(values[index - 1]))))
      throw new Error(`${rule.label}：请输入 ${rule.min || 0}–${rule.max} 范围内的${rule.tuple ? "三个递增整数" : "整数"}`);
    return [key, values.map(Number).join(" ")];
  }));
}

export function systemBBRState(agent, now = Date.now()) {
  const status = agent.metrics?.bbr;
  if (!(agent.features || []).includes(systemBBRFeature))
    return { text: "需升级 Agent", tone: "", controllable: false };
  if (agent.status !== "online")
    return { text: "节点离线 · 历史状态", tone: "", controllable: false };
  const collected = Date.parse(status?.collected_at || "");
  if (!Number.isFinite(collected) || now - collected > 90_000 || collected > now + 30_000)
    return { text: "等待最新状态", tone: "warn", controllable: false };
  if (!status?.available)
    return { text: "系统参数不可用", tone: "warn", controllable: false };
  return {
    text: /^bbr/.test(status.congestion_control) ? `BBR 已启用 · ${status.congestion_control}` : `当前默认 ${status.congestion_control || "未知"}`,
    tone: /^bbr/.test(status.congestion_control) ? "ok" : "",
    controllable: status.persistence !== "error",
  };
}

export const taskLabel = (status) => ({ pending: "等待执行", running: "执行中", succeeded: "执行成功", failed: "执行失败", canceled: "已取消", submitted: "已提交（无任务查看权限）" })[status] || status;
export const actionLabel = (action) => ({ "enable-bbr": "启用 BBR", "disable-bbr": "切换 CUBIC", "configure-tcp": "自定义 TCP 调优" })[action] || action;

export const dialogID = (agentID, kind) => `bbr-${kind}-${agentID}`;
