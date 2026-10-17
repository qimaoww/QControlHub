import assert from "node:assert/strict";
import { sharedInstancePresentation } from "./modules/shared-instance-state.js";

const node = (runtime = {}, instance = {}, status = "online") => ({can_manage:false, status,
  runtime:{xray:{installed:true, service_status:"unknown", ...runtime, instance:{...instance}}}});
assert.equal(sharedInstancePresentation({can_manage:true}, "xray"), null);
assert.match(sharedInstancePresentation(node(), "xray").label, /尚未配置/);
assert.equal(sharedInstancePresentation(node({}, {saved_version:1}), "xray").label, "待部署");
const running = node({service_status:"active"}, {created:true, saved_config_id:"cfg_a", saved_version:2, deployed_config_id:"cfg_a", deployed_version:1});
assert.equal(sharedInstancePresentation(running,"xray").version, "运行 v1 · v2 待部署");
for (const [status,label] of [["inactive","已停止"],["unknown","状态未知"],["failed","启动失败"]]) {
  const state = sharedInstancePresentation(node({service_status:status},running.runtime.xray.instance),"xray");
  assert.equal(state.label,label);
  assert.equal(state.version,"已部署 v1 · v2 待部署");
}
assert.equal(sharedInstancePresentation({...running,status:"offline"},"xray").label,"离线 · 状态未知");
const failed = structuredClone(running);
failed.runtime.xray.instance.operation = {status:"failed", action:"deploy", error:"端口已占用，已恢复旧版本"};
assert.equal(sharedInstancePresentation(failed,"xray").label,"正在运行");
assert.match(sharedInstancePresentation(failed,"xray").reason,/端口已占用/);
const queued = structuredClone(running);
queued.runtime.xray.instance.operation = {status:"pending",action:"stop"};
assert.equal(sharedInstancePresentation(queued,"xray").label,"停止排队中");
assert.equal(sharedInstancePresentation(queued,"xray").busy,true);
console.log("shared instance state smoke checks passed");
