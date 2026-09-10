import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {splitConfigFiles,mergeConfigFiles,configFileDisplayName} from "./modules/config-files.js";

assert.equal(configFileDisplayName("common.json"), "公共配置.json");
assert.equal(configFileDisplayName("inbounds/VLESS-REALITY-8443.json"), "VLESS-REALITY-8443.json");
assert.equal(configFileDisplayName("outbounds/direct.json"), "direct.json");
assert.equal(configFileDisplayName("config.json"), "config.json");
assert.equal(configFileDisplayName("outbounds/香港节点.json",'{"outbounds":[{"type":"vless"}]}'), "香港节点.json · vless");
assert.equal(configFileDisplayName("outbounds/proxy.json",'{"outbounds":[{"protocol":"trojan"}]}'), "proxy.json · trojan");
const named = splitConfigFiles("xray", JSON.stringify({inbounds:[{tag:"VLESS-REALITY-8443.json"},{tag:"香港入口"},{tag:"香港入口"},{tag:"../a/b"},{tag:"..."},{tag:"a".repeat(60)}],outbounds:[{tag:"direct"}]}));
assert.deepEqual(named.map(f=>f.path), ["common.json","inbounds/VLESS-REALITY-8443.json","inbounds/香港入口.json","inbounds/香港入口-2.json","inbounds/_a_b.json","inbounds/inbound-5.json",`inbounds/${"a".repeat(48)}.json`]);

const original = '{"inbounds":[{"tag":"a","port":1080,"test":"[,\\\"}x"},{"tag":"b","port":1081}],"outbounds":[{"tag":"direct"}],"large":9007199254740993,"__proto__":{"safe":true}}';
for (const engine of ["xray","sing-box"]) {
  const files = splitConfigFiles(engine,original);
  assert.equal(files.length,3);
  assert.deepEqual(files.map(f=>f.path),["common.json","inbounds/a.json","inbounds/b.json"]);
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
const legacy=[{path:"00-common.json",content:'{}'},{path:"inbounds/0000.json",content:'{"inbounds":[{"tag":"old"}]}'}];
assert.equal(splitConfigFiles("xray",mergeConfigFiles(legacy))[1].path,"inbounds/old.json");
assert.throws(()=>mergeConfigFiles([{path:"common.json",content:'{}'}, {path:"inbounds/a.json",content:'{"inbounds":[{}]}'}, {path:"inbounds/a.json",content:'{"inbounds":[{}]}'}]));
const cases = JSON.parse(await readFile(new URL("../internal/serverconfig/testdata/paired_config_files.json", import.meta.url), "utf8"));
for (const engine of ["xray", "sing-box"]) for (const test of cases) {
  const files = splitConfigFiles(engine, JSON.stringify(test.config));
  assert.deepEqual(files.map(file => JSON.parse(file.content).outbounds?.length || 0), test.outbound_counts, test.name);
  assert.ok(!files.some(file => file.path.startsWith("outbounds/")), test.name);
  assert.deepEqual(JSON.parse(mergeConfigFiles(files)), test.config, test.name);
}
for (const common of ["common.json", "00-common.json"]) {
  const old = [{path:common,content:'{}'}, {path:"inbounds/0000.json",content:'{"inbounds":[{"tag":"a"}]}'}, {path:"outbounds/0000.json",content:'{"outbounds":[{"tag":"direct"}]}'}];
  assert.equal(splitConfigFiles("xray", mergeConfigFiles(old)).length, 2);
}
for (const outbounds of ["null", "[null]", "{}", "[1]"]) assert.throws(() => mergeConfigFiles([{path:"common.json",content:'{}'}, {path:"inbounds/a.json",content:`{"inbounds":[{}],"outbounds":${outbounds}}`}]));
assert.throws(() => mergeConfigFiles([{path:"common.json",content:'{}'}, {path:"inbounds/a.json",content:'{"inbounds":[{}],"outbounds":[{}]}'}, {path:"outbounds/b.json",content:'{"outbounds":[{}]}'}]));
console.log("Config file split/merge smoke passed");
