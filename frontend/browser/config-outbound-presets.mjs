import { assert, pause, waitFor, createConfigFixture as fixture } from "./config-fixture.mjs";

export async function testOutboundPresetsRuntime() {
  const change = (element, value) => {
    element.value = value;
    element.dispatchEvent(new Event(element.tagName === "SELECT" ? "change" : "input", {bubbles:true}));
  };
  const open = async () => {
    document.querySelector('[data-outbound-action="add"]').click();
    await waitFor(()=>document.querySelector("[data-outbound-mode]"), "outbound preset selector missing");
  };
  for (const engine of ["xray", "sing-box"]) {
    const test = await fixture(engine);
    test.select("first");
    await open();
    const field = key => document.querySelector(`[data-outbound-value="${key}"]`);
    const input = document.querySelector('textarea[aria-label="出站配置 JSON"]');
    const mode = document.querySelector("[data-outbound-mode]");
    assert(mode.value === "preset" && input.readOnly, "new outbound should start with a preset, not raw JSON");
    change(document.querySelector("[data-outbound-protocol]"), "vless");
    assert(document.querySelector("[data-outbound-credential-label]").textContent.includes("UUID"), "VLESS preset mislabels its credential as a password");
    document.querySelector('[data-intent="validate"]').click();
    await waitFor(()=>document.querySelector("[data-outbound-error]").textContent.includes("服务器地址"), "incomplete preset submitted");
    assert(!test.writes.length, "incomplete preset wrote source");
    change(field("server"), "edge.example");
    change(field("credential"), "123e4567-e89b-42d3-a456-426614174000");
    change(field("security"), "reality");
    assert(field("serverName").placeholder.includes("必填"), "Reality placeholder conflicts with required ServerName validation");
    change(field("serverName"), "tls.example");
    change(field("publicKey"), "public-only");
    change(field("shortId"), "aabb");
    assert(input.value.includes("public-only") && input.value.includes("tls.example"), "preset did not produce Reality parameters");
    change(mode, "json");
    assert(!input.readOnly && input.value.includes("public-only"), "switching to JSON lost preset output");
    input.value = input.value.replace('"tag":', '"large":9007199254740993,"tag":');
    input.dispatchEvent(new Event("input"));
    change(mode, "preset");
    change(mode, "json");
    assert(input.value.includes("9007199254740993"), "mode switch lost advanced JSON draft");
    test.confirm = true;
    document.querySelector('[data-outbound-close]').click();
    await waitFor(()=>!document.querySelector("dialog"), "preset draft did not close");
    await open();
    let release;
    test.peerGate = new Promise(resolve=>{release=resolve;});
    change(document.querySelector("[data-outbound-mode]"), "node");
    assert(document.querySelector("[data-outbound-peer-status]").textContent.includes("正在读取"), "peer picker gave no loading feedback");
    release();
    await waitFor(()=>document.querySelector('[data-outbound-node-select] option[value="peer-node"]'), "deployed peer listing missing");
    const node = document.querySelector("[data-outbound-node-select]");
    assert(![...node.options].some(option=>option.value === test.agent.id), "peer picker permits selecting this same node");
    change(node, "incompatible");
    assert(document.querySelector("[data-outbound-peer-status]").textContent.includes("Snell"), "unsupported peer has no explanation");
    document.querySelector('[data-intent="validate"]').click(); await pause();
    assert(!test.writes.length, "incompatible node silently reused another node's outbound");
    change(node, "peer-node");
    change(field("tag"), "node-exit");
    const preview = document.querySelector('textarea[aria-label="出站配置 JSON"]');
    assert(preview.readOnly && preview.value.includes("203.0.113.10") && preview.value.includes("peer.example"), "peer address or TLS not filled");
    const originalPeer = JSON.parse(preview.value);
    const peerSelect = document.querySelector("[data-outbound-peer-select]");
    test.peerEntries = [{agent_id:"peer-node", agent_name:"出口 · JP", engine:"xray", profiles:[
      {tag:"new-first", protocol:"VLESS", port:3443, outbound:{...originalPeer, tag:"new-first"}},
      {tag:"remote-inbound", protocol:"VLESS", port:2443, address:"203.0.113.10", outbound:originalPeer},
    ]}];
    document.querySelector("[data-outbound-peer-reload]").click();
    await waitFor(()=>!document.querySelector("[data-outbound-peer-reload]").disabled, "peer refresh did not finish");
    assert(peerSelect.selectedOptions[0].textContent.includes("remote-inbound"), "refresh silently switched to a different target inbound");
    test.peerEntries[0].profiles.pop();
    document.querySelector("[data-outbound-peer-reload]").click();
    await waitFor(()=>!document.querySelector("[data-outbound-peer-reload]").disabled, "peer removal refresh did not finish");
    assert(peerSelect.value === "" && preview.value === "" &&
      document.querySelector("[data-outbound-peer-status]").textContent.includes("不会自动"), "removed peer silently fell back to another target");
    document.querySelector('[data-intent="validate"]').click(); await pause();
    assert(!test.writes.length, "removed peer could still submit its stale credentials");
    test.peerEntries = undefined;
    document.querySelector("[data-outbound-peer-reload]").click();
    await waitFor(()=>!document.querySelector("[data-outbound-peer-reload]").disabled, "restored peer refresh did not finish");
    assert(peerSelect.value === "", "refresh selected an inbound without the user's choice");
    change(peerSelect, "0");
    document.querySelector('[data-intent="validate"]').click();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "peer outbound did not save");
    const saved = JSON.parse(test.saved().content), route = engine === "xray" ? saved.routing : saved.route;
    assert(saved.outbounds.some(entry=>entry.tag === "node-exit") &&
      route.rules.some(rule=>(rule.outboundTag || rule.outbound) === "node-exit"), "peer outbound lost local inbound binding");
    assert(test.calls.filter(call=>call.method !== "GET").every(call=>!call.path.includes("peer-node")), "peer selection modified target node");
    test.dispose();
  }
  const restricted = await fixture("xray", {noPeerRead:true});
  restricted.select("first");
  await open();
  assert(document.querySelector('[data-outbound-mode] option[value="node"]').disabled &&
    !restricted.calls.some(call=>call.path.startsWith("/client-access")), "peer selector bypassed client-access permission");
  restricted.dispose();
}
