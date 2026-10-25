import assert from "node:assert/strict";
import { configFieldURL, renderSSRustFieldStudio, ssRustFieldGroups, ssRustPlanBinding } from "../modules/ss-rust-fields.js";

export async function testFieldScopes({ FakeForm, AGENT_ID, makeAgent, workspaceWithConfig, buildContext, installForms }) {
  // Scope-separated SS Rust renderer and real save handlers use the same target.
  {
    const engine = "ss-rust";
    const inbound = { tag: "HK & JP", protocol: "ss2022", listen: "::", port: 20001 };
    const fields = [
      { key: "server", label: "监听", scope: "inbound" },
      { key: "mode", label: "模式", scope: "override" },
      { key: "dns", label: "DNS", scope: "global" },
      { key: "timeout", label: "超时", scope: "global" },
      { key: "servers", label: "结构", scope: "structure" },
    ];
    const groups = ssRustFieldGroups(fields);
    assert.deepEqual(groups.inbound.map((f) => f.key), ["server", "mode"]);
    assert.deepEqual(groups.global.map((f) => f.key), ["mode", "dns", "timeout"]);
    assert.throws(() => configFieldURL("/base", "mode", ""), /选择/);
    assert.equal(configFieldURL("/base", "mode", inbound.tag), "/base/fields/mode?inbound=HK%20%26%20JP");
    assert.equal(configFieldURL("/base", "mode"), "/base/fields/mode");
    const empty = renderSSRustFieldStudio({ scope: "inbound", fields: groups.inbound,
      selected: fields[1], value: {}, config: {}, inbound: null });
    assert.ok(!empty.includes('id="inbound-field-form"'), "new inbound has no ambiguous save target");
    const escaped = renderSSRustFieldStudio({ scope: "inbound", fields: groups.inbound,
      selected: fields[1], value: { present: true, fragment: '</textarea><script>bad()</script>', inherited_fragment: '"tcp_only"' },
      config: {}, inbound: { tag: '<img src=x onerror="bad()">' } });
    assert.ok(!escaped.includes("<script>"));
    assert.ok(!escaped.includes("<img"));
    assert.ok(escaped.includes("删除后继承全局值"));
    assert.equal(ssRustPlanBinding({ ss_rust_outbound_bind_addr: "192.0.2.1" },
      { content: '{"server":"::","server_port":443,"outbound_bind_addr":"192.0.2.1"}' }, inbound), "");
    const state = { route: "agent-config", data: { agents: [{ ...makeAgent(), capabilities: [engine],
      runtime: { [engine]: { installed: true } } }], agentId: AGENT_ID, engine, inboundTag: inbound.tag,
      configField: "dns", configInboundField: "mode" }, session: { role: "admin" } };
    const ws = workspaceWithConfig();
    ws.inbounds = [inbound];
    ws.catalog.fields = fields;
    const portForm = new FakeForm({ mutation: "modify", fragment: '"udp_only"' });
    const globalForm = new FakeForm({ mutation: "modify", fragment: '"9.9.9.9"' });
    const planForm = new FakeForm({ operation: "modify", tag: inbound.tag, port: "20001" });
    const posted = [];
    const { ctx, markup } = buildContext(state, {
      "GET /agents/test-agent/configs/ss-rust/workspace": ws,
      "GET /configs/cfg-1/revisions.*": [],
      "GET /agents/test-agent/configs/ss-rust/fields/dns": { present: true, fragment: '"1.1.1.1"' },
      "GET /agents/test-agent/configs/ss-rust/fields/mode\\?inbound=HK%20%26%20JP": { present: true, fragment: '"tcp_only"', inherited_fragment: '"tcp_and_udp"' },
      "POST /agents/test-agent/configs/ss-rust/.*": (options, path) => {
        posted.push({ path, body: JSON.parse(options.body) });
        return { config: { version: 2 }, task: {} };
      },
    });
    const pages = installForms(ctx, { "#inbound-field-form": portForm, "#field-form": globalForm, "#server-plan-form": planForm });
    await pages.agentConfig();
    assert.ok(markup().includes("当前端口选项") && markup().includes("全局与默认值"));
    assert.ok(!markup().includes("全局字段与源码"));
    const portStudio = markup().split('id="inbound-options"')[1].split('id="advanced"')[0];
    assert.ok(!portStudio.includes('data-inbound-field="dns"'));
    assert.ok(!portStudio.includes('data-inbound-field="timeout"'));
    await portForm.dispatchSubmit({ fieldIntent: "validate" });
    await globalForm.dispatchSubmit({ fieldIntent: "validate" });
    await planForm.dispatchSubmit({ planIntent: "validate" });
    assert.equal(posted[0].path, "/agents/test-agent/configs/ss-rust/fields/mode?inbound=HK%20%26%20JP");
    assert.equal(posted[1].path, "/agents/test-agent/configs/ss-rust/fields/dns");
    assert.equal(posted[2].body.preserve_ss_rust_globals, true);
    assert.equal(posted[0].body.expected_version, 1);
  }

}
