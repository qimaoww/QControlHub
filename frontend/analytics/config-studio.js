import { labels, numeric, booleans, multiline } from "./inbound-fields.js";
import { bindAnalyticsCertificateSelector } from "./certificate-selector.js";
import { bindInboundTransport } from "./inbound-tls.js";
import { readServerPlanInput, generatedFieldActions, isXHTTPGroup } from "../modules/inbound-options-model.js";
import { protocolOptions, outboundPresetFields } from "./inbound-options.js";
import { editInboundAccess } from "./access-control.js";
import { deploymentStatus } from "./config-deployment.js";
import { editInboundSource } from "./inbound-source.js";
import { outboundEntries, inboundOutbound, bindOutbound, changeInboundOutbound, outboundCanBind } from '../modules/config-outbound-model.js';
import { bindOutboundPresetController } from '../modules/outbound-preset-controller.js';
import { commonConfigFields } from '../modules/config-field-model.js';
import { xrayMutableAPIServices } from '../modules/xray-api.js';
import { coreInstallBlockReason } from '../modules/core-install-state.js';
import { copyText, node, button, actions, stack, grid, section, table, badge, field, code, muted, notice, link, list, pathID } from './ui.js';
import { taskDetail, date } from './operations.js';

const group = (title, content, open = false) => node('details', { className:'a-disclosure', open }, node('summary',{},title), node('div',{className:'a-disclosure-body'},content));

// All operations stay bound to one account/agent/engine workspace revision.
export function configStudio(ctx, { agent, engine, base, workspace, getDraft, accept, beforeDeploy, dirty, sourceTools, readOnly = false }) {
  let current = workspace;
  const surface = node('div', {className:'a-stack'});
  const receipt = notice(''); receipt.hidden = true;
  const deployment = deploymentStatus(ctx, { base, workspace, getConfig: () => current.config, isPreview:()=>Boolean(current.snapshot_task_id), agent, engine });
  const workspacePath = () => current.snapshot_task_id ? `${base}/workspace/snapshots/${pathID(current.snapshot_task_id)}` : `${base}/workspace`;
  surface.previewSnapshot = async (id,isCurrent=ctx.current) => {
    const previous = current.config;
    const fresh = await ctx.api(`${base}/workspace/snapshots/${pathID(id)}`);
    if (!ctx.current() || !isCurrent()) return;
    if (current.config !== previous || (fresh.config?.version || 0) !== (previous?.version || 0)) throw new Error('配置已变化，请重新读取后编辑');
    current = {...fresh,snapshot_task_id:fresh.config?.content===previous?.content?undefined:id}; draw();
  };
  surface.acceptConfig = config => { current = { ...current, config, snapshot_task_id:undefined }; void deployment.refresh(); };
  surface.reload = async () => {
    const config = current.config;
    const fresh = await ctx.api(workspacePath());
    if (!ctx.current() || current.config !== config) return;
    current = {...fresh,snapshot_task_id:current.snapshot_task_id}; draw(); void deployment.refresh();
    return fresh;
  };
  const editable = !readOnly && ctx.can('agent-config.write'), execute = ctx.can('tasks.execute') && agent.status === 'online';
  const writable = editable && ctx.can('tasks.execute') && !agent.runtime?.[engine]?.existing_config_unsupported_reason;
  async function latest() {
    if (dirty() && !await ctx.confirm('打开编辑工具会替换尚未保存的源码修改，确定继续？','打开编辑工具')) return false;
    const draft = getDraft(), previous = current.config;
    const fresh = await ctx.api(workspacePath());
    if (draft !== getDraft()) throw new Error('源码已修改，请保存后重新打开编辑工具');
    if (!ctx.current()) return false;
    if (current.config !== previous || fresh.config?.id !== previous?.id || current.snapshot_task_id && fresh.config?.version !== previous?.version)
      throw new Error('配置已变化，请重新读取后再打开编辑工具');
    current = {...fresh,snapshot_task_id:current.snapshot_task_id};
    if (fresh.config && !current.snapshot_task_id) accept(fresh.config);
    return true;
  }
  function track(form, box, requiresTask = true, isDirty) {
    if (requiresTask && !execute) { form.querySelector('button[type=submit]').disabled = true; form.append(notice('节点当前离线，可以编辑草稿，节点上线后重新打开再提交。')); }
    let changed = false;
    const update = () => { changed = isDirty ? isDirty() : true; ctx.setDirty(changed); };
    form.addEventListener('input', update);
    form.addEventListener('change', update);
    box.onRequestClose = async () => {
      if (form.getAttribute('aria-busy') === 'true') return;
      if ((isDirty ? isDirty() : changed) && !await ctx.confirm('尚未保存，确定放弃修改？','放弃更改') || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
    return () => { changed = false; ctx.setDirty(false); };
  }
  const intents = () => node('input',{type:'hidden',name:'intent',value:'deploy'});
  async function preparing(intent) {
    if (intent !== 'deploy') throw new Error('请选择保存并部署。');
    if (!await ctx.confirm(`部署到 ${agent.name} · ${engine} · ${agent.shared_allocation ? '当前共享实例' : '当前主实例'} 并重启此实例内核？`,'部署并重启') || !ctx.current()) return false;
    if (beforeDeploy) await beforeDeploy();
    return ctx.current();
  }
  async function reloadSaved() {
    if (!await latest()) return;
    receipt.hidden = true; draw(); void deployment.refresh();
  }
  async function write(path, body, box, method = 'POST') {
    try { return await ctx.api(path, { method, body }); }
    catch (error) {
      if (error.name === 'AbortError') throw error;
      if (error.code === 'port_conflict') {
        const form = box.querySelector('form'), field = form.elements.namedItem(error.field || 'port');
        error.message += ' 草稿已保留。';
        form.querySelector('button[type=submit]').addEventListener('mutationend', () => {
          if (!ctx.current() || !box.open || !field) return;
          for (let parent = field.parentElement; parent && parent !== form; parent = parent.parentElement)
            if (parent.tagName === 'DETAILS') parent.open = true;
          field.focus();
        }, { once: true });
      } else if (!error.status || error.status === 200 || error.status === 408 || error.status >= 500 || error.status === 409) {
        const form = box.querySelector('form'), submit = form.querySelector('button[type=submit]');
        submit.addEventListener('mutationend', () => { submit.disabled = true; }, { once: true });
        form.append(notice('保存结果需重新核对，草稿已保留。请勿重复提交。'), actions(button('重新读取并核对', async event => {
          if (!await ctx.confirm('重新读取已保存配置并放弃此弹窗草稿？请先核对任务记录，避免重复执行。', '重新读取配置') || !ctx.current()) return;
          await ctx.mutate(event.currentTarget, async () => { ctx.setDirty(false); box.close(); await reloadSaved(); });
        })));
      }
      throw error;
    }
  }
  async function committed(result, box) {
    if (!ctx.current()) return;
    current = { ...current, config: result.config, snapshot_task_id:undefined };
    accept(result.config); ctx.setDirty(false); box?.close();
    ctx.announce(result.task?.id ? '配置已保存，任务已提交' : '配置已保存');
    if (result.task?.id && ctx.can('tasks.read')) await taskDetail(ctx,result.task.id);
    try {
      current = await ctx.api(`${base}/workspace`);
      if (ctx.current()) { receipt.hidden = true; draw(); void deployment.refresh(); }
    } catch (error) {
      if (!ctx.current() || error.name === 'AbortError') return;
      receipt.replaceChildren('配置已保存且任务已提交，状态读取失败。', actions(button('重新读取状态', event => void ctx.mutate(event.currentTarget, reloadSaved))));
      receipt.hidden = false; draw(); ctx.announce('配置已保存且任务已提交，状态读取失败。', 'error');
    }
  }
  async function restrictions(target) {
    if (current.snapshot_task_id) throw new Error('读取的节点配置尚未保存，请先保存并部署，再设置访问限制。');
    if (dirty()) throw new Error('配置源码有未保存修改，请先保存，再设置入站访问限制。');
    if (!await latest() || !current.config) return;
    const config = current.config, draft = getDraft();
    const entries = list(await ctx.api('/access-controls')).filter(item => item.agent_id === agent.id && item.engine === engine);
    if (!ctx.current() || dirty() || draft !== getDraft() || current.config !== config) return;
    const entry = entries.find(item => item.tag === target.tag && item.port === target.port && item.config_id === config.id && item.config_version === config.version);
    if (!entry) throw new Error('入站或配置已变化，请重新读取后再设置访问限制。');
    editInboundAccess(ctx, { agent, engine, entry, siblings: entries, beforeDeploy, onSaved: committed });
  }
  async function copyInbound(input) {
    const workspace = current, draft = getDraft();
    const active = () => ctx.current() && current === workspace && draft === getDraft();
    if (!workspace.client_source) throw new Error('请重新读取配置后复制入站连接');
    const query = new URLSearchParams({ source: workspace.client_source, tag: input.tag, port: input.port, protocol: input.protocol });
    if (workspace.snapshot_task_id) query.set('snapshot', workspace.snapshot_task_id);
    const result = await ctx.api(`${base}/client-access?${query}`);
    if (!active()) throw new DOMException('配置目标已变化', 'AbortError');
    if (!result.content?.trim()) throw new Error('暂无可用客户端连接');
    await copyText(result.content, active);
    if (active()) ctx.announce(result.source === 'snapshot' ? '已复制读取快照的连接' : '已复制面板保存的配置连接');
  }
  async function inboundEdit(input, operation = 'modify') {
    if (!await latest()) return;
    if (operation !== 'add') {
      const found = current.inbounds.find(item => item.tag === input.tag);
      if (!found) throw new Error('入站已变化，请重新选择');
      input = found;
    }
    const originalProtocol = current.protocols.find(item => item.key === input?.protocol);
    // Native sing-box endpoints and inbounds have different identities in the API.
    const protocols = operation === 'modify' && engine === 'sing-box'
      ? current.protocols.filter(item => Boolean(item.uses_endpoint) === Boolean(originalProtocol?.uses_endpoint)) : current.protocols;
    const protocolSelect = field('协议','protocol',input?.protocol || protocols[0]?.key,{options:protocols.map(protocol=>[protocol.key,protocol.name])});
    const area = node('fieldset',{className:'a-form-fields a-stack'}), feedback=notice('');
    const protocolHint = muted('保存后将切换协议，客户端需更新接入配置。'); protocolHint.hidden = true;
    let protocol, baseline, request=0, revision=0, box, initialInput=input;
    const protocolDrafts = new Map();
    if (!ctx.current()) return;
    const regenerate=button('重新生成凭据',async event=>{
      const control=event.currentTarget;
      if(!baseline||!await ctx.confirm('重新生成当前入站的凭据与密钥？保存后旧凭据将失效。','重新生成凭据')||!ctx.current())return;
      await ctx.mutate(control,async()=>{
        const draft=JSON.stringify(read()),selected=protocol, requestedRevision=revision;
        const input=read();for(const key of ['credential','secondary_credential','reality_private_key','reality_public_key','reality_short_id','reality_mldsa65_seed','reality_mldsa65_verify','wireguard_server_private_key','wireguard_server_public_key','wireguard_client_private_key','wireguard_client_public_key','wireguard_preshared_key'])input[key]='';
        const generated=await ctx.api(`${base}/plans`,{method:'POST',body:{protocol:protocol.key,input}});
        if(ctx.current()&&box.isConnected&&selected===protocol){
          if(requestedRevision!==revision||draft!==JSON.stringify(read()))throw new Error('参数已修改，未应用过期生成结果');
          render(generated);ctx.setDirty(true);
        }
      });
    });
    const native = input && operation === 'modify' && button('完整入站配置',async event=>{
      const control = event.currentTarget;
      if (baseline && (protocol.key !== input.protocol || JSON.stringify(read()) !== JSON.stringify(baseline)) && !await ctx.confirm('切换编辑方式会放弃当前参数修改，确定继续？','切换编辑方式')) return;
      if (!ctx.current()) return;
      ctx.setDirty(false); box.close();
      await ctx.mutate(control,()=>inboundSource(input));
    });
    const form = ctx.form([protocolSelect,area,actions(regenerate,native),protocolHint,intents(),feedback],'保存并部署',async values=>{
      if(!protocol || !baseline || protocol.key !== form.elements.protocol.value) throw new Error('请等待方案加载');
      const payload=read();
      if(!await preparing(values.intent))return;
      if(operation==='add' && !agent.runtime?.[engine]?.installed) {
        const reason=coreInstallBlockReason(current.agent,engine,ctx.can,{automatic:true});
        if(reason)throw new Error(reason);
        if(!await ctx.confirm('此内核尚未安装，将自动安装稳定版并部署配置。确定继续？','安装并部署')||!ctx.current())return;
      }
      const result=await write(`${base}/server-inbounds`,{operation,original_tag:operation==='add'?'':input.tag,expected_version:current.config?.version||0,name:current.config?.name||`${agent.name} ${engine}`,description:current.config?.description||'',intent:values.intent,input:payload,snapshot_task_id:current.snapshot_task_id,install_if_missing:operation==='add'&&!agent.runtime?.[engine]?.installed,preserve_ss_rust_globals:engine==='ss-rust'},box);
      await committed(result,box);
    });
    function read() {
      const values = new Map(Object.entries(baseline || {}).filter(([key]) => !form.elements.namedItem(key)).map(([key,value]) =>
        [key, typeof value === 'boolean' ? value ? '1' : '0' : typeof value === 'object' ? JSON.stringify(value) : String(value ?? '')]));
      for (const control of form.querySelectorAll('[name]')) if (!control.disabled && (control.type !== 'checkbox' || control.checked)) values.set(control.name, control.value);
      return { ...baseline, ...readServerPlanInput(form, protocol, values) };
    }
    function render(plan) {
      baseline=plan;
      form.dataset.blockMainlandDestination = plan.block_mainland_destination ? '1' : '0';
      form.dataset.blockMainlandSource = plan.block_mainland_source ? '1' : '0';
      const keys=['tag','listen','port',...(!protocol.port_forward&&!protocol.ignores_username?['username']:[]),...(!protocol.port_forward?[ 'credential',...(protocol.secondary_credential_label?['secondary_credential']:[])]:[]),...(protocol.methods?.length?['method']:[])];
      const certificateID=plan.panel_certificate_id||plan.certificate_path?.match(/^\/etc\/qcontrolhub\/panel-certificates\/([^/]+)\//)?.[1]||'';
      const certificateOptions=[['','节点已有证书 / 原有 TLS 方式']];
      if(certificateID)certificateOptions.push([certificateID,'正在加载所选证书…']);
      const options=key=>key==='tls_static_page'?[['','不显示页面'],...(protocol.static_pages||[]).map(page=>[page.key,page.name]),['custom','自定义 HTML']]:key==='mieru_transport'?['TCP','UDP']:key==='mieru_traffic_pattern'?[['off','关闭'],['light','轻度'],['balanced','均衡'],['aggressive','激进'],['custom','自定义']]:key==='xhttp_mode'?['auto','stream-one','stream-up','packet-up']:key==='xhttp_cdn'?[['none','无'],['cloudflare','Cloudflare'],['other','其他 CDN']]:key==='panel_certificate_id'?certificateOptions:key==='method'?protocol.methods:key==='transport'?protocol.transports:key==='tls_mode'?[['native','内核 TLS'],['caddy','Caddy'],['nginx','Nginx'],...(protocol.key==='vless-xhttp-tls'?[]:[['external','外部终止']])]:undefined;
      const control=key=>field(key==='credential'?protocol.credential_label||labels[key]:labels[key]||key,key,key==='tls_static_page'?(plan.tls_static_html?(protocol.static_pages||[]).find(page=>page.html===plan.tls_static_html)?.key||'custom':''):key==='panel_certificate_id'?certificateID:typeof plan[key]==='object'?JSON.stringify(plan[key]||{},null,2):plan[key]??(booleans.has(key)?false:''),{...(booleans.has(key)?{type:'checkbox',value:'1'}:numeric.has(key)?{type:'number',min:0,max:key.includes('port')?65535:undefined}:multiline.has(key)?{multiline:4,spellcheck:false}:/credential|private_key|password|auth_key|preshared_key|seed|decryption/.test(key)?{type:'password',autocomplete:'new-password'}:{}),...(options(key)?{options:options(key)}:{}),...(key==='reality_server_name'?{suggestions:current.reality_presets}:{}),required:['tag','listen','port'].includes(key),readonly:key==='listen'&&engine==='sing-box'&&protocol.uses_wireguard,maxlength:multiline.has(key)?undefined:2000});
      const sections=[node('div',{className:'a-form-grid'},keys.map(control))];
      if(protocol.transport_config)sections.push(group('传输',node('div',{className:'a-form-grid'},['transport','transport_path','transport_host','transport_http_method','flow'].map(control)),true));
      if(protocol.supports_tls||protocol.requires_tls)sections.push(group('TLS',node('div',{className:'a-form-grid'},['tls_enabled','tls_server_name','tls_alpn','tls_client_alpn','certificate_path','private_key_path','panel_certificate_id','tls_client_certificate',...(protocol.supports_managed_tls?['tls_mode','tls_acme_email','tls_public_port',...(protocol.key==='vless-xhttp-tls'?['tls_static_page','tls_static_html']:[])]:[])].map(control)),Boolean(plan.tls_enabled||protocol.requires_tls)));
      if(isXHTTPGroup(protocol.key))sections.push(group('TLS/CDN 入口',grid(...['tls_server_name','tls_alpn','tls_client_alpn','certificate_path','private_key_path','panel_certificate_id'].map(control)),true));
      if(protocol.uses_reality||protocol.key==='vless')sections.push(group('Reality',node('div',{className:'a-form-grid'},['reality_enabled','reality_private_key','reality_public_key','reality_short_id','reality_server_name','reality_target','reality_server_names',...(protocol.supports_reality_mldsa?['reality_min_client_ver','reality_mldsa65_seed','reality_mldsa65_verify']:[])].map(control)),protocol.uses_reality));
      if(protocol.uses_vless_encryption)sections.push(group('VLESS Encryption',grid(control('vless_decryption'),control('vless_encryption')),true));
      const prefixes=protocol.key.startsWith('vless-xhttp')||protocol.key.includes('xhttp')?['xhttp_']:protocol.key.startsWith('snell')?['snell_']:protocol.key.startsWith('sudoku')?['sudoku_']:protocol.key.startsWith('mieru')?['mieru_']:protocol.uses_wireguard?['wireguard_']:protocol.key==='tailscale'?['tailscale_']:protocol.key==='openvpn-server'?['openvpn_']:[];
      const special=Object.keys(labels).filter(key=>key!=='mieru_custom_pattern'&&(!['xhttp_backend_port','xhttp_tls_port','xhttp_tls_address','xhttp_tls_client_port','xhttp_tls_host'].includes(key)||isXHTTPGroup(protocol.key))&&!(key==='wireguard_server_address'&&engine!=='sing-box')&&prefixes.some(prefix=>key.startsWith(prefix)));
      if(protocol.port_forward)special.push('target_address','target_port','network');
      if(engine==='ss-rust')special.push('ss_rust_dns','ss_rust_outbound_bind_addr','ss_rust_ipv6_first');
      if(special.length)sections.push(group('协议参数',node('div',{className:'a-form-grid'},special.map(control)),true));
      sections.push(group('客户端与路由',node('div',{className:'a-form-grid'},['client_address','client_port','client_host',...(engine==='mihomo'?['listener_routing_mark','listener_rule','listener_proxy']:[])].map(control))));
      area.replaceChildren(...sections);
      area.append(...protocolOptions(form, protocol, plan));
      const minimum = form.elements.namedItem('reality_min_client_ver');
      if (minimum) {
        minimum.required = true;
        minimum.pattern = '[0-9]{1,3}(\\.[0-9]{1,3}){2}';
        minimum.value ||= '0.0.0';
      }
      for (const input of area.querySelectorAll('input[type=password]')) {
        const reveal = button('显示', () => { input.type = input.type === 'password' ? 'text' : 'password'; reveal.textContent = input.type === 'password' ? '显示' : '隐藏'; });
        input.closest('.a-field')?.append(actions(reveal, button('复制', event => void ctx.mutate(event.currentTarget, async () => {
          await copyText(input.value);
          if (ctx.current() && box.isConnected) ctx.announce('已复制');
        }))));
      }
      for (const [name, fields, label] of generatedFieldActions) {
        const input = form.elements.namedItem(name);
        if (!input || input.type === 'hidden') continue;
        const requested = (name === 'credential' && protocol.key === 'sudoku' ? 'credential,sudoku_client_key' : fields).split(',');
        const generate = button(requested.length > 1 ? '生成密钥对' : '生成', event => void ctx.mutate(event.currentTarget, async () => {
          const draft = JSON.stringify(read()), selected = protocol, requestedRevision = revision;
          const generated = await ctx.api(`${base}/plans`, { method: 'POST', body: { protocol: selected.key, input: read() } });
          if (!ctx.current() || !box.isConnected || selected !== protocol) return;
          if (requestedRevision !== revision || draft !== JSON.stringify(read())) throw new Error('参数已修改，未应用过期生成结果');
          for (const key of requested) {
            const control = form.elements.namedItem(key);
            if (control && Object.hasOwn(generated, key)) {
              control.value = generated[key] ?? '';
              control.dispatchEvent(new Event('input', { bubbles: true }));
            }
          }
          form.dispatchEvent(new Event('input', { bubbles: true }));
          ctx.announce(`${label.replace(/^生成/, '')}已生成`);
        }), { 'aria-label': label });
        const field = input.closest('.a-field');
        if (field) (field.querySelector('.a-actions') || field.appendChild(actions())).append(generate);
      }
      bindAnalyticsCertificateSelector(ctx, { form: area, protocol, plan, requireManagedCertificate: operation === 'add' });
      bindInboundTransport(form, protocol, { requireManagedCertificate: operation === 'add' });
      form.elements.namedItem('tls_static_page')?.addEventListener('change', event => {
        const page = (protocol.static_pages || []).find(page => page.key === event.target.value);
        if (page) form.elements.namedItem('tls_static_html').value = page.html;
      });
      form.elements.namedItem('tls_mode')?.addEventListener('change', () => queueMicrotask(() => {
        const port = form.elements.namedItem('port'), mode = form.elements.namedItem('tls_mode');
        if (protocol.key === 'vless-xhttp-tls' && ['caddy','nginx'].includes(mode.value) && port?.value === form.elements.namedItem('tls_public_port')?.value) {
          port.closest('.a-field')?.querySelector('[aria-label="生成监听端口"]')?.click();
        }
      }));
    }
    async function choose() {
      const epoch = ++request;
      const selected = protocols.find(item => item.key === form.elements.protocol.value);
      if (!selected) return;
      const previous = baseline || protocolDrafts.get(protocol?.key);
      baseline = null;
      const submit = form.querySelector('button[type=submit]');
      submit.disabled = area.disabled = regenerate.disabled = true;
      if (native) native.disabled = true;
      feedback.textContent = '正在读取协议方案…';
      try {
        let generated = protocolDrafts.get(selected.key) || (initialInput?.protocol === selected.key ? initialInput : null);
        if (!generated) {
          generated = await ctx.api(`${base}/plans`, { method: 'POST', body: { protocol: selected.key } });
          if (operation === 'modify') {
            // Retain the inbound's address and routing identity, never reuse
            // another protocol's authentication, transport or TLS parameters.
            for (const key of ['tag','listen','port','username','client_address','client_port','client_host','listener_routing_mark','listener_rule','listener_proxy','block_mainland_destination','block_mainland_source','ss_rust_dns','ss_rust_outbound_bind_addr','ss_rust_ipv6_first'])
              if (Object.hasOwn(previous || input, key)) generated[key] = (previous || input)[key];
          }
        }
        if (epoch !== request || !ctx.current() || !box.isConnected) return;
        initialInput = null;
        area.disabled = regenerate.disabled = false;
        if (native) native.disabled = false;
        protocol = selected; render(generated);
        protocolHint.hidden = operation !== 'modify' || selected.key === input.protocol;
        const reason = operation === 'add' && !agent.runtime?.[engine]?.installed ? coreInstallBlockReason(current.agent, engine, ctx.can, { automatic: true }) : '';
        feedback.textContent = reason || (!execute ? '节点当前离线，草稿可继续编辑，上线后重新打开再提交。' : ''); submit.disabled = !execute || Boolean(reason);
      } catch (error) {
        if (epoch !== request || !ctx.current() || !box.isConnected || error.name === 'AbortError') return;
        baseline = previous;
        if (protocol && baseline) {
          form.elements.protocol.value = protocol.key;
          area.disabled = regenerate.disabled = false;
          if (native) native.disabled = false;
          submit.disabled = !execute;
        }
        feedback.textContent = error.message;
      }
    }
    form.addEventListener('input', () => { revision++; });
    form.addEventListener('change', () => { revision++; });
    form.elements.protocol.addEventListener('change', () => {
      try { if (protocol && baseline) protocolDrafts.set(protocol.key, read()); }
      catch (error) { form.elements.protocol.value = protocol.key; feedback.textContent = error.message; return; }
      void choose();
    });
    box=ctx.dialog(operation==='add'?'添加入站':`修改入站 · ${input.tag}`,form,{wide:true});
    track(form,box); await choose();
  }
  async function inboundSource(target) {
    if (!await latest() || !current.config) return;
    const config=current.config, draft=getDraft();
    await editInboundSource(ctx,{target,agent,engine,base,workspace:current,preparing,write,committed,track,isCurrent:()=>ctx.current()&&current.config===config&&getDraft()===draft});
  }
  async function files() {
    if(!await latest()||!current.config)return;
    const editor=field('完整配置','content',current.config.content,{multiline:24,spellcheck:false,readonly:!editable});
    const form=ctx.form([node('div',{className:'a-editor'},editor)], '保存并部署',async()=>{
      if(!await preparing('deploy'))return;
      const result=await write(`${base}/source`,{name:current.config.name,description:current.config.description,version:current.config.version,content:form.elements.content.value,intent:'deploy'},box);
      await committed(result,box);
    });
    const box=ctx.dialog('展开源码',form,{wide:true});
    track(form,box);
    if(!editable)form.querySelector('button[type=submit]').remove();
    else if (!agent.runtime?.[engine]?.installed && !agent.shared_allocation?.enabled) {
      form.querySelector('button[type=submit]').disabled=true;
      form.append(notice('内核尚未安装，请先安装内核后再部署源码。'));
    }
  }
  async function commonEditor(inbound) {
    if (!await latest() || !current.config) return;
    const config = current.config, fields = inbound ? list(current.catalog?.fields).filter(item => ['inbound','override'].includes(item.scope)) : commonConfigFields(engine,list(current.catalog?.fields));
    const drafts = new Map(), baseline = new Map();
    const mutation = field('操作','mutation','add',{options:[['add','添加'],['modify','修改'],['delete','删除']]}),
      selector = field('配置项','key','',{options:[]}), fragment = field('字段片段','fragment','',{multiline:12,spellcheck:false}), feedback = notice('');
    let epoch = 0, selected = '', selectedOperation = '', ready = false, box;
    const form = ctx.form([mutation,selector,fragment,feedback,intents()], '保存并部署', async values => {
      if (!ready || values.key !== selected || values.mutation !== selectedOperation) throw new Error('配置项状态或操作已变化，请重新选择');
      if ([...drafts].some(([key,value])=>key!==selected && value!==baseline.get(key)) && !await ctx.confirm('其他配置项还有未保存草稿，保存当前项后将关闭编辑器，确定继续？','其他草稿尚未保存')) return;
      if (values.mutation === 'delete' && !await ctx.confirm(`删除配置项“${selected}”？${values.intent==='deploy'?'部署会重启当前实例内核。':''}`,'删除配置项')) return;
      if (!await preparing(values.intent)) return;
      const result = await write(`${base}/fields/${pathID(selected)}${inbound?`?inbound=${pathID(inbound.tag)}`:''}`, {
        mutation: selectedOperation, fragment: selectedOperation === 'delete' ? '' : values.fragment,
        expected_version: config.version, name: config.name, description: config.description, intent: values.intent,
      }, box);
      await committed(result,box);
    });
    const capture = () => { if (ready && selectedOperation!=='delete') drafts.set(selected,form.elements.fragment.value); };
    async function load() {
      capture(); const expected=++epoch, operation=form.elements.mutation.value, key=form.elements.key.value;
      selected=key; selectedOperation=operation; ready=false; form.querySelector('button[type=submit]').disabled=true;
      feedback.textContent=key?'正在读取配置项…':operation==='add'?'没有可添加的配置项':'尚无可操作的配置项';
      fragment.hidden=!key; if(!key)return;
      try {
        const value=await ctx.api(`${base}/fields/${pathID(key)}${inbound?`?inbound=${pathID(inbound.tag)}`:''}`);
        if(expected!==epoch||!ctx.current()||!box.isConnected)return;
        if(value.version!==undefined&&value.version!==config.version)throw new Error('配置已变化，请关闭并重新打开');
        if((operation==='add')===Boolean(value.present))throw new Error('配置项状态已变化，请重新读取');
        baseline.set(key,value.fragment||'');
        form.elements.fragment.value=operation==='delete'?value.fragment||'':drafts.get(key)??value.fragment??'';
        form.elements.fragment.readOnly=operation==='delete'; feedback.textContent='';ready=true;
        form.querySelector('button[type=submit]').disabled=!execute||!agent.runtime?.[engine]?.installed;
        if(!execute||!agent.runtime?.[engine]?.installed)feedback.textContent='节点离线或内核未安装，草稿可编辑，暂不能提交。';
      } catch(error) { if(expected===epoch&&ctx.current()&&box.isConnected&&error.name!=='AbortError')feedback.textContent=error.message; }
    }
    function choose() {
      capture(); const operation=form.elements.mutation.value;
      const choices=fields.filter(item=>operation==='add'?!current.present_fields?.[item.key]:current.present_fields?.[item.key]);
      form.elements.key.replaceChildren(...choices.map(item=>node('option',{value:item.key},`${item.label} · ${item.key}`)));
      selected=''; ready=false;void load();
    }
    form.elements.mutation.addEventListener('change',choose);form.elements.key.addEventListener('change',()=>void load());
    box=ctx.dialog(inbound?`${inbound.tag} · 端口配置项`:'通用配置项',form,{wide:true});
    track(form,box,true,()=>{ capture(); return [...drafts].some(([key,value])=>value!==baseline.get(key)); });choose();
  }
  async function outbound(inbound,operation) {
    if (dirty()) throw new Error('配置源码有未保存修改，请先保存，再操作出站。');
    const loading=muted('正在读取出站配置…');loading.setAttribute('role','status');
    const box=ctx.dialog(`${inbound.tag} · 出站`,loading,{wide:true});
    let preset;
    box.addEventListener('close',()=>preset?.dispose(),{once:true});
    try {
      if(!await latest()||!current.config||!box.isConnected)return;
      const config=current.config,content=config.content,binding=inboundOutbound(content,engine,inbound);
      const entries=outboundEntries(content).filter(entry=>outboundCanBind(entry,engine)),chosen=entries.find(entry=>entry.tag===binding);
      if(['modify','delete'].includes(operation)&&!chosen)throw new Error('当前入站尚未绑定可编辑出站');
      const selecting=operation==='bind',deleting=operation==='delete';
      const fragment=field('出站 JSON','fragment',chosen?.content||JSON.stringify(engine==='xray'?{tag:`exit-${inbound.port}`,protocol:'freedom'}:{tag:`exit-${inbound.port}`,type:'direct'},null,2),{multiline:12,spellcheck:false,readonly:selecting||deleting});
      fragment.querySelector('textarea').setAttribute('aria-label','出站配置 JSON');
      const picker=selecting&&field('已有出站','tag',binding||entries[0]?.tag||'',{options:entries.map(entry=>[entry.tag,entry.tag]),required:true});
      const context=stack(muted(`${agent.name} · ${engine} · ${agent.shared_allocation?'当前共享实例':'主实例'} · 入站 ${inbound.tag} · ${inbound.port}`),
        muted(`mark · 0x${(0x51430000|inbound.port).toString(16)} · 绑定作为当前入站的兜底出口`));
      const form=ctx.form([
        picker,fragment,intents(),
      ],'保存并部署',async values=>{
        const raw=preset?await preset.verifiedContent():form.elements.fragment.value;
        if((deleting||selecting)&&!await ctx.confirm(deleting?'删除当前绑定出站？默认或共享出口不可在此删除。':'将当前入站绑定到所选出站？已有条件分流保持不变。',deleting?'删除出站':'绑定出站'))return;
        if(!ctx.current()||!box.isConnected||!await preparing(values.intent))return;
        let next;
        try { next=selecting?bindOutbound(content,engine,inbound,values.tag):changeInboundOutbound(content,engine,inbound,operation,chosen?.index,raw); }
        catch(error){if(error instanceof SyntaxError)throw new Error('出站 JSON 格式无效，请检查后重试。',{cause:error});throw error;}
        const result=await write(`${base}/source`,{name:config.name,description:config.description,content:next,version:config.version,intent:values.intent},box);
        await committed(result,box);
      });
      if(deleting)form.querySelector('button[type=submit]').classList.add('danger');
      box.querySelector('.a-dialog-body').replaceChildren(form);
      track(form,box);
      if(selecting){
        const update=()=>{form.elements.fragment.value=entries.find(entry=>entry.tag===form.elements.tag.value)?.content||'';};
        form.elements.tag.addEventListener('change',update);update();
      }
      if(operation==='add'){
        preset=bindOutboundPresetController({form:form.querySelector('fieldset'),input:form.elements.fragment,engine,agentId:agent.id,
          api:ctx.api,canReadPeers:ctx.can('client-access.read'),active:()=>ctx.current()&&box.isConnected,
          onChange:()=>form.dispatchEvent(new Event('input',{bubbles:true})),host:outboundPresetFields(engine),
          visibility:(element,visible)=>{if(element)element.hidden=!visible;},feedback:(element,value)=>{element.textContent=value;},
        });
      }
      form.prepend(context);
    }catch(error){if(ctx.current()&&box.isConnected&&error.name!=='AbortError')box.querySelector('.a-dialog-body').replaceChildren(notice(error.message));}
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
    ],'保存并部署',async values=>{
      if(object&&operation==='modify'&&!await ctx.confirm(`更新“${object.tag}”及其所有引用？`,'更新出站'))return;
      if(!await preparing(values.intent))return;
      const result=await write(`${base}/outbounds`,{version:config.version,intent:values.intent,section:object?.section||values.section,tag:object?.tag||'',fragment:values.fragment||'',operation},box);
      await committed(result,box);
    });
    const box=ctx.dialog(operation==='add'?'添加出站':operation==='delete'?'删除出站':`${object.tag} · 出站`,form,{wide:true});track(form,box);
  }
  async function removeInbound(target) {
    if (!await latest() || !current.config) return;
    const config = current.config;
    const fresh = list(current.inbound_targets).find(item => item.tag === target.tag && item.port === target.port);
    if (!fresh) throw new Error('入站已变化，请重新选择');
    const mutable = xrayMutableAPIServices(engine, config.content);
    const form = ctx.form([
      muted(`删除入站“${fresh.tag}”（端口 ${fresh.port}）？不会删除其他入站。`),
      mutable.length > 0 && notice(`删除需要关闭 ${mutable.join('、')}，保留日志、统计及其他 API 配置。`),
      intents(),
    ], '保存并部署', async values => {
      if (mutable.length && !await ctx.confirm(`关闭 ${mutable.join('、')} 并删除此入站？`, '关闭可修改路由的服务')) return;
      if (!await preparing(values.intent)) return;
      const input = list(current.inbounds).find(item => item.tag === fresh.tag && item.port === fresh.port);
      const result = await write(`${base}/server-inbounds`, {
        operation: 'delete', disable_mutable_api: mutable.length > 0, original_tag: fresh.tag,
        expected_version: config.version, name: config.name, description: config.description,
        intent: values.intent, input: { tag: fresh.tag, port: fresh.port },
      }, box);
      await committed(result, box);
    });
    const box = ctx.dialog('删除入站', form); track(form, box);
  }
  function draw() {
    deployment.resetPorts();
    const inbounds=list(current.inbounds),targets=list(current.inbound_targets);
    const managed=list(current.managed_objects);
    const toggle=object=>button(object.enabled?'停用':'启用',event=>void ctx.mutate(event.currentTarget,async()=>{
      if(!await latest())return;
      const fresh=list(current.managed_objects).find(item=>item.section===object.section&&item.tag===object.tag);
      if(!fresh||fresh.enabled!==object.enabled)throw new Error('配置状态已变化，请重新选择');
      const disabled=list(current.management?.disabled).filter(item=>item.section!==object.section||item.tag!==object.tag);
      const expectedVersion=current.config.version;
      if(object.enabled)disabled.push({section:object.section,tag:object.tag});
      const form=ctx.form([muted(`${object.enabled?'停用':'启用'} ${object.tag}`),intents()], '保存并部署',async values=>{
        if(!await preparing(values.intent))return;
        const result=await write(`${base}/management`,{version:expectedVersion,disabled,intent:values.intent},box,'PUT');
        await committed(result,box);
      });
      const box=ctx.dialog(object.enabled?'停用配置对象':'启用配置对象',form); track(form, box);
    }));
    const controls=writable&&button('添加入站',event=>void ctx.mutate(event.currentTarget,()=>inboundEdit(null,'add')),{tone:'primary'});
    const status=deployment.element;
    const rows=targets.map(target=>{
      const input=inbounds.find(input=>input.tag===target.tag&&input.port===target.port);
      const object=managed.find(item=>item.inbound&&item.tag===target.tag);
      return [target.port||'无固定端口',stack(target.tag,muted([object?.listen||input?.listen,object?.network].filter(Boolean).join(' · '))),target.kind,
        deployment.inboundState(target.port,object?.enabled),actions(
        ctx.can('agent-config.read')&&button('访问限制',event=>void ctx.mutate(event.currentTarget,()=>restrictions(target))),
        writable&&object&&toggle(object),
        writable&&button('修改',event=>void ctx.mutate(event.currentTarget,()=>input?inboundEdit(input):inboundSource(target))),
        ctx.can('client-access.read')&&input&&button('复制入站连接',event=>void ctx.mutate(event.currentTarget,()=>copyInbound(input))),
        writable&&button('删除',event=>void ctx.mutate(event.currentTarget,()=>removeInbound(target)),{tone:'danger'}),
        writable&&['xray','sing-box'].includes(engine)&&button('出站',()=>{
          let binding='';try{binding=inboundOutbound(current.config.content,engine,target);}catch(error){ctx.announce(error.message,'error');return;}
          const chooser=ctx.dialog(`${target.tag} · 出站操作`,actions(['add','bind','modify','delete'].map((operation,index)=>button(['添加并绑定','绑定已有出站','修改绑定出站','删除绑定出站'][index],()=>{chooser.close();void outbound(target,operation);},{disabled:['modify','delete'].includes(operation)&&!binding}))));
        }),
        writable&&engine==='ss-rust'&&button('端口配置项',event=>void ctx.mutate(event.currentTarget,()=>fieldChooser(target))),
      )];
    });
    const outboundObjects=managed.filter(item=>!item.inbound);
    const headers=['端口','监听','协议','状态','操作'];
    const portTable=table(headers,rows,{label:'入站配置',columnWidths:['11%','25%','16%','16%','32%']});
    portTable.classList.add('a-port-list');
    for(const element of portTable.querySelectorAll('table,thead,tbody,tr,th,td')) {
      const roles={TABLE:'table',THEAD:'rowgroup',TBODY:'rowgroup',TR:'row',TH:'columnheader',TD:'cell'};
      element.setAttribute('role',roles[element.tagName]);
    }
    for(const row of portTable.querySelectorAll('tbody tr')) [...row.cells].forEach((cell,index)=>cell.dataset.label=headers[index]);
    const inboundSection=section('入站配置',stack(deployment.feedback,portTable),actions(status,controls),current.snapshot_task_id?'读取的节点配置':'面板保存的配置');
    surface.replaceChildren(receipt,inboundSection,
      ['xray','sing-box','mihomo'].includes(engine)&&section('出站配置',table(['名称','类型','目标状态','操作'],outboundObjects.map(object=>[object.tag,object.kind||object.section,badge(object.enabled?'启用':'停用'),writable&&actions(toggle(object),button('修改',event=>void ctx.mutate(event.currentTarget,()=>outboundObject(object))),button('删除',event=>void ctx.mutate(event.currentTarget,()=>outboundObject(object,'delete')),{tone:'danger'}))]),{label:'出站配置'}),writable&&button('添加出站',event=>void ctx.mutate(event.currentTarget,()=>outboundObject(null,'add')))),
    );
    sourceTools?.replaceChildren(...(current.config?[
      button('展开源码',event=>void ctx.mutate(event.currentTarget,files)),
      writable&&button('通用配置项',event=>void ctx.mutate(event.currentTarget,()=>fieldChooser())),
    ].filter(Boolean):[]));
  }
  function fieldChooser(inbound) {
    return commonEditor(inbound);
  }
  draw();return surface;
}
