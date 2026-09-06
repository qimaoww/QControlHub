import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { renderConfigSourceStudio, renderFieldEditor, renderFieldRail, renderGlobalFieldStudio, revealSelectedFields } from "./modules/config-fields.js";
import { renderSSRustFieldStudio, ssRustFieldGroups } from "./modules/ss-rust-fields.js";

const fields = [
  { key: "server", label: "监听地址", scope: "inbound", kind: "string" },
  { key: "mode", label: "转发模式", scope: "override", kind: "string", description: "TCP / UDP" },
  { key: "dns", label: "DNS", scope: "global", kind: "object" },
  { key: "outbound_udp_allow_fragmentation", label: "出站 UDP 分片", scope: "override", kind: "boolean" },
  { key: "servers", label: "服务端列表", scope: "structure", kind: "array" },
];
const config = { name: "HK & JP", description: '"node" <config>', version: 3, content: '{"large":9007199254740993}' };
const groups = ssRustFieldGroups(fields);
const value = { fragment: '"udp_only"', present: true, inherited_fragment: '"tcp_and_udp"' };

for (const scope of ["inbound", "global"]) {
  const html = renderSSRustFieldStudio({ scope, fields: groups[scope], selected: fields[1], value, config, inbound: { tag: "HK & JP" } });
  assert.match(html, /config-field-studio ss-rust-field-studio/);
  assert.match(html, /class="field-link-copy"><strong>转发模式/);
  assert.match(html, /class="field-rail-list" data-refresh-scroll/);
  assert.match(html, /aria-current="true"/);
  assert.match(html, /class="field-editor-heading"/);
  assert.match(html, /rows="5"/);
  assert.match(html, /mutation-mode-set/);
  assert.match(html, /value-mode-set/);
  assert.match(html, /<option value="modify" selected>/);
  assert.match(html, /部署会重启整个 ssserver/);
  assert.match(html, /仅检查配置结构/);
  assert.equal(html.includes('id="inbound-field-form"'), scope === "inbound");
  assert.equal(html.includes('id="field-form"'), scope === "global");
  assert.ok(!html.includes('data-inbound-field="dns"'));
  assert.ok(!html.includes('data-config-field="server"'));
  assert.ok(!html.includes('data-config-field="servers"'));
}

const emptyPort = renderSSRustFieldStudio({ scope: "inbound", fields: groups.inbound, selected: fields[1], config });
assert.match(emptyPort, /尚未选择端口/);
assert.ok(!emptyPort.includes('class="field-rail"'), "empty port must not render a full-height field rail");
assert.ok(!emptyPort.includes("<form"), "an empty port has no accidental global save target");
const emptyGlobal = renderSSRustFieldStudio({ scope: "global", fields: groups.global, selected: fields[1] });
assert.match(emptyGlobal, /尚未保存配置/);
assert.ok(!emptyGlobal.includes("<form"));

const malicious = '</textarea><img src=x onerror="bad()"><script>bad()</script>';
const failed = renderFieldEditor({ selected: fields[1], value: { error: malicious }, refreshKey: malicious });
assert.match(failed, /字段暂不可编辑/);
assert.match(failed, /完整源码/);
assert.ok(!failed.includes("<form"));
assert.ok(!failed.includes("<img"));
assert.ok(!failed.includes("<script"));
const escaped = renderFieldEditor({ selected: { ...fields[1], label: malicious }, value: { fragment: malicious } });
assert.ok(!escaped.includes("<img"));
assert.ok(!escaped.includes("<script"));
assert.equal((escaped.match(/<textarea/g) || []).length, 1);
assert.match(renderFieldEditor({ selected: fields[2], value: { fragment: "{\n".repeat(30) } }), /rows="16"/);

const rail = renderFieldRail({ fields, selected: fields[3], presentFields: { dns: true } });
assert.match(rail, /class="field-kind present"/);
assert.match(rail, /title="outbound_udp_allow_fragmentation"/);

for (const engine of ["mihomo", "xray", "sing-box"]) {
  const catalog = { format: engine === "mihomo" ? "YAML" : "JSON", fields, topic_count: 1,
    topic_groups: [{ name: "官方 & 文档", topics: [{ label: "示例 <一>", docs: "https://example.invalid/config" }] }] };
  const html = renderGlobalFieldStudio({ fields, selected: fields[2], value, config, catalog });
  assert.match(html, /class="advanced-studio config-field-studio" id="advanced"/);
  assert.match(html, /<b>全局字段<\/b>/);
  assert.match(html, /id="field-form"/);
  assert.match(html, /class="field-docs"/);
  assert.match(html, /官方 &amp; 文档/);
  assert.ok(!html.includes('class="official-rail"'), "documentation must not squeeze the editor into three columns");
  assert.ok(!html.includes('id="source-config-form"'), "source is separate from a selected global field");
  assert.match(renderGlobalFieldStudio({ fields, value: {}, catalog }), /尚未保存配置/);
  const source = renderConfigSourceStudio({ config, catalog, engineInstalled: false, open: true });
  assert.match(source, /class="source-studio config-source-studio" open/);
  assert.match(source, /id="source-config-form"/);
  assert.match(source, /HK &amp; JP/);
  assert.match(source, /9007199254740993/);
  assert.equal((source.match(/ disabled>/g) || []).length, 2, "uninstalled cores must not be deployable from source");
}
assert.equal(renderConfigSourceStudio({ config: null }), "");
assert.match(renderFieldEditor({ selected: fields[1], value: {} }), /mutation-mode-unset/);
assert.match(renderFieldEditor({ selected: fields[1], value: {} }), /value-mode-unset/);
assert.match(renderFieldEditor({ selected: fields[1], value: {} }), /<option value="add" selected>/);

const railBounds = { left: 0, right: 200, top: 0, bottom: 400 };
let selectedBounds = { left: 420, right: 600, top: 2, bottom: 55 };
const scroller = { clientWidth: 200, scrollLeft: 0, scrollTop: 0,
  getBoundingClientRect: () => railBounds, querySelector: () => ({ getBoundingClientRect: () => selectedBounds }) };
const root = { querySelectorAll: () => [scroller] };
revealSelectedFields(root);
assert.equal(scroller.scrollLeft, 412, "a selected mobile field aligns to its own scroll-snap start");
assert.equal(scroller.scrollTop, 0);
selectedBounds = { left: 0, right: 180, top: 420, bottom: 475 };
revealSelectedFields(root);
assert.equal(scroller.scrollTop, 81, "a selected desktop field is revealed within its own rail");
selectedBounds = { left: 0, right: 180, top: 30, bottom: 85 };
revealSelectedFields(root);
assert.equal(scroller.scrollLeft, 412, "already-visible fields do not move the rail");
assert.equal(scroller.scrollTop, 81);

// Keep the visual contract scoped: other work-in-progress theme changes do not
// mask regressions in this component, and the full app contract still runs.
const css = readFileSync(new URL("app.css", import.meta.url), "utf8").split("/* Field workspaces share")[1];
assert.ok(css, "shared field workspace stylesheet exists");
assert.match(css, /grid-template-columns:minmax\(0,1fr\) auto/);
assert.match(css, /@container field-studio \(max-width:660px\)/);
assert.match(css, /max-height:460px/);
assert.match(css, /text-overflow:ellipsis;white-space:nowrap/);
assert.match(css, /background:var\(--code-canvas\)/);
assert.match(css, /@media\(pointer:coarse\)/);
assert.ok(!/#[0-9a-f]{3,8}\b/i.test(css), "field surfaces inherit both themes instead of hard-coding colors");
assert.ok(!/font-size:[1-9]\d*(?:\.\d+)?px/.test(css), "field typography follows the panel font scale");

console.log("Config field studio smoke passed");
