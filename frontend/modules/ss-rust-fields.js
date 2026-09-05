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

export function renderSSRustFieldStudio({ scope, fields, selected, value, config, inbound, presentFields = {} }) {
  const perPort = scope === "inbound";
  const title = perPort ? "当前端口选项" : "全局与默认值";
  const target = perPort ? inbound?.tag : "所有端口";
  const available = Boolean(config && selected && (!perPort || inbound) && !value.error);
  const attribute = perPort ? "data-inbound-field" : "data-config-field";
  const hint = perPort
    ? `仅修改 ${target || "选中端口"} 的配置；不会改动其他端口或全局默认值。部署仍会重启整个 ssserver 进程，可能短暂影响所有端口。`
    : "全局字段影响所有端口；标为「默认值」的字段仅由未设置端口覆盖的端口继承。";
  const overrideHint = selected?.key === "acl"
    ? `<p class="validation-note">${perPort ? "端口 ACL 优先；删除后回到启动 --acl（若有）或顶层 acl。" : "顶层 ACL 可被启动 --acl 覆盖；新版 QAgent 模板指定固定 ACL。需要独立生效请设置端口 ACL。"} 配置顶层值：<code>${esc(perPort ? value.inherited_fragment || "未设置" : value.fragment || "未设置")}</code></p>`
    : selected?.scope === "override"
    ? perPort
      ? `<p class="validation-note">${value.present ? "已设置端口覆盖；删除后继承全局值。" : "未设置端口覆盖，继承全局值。"} 全局值：<code>${esc(value.inherited_fragment || "未设置，使用内核默认值")}</code></p>`
      : '<p class="validation-note">这是全局默认值，已有端口覆盖保持不变。</p>'
    : "";
  return `<details class="advanced-studio ss-rust-field-studio" id="${perPort ? "inbound-options" : "advanced"}" open>
    <summary><b>${title}</b><small>${esc(target || "请先选择端口")}</small></summary>
    <div class="advanced-studio-body scoped-field-body">
      <nav class="field-rail"><header><b>${perPort ? "端口配置项" : "全局配置项"}</b><small>${fields.length}</small></header>
        ${fields.map((field) => `<a class="${field.key === selected?.key ? "active" : ""}" href="#agent-config" ${attribute}="${esc(field.key)}">${perPort ? "" : `<i class="${presentFields[field.key] ? "present" : ""}"></i>`}<span><strong>${esc(field.label)}</strong><code>${perPort ? "servers[]." : ""}${esc(field.key)}</code></span><small>${field.scope === "override" ? perPort ? "可覆盖" : "默认值" : perPort ? "端口" : "全局"}</small></a>`).join("")}
      </nav>
      <section class="field-canvas" data-refresh-key="${perPort ? "inbound" : "global"}-field-${esc(target)}-${esc(selected?.key)}">
        <header><div><h2>${esc(selected?.label || title)}</h2><code>${perPort ? "servers[]." : ""}${esc(selected?.key)}</code></div>${selected ? `<a href="${esc(selected.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a>` : ""}</header>
        <p>${esc(hint)}</p><p>${esc(selected?.description)}</p><p>保存并校验仅检查配置结构；ssserver 没有不启动服务的完整校验模式。</p>
        ${available ? `${overrideHint}<form id="${perPort ? "inbound-field-form" : "field-form"}">
          <div class="field-mutation"><label>操作<select name="mutation">${value.present ? '<option value="modify">修改字段</option><option value="delete">删除字段</option>' : '<option value="add">新增字段</option>'}</select></label></div>
          <label>JSON 字段值<textarea name="fragment" spellcheck="false">${esc(value.fragment)}</textarea></label>
          <footer><div><button class="button" type="submit" data-field-intent="validate">保存并校验</button><button class="button primary" type="submit" data-field-intent="deploy">保存并部署</button></div></footer>
        </form>` : `<div class="empty compact"><strong>${esc(value.error || "先创建并选择一个服务端端口")}</strong>${value.error ? '<p>端口编辑暂不可用；可在下方完整源码中检查配置。</p>' : ""}</div>`}
      </section>
    </div>
  </details>`;
}
