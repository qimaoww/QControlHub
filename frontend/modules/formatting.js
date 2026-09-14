// Shared display helpers for the application shell and route renderers.
export function createDisplayHelpers(state) {
  const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
      char
    ],
  );
  const date = (value) => {
    if (!value) return "-";
    const timeZone = state.data.settings?.time_zone;
    const options = timeZone && timeZone !== "browser" ? { timeZone } : undefined;
    return new Date(value).toLocaleString(undefined, options);
  };
  const bytes = (value) => {
    const n = Number(value || 0);
    if (!n) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
    return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
  };
  const label = (value) => String(value || "").replaceAll("_", " ");
  const actionName = (value) => ({ validate: "校验配置", deploy: "部署并重启", start: "启动服务", stop: "停止服务", restart: "重启服务", status: "查询状态", install: "安装或升级内核", "read-config": "读取可导入配置", "read-managed-config": "读取 QAgent 配置", "import-existing": "导入并迁移现有服务", "upgrade-agent": "升级 Agent", "enable-bbr": "启用系统 BBR", "disable-bbr": "关闭 BBR / 切换 CUBIC", "configure-tcp": "自定义 BBR / TCP 调优" })[value] || label(value);
  const statusName = (value) => ({ pending: "准备中", running: "执行中", succeeded: "成功", failed: "失败", canceled: "已取消" })[value] || label(value);
  const engineName = (value) => ({ mihomo: "Mihomo", xray: "Xray", "sing-box": "sing-box", "ss-rust": "ss-rust" })[value] || value;
  const serviceStatusName = (value) => ({ online: "在线", offline: "离线", pending: "准备中", running: "执行中", succeeded: "成功", active: "运行中", inactive: "已停止", activating: "启动中", deactivating: "停止中", failed: "失败" })[value] || value || "未知";
  const short = (value) => String(value || "").slice(0, 12);
  const statusTone = (value) => ["online", "succeeded", "active"].includes(value) ? "ok" : ["pending", "running", "activating", "deactivating"].includes(value) ? "warn" : ["offline", "failed", "inactive"].includes(value) ? "bad" : "muted";
  const percent = (used, total) => total > 0 ? Math.min(100, Math.max(0, (Number(used) / Number(total)) * 100)) : 0;
  const ago = (value) => {
    if (state.data.settings?.time_display === "absolute") return date(value);
    const elapsed = Date.now() - new Date(value).getTime();
    if (!Number.isFinite(elapsed)) return "未知";
    if (elapsed < 60_000) return "刚刚";
    if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
    if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
    return `${Math.floor(elapsed / 86_400_000)} 天前`;
  };
  const heartbeat = (value) => value ? `心跳 ${ago(value)}` : "尚未心跳";
  const conciseVersion = (engine, value) => {
    const match = String(value || "").match(/\b(?:v?\d+(?:\.\d+){1,5}(?:[-.][0-9A-Za-z]+)*|(?:alpha|beta|dev|rc|pre|nightly|stable)-?[0-9A-Za-z]{6,})/i);
    if (!match) return `${engineName(engine)} 内核版本未知`;
    return `${engineName(engine)} 内核 ${/^\d/.test(match[0]) ? `v${match[0]}` : match[0]}`;
  };
  const rate = (value) => `${bytes(value)}/s`;
  const trafficChart = (samples) => {
    if (!Array.isArray(samples) || samples.length < 2) return "";
    const width = 480;
    const height = 64;
    const pad = 3;
    const peak = Math.max(1, ...samples.flatMap((sample) => [sample.rx_rate_bps, sample.tx_rate_bps]));
    const points = (field) => samples.map((sample, index) => {
      const x = pad + (index * (width - 2 * pad)) / (samples.length - 1);
      const y = height - pad - (Number(sample[field] || 0) / peak) * (height - 2 * pad);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    }).join(" ");
    const latest = samples.at(-1);
    return `<svg class="metric-trend-chart" viewBox="0 0 ${width} ${height}" role="img" aria-label="最近 24 小时上下行速率趋势"><polyline class="trend-line trend-rx" points="${points("rx_rate_bps")}"></polyline><polyline class="trend-line trend-tx" points="${points("tx_rate_bps")}"></polyline></svg><dl class="metric-trend-legend"><div><i class="trend-dot trend-rx"></i><span>下载</span><b>${esc(rate(latest.rx_rate_bps))}</b></div><div><i class="trend-dot trend-tx"></i><span>上传</span><b>${esc(rate(latest.tx_rate_bps))}</b></div></dl>`;
  };
  const renderConfigDiff = (savedContent, deployedContent) => {
    const split = (value) => String(value || "").replaceAll("\r\n", "\n").replace(/\n$/, "").split("\n");
    const before = split(deployedContent);
    const after = split(savedContent);
    if (before.join("\n") === after.join("\n")) return "";
    let prefix = 0;
    while (prefix < before.length && prefix < after.length && before[prefix] === after[prefix]) prefix += 1;
    let suffix = 0;
    while (suffix < before.length - prefix && suffix < after.length - prefix && before[before.length - suffix - 1] === after[after.length - suffix - 1]) suffix += 1;
    const rows = [];
    before.slice(Math.max(0, prefix - 2), prefix).forEach((line) => rows.push(`<span class="diff-context"></span>${esc(line)}`));
    before.slice(prefix, before.length - suffix).forEach((line) => rows.push(`<span class="diff-remove">- </span>${esc(line)}`));
    after.slice(prefix, after.length - suffix).forEach((line) => rows.push(`<span class="diff-add">+ </span>${esc(line)}`));
    after.slice(after.length - suffix, after.length - Math.max(0, suffix - 2)).forEach((line) => rows.push(`<span class="diff-context"></span>${esc(line)}`));
    return `<pre class="config-diff" aria-label="配置差异">${rows.join("\n")}\n</pre>`;
  };
  return { esc, date, bytes, label, actionName, statusName, engineName, serviceStatusName, short, statusTone, percent, ago, heartbeat, conciseVersion, rate, trafficChart, renderConfigDiff };
}
