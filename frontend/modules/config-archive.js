import { bindEvent } from "./refresh.js";

export function createConfigArchive({ api, optionalAPI, state, engines, can, esc, engineName, date, ago, confirmAction, notify, shell, submitTask, bindCodeEditors }) {
let archiveConfigRequest = 0;

async function archiveConfigs() {
  const request = ++archiveConfigRequest;
  const [items, templates, agents] = await Promise.all([
    api("/configs"),
    can("templates.read") ? api("/templates") : [],
    can("agents.read") ? api("/agents") : [],
  ]);
  if (request !== archiveConfigRequest || state.route !== "archive-config") return;
  state.data.configs = items;
  state.data.agents = agents;
  const isNew = state.data.newConfig || items.length === 0;
  let selected = items.find((item) => item.id === state.data.archiveConfigId);
  if (!selected && !isNew) selected = items[0];
  const formConfig = selected || {
    id: "",
    name: "新配置",
    description: "",
    engine: "mihomo",
    content:
      "mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nproxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n",
    version: 0,
  };
  state.data.archiveConfigId = formConfig.id;
  const revisions = formConfig.id
    ? await api(
        `/configs/${encodeURIComponent(formConfig.id)}/revisions?limit=50`,
      )
    : [];
  if (request !== archiveConfigRequest || state.route !== "archive-config") return;
  let preview = null;
  if (formConfig.id && state.data.revisionVersion) {
    preview = await optionalAPI(
      `/configs/${encodeURIComponent(formConfig.id)}/revisions/${state.data.revisionVersion}`,
    );
  }
  if (request !== archiveConfigRequest || state.route !== "archive-config") return;
  const deployAgents = agents.filter(
    (agent) =>
      agent.status === "online" &&
      (agent.capabilities || []).includes(formConfig.engine) &&
      agent.runtime?.[formConfig.engine]?.installed,
  );
  const templateCards =
    templates
      .map((item) => {
        const eligibleAgents = agents.filter(
          (agent) =>
            agent.status === "online" &&
            (agent.capabilities || []).includes(item.engine) &&
            agent.runtime?.[item.engine]?.installed,
        );
        return `<article class="template-card" data-refresh-key="template-${esc(item.id)}"><header><span class="engine-badge ${esc(item.engine)}">${esc(engineName(item.engine))}</span><h4>${esc(item.name)}</h4><small>${ago(item.updated_at)}</small></header><pre>${esc(item.content)}</pre>${
            can("templates.write") && can("agent-config.write")
              ? `<footer><form data-template-apply="${esc(item.id)}"><label>应用至<select name="agent_id" required><option value="">${eligibleAgents.length ? "选择在线且已安装内核的节点" : "没有可用节点"}</option>${eligibleAgents
                  .map(
                    (agent) =>
                      `<option value="${esc(agent.id)}">${esc(agent.name)} · 在线 · 已安装</option>`,
                  )
                  .join(
                    "",
                  )}</select></label><button class="button small" type="submit" ${eligibleAgents.length ? "" : "disabled"}>应用</button></form>${can("templates.delete") ? `<button class="button small danger-button" type="button" data-delete-template="${esc(item.id)}">删除</button>` : ""}</footer>`
              : ""
          }</article>`;
      })
      .join("") ||
    '<p class="template-empty">还没有模板。新建模板后可按节点变量生成配置。</p>';
  const revisionTimeline = formConfig.id
    ? `<details class="revision-timeline" ${preview ? "open" : ""}><summary><b>版本历史</b><strong>${revisions.length} 个版本</strong></summary><div class="timeline-body"><nav aria-label="配置修订历史">${revisions.map((revision) => `<button class="${preview?.version === revision.version ? "active" : ""} ${revision.version === formConfig.version ? "current" : ""}" type="button" data-revision="${revision.version}"><i></i><span><b>v${revision.version}</b><strong>${esc(revision.name)}</strong><small>${ago(revision.updated_at)}${revision.version === formConfig.version ? " · 当前" : ""}</small></span></button>`).join("")}</nav>${preview ? `<section class="timeline-preview"><header><div><b>v${preview.version} · ${esc(preview.name)}</b><small>${esc(engineName(preview.engine))} · ${date(preview.updated_at)}</small></div>${preview.version === formConfig.version ? '<span class="status-label ok">当前版本</span>' : ""}</header><textarea readonly>${esc(preview.content)}</textarea>${can("configs.restore") && preview.version !== formConfig.version ? `<button class="button" type="button" data-restore-revision="${preview.version}">以此版本创建新版本</button>` : ""}</section>` : '<div class="timeline-placeholder">选择版本</div>'}</div></details>`
    : "";
  const delivery = formConfig.id
    ? `<section class="delivery-bar"><div><span class="delivery-icon"><svg viewBox="0 0 24 24"><path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/></svg></span><h3>校验或部署</h3></div><form id="archive-delivery"><label>目标节点<select name="agent_id" required><option value="">${deployAgents.length ? `选择在线且已安装 ${esc(engineName(formConfig.engine))} 的节点` : `没有在线且已安装 ${esc(engineName(formConfig.engine))} 的节点`}</option>${deployAgents.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)} · 在线 · 已安装</option>`).join("")}</select></label><label>执行方式<select name="action"><option value="validate">仅校验，不写入</option><option value="deploy">部署并重启</option></select></label><button class="button primary" type="submit" ${!deployAgents.length || !can("tasks.execute") ? "disabled" : ""}>提交任务</button></form></section>`
    : "";
  shell(
    `<article class="config-workspace"><header class="editor-toolbar"><h2>${esc(formConfig.name)}</h2><div class="editor-toolbar-state"><span class="engine-badge ${esc(formConfig.engine)}">${esc(engineName(formConfig.engine))}</span><b>${isNew ? "草稿" : `v${formConfig.version}`}</b></div></header><form class="config-editor-grid" id="archive-form" data-profile-editor data-new-config="${isNew ? 1 : 0}" data-engine="${esc(formConfig.engine)}"><section class="code-workspace" data-code-editor data-code-language="${formConfig.engine === "mihomo" ? "YAML" : "JSON"}" data-code-max-bytes="2097152"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>${formConfig.engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${formConfig.engine === "mihomo" ? "YAML" : "JSON"}</span><span data-code-status aria-live="polite">${isNew ? "草稿" : "已保存"}</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="${esc(engineName(formConfig.engine))} 配置档案源码" spellcheck="false" required ${can("configs.write") ? "" : "readonly"}>${esc(formConfig.content)}</textarea></div><footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite"></span></span><div><button class="button code-reset" type="button" data-code-reset data-archive-reset disabled>恢复原文</button>${can("configs.write") ? `<button class="button code-format" type="button" data-code-format>格式化配置</button><button class="button primary" type="submit">${isNew ? "创建配置档案" : "保存新版本"}</button>` : ""}</div></footer></section><aside class="config-inspector"><header><h3>属性</h3></header><label>名称<input name="name" maxlength="100" required value="${esc(formConfig.name)}" ${can("configs.write") ? "" : "readonly"}></label><label>内核<select name="engine" ${isNew && can("configs.write") ? "" : "disabled"}>${engines.map((engine) => `<option value="${engine}" ${engine === formConfig.engine ? "selected" : ""}>${esc(engineName(engine))} · ${engine === "mihomo" ? "YAML" : "JSON"}</option>`).join("")}</select></label><label>说明<textarea class="description-input" name="description" maxlength="300" placeholder="填写用途、节点或变更说明" ${can("configs.write") ? "" : "readonly"}>${esc(formConfig.description || "")}</textarea></label></aside></form>${delivery}${revisionTimeline}${can("configs.delete") && formConfig.id ? '<footer class="config-danger"><span><b>删除配置档案</b><small>相关任务记录会保留，配置档案删除后无法恢复。</small></span><button type="button" data-remove="' + esc(formConfig.id) + '">删除配置</button></footer>' : ""}</article><section class="template-workspace" id="templates"><header class="template-head"><h3>配置模板</h3><span>用 {{node_name}}、{{node_id}}、{{lan_ip}}、{{random_port}} 占位符，按节点批量生成配置。</span></header>${can("templates.write") ? '<details class="template-create" ' + (!templates.length ? "open" : "") + '><summary><b>＋ 新建模板</b></summary><form id="template-form"><label>模板名称<input name="name" maxlength="100" required></label><label>内核<select name="engine">' + engines.map((engine) => `<option value="${engine}">${esc(engineName(engine))}</option>`).join("") + '</select></label><label class="template-content-field">模板正文<textarea name="content" spellcheck="false" required></textarea></label><button class="button primary" type="submit">保存模板</button></form></details>' : ""}<div class="template-grid">${templateCards}</div></section>`,
    "配置档案",
    {
      viewKey: `archive-config-${formConfig.id || "new"}-${state.data.revisionVersion || "current"}`,
    },
  );
  document.querySelectorAll("[data-archive-config]").forEach((link) => {
    link.onclick = (event) => {
      event.preventDefault();
      state.data.newConfig = false;
      state.data.archiveConfigId = link.dataset.archiveConfig;
      state.data.revisionVersion = 0;
      archiveConfigs();
    };
  });
  bindEvent(document.querySelector("#new-config"), "click", () => {
    state.data.newConfig = true;
    state.data.archiveConfigId = "";
    state.data.revisionVersion = 0;
    archiveConfigs();
  });
  bindCodeEditors();
  const archiveForm = document.querySelector("#archive-form");
  const archiveEngine = archiveForm?.querySelector('select[name="engine"]');
  if (archiveEngine && !archiveEngine.disabled) {
    bindEvent(archiveEngine, "change", () => {
      const editor = archiveForm.querySelector("[data-code-editor]");
      if (!editor) return;
      const engine = archiveEngine.value;
      const language = engine === "mihomo" ? "YAML" : "JSON";
      editor.dataset.codeLanguage = language;
      const languageLabel = editor.querySelector(".code-language");
      if (languageLabel) languageLabel.textContent = language;
      const fileLabel = editor.querySelector(".code-file-meta b");
      if (fileLabel)
        fileLabel.textContent = engine === "mihomo" ? "config.yaml" : "config.json";
      const input = editor.querySelector("[data-code-input]");
      if (input) input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  bindEvent(document.querySelector("#archive-form"), "submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      const payload = {
        name: form.get("name"),
        description: form.get("description"),
        engine: form.get("engine") || formConfig.engine,
        content: form.get("content"),
        version: formConfig.version,
      };
      try {
        const saved = await api(
          formConfig.id
            ? `/configs/${encodeURIComponent(formConfig.id)}`
            : "/configs",
          {
            method: formConfig.id ? "PUT" : "POST",
            body: JSON.stringify(payload),
          },
        );
        state.data.newConfig = false;
        state.data.archiveConfigId = saved.id;
        await archiveConfigs();
      } catch (error) {
        notify(error.message, "error");
      }
  });
  bindEvent(document.querySelector("#archive-delivery"), "submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      if (
        form.get("action") === "deploy" &&
        !(await confirmAction(
          "确定将当前配置部署到所选节点并重启对应服务？同一主机每种内核只运行一份配置，本次部署会替换其当前配置。",
          "部署并重启",
        ))
      )
        return;
      await submitTask({
        agent_id: form.get("agent_id"),
        engine: formConfig.engine,
        action: form.get("action"),
        config_id: formConfig.id,
        expected_config_version: formConfig.version,
      });
      location.hash = "#tasks";
  });
  document.querySelectorAll("[data-revision]").forEach(
    (button) =>
      (button.onclick = () => {
        state.data.revisionVersion = Number(button.dataset.revision);
        archiveConfigs();
      }),
  );
  bindEvent(document.querySelector("[data-restore-revision]"), "click", async (event) => {
      if (!(await confirmAction(
        `确定以 v${event.currentTarget.dataset.restoreRevision} 的内容创建新版本？`,
        "创建新版本",
      )))
        return;
      await api(
        `/configs/${encodeURIComponent(formConfig.id)}/revisions/${event.currentTarget.dataset.restoreRevision}/restore`,
        {
          method: "POST",
          body: JSON.stringify({ expected_version: formConfig.version }),
        },
      );
      state.data.revisionVersion = 0;
      await archiveConfigs();
  });
  document.querySelectorAll("[data-remove]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (!(await confirmAction("确认删除配置？", "删除配置"))) return;
        try {
          await api(`/configs/${button.dataset.remove}`, { method: "DELETE" });
          state.data.archiveConfigId = "";
          state.data.newConfig = false;
          archiveConfigs();
        } catch (error) {
          notify(error.message, "error");
        }
      }),
  );
  bindEvent(document.querySelector("#template-form"), "submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      try {
        await api("/templates", {
          method: "POST",
          body: JSON.stringify({
            name: form.get("name"),
            engine: form.get("engine"),
            content: form.get("content"),
          }),
        });
        archiveConfigs();
      } catch (error) {
        notify(error.message, "error");
      }
  });
  document.querySelectorAll("[data-delete-template]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (!(await confirmAction("确认删除模板？", "删除模板"))) return;
        try {
          await api(`/templates/${button.dataset.deleteTemplate}`, {
            method: "DELETE",
          });
          archiveConfigs();
        } catch (error) {
          notify(error.message, "error");
        }
      }),
  );
  document.querySelectorAll("[data-template-apply]").forEach(
    (form) =>
      (form.onsubmit = async (event) => {
        event.preventDefault();
        const agentID = new FormData(form).get("agent_id");
        try {
          await api(`/templates/${form.dataset.templateApply}/apply`, {
            method: "POST",
            body: JSON.stringify({ agent_id: agentID }),
          });
          notify("模板已应用");
        } catch (error) {
          notify(error.message, "error");
        }
      }),
  );
}
  return archiveConfigs;
}
