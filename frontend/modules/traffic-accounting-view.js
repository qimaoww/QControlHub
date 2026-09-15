export function renderTrafficAccounting(policy, esc, bytes) {
  if (policy.shared_quota && policy.enforcement_available !== false && !policy.enforcement_error)
    return '<span class="settings-hint">共享额度 · 监听端口收发</span>';
  const accounting = policy.accounting;
  const dual = ["core-api", "nft-dual"].includes(accounting?.source);
  const raw = String(policy.enforcement_error || "").replace(/^[;\s]+/, "");
  const scopeOnly = /^dual accounting unavailable: single-protocol policy uses listener-only accounting; (?:dual accounting requires TCP\+UDP because core counters and outbound marks are shared|exclusive listener transport could not be verified)$/.test(raw);
  const failed = policy.enforcement_available === false || (raw && !scopeOnly);
  const partialEgress = policy.engine === "mihomo" && accounting?.source === "nft-dual";
  const tone = failed ? "bad" : dual && !partialEgress ? "ok" : "limited";
  const title = failed ? "统计异常" : partialEgress ? "双链路 · 范围受限" : dual ? "双链路统计" : "仅监听端口";
  const outboundTagHint = raw.endsWith("outbound tags must be present and unique")
    ? "出口缺少标签或标签重复，独立出口统计未启用。新版 Agent 可自动补齐缺失标签；重复标签需修正配置，不能直接合并计数。"
    : /outbounds\[\d+\]\.tag duplicates an earlier outbound;/.test(raw)
      ? "多个出口使用了相同标签，无法确定路由与统计归属。请为这些出口设置不同标签，并同步修改对应路由。"
      : "";
  const hint = failed
    ? outboundTagHint || (raw ? "请查看诊断信息，确认统计是否完整。" : "Agent 报告监控不可用，暂未提供详细诊断。")
    : scopeOnly ? "暂未确认入站协议独占，当前仅统计入口收发。"
    : !dual ? "独立出口统计尚未生效，当前仅计入口。"
    : accounting.source === "core-api" ? "内核计数 · 包含入站协议开销" : "网络层计数 · 包含包头及重传";
  const legs = dual ? `<dl class="traffic-accounting-legs">${[
    ["客户端 → 代理", accounting.client_received],
    ["代理 → 客户端", accounting.client_sent],
    ["目标 → 代理", accounting.target_received],
    ["代理 → 目标", accounting.target_sent],
  ].map(([label, value]) => `<div><dt>${label}</dt><dd>${bytes(value)}</dd></div>`).join("")}</dl>` : "";
  const content = `<section class="traffic-accounting-panel ${tone}" aria-label="统计口径">
    <div class="traffic-accounting-heading"><span class="traffic-accounting-badge"><i aria-hidden="true"></i>${title}</span><span class="traffic-accounting-caption">${partialEgress ? "入口 + 已标记出口" : dual ? "入口 + 出口" : "入口收发"}</span></div>
    <p class="traffic-accounting-hint">${hint}</p>
    ${partialEgress ? '<p class="traffic-accounting-note">Mihomo 不为回环（127.0.0.1、::1）等非全局单播目标设置出口标记。这些连接仍统计入口收发，但出口未计入；当前无法可靠补算，也不按入口流量翻倍估算。此提示说明能力限制，不表示当前一定存在漏计连接。</p>' : ""}
    ${dual || raw ? `<details class="traffic-accounting-details">
      <summary>${failed ? "查看诊断" : dual ? "查看链路明细" : "查看统计说明"}<span aria-hidden="true">⌄</span></summary>
      <div class="traffic-accounting-detail-body">${dual ? `<p>本计量代次累计，不等同于本月总量</p>${legs}` : ""}${raw ? `<p>Agent 原始诊断</p><pre>${esc(raw)}</pre>` : ""}</div>
    </details>` : ""}
  </section>`;
  return `<button class="button small traffic-status-button ${tone}" type="button" data-traffic-status-open="${esc(policy.id)}" aria-haspopup="dialog"><i aria-hidden="true"></i>状态${failed ? "异常" : ""}</button><dialog class="traffic-status-dialog" data-refresh-live data-traffic-status-dialog="${esc(policy.id)}" aria-label="统计状态"><header><div><p class="eyebrow">统计状态</p><h2>${esc(policy.name || "端口统计")}</h2></div><button class="deploy-command-close" type="button" data-traffic-status-close aria-label="关闭统计状态">×</button></header>${content}</dialog>`;
}
