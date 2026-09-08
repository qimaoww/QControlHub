import assert from "node:assert/strict";
import {splitConfigFiles,mergeConfigFiles,configFileDisplayName} from "./modules/config-files.js";

assert.equal(configFileDisplayName("00-common.json", "香港节点"), "香港节点 · 公共配置.json");
assert.equal(configFileDisplayName("inbounds/0000.json", "香港节点"), "香港节点 · 入站 1.json");
assert.equal(configFileDisplayName("outbounds/0010.json", "香港节点"), "香港节点 · 出站 11.json");
assert.equal(configFileDisplayName("config.json", "香港节点"), "config.json");

const original = '{"inbounds":[{"tag":"a","port":1080,"test":"[,\\\"}x"},{"tag":"b","port":1081}],"outbounds":[{"tag":"direct"}],"large":9007199254740993,"__proto__":{"safe":true}}';
for (const engine of ["xray","sing-box"]) {
  const files = splitConfigFiles(engine,original);
  assert.equal(files.length,4);
  const merged = mergeConfigFiles(files);
  assert.deepEqual(JSON.parse(merged),JSON.parse(original));
  assert.ok(merged.includes("9007199254740993"));
  files[1].content='{"inbounds":[{"tag":"edited","port":2080}]}';
  files[2].content='{"inbounds":[{"tag":"second","port":2081}]}';
  assert.deepEqual(JSON.parse(mergeConfigFiles(files)).inbounds.map(v=>v.port),[2080,2081]);
  files[1].path="inbounds/../escape.json";
  assert.throws(()=>mergeConfigFiles(files));
}
for (const engine of ["mihomo","ss-rust"]) assert.equal(splitConfigFiles(engine,original).length,1);
assert.throws(()=>splitConfigFiles("xray",'{"inbounds":null}'));
assert.throws(()=>splitConfigFiles("xray",'{"inbounds":[null]}'));
assert.throws(()=>splitConfigFiles("xray",'{"inbounds":[],"inbounds":[]}'));
assert.throws(()=>mergeConfigFiles([{path:"00-common.json",content:'{"inbounds":[]}'},{path:"inbounds/0000.json",content:'{"inbounds":[{}]}'}]));
console.log("Config file split/merge smoke passed");
