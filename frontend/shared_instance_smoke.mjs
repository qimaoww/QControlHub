import assert from "node:assert/strict";
import { sharedInstancePresentation, sharedConfigPresentation } from "./modules/shared-instance-state.js";

const node = (runtime = {}, instance = {}, status = "online") => ({can_manage:false, status,
  runtime:{xray:{installed:true, service_status:"unknown", ...runtime, instance:{...instance}}}});
assert.equal(sharedInstancePresentation({can_manage:true}, "xray"), null);
for (const runtime of [undefined, {}, { installed: true }, { installed: undefined, instance: {} }]) {
  const agent = { can_manage: false, status: "online", runtime: { xray: runtime } };
  assert.equal(sharedInstancePresentation(agent, "xray").label, "状态未知");
  assert.equal(sharedConfigPresentation(agent, "xray").label, "配置待核实");
}
assert.equal(sharedInstancePresentation(node({}, {}, "offline"), "xray").label, "离线 · 状态未知", "offline must not become no configuration");
assert.equal(sharedInstancePresentation(node({}, {}, "unknown"), "xray").label, "状态未知");
assert.equal(sharedInstancePresentation({ can_manage: false, status: "online", runtime: { xray: { installed: false } } }, "xray").label, "未安装");
assert.equal(sharedInstancePresentation(node({}, { config_uncertain: true }), "xray").label, "状态未知");
assert.equal(sharedInstancePresentation(node({installed:false}), "xray").label, "未安装");
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
assert.equal(sharedConfigPresentation({can_manage:true}, "xray"), null);
assert.equal(sharedConfigPresentation(node({installed:false}), "xray").label, "未安装");
assert.equal(sharedConfigPresentation(node(), "xray").label, "尚未配置");
assert.equal(sharedConfigPresentation({can_manage:false, runtime:{xray:{installed:true}}}, "xray").label, "配置待核实");
const configStates = [
  [{saved_version:1, saved_config_id:"a"}, "v1 待部署"],
  [{saved_version:1, saved_config_id:"a", deployed_version:1, deployed_config_id:"a"}, "已部署 v1"],
  [{saved_version:2, saved_config_id:"a", deployed_version:1, deployed_config_id:"a"}, "v2 待部署"],
  [{saved_version:1, saved_config_id:"b", deployed_version:1, deployed_config_id:"a"}, "v1 待部署"],
  [{deployed_version:1, deployed_config_id:"a"}, "已部署 v1"],
  [{deployed_version:1, config_uncertain:true}, "配置待核实"],
];
for (const [instance, label] of configStates) {
  for (const service_status of ["active", "inactive", "failed", "unknown"])
    assert.equal(sharedConfigPresentation(node({service_status}, instance), "xray").label, label);
  assert.equal(sharedConfigPresentation(node({}, instance, "offline"), "xray").label, label);
}
console.log("shared instance state smoke checks passed");
