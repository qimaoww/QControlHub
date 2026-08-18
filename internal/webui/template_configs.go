package webui

const configWorkbenchTemplate = `
{{define "live-config-page"}}
  {{$page := .LiveConfigPage}}
  {{if $page}}
  <article class="live-config-workspace">
    <header class="editor-toolbar">
      <h2>{{$page.Agent.Name}} · {{engineName $page.Engine}}</h2>
      <div class="editor-toolbar-state"><span class="engine-badge {{$page.Engine}}">{{engineName $page.Engine}}</span><b>{{if $page.HasSavedConfig}}v{{$page.Config.Version}}{{else}}未保存{{end}}</b></div>
    </header>
    {{if $page.SourceLoaded}}
    <form class="live-config-editor" id="config-editor" method="post" action="/ui/agents/{{$page.Agent.ID}}/config/{{$page.Engine}}/save" data-profile-editor data-new-config="0" data-engine="{{$page.Engine}}">
      <input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="mode" value="source"><input type="hidden" name="version" value="{{$page.Config.Version}}"><input type="hidden" name="name" value="{{$page.Config.Name}}"><input type="hidden" name="description" value="{{$page.Config.Description}}"><input type="hidden" name="return_to" value="{{$page.ReturnTo}}">
      <section class="code-workspace" data-code-editor data-code-language="{{if eq $page.Engine "mihomo"}}YAML{{else}}JSON{{end}}" data-code-max-bytes="2097152">
        <header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>{{if eq $page.Engine "mihomo"}}config.yaml{{else}}config.json{{end}}</b></div><div class="code-editor-meta"><span class="code-language">{{if eq $page.Engine "mihomo"}}YAML{{else}}JSON{{end}}</span><span data-code-status aria-live="polite">已读取</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header>
        <div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="{{engineName $page.Engine}} 节点配置源码" spellcheck="false" required>{{$page.Config.Content}}</textarea></div>
        <footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite">{{if $page.Draft}}未部署{{end}}</span></span><div><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button><button class="button" name="intent" value="validate" type="submit">校验修改</button><button class="button primary" name="intent" value="deploy" type="submit" data-confirm-submit="确定保存当前源码、写入节点固定配置并重启服务？" data-confirm-label="保存并部署">保存并部署</button></div></footer>
      </section>
      <aside class="live-config-inspector"><dl><div><dt>节点</dt><dd>{{$page.Agent.Name}}</dd></div><div><dt>系统</dt><dd>{{$page.Agent.OS}} / {{$page.Agent.Arch}}</dd></div><div><dt>内核</dt><dd>{{displayEngineVersion $page.Engine $page.Runtime.Version}}</dd></div><div><dt>来源</dt><dd>{{if $page.Draft}}控制面版本{{else}}节点文件{{end}}</dd></div></dl></aside>
    </form>
    {{else if ne $page.Agent.Status "online"}}
    <section class="node-config-source"><h2>节点离线</h2><span class="status-label warn">无法读取</span></section>
    {{else if not .FocusTaskID}}
    <section class="node-config-source" role="status" aria-live="polite"><h2>正在读取配置</h2><span class="status-label warn">读取中</span><form method="post" action="/ui/tasks" data-auto-read-current hidden><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="agent_id" value="{{$page.Agent.ID}}"><input type="hidden" name="engine" value="{{$page.Engine}}"><input type="hidden" name="action" value="read-config"><input type="hidden" name="automatic_read" value="1"><input type="hidden" name="return_to" value="{{$page.ReturnTo}}"></form></section>
    {{end}}
  </article>
  {{else}}
  <section class="empty large live-config-empty"><strong>没有可读取的节点配置</strong><p>请先让节点上线并安装支持的内核。</p><a class="button primary" href="/agents">前往节点管理</a></section>
  {{end}}
{{end}}

{{define "configs-page"}}
  <article class="config-workspace">
    <header class="editor-toolbar">
      <h2>{{.FormConfig.Name}}</h2>
      <div class="editor-toolbar-state"><span class="engine-badge {{.FormConfig.Engine}}">{{engineName .FormConfig.Engine}}</span><b>{{if .IsNewConfig}}草稿{{else}}v{{.FormConfig.Version}}{{end}}</b></div>
    </header>

    <form class="config-editor-grid" id="config-editor" method="post" action="/ui/configs/save" data-profile-editor data-new-config="{{if .IsNewConfig}}1{{else}}0{{end}}" data-engine="{{.FormConfig.Engine}}">
      <input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="id" value="{{.FormConfig.ID}}"><input type="hidden" name="version" value="{{.FormConfig.Version}}">
      <section class="code-workspace" data-code-editor data-code-language="{{if eq .FormConfig.Engine "mihomo"}}YAML{{else}}JSON{{end}}" data-code-max-bytes="2097152">
        <header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>{{if eq .FormConfig.Engine "mihomo"}}config.yaml{{else}}config.json{{end}}</b></div><div class="code-editor-meta"><span class="code-language">{{if eq .FormConfig.Engine "mihomo"}}YAML{{else}}JSON{{end}}</span><span data-code-status aria-live="polite">{{if .IsNewConfig}}草稿{{else}}已保存{{end}}</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header>
        <div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="{{engineName .FormConfig.Engine}} 配置档案源码" spellcheck="false" required>{{.FormConfig.Content}}</textarea></div>
        <footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite"></span></span><div><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button><button class="button primary" type="submit">{{if .IsNewConfig}}创建配置档案{{else}}保存新版本{{end}}</button></div></footer>
      </section>
      <aside class="config-inspector">
        <header><h3>属性</h3></header>
        <label>名称<input name="name" maxlength="100" required value="{{.FormConfig.Name}}"></label>
        <label>内核<select name="engine"><option value="mihomo" {{if eq .FormConfig.Engine "mihomo"}}selected{{end}}>Mihomo · YAML</option><option value="xray" {{if eq .FormConfig.Engine "xray"}}selected{{end}}>Xray · JSON</option><option value="sing-box" {{if eq .FormConfig.Engine "sing-box"}}selected{{end}}>sing-box · JSON</option><option value="ss-rust" {{if eq .FormConfig.Engine "ss-rust"}}selected{{end}}>Shadowsocks Rust · JSON</option></select></label>
        <label>说明<textarea class="description-input" name="description" maxlength="300" placeholder="填写用途、节点或变更说明">{{.FormConfig.Description}}</textarea></label>
      </aside>
    </form>

    {{if not .IsNewConfig}}
    <section class="delivery-bar">
      <div><span class="delivery-icon"><svg viewBox="0 0 24 24"><path d="M13 2.5 5.5 13H11l-1 8.5L18.5 11H13z"/></svg></span><h3>校验或部署</h3></div>
      <form method="post" action="/ui/tasks" data-confirm="确定将当前配置部署到所选节点并重启对应服务？" data-confirm-label="部署并重启" data-confirm-action="deploy"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="config_id" value="{{.FormConfig.ID}}"><input type="hidden" name="engine" value="{{.FormConfig.Engine}}"><input type="hidden" name="return_to" value="/configs/archive?id={{.FormConfig.ID}}"><label>目标节点<select name="agent_id" required><option value="">{{if .DeployAgents}}选择在线且支持 {{engineName .FormConfig.Engine}} 的节点{{else}}没有在线且支持 {{engineName .FormConfig.Engine}} 的节点{{end}}</option>{{range .DeployAgents}}<option value="{{.ID}}">{{.Name}} · 在线</option>{{end}}</select></label><label>执行方式<select name="action"><option value="validate">仅校验，不写入</option><option value="deploy">部署并重启</option></select></label><button class="button primary" type="submit" {{if not .DeployAgents}}disabled{{end}}>提交任务</button></form>
    </section>

    <details class="revision-timeline" {{if .HasRevisionPreview}}open{{end}}>
      <summary><b>版本历史</b><strong>{{len .ConfigRevisions}} 个版本</strong></summary>
      <div class="timeline-body">
        <nav aria-label="配置修订历史">{{range .ConfigRevisions}}<a class="{{if and $.HasRevisionPreview (eq .Version $.RevisionPreview.Version)}}active{{end}} {{if eq .Version $.FormConfig.Version}}current{{end}}" href="/configs/archive?id={{$.FormConfig.ID}}&revision={{.Version}}"><i></i><span><b>v{{.Version}}</b><strong>{{.Name}}</strong><small>{{ago .UpdatedAt}}{{if eq .Version $.FormConfig.Version}} · 当前{{end}}</small></span></a>{{end}}</nav>
        {{if .HasRevisionPreview}}<section class="timeline-preview"><header><div><b>v{{.RevisionPreview.Version}} · {{.RevisionPreview.Name}}</b><small>{{engineName .RevisionPreview.Engine}} · {{clock .RevisionPreview.UpdatedAt}}</small></div>{{if eq .RevisionPreview.Version .FormConfig.Version}}<span class="status-label ok">当前版本</span>{{end}}</header><textarea readonly aria-label="v{{.RevisionPreview.Version}} 配置正文">{{.RevisionPreview.Content}}</textarea>{{if and (ne .RevisionPreview.Version .FormConfig.Version) (roleAtLeast .Role "admin")}}<form method="post" action="/ui/configs/{{.FormConfig.ID}}/revisions/{{.RevisionPreview.Version}}/restore" data-confirm="确定以 v{{.RevisionPreview.Version}} 的内容创建一个新版本？"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="expected_version" value="{{.FormConfig.Version}}"><input type="hidden" name="return_to" value="/configs/archive?id={{.FormConfig.ID}}&revision={{.RevisionPreview.Version}}"><button class="button" type="submit">以此版本创建新版本</button></form>{{end}}</section>{{else}}<div class="timeline-placeholder">选择版本</div>{{end}}
      </div>
    </details>

    {{if roleAtLeast .Role "admin"}}<footer class="config-danger"><span><b>删除配置档案</b><small>相关任务记录会保留，配置档案删除后无法恢复。</small></span><form method="post" action="/ui/configs/{{.FormConfig.ID}}/delete" data-confirm="确定删除此配置档案？相关任务记录会保留，但配置档案无法恢复。"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">删除配置</button></form></footer>{{end}}
    {{end}}
  </article>

  <section class="template-workspace" id="templates">
    <header class="template-head"><h3>配置模板</h3><span>用 {{"{{node_name}}"}}、{{"{{node_id}}"}}、{{"{{lan_ip}}"}}、{{"{{random_port}}"}} 占位符，按节点批量生成配置。</span></header>
    {{if roleAtLeast .Role "operator"}}<details class="template-create" {{if not .Templates}}open{{end}}>
      <summary><b>＋ 新建模板</b></summary>
      <form method="post" action="/ui/templates">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <label>模板名称<input name="name" maxlength="100" required autocomplete="off" placeholder="例如 通用 SS 服务端"></label>
        <label>内核<select name="engine"><option value="mihomo">Mihomo 路 YAML</option><option value="xray">Xray 路 JSON</option><option value="sing-box">sing-box 路 JSON</option><option value="ss-rust">Shadowsocks Rust 路 JSON</option></select></label>
        <label class="template-content-field">模板正文<textarea name="content" spellcheck="false" required placeholder='{"server":"{{"{{lan_ip}}"}}","server_port":{{"{{random_port}}"}},"password":"change-me","method":"chacha20-ietf-poly1305"}'></textarea></label>
        <button class="button primary" type="submit">保存模板</button>
      </form>
    </details>{{end}}
    {{if .Templates}}<div class="template-grid">
      {{range .Templates}}<article class="template-card">
        <header><span class="engine-badge {{.Engine}}">{{engineName .Engine}}</span><h4>{{.Name}}</h4><small>{{ago .UpdatedAt}}</small></header>
        <pre>{{.Content}}</pre>
        {{if roleAtLeast $.Role "operator"}}<footer>
          <form method="post" action="/ui/templates/{{.ID}}/apply" data-confirm="确定将模板应用到所选节点并保存为其配置？"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>应用至<select name="agent_id" required><option value="">{{if $.Agents}}选择在线节点{{else}}没有节点{{end}}</option>{{range $.Agents}}{{if eq .Status "online"}}<option value="{{.ID}}">{{.Name}} · {{engineName $.FormConfig.Engine}}</option>{{end}}{{end}}</select></label><button class="button small" type="submit">应用</button></form>
          <form method="post" action="/ui/templates/{{.ID}}/delete" data-confirm="确定删除模板 {{.Name}}？"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="button small danger-button" type="submit">删除</button></form>
        </footer>{{end}}
      </article>{{end}}
    </div>{{else}}<p class="template-empty">还没有模板。新建模板后可按节点变量一键生成配置。</p>{{end}}
  </section>
{{end}}
`
