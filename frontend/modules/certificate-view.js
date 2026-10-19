export function createCertificateView({ esc, date }) {
  const statusNames = {offline:"来源节点离线",unverified:"来源待核验",paused:"已暂停同步",detached:"已解除关联",deleted:"已删除并停止导入",queued:"等待处理",running:"处理中",waiting_dns:"等待 DNS",validating:"验证中",issued:"已签发",succeeded:"成功",failed:"失败",canceled:"已取消",canceling:"取消中",pending:"待同步",synced:"已同步",blocked:"授权或环境阻断",processing:"验证中",awaiting_challenge:"等待验证",valid:"已验证",expired:"已过期"};
  const badge = value => `<span class="status-label ${["issued","succeeded","synced","valid"].includes(value) ? "ok" : ["failed","blocked","expired"].includes(value) ? "bad" : "warn"}">${esc(statusNames[value] || value)}</span>`;
  const stages={queued:"等待处理",order_created:"订单已创建",challenges_ready:"验证记录已准备",waiting_dns:"等待 DNS 记录",dns_create_intent:"准备 DNS 记录",dns_created:"DNS 记录已创建",http_preflight:"检查 HTTP 入口",validating:"验证中",finalizing:"签发中",cleanup:"清理验证资源",complete:"已完成",canceled:"已取消",retry:"等待重试"};
  const source = {caddy:"Caddy 托管",acme:"ACME",import:"导入","self-signed":"自签"};
  const method = {"dns-api":"DNS API 自动验证","dns-manual":"手动 DNS 验证","http-01":"已有节点 HTTP-01"};
  const ca = {letsencrypt:"Let’s Encrypt","letsencrypt-staging":"Let’s Encrypt 测试环境",zerossl:"ZeroSSL"};
  const expiry = item => !item.not_after ? "尚未签发" : Date.parse(item.not_after) <= Date.now() ? "已过期" : `${Math.ceil((Date.parse(item.not_after)-Date.now())/86400000)} 天后到期`;
  function page(items, filters, encryptionReady) {
    const visible = items.filter(item => `${item.name} ${item.domains.join(" ")}`.toLowerCase().includes(filters.search.toLowerCase()) && (!filters.status||item.status===filters.status) && (!filters.source || item.source===filters.source) && (!filters.expiry || (item.not_after && Date.parse(item.not_after)-Date.now()<Number(filters.expiry)*86400000)));
    return `<section class="settings-workspace users-workspace user-admin-workspace core-log-workspace cert-center"><header class="users-toolbar"><div class="users-title"><h2>证书中心</h2></div><div class="users-toolbar-actions"><button class="button small" data-cert-refresh>刷新</button><button class="button small" data-cert-sources>Caddy 同步</button><button class="button small" data-cert-accounts>账号管理</button><button class="button primary small" data-cert-create ${encryptionReady?"":"disabled"}>新增证书</button></div></header>
      <div class="core-log-filters">
        <label class="core-log-search"><span class="visually-hidden">搜索名称或域名</span><input data-cert-search type="search" placeholder="搜索名称或域名" value="${esc(filters.search)}"></label>
        <label class="core-log-search"><span class="visually-hidden">来源筛选</span><select data-cert-source><option value="">全部来源</option>${Object.entries(source).map(([key,name])=>`<option value="${key}" ${filters.source===key?"selected":""}>${name}</option>`).join("")}</select></label>
        <label class="core-log-search"><span class="visually-hidden">到期筛选</span><select data-cert-expiry><option value="">全部有效期</option><option value="30" ${filters.expiry==="30"?"selected":""}>30 天内到期</option><option value="0" ${filters.expiry==="0"?"selected":""}>已过期</option></select></label>
        <label class="core-log-search"><span class="visually-hidden">状态筛选</span><select data-cert-status><option value="">全部状态</option>${["issued","queued","waiting_dns","validating","failed","canceled","expired"].map(key=>`<option value="${key}" ${filters.status===key?"selected":""}>${esc(statusNames[key])}</option>`).join("")}</select></label>
      </div>
      ${encryptionReady?"":'<p class="alert error notice-message" role="alert">未配置加密密钥，请配置 QCH_CONFIG_ENCRYPTION_KEY。</p>'}
      <div class="settings-section user-allocation-panel">${visible.map(item=>`<article class="user-allocation-row">
        <div class="user-allocation-node"><strong>${esc(item.name)}</strong>${badge(item.status)}<span class="settings-hint">${item.owned?"我的证书":"授权给我的证书"} · v${item.version}</span></div>
        <dl class="user-allocation-terms"><div><dt>域名</dt><dd>${item.domains.map(domain=>esc(domain)).join("<br>")}</dd></div><div><dt>证书</dt><dd>${esc(source[item.source]||item.source)} · ${esc(item.key_type.toUpperCase())}<small>${esc(ca[item.ca]||source[item.source])}</small></dd></div><div><dt>有效期</dt><dd>${esc(expiry(item))}<small>${item.not_after?esc(date(item.not_after)):""}</small></dd></div><div><dt>续期管理方</dt><dd>${item.source==="caddy"?"Caddy · "+(statusNames[item.sync_status]||item.sync_status):item.auto_renew?"已启用":item.method==="dns-manual"?"需要人工 DNS 配合":"未启用"}</dd></div><div><dt>实例同步</dt><dd>${item.binding_count||0} 个绑定${item.pending_count?` · ${item.pending_count} 个待处理`:""}</dd></div></dl>
        <div class="user-allocation-row-actions"><button class="button small" data-cert-detail="${esc(item.id)}">详情</button></div>
      </article>`).join("")||`<div class="user-share-empty"><strong>${items.length?"没有匹配的证书":"还没有证书"}</strong><p>${items.length?"调整搜索或筛选条件后重试。":"新增证书或查看 Caddy 同步。"}</p></div>`}</div>
    </section>`;
  }

  // Match the existing settings sections, allocation metadata and sharing rows.
  const terms = rows => `<div><dl class="user-allocation-terms">${rows.map(([name,value])=>`<div><dt>${name}</dt><dd>${value}</dd></div>`).join("")}</dl></div>`;
  const panel = (title,body,actions="") => `<section class="settings-section user-allocation-panel"><header class="user-allocation-head"><div class="user-allocation-title"><h3 class="notice-message">${title}</h3></div>${actions?`<div class="user-allocation-tools">${actions}</div>`:""}</header><div class="settings-subsection settings-form">${body}</div></section>`;
  const disclosure = (title,body) => `<details class="user-owned-nodes"><summary><span>${title}</span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg></summary><ul><li>${body}</li></ul></details>`;
  function detail({certificate:item,orders,bindings,grants,versions=[]}) {
    const active = orders.find(order=>["queued","running","waiting_dns","validating","canceling"].includes(order.status));
    return `<h2>${esc(item.name)}</h2><div class="settings-form">
      ${panel("证书信息",terms([
        ["域名",esc(item.domains.join(" · "))],
        ["来源 / 验证",`${esc(source[item.source])} / ${esc(method[item.method]||"上传或本地生成")}`],
        ["机构 / 密钥",`${esc(ca[item.ca]||source[item.source])} / ${esc(item.key_type)}`],
        ["生效时间",item.not_before?esc(date(item.not_before)):"—"],
        ["到期时间",item.not_after?esc(date(item.not_after)):"—"],
        ["当前版本",`v${item.version} · ${esc(expiry(item))}`],
        ...(item.agent_id?[["HTTP-01 验证节点",esc(item.agent_name||"验证节点")]]:[])
      ])+disclosure("SHA-256 指纹",`<code>${esc(item.fingerprint||"待签发")}</code>`)+
      (item.source==="self-signed"?'<p class="settings-hint">自签证书需在客户端手动信任。</p>':""))}
      <div class="user-allocation-tools">${item.version?`<a class="button small" href="/api/v1/certificates/${item.id}/public-chain" download>下载公共证书链</a>`:""}${item.owned?`<button class="button small" data-cert-replace="${item.id}">替换版本</button>${item.source==="acme"?`<button class="button small" data-cert-renew="${item.id}" ${active?"disabled":""}>申请续期</button><button class="button small" data-cert-auto="${item.id}" data-enabled="${!item.auto_renew}" ${item.method==="dns-manual"?"disabled":""}>${item.auto_renew?"关闭":"开启"}自动续期</button>`:""}<button class="button small" data-cert-share="${item.id}">使用授权</button><button class="button small danger-button" data-cert-delete="${item.id}" ${bindings.length||active?"disabled":""}>删除证书</button>`:""}</div>
      ${item.source==="caddy"?panel("Caddy 来源与续期同步",terms([
        ["来源节点",esc(item.source_agent||"—")],
        ["续期管理方","Caddy 自动续期"],
        ["面板同步",badge(item.sync_status||"pending")],
        ["最近成功同步",item.last_synced?esc(date(item.last_synced)):"尚未同步"],
        ["信任来源",esc({"system-trusted":"受信任 CA","self-signed":"自签来源",untrusted:"未受信任 / 测试 CA"}[item.environment]||"待核验")]
      ])+(item.sync_error?`<p class="alert error notice-message" role="status">${esc(item.sync_error)}</p>`:""),'<button class="button small" data-cert-sources>管理来源同步</button>'):""}
      ${panel("实例绑定与同步",(bindings.length?'<p class="settings-hint">更换证书并部署后解绑。</p>':"")+bindings.map(b=>`<div class="agent-share-recipient"><div class="agent-share-recipient-head"><div class="user-allocation-node notice-message"><strong>${esc(b.agent_name||"绑定节点")} / ${esc(b.engine)}</strong><span class="agent-share-usage">${esc(b.config_name||"协议配置")} · ${esc(b.username||"资源所有者")} · v${b.version} · ${b.authorized?"授权有效":"授权已撤销 · 停止同步"}</span></div>${badge(b.status)}</div>${b.error?`<p class="alert error notice-message">${esc(b.error)}</p>`:""}</div>`).join("")||( '<p class="settings-hint">暂无绑定</p>'))}
      ${item.owned?panel("申请与续期记录",orders.map(order=>`<article class="agent-share-recipient"><div class="agent-share-meta"><strong class="agent-share-usage">${esc(date(order.created_at))}</strong>${badge(order.status)}</div><p class="settings-hint">${esc(stages[order.stage]||order.stage)} · 第 ${order.attempt} 次执行</p>${order.error?`<p role="status" class="alert error notice-message">${esc(order.error)}</p>`:""}${order.challenges.map(c=>`<div class="settings-form"><div class="user-allocation-tools"><strong class="agent-share-usage notice-message">${esc(c.domain)}</strong>${badge(c.status==="pending"?"awaiting_challenge":c.status)}</div>${c.status==="valid"&&!c.value?`<p class="settings-hint">CA 授权仍有效</p>`:c.type==="dns-01"?`<div class="settings-field"><span>名称 · TXT</span><code class="notice-message">${esc(c.name)}</code><div class="user-allocation-tools"><button class="button small" data-cert-copy="${esc(c.name)}">复制名称</button></div></div><div class="settings-field"><span>记录值</span><code class="notice-message">${esc(c.value)}</code><div class="user-allocation-tools"><button class="button small" data-cert-copy="${esc(c.value)}">复制值</button></div></div>`:`<p class="settings-hint">公网 80 端口 · /.well-known/acme-challenge/</p>`}</div>`).join("")}${order.status==="waiting_dns"?'<p class="settings-hint">添加以上 TXT 记录后继续验证。</p>':""}<div class="user-allocation-tools">${order.status==="waiting_dns"?`<button class="button primary small" data-cert-continue="${item.id}" data-order="${order.id}">继续验证</button>`:""}${["queued","running","waiting_dns","validating"].includes(order.status)?`<button class="button small" data-cert-cancel="${item.id}" data-order="${order.id}">取消申请</button>`:""}</div>${["succeeded","canceled","failed"].includes(order.status)&&item.method==="dns-manual"?'<p class="settings-hint">可删除本次验证的 TXT 记录。</p>':""}</article>`).join("")||'<p class="settings-hint">暂无 ACME 订单</p>'):""}
      ${disclosure(`证书版本历史 · ${versions.length}`,versions.map(v=>`<div class="settings-field"><span>v${v.version} · ${v.not_after?esc(date(v.not_after)):"—"}</span><code class="notice-message">${esc(v.fingerprint)}</code></div>`).join("")||"暂无版本")}
      <div class="user-allocation-tools"><button class="button small" data-cert-reload="${item.id}">刷新详情</button></div></div>`;
  }
  function sharing(item,grants,bindings) {
    return `<h2>使用授权</h2><div class="settings-form">
      <form data-cert-grant-form="${item.id}" class="agent-share-recipient"><div class="agent-share-recipient-head"><label class="settings-field"><span>用户账号</span><input name="username" required autocomplete="off"></label><button class="button primary small">授权</button></div><p class="alert error notice-message" data-cert-form-error role="alert" hidden></p></form>
      <div>${grants.map(g=>`<div class="agent-share-recipient"><div class="agent-share-recipient-head"><div class="user-allocation-node notice-message"><strong>${esc(g.username)}</strong><span class="agent-share-usage">${bindings.filter(b=>b.owner_id===g.user_id).map(b=>`${esc(b.agent_name||"绑定节点")} / ${esc(b.engine)}`).join("<br>")||"未绑定实例"}</span></div><button class="button small danger-button" data-cert-revoke="${item.id}" data-username="${esc(g.username)}">撤销</button></div></div>`).join("")||'<p class="settings-hint">暂无授权</p>'}</div>
    </div>`;
  }
  return {page,detail,sharing,badge,panel,terms};
}
