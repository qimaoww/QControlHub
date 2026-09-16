import assert from "node:assert/strict";
import { createAccessControlView } from "./modules/access-control-view.js";
import { createDisplayHelpers } from "./modules/formatting.js";

const { esc } = createDisplayHelpers({data:{}});
let editable = true;
const { card } = createAccessControlView({state:{}, esc, engineName:value => value, shell:() => {}},
  {editable:() => editable});
const sourceInput = html => html.match(/<input[^>]*name="block_mainland_source"[^>]*>/)?.[0] || "";
for (const engine of ["xray", "sing-box"]) {
  const entry = {engine, kind:"wireguard", tag:"wg", port:51820, agent_status:"online"};
  const html = card(entry);
  assert.match(sourceInput(html), /disabled/, "WireGuard must not offer ineffective public source blocking");
  assert.match(html, /公网来源限制须在节点防火墙配置/);
  assert.doesNotMatch(html.match(/<input[^>]*name="block_mainland_destination"[^>]*>/)[0], /disabled/,
    "WireGuard destination blocking remains available");
  const old = card({...entry, block_mainland_source:true});
  assert.match(sourceInput(old), /checked/);
  assert.doesNotMatch(sourceInput(old), /disabled/, "operators can clear a legacy source rule");
}
assert.doesNotMatch(sourceInput(card({kind:"vless"})), /disabled/, "ordinary source policy remains editable");
editable = false;
assert.match(sourceInput(card({kind:"wireguard", block_mainland_source:true})), /disabled/);
console.log("Access control source policy smoke passed");
