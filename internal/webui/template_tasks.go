package webui

const taskAuditTemplate = `
{{define "tasks-page"}}
  <div class="task-workspace" data-task-page data-task-poll-ms="{{taskPollInterval .Settings}}">
    <details class="task-filter-panel" open>
      <summary><b>筛选</b><i>⌄</i></summary>
      <form class="audit-query" method="get" action="/tasks">
        <label>节点<select name="agent_id"><option value="">全部节点</option>{{range .Agents}}<option value="{{.ID}}" {{if eq .ID $.TaskAgentFilter}}selected{{end}}>{{.Name}}</option>{{end}}</select></label>
        <label>状态<select name="status"><option value="">全部状态</option><option value="pending" {{if eq .TaskStatusFilter "pending"}}selected{{end}}>准备中</option><option value="running" {{if eq .TaskStatusFilter "running"}}selected{{end}}>执行中</option><option value="succeeded" {{if eq .TaskStatusFilter "succeeded"}}selected{{end}}>成功</option><option value="failed" {{if eq .TaskStatusFilter "failed"}}selected{{end}}>失败</option><option value="canceled" {{if eq .TaskStatusFilter "canceled"}}selected{{end}}>已取消</option></select></label>
        <label>动作<select name="action"><option value="">全部动作</option><option value="validate" {{if eq .TaskActionFilter "validate"}}selected{{end}}>校验配置</option><option value="deploy" {{if eq .TaskActionFilter "deploy"}}selected{{end}}>部署并重启</option><option value="read-config" {{if eq .TaskActionFilter "read-config"}}selected{{end}}>读取当前配置</option><option value="start" {{if eq .TaskActionFilter "start"}}selected{{end}}>启动服务</option><option value="stop" {{if eq .TaskActionFilter "stop"}}selected{{end}}>停止服务</option><option value="restart" {{if eq .TaskActionFilter "restart"}}selected{{end}}>重启服务</option><option value="status" {{if eq .TaskActionFilter "status"}}selected{{end}}>查询状态</option><option value="install" {{if eq .TaskActionFilter "install"}}selected{{end}}>升级内核</option></select></label>
        <label>每页数量<select name="limit"><option value="50" {{if eq .TaskLimit 50}}selected{{end}}>50 条</option><option value="100" {{if eq .TaskLimit 100}}selected{{end}}>100 条</option><option value="500" {{if eq .TaskLimit 500}}selected{{end}}>500 条</option></select></label>
        <button class="button primary" type="submit">应用筛选</button><a href="/tasks">重置筛选</a>
      </form>
    </details>
    <div class="audit-live" role="status" aria-live="polite" data-task-refresh-status><i></i>自动更新</div>

    {{if .Tasks}}
    <section class="task-timeline" aria-label="任务时间线">
      {{range .Tasks}}{{$diagnostic := taskDiagnostic .}}<article id="task-{{.ID}}" class="audit-event task-event {{if eq .ID $.FocusTaskID}}focused{{end}}" data-task-id="{{.ID}}" data-task-status="{{.Status}}" data-task-simulated="{{if .Simulated}}1{{else}}0{{end}}" aria-busy="{{if or (eq .Status "pending") (eq .Status "running")}}true{{else}}false{{end}}">
        <div class="timeline-marker"><i class="{{if and .Simulated (eq .Status "succeeded")}}warn{{else}}{{statusClass .Status}}{{end}}"></i><span></span></div>
        <div class="task-event-card">
          <header><div class="event-action"><span class="status-label {{if and .Simulated (eq .Status "succeeded")}}warn{{else}}{{statusClass .Status}}{{end}}" data-live-task-label>{{if and .Simulated (eq .Status "succeeded")}}模拟完成{{else}}{{statusName .Status}}{{end}}</span><strong>{{actionName .Action}}</strong><small><code title="{{.ID}}">{{short .ID}}</code> · <span data-live-task-attempt>{{if .Attempt}}第 {{.Attempt}} 次执行{{else}}尚未开始{{end}}</span></small>{{if eq .Status "pending"}}<form method="post" action="/ui/tasks/{{.ID}}/cancel" data-confirm="确定取消这个准备中的任务？取消后不会再发送给 Agent。" data-confirm-label="取消任务" data-live-pending-action><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit">取消任务</button></form>{{else if or (eq .Status "failed") (eq .Status "canceled")}}{{with index $.TaskRetryReasons .ID}}<small>{{.}}</small>{{else}}<form method="post" action="/ui/tasks/{{.ID}}/retry"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit">{{if .ConfigID}}使用当前配置重试{{else}}重试任务{{end}}</button></form>{{end}}{{end}}</div><time><b>{{clock .CreatedAt}}</b><small>{{ago .CreatedAt}}</small></time></header>
          <div class="task-event-body"><div class="event-target"><span class="engine-badge {{.Engine}}">{{engineName .Engine}}</span><span><b>节点</b><small>{{short .AgentID}}</small></span>{{if .ConfigID}}<span><b>配置</b><small>{{short .ConfigID}} · v{{.ConfigVersion}}</small></span>{{else if .CoreVersion}}<span><b>版本</b><small>{{.CoreVersion}}</small></span>{{end}}<span class="task-lifecycle"><b>耗时</b><small data-live-task-timing>{{taskTiming .}}</small></span></div><div class="event-result" data-live-task-result>{{if $diagnostic.Title}}<div class="task-diagnostic"><b>{{$diagnostic.Title}}</b><small>{{$diagnostic.Advice}}</small></div>{{end}}{{if or .Output .Error}}<details {{if eq .ID $.FocusTaskID}}open{{end}}><summary>节点结果 <span>→</span></summary>{{if .Error}}<div class="task-result-block"><header><b>错误</b><button type="button" data-copy-value data-copy-target="#task-error-{{.ID}}">复制</button></header><pre id="task-error-{{.ID}}" class="task-error">{{.Error}}</pre></div>{{end}}{{if .Output}}<div class="task-result-block"><header><b>输出</b><button type="button" data-copy-value data-copy-target="#task-output-{{.ID}}">复制</button></header><pre id="task-output-{{.ID}}">{{.Output}}</pre></div>{{end}}</details>{{else if or (eq .Status "pending") (eq .Status "running")}}<span>执行中</span>{{end}}</div></div>
        </div>
      </article>{{end}}
    </section>
    <footer class="task-load-more" data-task-load-more hidden><span>已显示 <b data-task-visible-count>{{len .Tasks}}</b> / {{len .Tasks}} 条记录</span><button class="button" type="button" data-task-load-more-button>再显示 20 条</button></footer>
    {{else}}<div class="empty large"><strong>没有符合条件的任务</strong></div>{{end}}
  </div>
{{end}}
`
