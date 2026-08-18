package webui

const agentFleetTemplate = `
{{define "agents-page"}}
  {{if and .EnrollmentSecret (roleAtLeast .Role "admin")}}
  <div class="access-secret"><span>一次性注册码</span><code>{{.EnrollmentSecret}}</code><button type="button" data-copy-secret>复制</button><small>写入目标服务器的 <b>QCH_ENROLLMENT_TOKEN</b></small></div>
  {{end}}

  {{if roleAtLeast .Role "admin"}}<details class="enrollment-sheet" id="enrollment" data-enrollment-panel data-has-agents="{{if .Agents}}1{{else}}0{{end}}" {{if not .Agents}}open{{end}}>
    <summary><b>＋ 注册新节点</b><i>＋</i></summary>
    <div class="enrollment-sheet-body">
      <form class="access-form" method="post" action="/ui/enrollment-tokens">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <label>注册码名称<input name="name" maxlength="100" required autocomplete="off" placeholder="例如 shanghai-edge-01"></label>
        {{$defaultTTL := enrollmentTTL .Settings}}<label>有效期<select name="ttl_minutes"><option value="10" {{if eq $defaultTTL 10}}selected{{end}}>10 分钟</option><option value="15" {{if eq $defaultTTL 15}}selected{{end}}>15 分钟</option><option value="30" {{if eq $defaultTTL 30}}selected{{end}}>30 分钟</option><option value="60" {{if eq $defaultTTL 60}}selected{{end}}>1 小时</option></select></label>
        <button class="button primary" type="submit">生成注册码</button>
      </form>
      <p class="enrollment-security-note"><b>生成后只显示一次</b></p>
      {{if .EnrollmentTokens}}<details class="access-history"><summary>注册码记录（{{len .EnrollmentTokens}}）</summary><div>{{range .EnrollmentTokens}}<article><div><strong>{{.Name}}</strong><small>{{if tokenUsable .}}有效至 {{clock .ExpiresAt}}{{else if .RevokedAt}}已吊销{{else if ge .UsedCount .MaxUses}}已使用{{else}}已过期{{end}} · 已用 {{.UsedCount}}/{{.MaxUses}} 次</small></div>{{if tokenUsable .}}<form method="post" action="/ui/enrollment-tokens/{{.ID}}/revoke" data-confirm="确定吊销这个注册码？吊销后将无法用于注册节点。"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit">吊销</button></form>{{end}}</article>{{end}}</div></details>{{end}}
    </div>
  </details>{{end}}

  {{if .Agents}}
  {{if roleAtLeast .Role "operator"}}<form class="batch-toolbar" method="post" action="/ui/tasks/batch" data-batch-form>
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <span class="batch-toolbar-title">批量操作</span>
    <label>内核<select name="engine"><option value="mihomo">Mihomo</option><option value="xray">Xray</option><option value="sing-box">sing-box</option><option value="ss-rust">Shadowsocks Rust</option></select></label>
    <label>动作<select name="action"><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label>
    <button class="button small" type="submit" data-batch-submit disabled>执行</button>
    <small data-batch-count>未选择节点</small>
  </form>{{end}}
  <section class="machine-stack">
    {{range .Agents}}{{$agent := .}}
    <details class="machine-workspace" id="node-{{.ID}}" name="node-workspace" data-agent-node="{{.ID}}" data-agent-metrics="{{.ID}}" data-available="{{if hasMetrics .Metrics}}1{{else}}0{{end}}" {{if eq .ID $.SelectedAgentID}}open{{end}}>
      <summary class="machine-header">
        <div class="machine-identity">{{if roleAtLeast $.Role "operator"}}<label class="batch-select" title="选择此节点参与批量操作"><input type="checkbox" data-batch-checkbox value="{{.ID}}" aria-label="选择 {{.Name}} 参与批量操作"><span></span></label>{{end}}<span class="machine-avatar">●</span><span><strong>{{.Name}}</strong><code>{{.OS}} / {{.Arch}} · {{short .ID}}</code></span></div>
        <section class="machine-resource-summary" aria-label="资源监控">
          <div><span>CPU</span><strong data-metric-text="cpu">{{if .Metrics.CPUAvailable}}{{metricPct .Metrics.CPUPercent}}{{else}}等待采集{{end}}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" max="100" value="{{if .Metrics.CPUAvailable}}{{.Metrics.CPUPercent}}{{else}}0{{end}}"></progress></div>
          <div><span>内存</span><strong data-metric-text="memory">{{if .Metrics.MemoryAvailable}}{{dataSize .Metrics.MemoryUsedBytes}} / {{dataSize .Metrics.MemoryTotalBytes}}{{else}}等待采集{{end}}</strong><progress aria-label="内存使用率" data-metric-progress="memory" max="100" value="{{if .Metrics.MemoryAvailable}}{{usagePct .Metrics.MemoryUsedBytes .Metrics.MemoryTotalBytes}}{{else}}0{{end}}"></progress></div>
          <div><span>磁盘</span><strong data-metric-text="disk">{{if .Metrics.DiskAvailable}}{{dataSize .Metrics.DiskUsedBytes}} / {{dataSize .Metrics.DiskTotalBytes}}{{else}}等待采集{{end}}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" max="100" value="{{if .Metrics.DiskAvailable}}{{usagePct .Metrics.DiskUsedBytes .Metrics.DiskTotalBytes}}{{else}}0{{end}}"></progress></div>
          <div class="machine-resource-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">{{if .Metrics.NetworkAvailable}}{{dataRate .Metrics.NetworkRXBPS}}{{else}}等待采集{{end}}</i> · ↑ <i data-metric-text="upload-rate">{{if .Metrics.NetworkAvailable}}{{dataRate .Metrics.NetworkTXBPS}}{{else}}等待采集{{end}}</i></strong><small>累计 ↓ <span data-metric-text="download-total">{{if .Metrics.NetworkAvailable}}{{dataSize .Metrics.NetworkRXBytes}}{{else}}—{{end}}</span> · ↑ <span data-metric-text="upload-total">{{if .Metrics.NetworkAvailable}}{{dataSize .Metrics.NetworkTXBytes}}{{else}}—{{end}}</span></small></div>
          <span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span>
        </section>
        <div class="machine-state"><span class="status-dot {{statusClass .Status}}" data-agent-status-dot></span><span><b data-agent-status-label>{{if eq .Status "online"}}在线{{else}}离线{{end}}</b><small data-agent-heartbeat>{{heartbeat .LastSeen}}</small></span></div>
        <i class="machine-chevron" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m7 10 5 5 5-5"/></svg></i>
      </summary>

      <div class="machine-body">
        <section class="service-canvas">
          <header class="service-canvas-head"><h2>节点内核</h2><span>{{len .Capabilities}} 个内核</span></header>
          <div class="service-grid">
          {{range $engine := .Capabilities}}{{$runtime := index $agent.Runtime $engine}}{{$serviceKey := deploymentKey $agent.ID $engine}}{{$deployment := index $.Deployments $serviceKey}}{{$deploymentDetail := index $.DeploymentDetails $serviceKey}}{{$deploymentStatus := index $.DeploymentStatuses $serviceKey}}{{$clientAccess := index $.ClientAccess $serviceKey}}{{$configDiff := index $.ConfigDiffs $serviceKey}}
          <article class="service-card service-{{$engine}}">
            <div class="service-card-main">
              <div class="service-overview">
                <header><span class="engine-badge {{$engine}}">{{engineName $engine}}</span><span class="engine-state {{statusClass $runtime.ServiceStatus}}"><i></i><b data-core-service="{{$engine}}">{{if $runtime.ServiceStatus}}{{statusName $runtime.ServiceStatus}}{{else}}未知{{end}}</b></span></header>
                <div class="service-version"><span class="service-version-label"><small>已安装版本</small><button class="service-version-toggle" type="button" data-open-version-form aria-label="打开 {{engineName $engine}} 版本切换">切换版本</button></span><strong data-core-version="{{$engine}}" title="{{if $runtime.Installed}}{{$runtime.Version}}{{else}}未检测到二进制{{end}}">{{if $runtime.Installed}}{{displayEngineVersion $engine $runtime.Version}}{{else}}未检测到二进制{{end}}</strong></div>
              </div>
              <div class="service-deployment">
                <dl class="service-facts"><div><dt>已部署配置</dt><dd>{{if $deployment.ConfigVersion}}v{{$deployment.ConfigVersion}}{{else}}—{{end}}</dd></div><div><dt>已保存配置</dt><dd>{{if $deploymentStatus.SavedVersion}}v{{$deploymentStatus.SavedVersion}}{{else}}—{{end}}</dd></div></dl>
                {{if $deploymentStatus.Drift}}<div class="deployment-drift"><span>{{$deploymentStatus.DriftLabel}}</span><b>{{$deploymentStatus.DriftDetail}}</b></div>{{end}}
                {{if $configDiff}}<details class="config-diff-drawer"><summary>查看配置差异 <i>＋</i></summary>{{$configDiff}}</details>{{end}}
                {{if $deploymentDetail.Endpoint}}<div class="service-endpoint"><span><b>{{$deploymentDetail.Protocol}}</b><small>{{$deploymentDetail.Mode}}</small></span><code>{{$deploymentDetail.Endpoint}}</code></div>{{else if $deployment.ConfigVersion}}<div class="service-endpoint empty"><b>自定义配置</b></div>{{else if $deploymentStatus.SavedVersion}}<div class="service-endpoint empty"><b>尚未部署</b></div>{{else}}<div class="service-endpoint empty"><b>尚未配置</b></div>{{end}}
              </div>
              <div class="service-primary-action">{{if $deploymentStatus.Drift}}<a class="button service-config" href="/agents/{{$agent.ID}}/config/{{$engine}}">查看配置</a><form method="post" action="/ui/tasks" data-deployment-sync data-confirm="确定将已保存配置 v{{$deploymentStatus.SavedVersion}} 部署到 {{engineName $engine}} 并重启服务？" data-confirm-label="部署 v{{$deploymentStatus.SavedVersion}}"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="agent_id" value="{{$agent.ID}}"><input type="hidden" name="engine" value="{{$engine}}"><input type="hidden" name="action" value="deploy"><input type="hidden" name="config_id" value="{{$deploymentStatus.SavedConfigID}}"><input type="hidden" name="return_to" value="/agents?node={{$agent.ID}}"><button class="button primary" type="submit">部署 v{{$deploymentStatus.SavedVersion}}</button></form>{{else}}<a class="button primary service-config" href="/agents/{{$agent.ID}}/config/{{$engine}}">配置 <span>→</span></a>{{end}}</div>
            </div>
            <details class="runtime-drawer">
              <summary><span><b>管理服务</b></span><i>＋</i></summary>
              <div class="runtime-drawer-body">
                <div class="service-actions">
                  <form method="post" action="/ui/tasks"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="agent_id" value="{{$agent.ID}}"><input type="hidden" name="engine" value="{{$engine}}"><input type="hidden" name="action" value="status"><input type="hidden" name="return_to" value="/agents?node={{$agent.ID}}"><button class="button small" type="submit" data-service-action="status" aria-label="查询 {{engineName $engine}} 服务状态" {{if ne $agent.Status "online"}}disabled{{end}}>查询状态</button></form>
                  <form method="post" action="/ui/tasks"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="agent_id" value="{{$agent.ID}}"><input type="hidden" name="engine" value="{{$engine}}"><input type="hidden" name="action" value="start"><input type="hidden" name="return_to" value="/agents?node={{$agent.ID}}"><button class="button small" type="submit" data-service-action="start" aria-label="启动 {{engineName $engine}} 服务" {{if or (ne $agent.Status "online") (eq $runtime.ServiceStatus "active") (eq $runtime.ServiceStatus "activating")}}disabled{{end}}>启动服务</button></form>
                  <form method="post" action="/ui/tasks"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="agent_id" value="{{$agent.ID}}"><input type="hidden" name="engine" value="{{$engine}}"><input type="hidden" name="action" value="restart"><input type="hidden" name="return_to" value="/agents?node={{$agent.ID}}"><button class="button small" type="submit" data-service-action="restart" aria-label="重启 {{engineName $engine}} 服务" {{if or (ne $agent.Status "online") (eq $runtime.ServiceStatus "inactive") (eq $runtime.ServiceStatus "activating") (eq $runtime.ServiceStatus "deactivating")}}disabled{{end}}>重启服务</button></form>
                  <form method="post" action="/ui/tasks" data-confirm="确定停止 {{engineName $engine}} 服务？现有连接会立即中断，需再次启动才能恢复。" data-confirm-label="停止服务"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="agent_id" value="{{$agent.ID}}"><input type="hidden" name="engine" value="{{$engine}}"><input type="hidden" name="action" value="stop"><input type="hidden" name="return_to" value="/agents?node={{$agent.ID}}"><button class="button small danger-button" type="submit" data-service-action="stop" aria-label="停止 {{engineName $engine}} 服务" {{if or (ne $agent.Status "online") (eq $runtime.ServiceStatus "inactive") (eq $runtime.ServiceStatus "deactivating")}}disabled{{end}}>停止服务</button></form>
                </div>
              </div>
            </details>
            <details class="runtime-drawer version-drawer">
              <summary><span><b>版本切换</b><small>安装或切换内核版本</small></span><i>＋</i></summary>
              <div class="runtime-drawer-body">
                <form class="core-version-form" method="post" action="/ui/tasks" data-confirm="确定提交内核安装或版本切换任务？下载和校验完成后，目标服务会重启。" data-confirm-label="提交任务"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="agent_id" value="{{$agent.ID}}"><input type="hidden" name="engine" value="{{$engine}}"><input type="hidden" name="action" value="install"><input type="hidden" name="return_to" value="/agents?node={{$agent.ID}}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options" role="radiogroup"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset><label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" {{if ne $agent.Status "online"}}disabled{{end}}>{{if $runtime.Installed}}升级或切换版本{{else}}安装内核{{end}}</button>{{if $runtime.Installed}}<small>官方 Release · SHA-256 校验</small>{{else}}<small>首次安装前需准备安全目录与 systemd 单元</small>{{end}}</form>
              </div>
            </details>
			{{if $clientAccess.Profiles}}<a class="service-client-access" href="/client-access?agent_id={{$agent.ID}}&amp;engine={{$engine}}"><span><b>客户端配置</b><small>{{$clientAccess.Source}} · {{$clientAccess.Address}}</small></span><strong>{{len $clientAccess.Profiles}} 个入站 <i>→</i></strong></a>{{end}}
          </article>
          {{end}}
          </div>
        </section>

        <details class="machine-profile node-inspector">
          <summary class="node-inspector-summary"><span><b>节点身份</b><small>身份信息 · 指标趋势</small></span><i>＋</i></summary>
          <div class="node-inspector-body">
            <section class="node-identity-panel">
              <h2>{{.Name}}</h2>
              <dl class="identity-list"><div><dt>节点 ID</dt><dd><code>{{.ID}}</code></dd></div><div><dt>系统平台</dt><dd>{{.OS}} / {{.Arch}}</dd></div><div><dt>Agent 版本</dt><dd data-agent-version>{{if .Version}}{{.Version}}{{else}}未知{{end}}</dd></div><div><dt>注册时间</dt><dd>{{clock .EnrolledAt}}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl>
              {{if .Labels}}<div class="labels">{{range $key, $value := .Labels}}<span>{{$key}}={{$value}}</span>{{end}}</div>{{end}}
              <footer class="node-identity-refresh"><span data-metric-text="stamp">{{if hasMetrics .Metrics}}{{ago .Metrics.CollectedAt}}{{else}}等待资源数据{{end}}</span><button type="button" data-agent-refresh>刷新</button></footer>
            </section>
            {{if $.MetricHistory}}<section class="metric-trend-panel" aria-label="最近 24 小时流量趋势"><header><b>流量趋势</b><small>最近 24 小时 · 每分钟采样</small></header>{{trafficChart $.MetricHistory}}</section>{{else}}<section class="metric-trend-empty" aria-label="暂无指标趋势"><span>⌁</span><b>暂无指标趋势</b><small>节点上线并上报指标后，这里将显示最近 24 小时的上下行速率曲线。</small></section>{{end}}
          </div>
        </details>

        <footer class="machine-footer"><span><i>●</i><b>节点身份已验证</b></span>{{if roleAtLeast $.Role "admin"}}<details><summary>节点管理</summary><form method="post" action="/ui/agents/{{.ID}}/delete" data-confirm="确定移除此节点并永久吊销其身份？移除后 Agent 将无法再次连接。"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit">移除节点并吊销身份</button></form></details>{{end}}</footer>
      </div>
    </details>
    {{end}}
  </section>
  {{else}}
  <div class="empty large"><strong>还没有节点</strong><p>请先生成注册码。</p><code>QCH_SERVER_URL=wss://control.example.com QCH_ENROLLMENT_TOKEN=*** qagent</code></div>
  {{end}}
  <script src="/assets/node-workspace.js?v=ui-desktop-3" defer></script>
  <script src="/assets/metrics.js?v=ui-functional-10" defer></script>
{{end}}
`
