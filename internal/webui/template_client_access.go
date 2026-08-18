package webui

const clientAccessTemplate = `
{{define "client-access-page"}}{{$page := .ClientAccessPage}}
  <section class="client-access-workspace">
    <header class="client-access-hero">
      <div><p class="eyebrow">Client access</p><h1>客户端配置</h1><p>集中查看已部署入站生成的客户端连接信息。凭据默认隐藏，只在本页按需显示或复制。</p></div>
      {{if $page}}<dl class="client-access-summary"><div><dt>可用节点</dt><dd>{{$page.TotalNodes}}</dd></div><div><dt>客户端入站</dt><dd>{{$page.TotalProfiles}}</dd></div></dl>{{end}}
    </header>

    {{if $page}}
    <section class="client-access-filter-panel" aria-label="客户端配置筛选">
      <form class="client-access-search" method="get" action="/client-access">
        {{if $page.AgentID}}<input type="hidden" name="agent_id" value="{{$page.AgentID}}">{{end}}
        {{if $page.Engine}}<input type="hidden" name="engine" value="{{$page.Engine}}">{{end}}
        <label><span>搜索入站</span><input type="search" name="q" value="{{$page.Query}}" placeholder="节点、地址、协议或入站名称" autocomplete="off"></label>
        <button class="button primary" type="submit">搜索</button>
        {{if $page.Query}}<a class="button" href="/client-access{{if $page.AgentID}}?agent_id={{$page.AgentID}}{{if $page.Engine}}&amp;engine={{$page.Engine}}{{end}}{{else if $page.Engine}}?engine={{$page.Engine}}{{end}}">清除搜索</a>{{end}}
      </form>
      <div class="client-access-filter-row"><span>节点</span><nav aria-label="按节点筛选"><a class="{{if not $page.AgentID}}active{{end}}" href="/client-access{{if $page.Engine}}?engine={{$page.Engine}}{{end}}">全部节点</a>{{range .Agents}}<a class="{{if eq .ID $page.AgentID}}active{{end}}" href="/client-access?agent_id={{.ID}}{{if $page.Engine}}&amp;engine={{$page.Engine}}{{end}}">{{.Name}}</a>{{end}}</nav></div>
      <div class="client-access-filter-row"><span>内核</span><nav aria-label="按内核筛选"><a class="{{if not $page.Engine}}active{{end}}" href="/client-access{{if $page.AgentID}}?agent_id={{$page.AgentID}}{{end}}">全部内核</a><a class="{{if eq $page.Engine "mihomo"}}active{{end}}" href="/client-access?engine=mihomo{{if $page.AgentID}}&amp;agent_id={{$page.AgentID}}{{end}}">Mihomo</a><a class="{{if eq $page.Engine "xray"}}active{{end}}" href="/client-access?engine=xray{{if $page.AgentID}}&amp;agent_id={{$page.AgentID}}{{end}}">Xray</a><a class="{{if eq $page.Engine "sing-box"}}active{{end}}" href="/client-access?engine=sing-box{{if $page.AgentID}}&amp;agent_id={{$page.AgentID}}{{end}}">sing-box</a><a class="{{if eq $page.Engine "ss-rust"}}active{{end}}" href="/client-access?engine=ss-rust{{if $page.AgentID}}&amp;agent_id={{$page.AgentID}}{{end}}">Shadowsocks Rust</a></nav></div>
    </section>

    <div class="client-access-results-head"><span>当前结果</span><strong>{{len $page.Entries}} 组内核配置</strong></div>
    <div class="client-access-entry-grid">
      {{range $entryIndex, $entry := $page.Entries}}{{$agent := $entry.Agent}}{{$engine := $entry.Engine}}
      <article class="client-access-entry">
        <header class="client-access-entry-head">
          <div class="client-access-node"><span class="node-avatar">●</span><span><strong>{{$agent.Name}}</strong><small>{{$agent.OS}} / {{$agent.Arch}} · {{short $agent.ID}}</small></span></div>
          <div class="client-access-engine"><span class="engine-badge {{$engine}}">{{engineName $engine}}</span><span class="status-label {{statusClass $agent.Status}}">{{if eq $agent.Status "online"}}在线{{else}}离线{{end}}</span></div>
        </header>
        <div class="client-access-entry-meta"><span><small>连接地址</small><code>{{$entry.Access.Address}}</code></span><span><small>地址来源</small><strong>{{$entry.Access.Source}}</strong></span><a href="/agents/{{$agent.ID}}/config/{{$engine}}">服务端配置 →</a></div>
        <div class="client-access-profile-grid">
          {{range $profileIndex, $profile := $entry.Access.Profiles}}<article class="client-profile-card">
            <header><span><b>{{$profile.Protocol}}</b><small>{{$profile.Tag}} · {{$profile.Profile.Format}}</small></span><span class="status-label warn">含凭据</span></header>
            <form class="secret-value-control client-share-control" action="#"><input id="client-access-share-{{$agent.ID}}-{{$engine}}-{{$entryIndex}}-{{$profileIndex}}" name="client_share" type="password" readonly autocomplete="off" spellcheck="false" aria-label="{{$profile.Tag}} 客户端分享 URI" value="{{$profile.Profile.URI}}"><button type="button" data-secret-visibility data-secret-label="{{$profile.Tag}} 客户端分享 URI" aria-label="显示{{$profile.Tag}}客户端分享 URI">显示</button><button type="button" data-copy-value data-copy-target="#client-access-share-{{$agent.ID}}-{{$engine}}-{{$entryIndex}}-{{$profileIndex}}">复制</button></form>
            <details class="client-parameter-menu"><summary>逐项参数 <i>展开</i></summary><div class="client-parameters">{{range $profile.Profile.Fields}}<div><span>{{.Label}}</span>{{if .Secret}}<form class="secret-value-control" action="#"><input name="client_secret" type="password" readonly autocomplete="off" spellcheck="false" aria-label="{{$profile.Tag}} {{.Label}}" value="{{.Value}}"><button type="button" data-secret-visibility data-secret-label="{{$profile.Tag}} {{.Label}}" aria-label="显示{{$profile.Tag}}{{.Label}}">显示</button></form>{{else}}<code title="{{.Value}}">{{.Value}}</code>{{end}}</div>{{end}}</div></details>
          </article>{{end}}
        </div>
      </article>
      {{else}}
      <section class="client-access-empty-state"><span>⌁</span><h2>没有匹配的客户端配置</h2><p>客户端信息只会从已部署且可解析的入站生成。请调整筛选条件，或先在节点内核中完成配置与部署。</p><a class="button primary" href="/agents">返回节点</a></section>
      {{end}}
    </div>
    {{end}}
  </section>
{{end}}
`
