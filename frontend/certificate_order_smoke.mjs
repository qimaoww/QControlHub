import assert from "node:assert/strict";
import { certificateOrderPresentation, certificateOrderDiagnostic } from "./modules/certificate-order.js";
import { recordDate } from "./modules/record-display.js";
import { formatDate } from "./analytics/appearance.js";

const scheduled = "2026-11-02T04:05:06Z";
const show = patch => certificateOrderPresentation({status:"queued",stage:"queued",attempt:0,...patch}, String);
assert.equal(show().label,"等待处理");
assert.equal(show().info,"尚未执行");
assert.equal(show({stage:"retry"}).label,"等待处理");
assert.match(show({status:"running",attempt:1}).info,/首次执行中/);
assert.match(show({status:"running",attempt:2}).info,/第 2 次执行中/);
const retry=show({stage:"retry",attempt:2,error:"not found",next_attempt:scheduled});
assert.equal(retry.label,"等待自动重试");
assert.match(retry.info,/已执行 2 次.*后台将自动继续申请.*2026-11-02T04:05:06Z/);
assert.match(retry.error,/暂无法确认具体资源或原因/);
assert.doesNotMatch(retry.error,/证书不存在|账号不存在/);
assert.doesNotMatch(show({stage:"retry"}).info,/下次/);
assert.equal(show({status:"waiting_dns"}).label,"等待人工 DNS 操作");
assert.match(show({status:"waiting_dns"}).info,/TXT/);
for(const [status,label] of [["succeeded","签发成功"],["failed","申请失败"],["canceling","取消中"],["canceled","已取消"]]) {
  const result=show({status,stage:"complete",attempt:2,error:"old failure"});
  assert.equal(result.label,label);
  assert.equal(Boolean(result.error),status==="failed");
  assert.doesNotMatch(result.info,/自动继续申请|下次/);
}
const cleanup=show({status:"succeeded",stage:"cleanup",error:"验证资源清理失败，将重试",next_attempt:scheduled});
assert.equal(cleanup.label,"签发成功");
assert.equal(cleanup.error,"");
assert.match(cleanup.info,/验证资源待清理.*下次清理/);
assert.doesNotMatch(show({status:"failed",stage:"cleanup"}).info,/将重试|下次/);
assert.match(show({status:"failed",stage:"cleanup",attempt:8,error:"验证资源清理失败，将重试"}).error,/原始申请错误未保留/);
const both=show({status:"failed",stage:"cleanup",attempt:8,failure_stage:"validating",error:"DNS TXT 尚未传播",cleanup_error:"DNS API 返回 HTTP 503",next_attempt:scheduled});
assert.match(both.error,/DNS TXT 尚未传播/);
assert.match(both.cleanupError,/清理失败.*HTTP 503/);
assert.match(show({status:"succeeded",stage:"cleanup",cleanup_error:"context deadline exceeded"}).cleanupError,/清理验证资源超时/);
assert.doesNotMatch(show({status:"failed",stage:"complete",cleanup_error:"stale"}).cleanupError,/stale/);
assert.doesNotMatch(show({stage:"retry",next_attempt:"invalid"}).info,/后台将|下次/);
assert.match(show({stage:"retry",failure_stage:"http_preflight",error:"timeout"}).error,/检查 HTTP 入口超时/);
assert.equal(show({error:"读取 ACME 申请账号失败: not found"}).error,"读取 ACME 申请账号失败，暂无法确认原因，请联系管理员检查。");
assert.doesNotMatch(certificateOrderDiagnostic('token=fixture-secret https://user:secret@example.test/secret -----BEGIN PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----'),/fixture-secret|user:secret|example.test|\nfixture/);
for(const missing of [undefined,null,"", "0001-01-01T00:00:00Z","invalid"]) assert.equal(recordDate(missing,String),"时间未知");
assert.equal(recordDate(scheduled,formatDate),formatDate(scheduled));
// Native certificate detail rendering is checked in browser/certificate-order-presentation.mjs.
console.log("Certificate order states, real schedules, record dates and diagnostic redaction passed");

assert.match(show({error:"读取 ACME 申请账号失败：申请账号解密失败，请联系管理员检查当前及旧加密密钥"}).error,/旧加密密钥/);
assert.match(show({error:"读取 DNS 账号失败：申请账号读取暂时失败，请稍后重试"}).error,/稍后重试/);
