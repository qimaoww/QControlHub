import assert from "node:assert/strict";
import {outboundEntries, mutateOutbound, bindOutbound, inboundOutbound, changeInboundOutbound} from "./modules/config-outbounds.js";
import {outboundPreset, outboundProtocols, outboundPeers} from "./modules/outbound-presets.js";
const source = '{"large":9007199254740993,"outbounds":[{"tag":"direct","type":"direct"},{"tag":"unused","type":"direct"},{"tag":"qch-trf-443-abcdef012345","type":"direct"}],"route":{"final":"direct"}}';
assert.deepEqual(outboundEntries(source).map(o=>o.tag), ["direct","unused"]);
assert.throws(()=>mutateOutbound(source,"delete",0,""), /引用/);
assert.throws(()=>mutateOutbound(source,"modify",0,'{"tag":"renamed","type":"direct"}'), /引用/);
assert.throws(()=>mutateOutbound(source,"add",-1,'{"tag":"direct"}'), /已存在/);
assert.throws(()=>mutateOutbound(source,"delete",2,""), /选择/);
const changed = mutateOutbound(source,"delete",1,"");
assert(changed.includes("9007199254740993"));
assert.deepEqual(JSON.parse(changed).outbounds.map(o=>o.tag), ["direct","qch-trf-443-abcdef012345"]);
assert.equal(JSON.parse(mutateOutbound(source,"modify",1,'{"tag":"proxy","type":"socks"}')).outbounds[1].tag,"proxy");
for (const engine of ["xray", "sing-box"]) {
  const route = engine === "xray" ? "routing" : "route", match = engine === "xray" ? "inboundTag" : "inbound", target = engine === "xray" ? "outboundTag" : "outbound";
  const policy = {[match]:["a"], domain:["policy.test"], [target]:"direct", ...(engine === "xray" ? {type:"field"} : {})};
  const fallback = {[target]:"direct", ...(engine === "xray" ? {type:"field"} : {})};
  const base = `{"large":9007199254740993,"inbounds":[{"tag":"a","port":443,"listen_port":443},{"tag":"b","port":8443,"listen_port":8443}],"outbounds":[{"tag":"direct","protocol":"freedom","type":"direct"},{"tag":"qch-trf-443-abcdef012345","protocol":"freedom","type":"direct"}],"${route}":{"rules":[${JSON.stringify(policy)},${JSON.stringify(fallback)}]}}`;
  const selected = {tag:"a", port:443};
  const fragment = '{"tag":"dedicated","protocol":"freedom","type":"direct","big":9007199254740993}';
  const bound = changeInboundOutbound(base, engine, selected, "add", -1, fragment);
  assert(bound.includes('"large": 9007199254740993') && bound.includes('"big":9007199254740993'));
  assert.equal(inboundOutbound(bound, engine, selected), "dedicated");
  assert.deepEqual(JSON.parse(bound)[route].rules, [policy, {...(engine === "xray" ? {type:"field"} : {}), [match]:["a"], [target]:"dedicated"}, fallback]);
  assert.equal(JSON.parse(bound).outbounds[1].tag, "dedicated", "new original must precede generated exit suffix");
  assert.equal(JSON.parse(bindOutbound(bound, engine, selected, "dedicated"))[route].rules.length, 3);
  const renamed = changeInboundOutbound(bound, engine, selected, "modify", 1, fragment.replace("dedicated", "renamed"));
  assert.equal(inboundOutbound(renamed, engine, selected), "renamed");
  assert.deepEqual(JSON.parse(renamed)[route].rules[0], policy);
  const removed = changeInboundOutbound(renamed, engine, selected, "delete", 1, "");
  assert.equal(inboundOutbound(removed, engine, selected), "");
  assert.deepEqual(JSON.parse(removed)[route].rules, [policy, fallback]);
  const shared = bindOutbound(bound, engine, {tag:"b",port:8443}, "dedicated");
  assert.throws(() => changeInboundOutbound(shared, engine, selected, "delete", 1, ""), /共享/);
  assert.throws(() => changeInboundOutbound(bindOutbound(base, engine, selected, "direct"), engine, selected, "modify", 0, '{"tag":"direct"}'), /共享/);
  assert.throws(() => bindOutbound(base, engine, {tag:"a",port:444}, "direct"), /入站/);
  assert.throws(() => bindOutbound(base, engine, selected, "qch-trf-443-abcdef012345"), /系统统计/);
  const duplicate = JSON.parse(bound);
  duplicate[route].rules.push(duplicate[route].rules[1]);
  assert.throws(() => bindOutbound(JSON.stringify(duplicate), engine, selected, "direct"), /多条/);
  const blocked = JSON.parse(base);
  blocked.outbounds.push({tag:"blocked", protocol:"blackhole", type:"block"});
  blocked[route].rules[1][target] = "blocked";
  assert.throws(() => bindOutbound(JSON.stringify(blocked), engine, selected, "direct"), /拦截/);
  if (engine === "sing-box") {
    blocked[route].rules = [{action:"reject"}];
    assert.throws(() => bindOutbound(JSON.stringify(blocked), engine, selected, "direct"), /拦截/);
  }
  const values = {tag:"exit-a", server:"2001:db8::1", port:443, credential:"123e4567-e89b-42d3-a456-426614174000", username:"test", password:"secret", method:"aes-128-gcm", security:"tls"};
  for (const [protocol] of outboundProtocols(engine)) {
    const out = outboundPreset(engine, protocol, values);
    assert.equal(out.tag, values.tag);
    assert(!JSON.stringify(out).includes("qch-trf-"));
  }
  const reality = outboundPreset(engine, "vless", {...values, security:"reality", publicKey:"public-only", shortId:"aabb", serverName:"tls.example", flow:"xtls-rprx-vision"});
  assert(JSON.stringify(reality).includes("public-only") && JSON.stringify(reality).includes("tls.example"));
  assert.throws(()=>outboundPreset(engine, "vless", {...values, credential:"not-uuid"}), /UUID/);
  assert.throws(()=>outboundPreset(engine, "trojan", {...values, server:"https://example.test"}), /URL/);
  assert.throws(()=>outboundPreset(engine, "vless", {...values, security:"reality"}), /公钥/);
  assert.throws(()=>outboundPreset(engine, "vless", {...values, security:"none", flow:"xtls-rprx-vision"}), /Vision/);
  assert.throws(()=>outboundPreset(engine, "vless", {...values, transport:"ws", flow:"xtls-rprx-vision"}), /Vision/);
}
for (const mixed of [false, true]) {
  const selected = {tag:"wg", port:51820}, other = {tag:"other", port:1080};
  const endpoint = {type:"wireguard", tag:selected.tag, listen_port:selected.port};
  const sibling = {type:mixed ? "socks" : "wireguard", tag:other.tag, listen_port:other.port};
  const document = {
    ...(mixed ? {inbounds:[sibling]} : {}),
    endpoints:mixed ? [endpoint] : [endpoint, sibling],
    outbounds:[{type:"direct", tag:"direct"}, {type:"direct", tag:"qch-trf-51820-abcdef012345"}],
    route:{final:"direct", rules:[{inbound:["wg"], domain:["policy.test"], outbound:"direct"}]},
  };
  const base = JSON.stringify(document);
  assert.equal(inboundOutbound(base, "sing-box", selected), "");
  assert.equal(inboundOutbound(changeInboundOutbound(base, "sing-box", selected, "bind", -1, "direct"), "sing-box", selected), "direct");
  const bound = changeInboundOutbound(base, "sing-box", selected, "add", -1, '{"type":"direct","tag":"wg-exit","custom":9007199254740993}');
  assert(bound.includes("9007199254740993"), "endpoint outbound edit lost native precision");
  assert.equal(inboundOutbound(bound, "sing-box", selected), "wg-exit");
  assert.equal(JSON.parse(bound).outbounds[1].tag, "wg-exit");
  const renamed = changeInboundOutbound(bound, "sing-box", selected, "modify", 1, '{"type":"direct","tag":"renamed"}');
  assert.equal(inboundOutbound(renamed, "sing-box", selected), "renamed");
  const removed = changeInboundOutbound(renamed, "sing-box", selected, "delete", 1, "");
  assert.equal(inboundOutbound(removed, "sing-box", selected), "");
  assert.deepEqual(JSON.parse(removed), document, "endpoint outbound lifecycle changed unrelated native configuration");
  const shared = bindOutbound(bound, "sing-box", other, "wg-exit");
  assert.throws(() => changeInboundOutbound(shared, "sing-box", selected, "modify", 1, '{"type":"direct","tag":"renamed"}'), /共享/);
  assert.throws(() => changeInboundOutbound(shared, "sing-box", selected, "delete", 1, ""), /共享/);
  const defaultBound = bindOutbound(base, "sing-box", selected, "direct");
  assert.throws(() => changeInboundOutbound(defaultBound, "sing-box", selected, "delete", 0, ""), /默认或共享/);
  const referenced = JSON.parse(bound);
  referenced.endpoints[0].detour = "wg-exit";
  assert.throws(() => changeInboundOutbound(JSON.stringify(referenced), "sing-box", selected, "delete", 1, ""), /共享/);
  assert.throws(() => bindOutbound(base, "sing-box", {...selected, port:51821}, "direct"), /入站已变化/);
  for (const section of ["inbounds", "endpoints"]) {
    const duplicate = structuredClone(document);
    (duplicate[section] ||= []).push({...endpoint, listen_port:51821});
    assert.throws(() => inboundOutbound(JSON.stringify(duplicate), "sing-box", selected), /入站已变化/);
  }
  const blocked = structuredClone(document);
  blocked.route.rules.push({action:"reject"});
  assert.throws(() => bindOutbound(JSON.stringify(blocked), "sing-box", selected, "direct"), /拦截/);
  const multiInbound = structuredClone(document);
  multiInbound.route.rules.push({inbound:["wg", "other"], outbound:"direct"});
  assert.throws(() => bindOutbound(JSON.stringify(multiInbound), "sing-box", selected, "direct"), /多入站共享/);
  assert.throws(() => inboundOutbound(base, "xray", selected), /入站已变化/, "Xray must not treat endpoints as inbounds");
}
assert.throws(()=>outboundPreset("sing-box", "vless", {tag:"t", transport:"xhttp"}, false), /传输/);
assert.throws(()=>outboundPreset("xray", "hysteria2", {}, false), /不支持/);
assert.deepEqual(outboundPeers([{agent_id:"self",profiles:[{tag:"private"}]},{agent_id:"other",profiles:[{tag:"peer"}]}],"self").map(peer=>peer.tag), ["peer"]);
console.log("Outbound binding and presets smoke passed");
