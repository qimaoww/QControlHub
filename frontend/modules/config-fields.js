// Shared presentation for scoped and global fields. Saving stays in configs.js.
const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (char) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);

export function renderFieldSummary(title, context) {
  return `<summary><b>${esc(title)}</b><span class="field-studio-context"><small>${esc(context)}</small><svg viewBox="0 0 20 20" aria-hidden="true"><path d="m7 4 6 6-6 6"/></svg></span></summary>`;
}

export function renderFieldRail({ fields, selected, perPort = false, presentFields = {} }) {
  const title = perPort ? "端口配置项" : "全局配置项";
  return `<nav class="field-rail" aria-label="${title}"><header><b>${title}</b><small>${fields.length} 项</small></header>
    <div class="field-rail-list" data-refresh-scroll>${fields.map((field) => {
      const active = field.key === selected?.key;
      const kind = field.scope === "override" ? perPort ? "可覆盖" : "默认值" : perPort ? "端口" : field.scope === "global" ? "全局" : field.kind;
      const path = `${perPort ? "servers[]." : ""}${field.key}`;
      return `<a class="field-link${active ? " active" : ""}" href="#agent-config" ${perPort ? "data-inbound-field" : "data-config-field"}="${esc(field.key)}"${active ? ' aria-current="true"' : ""}>
        <span class="field-link-copy"><strong>${esc(field.label)}</strong><code title="${esc(path)}">${esc(path)}</code></span>
        <small class="field-kind${!perPort && presentFields[field.key] ? " present" : ""}">${esc(kind)}</small></a>`;
    }).join("")}</div></nav>`;
}

export function renderFieldEmpty(title, description) {
  return `<div class="field-studio-empty" role="status"><span class="field-empty-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M4 7h16M4 17h16M8 4v6M16 14v6"/></svg></span><div><strong>${esc(title)}</strong><p>${esc(description)}</p></div></div>`;
}

export function revealSelectedFields(root = document) {
  root.querySelectorAll(".config-field-studio .field-rail-list").forEach((rail) => {
    const selected = rail.querySelector('[aria-current="true"]');
    if (!selected || !rail.clientWidth) return;
    const bounds = rail.getBoundingClientRect();
    const item = selected.getBoundingClientRect();
    // Scroll only the rail, never the page or an unrelated editor.
    // Align to the item start so horizontal scroll snapping cannot snap back
    // to the previous field and leave the selected item clipped.
    if (item.left < bounds.left || item.right > bounds.right) rail.scrollLeft += item.left - bounds.left - 8;
    if (item.top < bounds.top) rail.scrollTop += item.top - bounds.top - 6;
    else if (item.bottom > bounds.bottom) rail.scrollTop += item.bottom - bounds.bottom + 6;
  });
}

export function renderFieldEditor({ selected, value, perPort = false, format = "JSON", hint = "", inheritance = "", note = "", refreshKey = "" }) {
  // Scalar values should not look like an empty, full-file code editor.
  const rows = Math.min(16, Math.max(5, String(value.fragment || "").split("\n").length + 1));
  return `<section class="field-canvas" data-refresh-key="${esc(refreshKey)}">
    <header><div class="field-editor-heading"><h2>${esc(selected.label)}</h2><code>${perPort ? "servers[]." : ""}${esc(selected.key)}</code></div><a href="${esc(selected.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></header>
    ${hint ? `<p class="field-scope-hint">${esc(hint)}</p>` : ""}${selected.description ? `<p class="field-description">${esc(selected.description)}</p>` : ""}
    ${value.error ? renderFieldEmpty("字段暂不可编辑", `${value.error}；可在下方完整源码中检查配置。`) : `${inheritance}<form id="${perPort ? "inbound-field-form" : "field-form"}" data-refresh-key="${perPort ? "inbound" : "global"}-value-${esc(selected.key)}-${value.present ? "set" : "unset"}">
      <div class="field-mutation"><label>操作<select name="mutation" data-refresh-key="${perPort ? "inbound" : "global"}-mutation-${esc(selected.key)}-${value.present ? "set" : "unset"}">${value.present ? '<option value="modify" selected>修改字段</option><option value="delete">删除字段</option>' : '<option value="add" selected>新增字段</option>'}</select></label><span class="field-value-state">${value.present ? perPort ? "已设置端口值" : "已设置字段值" : "未单独设置"}</span></div>
      <label class="field-value-label">${esc(format)} 字段值<textarea name="fragment" rows="${rows}" spellcheck="false">${esc(value.fragment)}</textarea></label>
      <footer>${note ? `<p class="field-editor-note">${esc(note)}</p>` : ""}<div><button class="button" type="submit" data-field-intent="validate">保存并校验</button><button class="button primary" type="submit" data-field-intent="deploy">保存并部署</button></div></footer>
    </form>`}
  </section>`;
}

export function renderGlobalFieldStudio({ fields, selected, value, config, catalog, presentFields = {} }) {
  const topics = catalog.topic_groups || [];
  return `<details class="advanced-studio config-field-studio" id="advanced">
    ${renderFieldSummary("全局字段", `${fields.length} 项 · ${catalog.format}`)}
    ${config && selected ? `<div class="advanced-studio-body scoped-field-body">${renderFieldRail({ fields, selected, presentFields })}${renderFieldEditor({ selected, value, format: catalog.format, hint: "编辑配置顶层字段；完整配置在下方源码区单独保存。", refreshKey: `config-field-${selected.key}` })}</div>` : renderFieldEmpty("尚未保存配置", "先在上方创建并保存一个服务端入站，再编辑全局字段。")}
    ${topics.length ? `<details class="field-docs"><summary><b>配置参考文档</b><small>${catalog.topic_count} 个主题 ↗</small></summary><div>${topics.map((group) => `<details><summary>${esc(group.name)}<small>${group.topics.length}</small></summary><nav>${group.topics.map((topic) => `<a href="${esc(topic.docs)}" target="_blank" rel="noopener noreferrer">${esc(topic.label)} ↗</a>`).join("")}</nav></details>`).join("")}</div></details>` : ""}
  </details>`;
}

export function renderConfigSourceStudio({ config, catalog, engineInstalled, open = false, ssRust = false }) {
  if (!config) return "";
  return `<details class="source-studio config-source-studio"${open ? " open" : ""}>
    ${renderFieldSummary("完整源码", `${catalog.format === "YAML" ? "config.yaml" : "config.json"} · v${config.version}`)}
    <div class="config-source-body"><p class="field-source-note">${ssRust ? "包含 servers 列表及未收录字段，保存可能同时影响多个端口。" : "编辑完整配置文件，保存将创建新版本。"}<a href="${esc(catalog.source)}" target="_blank" rel="noopener noreferrer">官方完整配置定义 ↗</a></p>
    <form id="source-config-form"><div class="form-grid"><label>配置名称<input name="name" maxlength="100" required value="${esc(config.name)}"></label><label>说明<input name="description" maxlength="300" value="${esc(config.description)}"></label></div>
      <label class="field-value-label">${esc(catalog.format)} 配置源码<textarea name="content" spellcheck="false" required>${esc(config.content)}</textarea></label>
      <footer><div><button class="button" type="submit" data-source-intent="validate" ${engineInstalled ? "" : "disabled"}>保存源码并校验</button><button class="button primary" type="submit" data-source-intent="deploy" ${engineInstalled ? "" : "disabled"}>保存源码并部署</button></div></footer>
    </form></div>
  </details>`;
}
