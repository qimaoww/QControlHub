import { ipQualityEntries, ipQualityValue } from "./ip-quality-model.js";

export function createIPQualityReportView({ esc }) {
  const value = (item) => esc(ipQualityValue(item));
  const pairs = (entries) => `<dl class="ip-quality-report-fields">${entries.map(([key, item]) => `<div><dt>${esc(key)}</dt><dd>${value(item)}</dd></div>`).join("") || "<div><dt>数据</dt><dd>未知</dd></div>"}</dl>`;
  return (report) => {
    const info = report.Info || {}, mail = report.Mail || {}, blacklist = mail.DNSBlacklist || {};
    const ipv6 = String(report.Head?.IP || "").includes(":");
    return `<section class="ip-quality-report">
      <h4>${ipv6 ? "IPv6" : "IPv4"} · ${value(report.Head?.IP)}</h4>
      ${pairs([["ASN", info.ASN], ["运营商", info.Organization], ["地区", info.Region?.Name], ["城市", info.City?.Name], ["上游检测时间", report.Head?.Time], ["脚本版本", report.Head?.Version]])}
      <h5>风险评分（各数据源独立）</h5>${pairs(ipQualityEntries(report.Score))}
      <h5>IP 用途 / 类型</h5>${pairs(ipQualityEntries(report.Type?.Usage))}
      <h5>风险因子</h5>${ipQualityEntries(report.Factor).map(([factor, providers]) => `<details class="ip-quality-factor"><summary>${esc(({ CountryCode: "国家 / 地区", Proxy: "代理", Tor: "Tor", VPN: "VPN", Server: "数据中心", Abuser: "滥用", Robot: "机器人" })[factor] || factor)}</summary>${pairs(ipQualityEntries(providers))}</details>`).join("") || "<p>未知</p>"}
      <h5>流媒体 / AI 解锁</h5><div class="ip-quality-media">${ipQualityEntries(report.Media).map(([name, result]) => `<div><strong>${esc(name)}</strong><span>${value(result?.Status)}</span><small>${value(result?.Region)} · ${value(result?.Type)}</small></div>`).join("") || "<p>未知</p>"}</div>
      <h5>邮件连通性</h5>${pairs(ipQualityEntries(mail).filter(([name]) => name !== "DNSBlacklist"))}
      <h5>DNS 黑名单（不是 DNS 泄漏检测）</h5>${ipv6 ? "<p>未检测：当前上游仅查询 IPv4 DNS 黑名单。原始 JSON 中的 IPv6 黑名单数字可能沿用 IPv4 计数，不能作为本地址的检测结果。</p>" : pairs([["数据库总数", blacklist.Total], ["干净", blacklist.Clean], ["标记", blacklist.Marked], ["列入黑名单", blacklist.Blacklisted]])}
      <details class="ip-quality-raw"><summary>上游原始 JSON</summary><pre>${esc(JSON.stringify(report, null, 2))}</pre></details>
    </section>`;
  };
}
