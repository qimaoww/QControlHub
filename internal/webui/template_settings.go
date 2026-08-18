package webui

const settingsTemplate = `
{{define "settings-page"}}
  {{$ttl := enrollmentTTL .Settings}}{{$pageSize := taskPageSize .Settings}}{{$poll := taskPollInterval .Settings}}
  <div class="settings-workspace">
    <header class="settings-hero"><h2>系统设置</h2></header>

    <form class="settings-form" method="post" action="/ui/settings">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <section class="settings-section" id="identity">
        <header><span class="settings-section-number">01</span><h3>面板标识</h3></header>
        <div class="settings-grid">
          <label class="settings-field"><span>面板名称</span><input name="panel_name" value="{{panelName .Settings}}" maxlength="40" required autocomplete="organization"></label>
          <label class="settings-field"><span>面板说明</span><input name="panel_description" value="{{panelDescription .Settings}}" maxlength="120"></label>
        </div>
      </section>

      <section class="settings-section" id="defaults">
        <header><span class="settings-section-number">02</span><h3>操作默认值</h3></header>
        <div class="settings-grid">
          <label class="settings-field"><span>入网码默认有效期</span><select name="enrollment_ttl_minutes"><option value="10" {{if eq $ttl 10}}selected{{end}}>10 分钟</option><option value="15" {{if eq $ttl 15}}selected{{end}}>15 分钟</option><option value="30" {{if eq $ttl 30}}selected{{end}}>30 分钟</option><option value="60" {{if eq $ttl 60}}selected{{end}}>1 小时</option></select></label>
          <label class="settings-field"><span>任务默认显示数量</span><select name="task_page_size"><option value="50" {{if eq $pageSize 50}}selected{{end}}>50 条</option><option value="100" {{if eq $pageSize 100}}selected{{end}}>100 条</option><option value="500" {{if eq $pageSize 500}}selected{{end}}>500 条</option></select></label>
        </div>
      </section>

      <section class="settings-section" id="synchronization">
        <header><span class="settings-section-number">03</span><h3>状态同步</h3></header>
        <div class="settings-grid one-column">
          <label class="settings-field"><span>任务状态刷新频率</span><select name="task_poll_interval_ms"><option value="600" {{if eq $poll 600}}selected{{end}}>0.6 秒</option><option value="1000" {{if eq $poll 1000}}selected{{end}}>1 秒</option><option value="2000" {{if eq $poll 2000}}selected{{end}}>2 秒</option><option value="5000" {{if eq $poll 5000}}selected{{end}}>5 秒</option></select></label>
        </div>
      </section>

      <section class="settings-section" id="notifications">
        <header><span class="settings-section-number">04</span><h3>事件通知</h3></header>
        <div class="settings-grid one-column">
          <label class="settings-field"><span>Webhook 地址</span><input name="webhook_url" value="{{.Settings.WebhookURL}}" maxlength="500" placeholder="https://example.com/hooks/qcontrolhub（留空禁用）" autocomplete="off" spellcheck="false"></label>
          <p class="settings-hint">任务失败、节点离线或恢复在线时，控制面会向该地址 POST 带 <code>X-QControlHub-Signature</code> HMAC-SHA256 签名的 JSON 事件（通过 <code>QCH_WEBHOOK_SECRET</code> 签名），可对接钉钉 / 企业微信自定义机器人或自建接收端。</p>
        </div>
      </section>

      {{if .AuditLogs}}<section class="settings-section" id="audit">
        <header><span class="settings-section-number">05</span><h3>最近操作</h3></header>
        <ol class="audit-list">
          {{range .AuditLogs}}<li><time>{{clock .ActedAt}}</time><span class="audit-action">{{auditActionName .Action}}</span>{{if .Target}}<code>{{.Target}}</code>{{end}}{{if .Detail}}<small>{{.Detail}}</small>{{end}}<em>{{.RemoteIP}}</em></li>{{end}}
        </ol>
      </section>{{end}}

      {{if roleAtLeast .Role "admin"}}<footer class="settings-savebar"><div><button class="button" type="submit" name="action" value="reset" data-confirm-submit="确定恢复系统默认设置？面板名称和所有操作默认值都会重置。" data-confirm-label="恢复默认值">恢复默认值</button><button class="button primary" type="submit" name="action" value="save">保存设置</button></div></footer>{{else}}<p class="settings-hint">当前为只读角色，仅可查看设置与操作记录。</p>{{end}}
    </form>
  </div>
{{end}}
`
