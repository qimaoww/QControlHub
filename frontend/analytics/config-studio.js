import { outboundEntries, inboundOutbound, bindOutbound, changeInboundOutbound, outboundCanBind } from '../modules/config-outbound-model.js';
import { outboundProtocols, outboundPreset, outboundPeers } from '../modules/outbound-preset-model.js';
import { commonConfigFields } from '../modules/config-field-model.js';
import { xrayMutableAPIServices } from '../modules/xray-api.js';
import { coreInstallBlockReason } from '../modules/core-install-state.js';
import { copyText, node, button, actions, stack, grid, section, table, badge, field, code, muted, notice, link, list, pathID } from './ui.js';
import { taskDetail, date } from './operations.js';

const labels = {
  tag:'入站名称',listen:'监听地址',port:'监听端口',username:'用户名',credential:'凭据',secondary_credential:'用户密码',method:'加密方法',flow:'Flow',transport:'传输协议',transport_path:'传输路径',transport_host:'传输 Host',transport_http_method:'HTTP 方法',
  tls_enabled:'启用 TLS',tls_mode:'TLS 终止方式',tls_server_name:'TLS Server Name',tls_alpn:'TLS ALPN',tls_client_alpn:'客户端 ALPN',tls_acme_email:'ACME 邮箱',tls_public_port:'TLS 公网端口',tls_client_certificate:'客户端证书',certificate_path:'证书路径',private_key_path:'私钥路径',panel_certificate_id:'面板证书',
  reality_enabled:'启用 Reality',reality_private_key:'Reality 私钥',reality_public_key:'Reality 公钥',reality_short_id:'Reality Short ID',reality_server_name:'Reality Server Name',reality_server_names:'Reality 允许域名',reality_target:'Reality 目标',reality_min_client_ver:'最低客户端版本',reality_mldsa65_seed:'ML-DSA-65 Seed',reality_mldsa65_verify:'ML-DSA-65 Verify',
  client_address:'客户端地址',client_port:'客户端端口',client_host:'客户端 Host',target_address:'目标地址',target_port:'目标端口',network:'网络',listener_routing_mark:'路由标记',listener_rule:'入站规则',listener_proxy:'入站出口',
  vless_decryption:'VLESS Decryption',vless_encryption:'VLESS Encryption',block_mainland_destination:'限制访问中国大陆',block_mainland_source:'限制中国大陆来源',
  xhttp_mode:'XHTTP 模式',xhttp_extra:'XHTTP 客户端参数',xhttp_server_extra:'XHTTP 服务端参数',xhttp_tls_address:'XHTTP TLS 地址',xhttp_tls_client_port:'XHTTP TLS 客户端端口',xhttp_tls_host:'XHTTP TLS Host',xhttp_backend_port:'XHTTP 后端端口',xhttp_tls_port:'XHTTP TLS 监听端口',xhttp_cdn:'XHTTP CDN',
  snell_version:'Snell 版本',snell_udp:'Snell UDP',snell_reuse:'复用连接',snell_obfs_mode:'混淆模式',snell_obfs_host:'混淆 Host',snell_client_fingerprint:'客户端指纹',snell_shadow_tls_version:'ShadowTLS 版本',snell_shadow_tls_password:'ShadowTLS 密码',snell_shadow_tls_user:'ShadowTLS 用户',snell_shadow_tls_handshake:'ShadowTLS 握手地址',snell_shadow_tls_proxy:'ShadowTLS 出口',snell_shadow_tls_alpn:'ShadowTLS ALPN',
  mieru_transport:'Mieru 传输方式',mieru_traffic_pattern:'Mieru 流量模式',mieru_custom_pattern:'Mieru 自定义模式',
  sudoku_client_key:'Sudoku 客户端密钥',sudoku_padding_min:'最小填充',sudoku_padding_max:'最大填充',sudoku_table_type:'映射表类型',sudoku_handshake_timeout:'握手超时',sudoku_enable_pure_downlink:'纯下行',sudoku_httpmask_enabled:'启用 HTTP 伪装',sudoku_httpmask_mode:'HTTP 伪装模式',sudoku_httpmask_tls:'HTTP 伪装 TLS',sudoku_httpmask_host:'HTTP 伪装 Host',sudoku_httpmask_path_root:'HTTP 伪装根路径',sudoku_multiplex:'多路复用',sudoku_fallback:'回落目标',
  ss_rust_dns:'DNS 地址',ss_rust_outbound_bind_addr:'出口绑定地址',ss_rust_ipv6_first:'优先 IPv6',
  wireguard_server_private_key:'WireGuard 服务端私钥',wireguard_server_public_key:'WireGuard 服务端公钥',wireguard_client_private_key:'WireGuard 客户端私钥',wireguard_client_public_key:'WireGuard 客户端公钥',wireguard_preshared_key:'WireGuard 预共享密钥',wireguard_server_address:'WireGuard 服务端地址',wireguard_client_address:'WireGuard 客户端地址',wireguard_allowed_ips:'WireGuard 允许网段',wireguard_mtu:'WireGuard MTU',wireguard_keepalive:'WireGuard Keepalive',
  tailscale_state_directory:'Tailscale 状态目录',tailscale_auth_key:'Tailscale Auth Key',tailscale_control_url:'Tailscale 控制地址',tailscale_hostname:'Tailscale 主机名',
  openvpn_server_certificate_path:'OpenVPN 服务端证书路径',openvpn_server_key_path:'OpenVPN 服务端私钥路径',openvpn_client_ca_path:'OpenVPN 客户端 CA 路径',openvpn_username:'OpenVPN 用户名',openvpn_password:'OpenVPN 密码',openvpn_address:'OpenVPN 地址',
};
const numeric = new Set(['port','client_port','tls_public_port','target_port','listener_routing_mark','xhttp_tls_client_port','xhttp_backend_port','xhttp_tls_port','snell_version','snell_shadow_tls_version','sudoku_padding_min','sudoku_padding_max','sudoku_handshake_timeout','wireguard_mtu','wireguard_keepalive']);
const booleans = new Set(['tls_enabled','reality_enabled','snell_udp','snell_reuse','sudoku_enable_pure_downlink','sudoku_httpmask_enabled','sudoku_httpmask_tls','ss_rust_ipv6_first','block_mainland_destination','block_mainland_source']);
const multiline = new Set(['xhttp_extra','xhttp_server_extra','mieru_custom_pattern','tls_client_certificate']);
const group = (title, content, open = false) => node('details', { className:'a-disclosure', open }, node('summary',{},title), node('div',{className:'a-disclosure-body'},content));

// All operations stay bound to one account/agent/engine workspace revision.
export function configStudio(ctx, { agent, engine, base, workspace, getDraft, accept, beforeDeploy, dirty, sourceTools }) {
  let current = workspace;
  const surface = node('div', {className:'a-stack'});
  const editable = ctx.can('agent-config.write'), execute = ctx.can('tasks.execute') && agent.status === 'online';
  const writable = editable && execute && !agent.runtime?.[engine]?.existing_config_unsupported_reason;
  async function latest() {
    if (dirty() && !await ctx.confirm('打开编辑工具会替换尚未保存的源码修改，确定继续？','打开编辑工具')) return false;
    const draft = getDraft();
    const fresh = await ctx.api(`${base}/workspace`);
    if (draft !== getDraft()) throw new Error('源码已修改，请保存后重新打开编辑工具');
    if (!ctx.current()) return false;
    current = fresh;
    if (fresh.config) accept(fresh.config);
    return true;
  }
  function track(form, box) {
    let changed = false;
    form.addEventListener('input', () => { changed = true; ctx.setDirty(true); });
    form.addEventListener('change', () => { changed = true; ctx.setDirty(true); });
    box.onRequestClose = async () => {
      if (form.getAttribute('aria-busy') === 'true') return;
      if (changed && !await ctx.confirm('尚未保存，确定放弃修改？','放弃更改') || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
    return () => { changed = false; ctx.setDirty(false); };
  }
  const intents = () => field('保存方式','intent','validate',{options:[['validate','保存并校验'],...(agent.runtime?.[engine]?.installed || agent.shared_allocation?.enabled ? [['deploy','保存并部署']] : [])]});
  async function preparing(intent) {
    if (intent !== 'deploy') return true;
    if (!await ctx.confirm('部署到当前实例并重启内核？','部署并重启') || !ctx.current()) return false;
    if (beforeDeploy) await beforeDeploy();
    return ctx.current();
  }
  async function committed(result, box) {
    if (!ctx.current()) return;
    accept(result.config); ctx.setDirty(false); box?.close();
    ctx.announce(result.task?.id ? '配置已保存，任务已提交' : '配置已保存');
    if (result.task?.id && ctx.can('tasks.read')) await taskDetail(ctx,result.task.id);
    current = await ctx.api(`${base}/workspace`);
    if (ctx.current()) draw();
  }
  async function inboundEdit(input, operation = 'modify') {
    if (!await latest()) return;
    if (operation !== 'add') {
      const found = current.inbounds.find(item => item.tag === input.tag);
      if (!found) throw new Error('入站已变化，请重新选择');
      input = found;
    }
    const protocolSelect = field('协议','protocol',input?.protocol || current.protocols[0]?.key,{options:current.protocols.map(protocol=>[protocol.key,protocol.name]),disabled:operation!=='add'});
    const area = node('div',{className:'a-stack'}), feedback=notice('');
    let protocol, baseline, request=0, box, initialInput=input;
    let certificates = [];
    if (ctx.can('certificates.read')) certificates = list((await ctx.api('/certificates')).items);
    if (!ctx.current()) return;
    const regenerate=button('重新生成凭据',async event=>{
      const control=event.currentTarget;
      if(!baseline||!await ctx.confirm('重新生成当前入站的凭据与密钥？保存后旧凭据将失效。','重新生成凭据')||!ctx.current())return;
      await ctx.mutate(control,async()=>{
        const input=read();for(const key of ['credential','secondary_credential','reality_private_key','reality_public_key','reality_short_id','reality_mldsa65_seed','reality_mldsa65_verify','wireguard_server_private_key','wireguard_server_public_key','wireguard_client_private_key','wireguard_client_public_key','wireguard_preshared_key'])input[key]='';
        const generated=await ctx.api(`${base}/plans`,{method:'POST',body:{protocol:protocol.key,input}});
        if(ctx.current()&&box.isConnected){render(generated);ctx.setDirty(true);}
      });
    });
    const form = ctx.form([protocolSelect,area,actions(regenerate),intents(),feedback],operation==='add'?'添加入站':'保存入站',async values=>{
      if(!protocol || !baseline) throw new Error('请等待方案加载');
      const payload=read();
      if(!await preparing(values.intent))return;
      if(operation==='add' && !agent.runtime?.[engine]?.installed) {
        const reason=coreInstallBlockReason(current.agent,engine,ctx.can,{automatic:true});
        if(reason)throw new Error(reason);
        if(!await ctx.confirm('此内核尚未安装，保存后自动安装稳定版并校验配置？','安装并保存')||!ctx.current())return;
      }
      const result=await ctx.api(`${base}/server-inbounds`,{method:'POST',body:{operation,original_tag:operation==='add'?'':input.tag,expected_version:current.config?.version||0,name:current.config?.name||`${agent.name} ${engine}`,description:current.config?.description||'',intent:values.intent,input:payload,install_if_missing:operation==='add'&&!agent.runtime?.[engine]?.installed,preserve_ss_rust_globals:engine==='ss-rust'}});
      await committed(result,box);
    });
    function read() {
      const output={...baseline,protocol:protocol.key};
      for(const control of area.querySelectorAll('[name]')) {
        if(control.type==='checkbox')output[control.name]=control.checked;
        else if(control.name==='mieru_custom_pattern')output[control.name]=control.value.trim()?JSON.parse(control.value):undefined;
        else output[control.name]=numeric.has(control.name)?Number(control.value):control.value;
      }
      return output;
    }
    function render(plan) {
      baseline=plan;
      const keys=['tag','listen','port',...(!protocol.port_forward&&!protocol.ignores_username?['username']:[]),...(!protocol.port_forward?[ 'credential',...(protocol.secondary_credential_label?['secondary_credential']:[])]:[]),...(protocol.methods?.length?['method']:[])];
      const certificateID=plan.panel_certificate_id||plan.certificate_path?.match(/^\/etc\/qcontrolhub\/panel-certificates\/([^/]+)\//)?.[1]||'';
      const certificateOptions=[['','节点已有证书 / 原有 TLS 方式'],...certificates.map(cert=>[cert.id,`${cert.name} · ${(cert.domains||[]).join('、')}${!cert.version||!Number.isFinite(Date.parse(cert.not_after))||Date.parse(cert.not_after)<=Date.now()||Date.parse(cert.not_before)>Date.now()?' · 不可用':''}`])];
      if(certificateID&&!certificates.some(cert=>cert.id===certificateID))certificateOptions.push([certificateID,'所选证书不可用，请重新选择']);
      const options=key=>key==='panel_certificate_id'?certificateOptions:key==='method'?protocol.methods:key==='transport'?protocol.transports:key==='tls_mode'?[['native','内核 TLS'],['caddy','Caddy'],['nginx','Nginx'],['external','外部终止']]:undefined;
      const control=key=>field(key==='credential'?protocol.credential_label||labels[key]:labels[key]||key,key,key==='panel_certificate_id'?certificateID:typeof plan[key]==='object'?JSON.stringify(plan[key]||{},null,2):plan[key]??(booleans.has(key)?false:''),{...(booleans.has(key)?{type:'checkbox'}:numeric.has(key)?{type:'number',min:0,max:key.includes('port')?65535:undefined}:multiline.has(key)?{multiline:4,spellcheck:false}:/credential|private_key|password|auth_key|preshared_key|seed|decryption/.test(key)?{type:'password',autocomplete:'new-password'}:{}),...(options(key)?{options:options(key)}:{}),required:['tag','listen','port'].includes(key),maxlength:multiline.has(key)?undefined:2000});
      const sections=[node('div',{className:'a-form-grid'},keys.map(control))];
      if(protocol.transport_config)sections.push(group('传输',node('div',{className:'a-form-grid'},['transport','transport_path','transport_host','transport_http_method','flow'].map(control)),true));
      if(protocol.supports_tls||protocol.requires_tls)sections.push(group('TLS',node('div',{className:'a-form-grid'},['tls_enabled','tls_server_name','tls_alpn','tls_client_alpn','certificate_path','private_key_path','panel_certificate_id','tls_client_certificate',...(protocol.supports_managed_tls?['tls_mode','tls_acme_email','tls_public_port']:[])].map(control)),protocol.requires_tls));
      if(protocol.uses_reality||protocol.key==='vless')sections.push(group('Reality',node('div',{className:'a-form-grid'},['reality_enabled','reality_private_key','reality_public_key','reality_short_id','reality_server_name','reality_target','reality_server_names',...(protocol.supports_reality_mldsa?['reality_min_client_ver','reality_mldsa65_seed','reality_mldsa65_verify']:[])].map(control)),protocol.uses_reality));
      if(protocol.uses_vless_encryption)sections.push(group('VLESS Encryption',grid(control('vless_decryption'),control('vless_encryption')),true));
      const prefixes=protocol.key.startsWith('vless-xhttp')||protocol.key.includes('xhttp')?['xhttp_']:protocol.key.startsWith('snell')?['snell_']:protocol.key.startsWith('sudoku')?['sudoku_']:protocol.key.startsWith('mieru')?['mieru_']:protocol.uses_wireguard?['wireguard_']:protocol.key==='tailscale'?['tailscale_']:protocol.key==='openvpn-server'?['openvpn_']:[];
      const special=Object.keys(labels).filter(key=>prefixes.some(prefix=>key.startsWith(prefix)));
      if(protocol.port_forward)special.push('target_address','target_port','network');
      if(engine==='ss-rust')special.push('ss_rust_dns','ss_rust_outbound_bind_addr','ss_rust_ipv6_first');
      if(special.length)sections.push(group('协议参数',node('div',{className:'a-form-grid'},special.map(control)),true));
      sections.push(group('客户端与路由',node('div',{className:'a-form-grid'},['client_address','client_port','client_host',...(engine==='mihomo'?['listener_routing_mark','listener_rule','listener_proxy']:[])].map(control))));
      area.replaceChildren(...sections);
      const certSelect=area.querySelector('[name=panel_certificate_id]');
      if(certSelect){
        const localPaths=new Map(['certificate_path','private_key_path'].map(key=>[key,certificateID?'':plan[key]||'']));
        const sync=()=>{
          const id=certSelect.value, cert=certificates.find(cert=>cert.id===id);
          for(const [key,file] of [['certificate_path','cert.pem'],['private_key_path','key.pem']]){
            const input=area.querySelector(`[name=${key}]`);
            if(input){input.value=id?`/etc/qcontrolhub/panel-certificates/${id}/${file}`:localPaths.get(key);input.readOnly=Boolean(id);}
          }
          const tls=area.querySelector('[name=tls_enabled]');if(id&&tls)tls.checked=true;
          const mode=area.querySelector('[name=tls_mode]');if(id&&mode)mode.value='native';
          const name=area.querySelector('[name=tls_server_name]');if(cert&&name&&!name.value)name.value=(cert.domains||[]).find(domain=>!domain.startsWith('*.'))||'';
        };
        certSelect.addEventListener('change',sync);if(certificateID)sync();
      }
    }
    async function choose() {
      const epoch=++request;
      protocol=current.protocols.find(item=>item.key===form.elements.protocol.value);
      if(!protocol)return;
      const generated=operation==='add' ? initialInput || await ctx.api(`${base}/plans`,{method:'POST',body:{protocol:protocol.key}}) : input;
      initialInput=null;
      if(epoch!==request||!ctx.current()||!box.isConnected)return;
      render(generated);
    }
    form.elements.protocol.addEventListener('change',()=>void ctx.mutate(null,choose));
    box=ctx.dialog(operation==='add'?'添加入站':`修改入站 · ${input.tag}`,form,{wide:true});
    track(form,box); await choose();
  }
  async function files() {
    if(!await latest()||!current.config)return;
    const editor=field('完整配置','content',current.config.content,{multiline:24,spellcheck:false,readonly:!editable});
    const form=ctx.form([node('div',{className:'a-editor'},editor)], '保存配置',async()=>{
      const saved=await ctx.api(base,{method:'PUT',body:{name:current.config.name,description:current.config.description,version:current.config.version,content:form.elements.content.value}});
      await committed({config:saved},box);
    });
    const box=ctx.dialog('展开源码',form,{wide:true});
    track(form,box);
    if(!editable)form.querySelector('button[type=submit]').remove();
  }
  async function commonField(item,inbound) {
    if(!await latest()||!current.config)return;
    const url=`${base}/fields/${pathID(item.key)}${inbound?`?inbound=${pathID(inbound.tag)}`:''}`;
    const value=await ctx.api(url);
    if(value.version!==undefined&&value.version!==current.config.version)throw new Error('配置已更新，请重新打开字段');
    const form=ctx.form([
      muted(item.description),item.docs&&link('协议文档',item.docs),
      field('操作','mutation',value.present?'modify':'add',{options:[['add','添加'],['modify','修改'],['delete','删除']]}),
      field('字段片段','fragment',value.fragment||'',{multiline:12,spellcheck:false}),intents(),
    ],'保存配置项',async values=>{
      if(values.mutation==='delete'&&!await ctx.confirm(`删除配置项“${item.label}”？`,'删除配置项'))return;
      if(!await preparing(values.intent))return;
      const result=await ctx.api(url,{method:'POST',body:{mutation:values.mutation,fragment:values.mutation==='delete'?'':values.fragment,expected_version:current.config.version,name:current.config.name,description:current.config.description,intent:values.intent}});
      await committed(result,box);
    });
    const box=ctx.dialog(`${inbound?inbound.tag+' · ':''}${item.label}`,form,{wide:true});track(form,box);
  }
  async function outbound(inbound,operation) {
    if(!await latest()||!current.config)return;
    const content=current.config.content,binding=inboundOutbound(content,engine,inbound),entries=outboundEntries(content).filter(entry=>outboundCanBind(entry,engine));
    const chosen=entries.find(entry=>entry.tag===binding);
    if(['modify','delete'].includes(operation)&&!chosen)throw new Error('当前入站尚未绑定可编辑出站');
    const fragment=field('出站 JSON','fragment',chosen?.content||'{}',{multiline:12,spellcheck:false});
    const form=ctx.form([
      muted(`当前入站 · ${inbound.tag}`),
      operation==='bind'?field('出站','tag',binding||entries[0]?.tag||'',{options:entries.map(entry=>[entry.tag,entry.tag]),required:true}):fragment,intents(),
    ],operation==='delete'?'删除绑定出站':'保存出站',async values=>{
      if(operation==='delete'&&!await ctx.confirm('删除当前绑定出站？被其他路由引用时会拒绝删除。','删除出站'))return;
      if(!await preparing(values.intent))return;
      const next=operation==='bind'?bindOutbound(content,engine,inbound,values.tag):changeInboundOutbound(content,engine,inbound,operation,chosen?.index,values.fragment);
      const result=await ctx.api(`${base}/source`,{method:'POST',body:{name:current.config.name,description:current.config.description,content:next,version:current.config.version,intent:values.intent}});
      await committed(result,box);
    });
    const box=ctx.dialog(`${inbound.tag} · 出站`,form,{wide:true});track(form,box);
    if(operation==='add') {
      const fields=node('div',{className:'a-form-grid'},
        field('协议','protocol','direct',{options:outboundProtocols(engine)}),field('标签','tag',`exit-${inbound.port}`,{required:true}),field('服务器','server',''),field('端口','port',443,{type:'number',min:1,max:65535}),
        field('凭据 / UUID','credential',''),field('用户名','username',''),field('密码','password','',{type:'password'}),field('加密方法','method','aes-128-gcm'),
        field('安全','security','none',{options:[['none','无'],['tls','TLS'],['reality','Reality']]}),field('传输','transport','tcp',{options:['tcp','ws','grpc',...(engine==='xray'?['xhttp']:[])]}),
        field('Server Name','serverName',''),field('Reality 公钥','publicKey',''),field('Short ID','shortId',''),field('Flow','flow',''),field('Host','host',''),field('路径','path',''),field('Service Name','serviceName',''),
      );
      const preset=ctx.form([fields],'生成出站 JSON',values=>{
        form.elements.fragment.value=JSON.stringify(outboundPreset(engine,values.protocol,values),null,2);ctx.setDirty(true);
      });
      // Separate sibling forms preserve native form ownership and submission.
      box.querySelector('.a-dialog-body').prepend(group('出站预设',preset,true));
      if(ctx.can('client-access.read'))box.querySelector('.a-dialog-body').prepend(button('从其他节点导入',event=>void ctx.mutate(event.currentTarget,async()=>{
        const peers=outboundPeers(list(await ctx.api(`/client-access?outbound_engine=${pathID(engine)}`)),agent.id);
        const peersBox=ctx.dialog('选择目标入站',table(['节点','协议','操作'],peers.map(peer=>[peer.agentName,peer.protocol,button('导入',()=>{
          if(!peer.outbound) { ctx.announce(peer.outbound_error||'此入站不能转换为当前内核的出站','error'); return; }
          form.elements.fragment.value=JSON.stringify({...peer.outbound,tag:`exit-${inbound.port}`},null,2);
          ctx.setDirty(true); peersBox.close();
        })]),{label:'可用入站'}),{wide:true});
      })));
    }
  }
  async function outboundObject(object,operation='modify') {
    if(!await latest()||!current.config)return;
    const config=current.config;
    const sections=engine==='mihomo'?[['proxies','代理'],['proxy-groups','代理组']]:[['outbounds','出站']];
    const sectionName=object?.section||sections[0][0];
    let content=engine==='mihomo'?'name: new-exit\ntype: direct\n':JSON.stringify({tag:'new-exit',[engine==='xray'?'protocol':'type']:engine==='xray'?'freedom':'direct'},null,2);
    if(object) {
      const selected=await ctx.api(`${base}/outbounds/${pathID(object.section)}/${pathID(object.tag)}`);
      if(!ctx.current())return;
      if(selected.version!==config.version)throw new Error('配置已变化，请重新读取');
      content=selected.fragment;
    }
    const form=ctx.form([
      operation==='delete'?muted(`删除 ${object.tag}？仍被引用的出站会拒绝删除。`):field('配置区域','section',sectionName,{options:sections,disabled:Boolean(object)}),
      operation!=='delete'&&field(engine==='mihomo'?'出站 YAML':'出站 JSON','fragment',content,{multiline:12,spellcheck:false}),
      object&&operation==='modify'&&muted('保存会更新所有引用此出站的入站。'),intents(),
    ],operation==='delete'?'删除出站':'保存出站',async values=>{
      if(object&&operation==='modify'&&!await ctx.confirm(`更新“${object.tag}”及其所有引用？`,'更新出站'))return;
      if(!await preparing(values.intent))return;
      const result=await ctx.api(`${base}/outbounds`,{method:'POST',body:{version:config.version,intent:values.intent,section:object?.section||values.section,tag:object?.tag||'',fragment:values.fragment||'',operation}});
      await committed(result,box);
    });
    const box=ctx.dialog(operation==='add'?'添加出站':operation==='delete'?'删除出站':`${object.tag} · 出站`,form,{wide:true});track(form,box);
  }
  function draw() {
    const inbounds=list(current.inbounds),targets=list(current.inbound_targets);
    const managed=list(current.managed_objects);
    const toggle=object=>button(object.enabled?'停用':'启用',event=>void ctx.mutate(event.currentTarget,async()=>{
      if(!await latest())return;
      const fresh=list(current.managed_objects).find(item=>item.section===object.section&&item.tag===object.tag);
      if(!fresh||fresh.enabled!==object.enabled)throw new Error('配置状态已变化，请重新选择');
      const disabled=list(current.management?.disabled).filter(item=>item.section!==object.section||item.tag!==object.tag);
      const expectedVersion=current.config.version;
      if(object.enabled)disabled.push({section:object.section,tag:object.tag});
      const form=ctx.form([muted(`${object.enabled?'停用':'启用'} ${object.tag}`),intents()], '保存状态',async values=>{
        if(!await preparing(values.intent))return;
        const result=await ctx.api(`${base}/management`,{method:'PUT',body:{version:expectedVersion,disabled,intent:values.intent}});
        await committed(result,box);
      });
      const box=ctx.dialog(object.enabled?'停用配置对象':'启用配置对象',form);
    }));
    const controls=writable&&button('添加入站',event=>void ctx.mutate(event.currentTarget,()=>inboundEdit(null,'add')),{tone:'primary'});
    const deployment=current.deployment||{};
    const deploymentLabel={pending:'待部署',deploying:'部署中',failed:'部署失败',applied:'已部署',unknown:'部署状态未知'}[deployment.state]||'部署状态未知';
    const serviceLabel={active:'运行中',inactive:'已停止',failed:'服务异常'}[deployment.service_status]||'运行状态未知';
    const status=actions(badge(deploymentLabel),muted(serviceLabel));
    const rows=targets.map(target=>{
      const input=inbounds.find(input=>input.tag===target.tag);
      const object=managed.find(item=>item.inbound&&item.tag===target.tag);
      return [target.port||'无固定端口',stack(target.tag,muted([object?.listen||input?.listen,object?.network].filter(Boolean).join(' · '))),target.kind,
        object?badge(object.enabled?'启用':'停用'):muted('—'),actions(
        writable&&object&&toggle(object),
        writable&&input&&button('修改',event=>void ctx.mutate(event.currentTarget,()=>inboundEdit(input))),
        writable&&input&&button('复制入站',event=>void ctx.mutate(event.currentTarget,()=>inboundEdit({...input,tag:`${input.tag}-copy`},'add'))),
        writable&&button('删除',async event=>{
          const control=event.currentTarget;
          if(!await ctx.confirm(`删除入站“${target.tag}”？保存并校验后需部署生效。`,'删除入站')||!ctx.current())return;
          await ctx.mutate(control,async()=>{
            if(!await latest())return;
            const mutable=xrayMutableAPIServices(engine,current.config.content);
            if(mutable.length&&!await ctx.confirm(`删除需关闭 ${mutable.join('、')}，其他 API 配置将保留。确定继续？`,'关闭可修改路由的服务'))return;
            if(!ctx.current())return;
            const result=await ctx.api(`${base}/server-inbounds`,{method:'POST',body:{operation:'delete',disable_mutable_api:mutable.length>0,original_tag:target.tag,expected_version:current.config.version,name:current.config.name,description:current.config.description,intent:'validate',input:input||{tag:target.tag}}});
            await committed(result);
          });
        },{tone:'danger'}),
        writable&&['xray','sing-box'].includes(engine)&&button('出站',()=>ctx.dialog(`${target.tag} · 出站操作`,actions(['add','bind','modify','delete'].map((operation,index)=>button(['添加并绑定','绑定已有出站','修改绑定出站','删除绑定出站'][index],event=>void ctx.mutate(event.currentTarget,()=>outbound(target,operation))))))),
        writable&&engine==='ss-rust'&&button('端口配置项',()=>fieldChooser(target)),
      )];
    });
    const outboundObjects=managed.filter(item=>!item.inbound);
    const headers=['端口','监听','协议','目标状态','操作'];
    const portTable=table(headers,rows,{label:'入站配置'});
    portTable.classList.add('a-port-list');
    for(const element of portTable.querySelectorAll('table,thead,tbody,tr,th,td')) {
      const roles={TABLE:'table',THEAD:'rowgroup',TBODY:'rowgroup',TR:'row',TH:'columnheader',TD:'cell'};
      element.setAttribute('role',roles[element.tagName]);
    }
    for(const row of portTable.querySelectorAll('tbody tr')) [...row.cells].forEach((cell,index)=>cell.dataset.label=headers[index]);
    surface.replaceChildren(section('入站配置',portTable,actions(status,controls)),
      ['xray','sing-box','mihomo'].includes(engine)&&section('出站配置',table(['名称','类型','目标状态','操作'],outboundObjects.map(object=>[object.tag,object.kind||object.section,badge(object.enabled?'启用':'停用'),writable&&actions(toggle(object),button('修改',event=>void ctx.mutate(event.currentTarget,()=>outboundObject(object))),button('删除',event=>void ctx.mutate(event.currentTarget,()=>outboundObject(object,'delete')),{tone:'danger'}))]),{label:'出站配置'}),writable&&button('添加出站',event=>void ctx.mutate(event.currentTarget,()=>outboundObject(null,'add')))),
    );
    sourceTools?.replaceChildren(...(current.config?[
      button('展开源码',event=>void ctx.mutate(event.currentTarget,files)),
      writable&&button('通用配置项',()=>fieldChooser()),
    ].filter(Boolean):[]));
  }
  function fieldChooser(inbound) {
    const fields=inbound?list(current.catalog?.fields).filter(item=>['inbound','override'].includes(item.scope)):commonConfigFields(engine,list(current.catalog?.fields));
    ctx.dialog(inbound?`${inbound.tag} · 端口配置项`:'通用配置项',table(['配置项','状态','操作'],fields.map(item=>[stack(item.label,muted(item.key)),current.present_fields?.[item.key]?'已配置':'未配置',button('编辑',event=>void ctx.mutate(event.currentTarget,()=>commonField(item,inbound)))]),{label:'配置项'}),{wide:true});
  }
  draw();return surface;
}
