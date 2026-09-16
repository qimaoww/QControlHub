import { renderFieldEditor, renderFieldEmpty, renderFieldRail, renderFieldSummary } from "./config-fields.js";

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);

export function ssRustFieldGroups(fields) {
  return {
    inbound: fields.filter((field) => ["inbound", "override"].includes(field.scope)),
    global: fields.filter((field) => ["global", "override"].includes(field.scope)),
  };
}

export function ssRustPlanBinding(plan, config, selectedInbound) {
  if (config && selectedInbound) {
    try {
      const root = JSON.parse(config.content);
      if (!Array.isArray(root.servers) && !Array.isArray(root.shadowsocks)) return "";
    } catch { /* The server reports malformed documents. */ }
  }
  return plan.ss_rust_outbound_bind_addr || "";
}

export function configFieldURL(base, key, inbound) {
  const url = `${base}/fields/${encodeURIComponent(key)}`;
  // undefined means global; an empty selection is never a global edit.
  if (inbound === undefined) return url;
  if (!inbound) throw new Error("请先选择一个现有端口");
  return `${url}?inbound=${encodeURIComponent(inbound)}`;
}

export function renderSSRustFieldStudio({ scope, fields, selected, value = {}, config, inbound, presentFields = {} }) {
  const perPort = scope === "inbound";
  const title = perPort ? "当前端口选项" : "全局与默认值";
  const target = perPort ? inbound?.tag : "所有端口";
  const available = Boolean(config && selected && (!perPort || inbound));
  const hint = perPort
    ? "仅修改当前端口；其他端口及全局默认值不变。"
    : "全局字段影响所有端口；未设置端口覆盖时继承默认值。";
  const overrideHint = selected?.key === "acl"
    ? `<div class="field-inheritance"><span>${perPort ? "端口 ACL 优先；删除后回到启动 --acl（若有）或顶层 acl。" : "顶层 ACL 可被启动 --acl 覆盖；新版 QAgent 模板指定固定 ACL。需要独立生效请设置端口 ACL。"}</span><span>配置顶层值：<code>${esc(perPort ? value.inherited_fragment || "未设置" : value.fragment || "未设置")}</code></span></div>`
    : selected?.scope === "override"
    ? perPort
      ? `<div class="field-inheritance"><span>${value.present ? "已设置端口覆盖；删除后继承全局值。" : "未设置端口覆盖，继承全局值。"}</span><span>全局值：<code>${esc(value.inherited_fragment || "未设置，使用内核默认值")}</code></span></div>`
      : '<div class="field-inheritance"><span>这是全局默认值，已有端口覆盖保持不变。</span></div>'
    : "";
  return `<details class="advanced-studio config-field-studio ss-rust-field-studio" id="${perPort ? "inbound-options" : "advanced"}" open>
    ${renderFieldSummary(title, target || "未选择端口")}
    ${available ? `<div class="advanced-studio-body scoped-field-body">${renderFieldRail({ fields, selected, perPort, presentFields })}${renderFieldEditor({ selected, value, perPort, hint, inheritance: overrideHint,
      note: "部署会重启整个 ssserver，短暂影响所有端口；校验仅检查结构，不检查启动。",
      refreshKey: `${perPort ? "inbound" : "global"}-field-${target}-${selected.key}` })}</div>` : renderFieldEmpty(perPort && config ? "尚未选择端口" : "尚未保存配置", perPort && config ? "请在上方选择端口；全局字段可独立编辑。" : "请先创建并保存服务端端口。")}
  </details>`;
}
