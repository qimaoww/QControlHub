import { redactDiagnostic } from "./errors.js";
import { recordDate } from "./record-display.js";

const stages = {preparing:"准备申请",continuing:"核对 DNS 验证记录",order_created:"准备域名验证",challenges_ready:"核对验证记录",dns_create_intent:"准备 DNS 记录",dns_created:"核对 DNS 验证记录",http_preflight:"检查 HTTP 入口",validating:"验证域名",finalizing:"签发证书"};

export function certificateOrderDiagnostic(value) {
  return redactDiagnostic(value)
    .replace(/((?:token|eab_key|access_key(?:_id|_secret)?|secret(?:_id|_key)?)\s*["']?\s*[:=]\s*)["']?[^\s,"';}]+/gi, "$1[已脱敏]")
    .replace(/\b(Bearer|Basic)\s+[^\s,;]+/gi, "$1 [已脱敏]")
    .replace(/https?:\/\/[^\s<>]+/gi, "[服务地址已脱敏]");
}

function failureMessage(value, stage) {
  const safe = certificateOrderDiagnostic(value);
  if (/读取 ACME 申请账号失败/.test(safe)) return "读取 ACME 申请账号失败，请检查本人申请账号是否仍可用。";
  if (/读取 DNS 账号失败/.test(safe)) return "读取 DNS 账号失败，请检查本人 DNS 账号是否仍可用。";
  if (/not found/i.test(safe)) return `${stages[stage] || "申请处理"}失败，暂无法确认具体资源或原因，请刷新后核对申请详情。`;
  if (/\p{Script=Han}/u.test(safe)) return safe;
  if (/deadline exceeded|timed out|timeout/i.test(safe)) return `${stages[stage] || "申请处理"}超时，请检查网络及验证入口。`;
  return `${stages[stage] || "申请处理"}失败，请查看诊断信息后重试。`;
}

export function certificateOrderPresentation(order, date) {
  const retry = order.status === "queued" && order.stage === "retry";
  const cleanup = order.stage === "cleanup";
  const active = ["running","validating"].includes(order.status);
  const attempt = Math.max(0, Number(order.attempt) || 0);
  const label = retry ? "等待自动重试" : {queued:"等待处理",running:"正在执行",validating:"正在验证",waiting_dns:"等待人工 DNS 操作",succeeded:"签发成功",failed:"申请失败",canceling:"取消中",canceled:"已取消"}[order.status] || "状态待确认";
  const execution = !attempt ? "尚未执行" : active ? attempt === 1 ? "首次执行中" : `第 ${attempt} 次执行中` : `已执行 ${attempt} 次`;
  const parts = [active ? stages[order.stage] : "", execution];
  if (retry) parts.push("后台将自动继续申请");
  if (order.status === "waiting_dns") parts.push("添加或核对 TXT 记录后继续验证");
  if (cleanup) parts.push(order.status === "canceling" ? "正在停止申请并清理验证资源" : "验证资源待清理，后台将继续处理");
  if ((retry || cleanup) && order.next_attempt) parts.push(`下次处理：${recordDate(order.next_attempt, date)}`);
  const showError = order.error && ["queued","waiting_dns","failed"].includes(order.status);
  return { label, tone:order.status === "succeeded" ? "ok" : order.status === "failed" ? "bad" : "warn",
    info:parts.filter(Boolean).join(" · "), error:showError ? failureMessage(order.error, order.failure_stage || order.stage) : "",
    diagnostic:showError ? certificateOrderDiagnostic(order.error) : "" };
}
