import { managedCoreBlockReason } from "../modules/managed-core-state.js";
import { bindJSONEditor } from './json-editor.js';
import { readableConfigSource } from '../modules/code-format.js';
import { preparedDialog, loadingForm } from "./loading.js";
import { recoverConfigInstance } from "./config-status-recovery.js";
import { labels, numeric, booleans, multiline } from "./inbound-fields.js";
import { bindAnalyticsCertificateSelector } from "./certificate-selector.js";
import { bindInboundTransport } from "./inbound-tls.js";
import { readServerPlanInput, generatedFieldActions, isXHTTPGroup } from "../modules/inbound-options-model.js";
import { protocolOptions, outboundPresetFields } from "./inbound-options.js";
import { editInboundAccess } from "./access-control.js";
import { deploymentStatus } from "./config-deployment.js";
import { editInboundSource } from "./inbound-source.js";
import { inboundOutbound } from '../modules/config-outbound-model.js';
import { bindOutboundPresetController } from '../modules/outbound-preset-controller.js';
import { commonConfigFields } from '../modules/config-field-model.js';
import { xrayMutableAPIServices } from '../modules/xray-api.js';
import { configDeployBlockReason, prepareConfigDeployment } from '../modules/core-install-state.js';
import { copyText, node, button, actions, stack, grid, section, table, badge, field, code, muted, notice, link, list, pathID } from './ui.js';
import { taskDetail, date } from './operations.js';

const group = (title, content, open = false) => node('details', { className:'a-disclosure', open }, node('summary',{},title), node('div',{className:'a-disclosure-body'},content));

// All operations stay bound to one account/agent/engine workspace revision.
export function configStudio(ctx, { agent, engine, base, workspace, getDraft, accept, beforeDeploy, dirty, sourceTools, readOnly = false, onAgent }) {
  let current = workspace;
  const surface = node('div', {className:'a-stack'});
  const receipt = notice(''); receipt.hidden = true;
  const trackedForms = new Map();
  const deployment = deploymentStatus(ctx, { base, workspace, getConfig: () => current.config, isPreview:()=>Boolean(current.snapshot_task_id), agent, engine, onWorkspace(fresh) {
    if (!fresh.agent || fresh.agent.id !== agent.id || (fresh.agent.shared_allocation?.id || "") !== (agent.shared_allocation?.id || "")) return;
    Object.assign(agent, fresh.agent);
    execute = !configDeployBlockReason(agent, engine, ctx.can);
    onAgent?.(agent);
    for (const [form, box] of trackedForms) {
      if (!box.isConnected) { trackedForms.delete(form); continue; }
      const hint = form.querySelector('[data-deploy-block]');
      if (hint) { hint.textContent = configDeployBlockReason(agent,engine,ctx.can); hint.hidden = !hint.textContent; }
      if (form.getAttribute("aria-busy") !== "true" && !box.conflictPending && !box.submissionPending && form.dataset.configReady !== "false")
        form.querySelector("button[type=submit]")?.toggleAttribute("disabled", !execute);
    }
  } });
  const workspacePath = () => current.snapshot_task_id ? `${base}/workspace/snapshots/${pathID(current.snapshot_task_id)}` : `${base}/workspace`;
  surface.previewSnapshot = async (id,isCurrent=ctx.current) => {
    const previous = current.config;
    const fresh = await ctx.api(`${base}/workspace/snapshots/${pathID(id)}`);
    if (!ctx.current() || !isCurrent()) return;
    if (current.config !== previous || (fresh.config?.version || 0) !== (previous?.version || 0)) throw new Error('配置已变化，请重新读取后编辑');
    current = {...fresh,snapshot_task_id:fresh.config?.content===previous?.content?undefined:id}; draw();
  };
  surface.acceptConfig = config => { current = { ...current, config, source_content:config.content, snapshot_task_id:undefined }; void deployment.refresh(); };
  surface.reload = async () => {
    const config = current.config;
    const fresh = await ctx.api(workspacePath());
    if (!ctx.current() || current.config !== config) return;
    current = {...fresh,snapshot_task_id:current.snapshot_task_id}; draw(); void deployment.refresh();
    return fresh;
  };
  const editable = !readOnly && ctx.can('agent-config.write');
  let execute = !configDeployBlockReason(agent, engine, ctx.can);
  const writable = editable && ctx.can('tasks.execute') && !managedCoreBlockReason(agent, engine);
  async function latest(readCtx = ctx) {
    if (dirty() && !await ctx.confirm('打开编辑工具会替换尚未保存的源码修改，确定继续？','打开编辑工具')) return false;
    const draft = getDraft(), previous = current.config;
    const fresh = await readCtx.api(workspacePath());
    if (draft !== getDraft()) throw new Error('源码已修改，请保存后重新打开编辑工具');
    if (!readCtx.current()) return false;
    if (current.config !== previous || fresh.config?.id !== previous?.id || current.snapshot_task_id && fresh.config?.version !== previous?.version)
      throw new Error('配置已变化，请重新读取后再打开编辑工具');
    current = {...fresh,snapshot_task_id:current.snapshot_task_id};
    if (fresh.config && !current.snapshot_task_id) accept(fresh.config, fresh.source_content);
    return true;
  }
  function track(form, box, requiresTask = true, isDirty) {
    if (requiresTask && form.querySelector("button[type=submit]")) trackedForms.set(form,box);
    if (requiresTask) {
      const hint = notice(configDeployBlockReason(agent,engine,ctx.can));
      hint.dataset.deployBlock = ''; hint.hidden = !hint.textContent; form.append(hint);
      if (!execute) form.querySelector('button[type=submit]').disabled = true;
    }
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
    if (!await prepareConfigDeployment(ctx, base, agent, engine)) return false;
    if (beforeDeploy) await beforeDeploy();
    return ctx.current();
  }
  async function reloadSaved() {
    if (!await latest(ctx)) return;
    receipt.hidden = true; draw(); void deployment.refresh();
  }
  async function write(path, body, box, method = 'POST') {
    const candidate = {...body, ...(!agent.runtime?.[engine]?.installed ? {install_if_missing:true} : {})};
    if (box.mutationVersion !== undefined) {
      if ('version' in candidate) candidate.version = box.mutationVersion;
      if ('expected_version' in candidate) candidate.expected_version = box.mutationVersion;
    }
    try { return await ctx.api(path, { method, body: {...candidate, expected_instance_id:agent.shared_allocation?.id || ""} }); }
    catch (error) {
      if (error.name === 'AbortError' || error.deployPreflight) throw error;
      if (error.code === 'port_conflict') {
        const form = box.querySelector('form'), field = form.elements.namedItem(error.field || 'port');
        error.message += ' 草稿已保留。';
        if(error.recoveryEngine && ctx.can('tasks.read') && !form.querySelector('[data-port-recovery]')) {
          const feedback=notice(''),control=button('核对实例状态',async event=>{
            await ctx.mutate(event.currentTarget,async()=>{
              feedback.textContent='正在核对实例状态…';
              await recoverConfigInstance(ctx,agent,error.recoveryEngine,()=>ctx.current()&&box.isConnected);
              feedback.textContent='状态已核对，可继续提交；草稿已保留。';
            });
          });
          const recovery=stack(feedback,actions(control));recovery.dataset.portRecovery='';form.append(recovery);
        }
        form.querySelector('button[type=submit]').addEventListener('mutationend', () => {
          if (!ctx.current() || !box.open || !field) return;
          for (let parent = field.parentElement; parent && parent !== form; parent = parent.parentElement)
            if (parent.tagName === 'DETAILS') parent.open = true;
          field.focus();
        }, { once: true });
      } else if (error.status === 409 && error.configSubmissionRejected) {
        const form = box.querySelector('form'), submit = form.querySelector('button[type=submit]');
        box.conflictPending = true;
        submit.addEventListener('mutationend', () => { if (box.conflictPending) submit.disabled = true; }, {once:true});
        if (!form.querySelector('[data-conflict-recovery]')) {
          const feedback = notice('尚未保存，当前草稿已保留。');
          const preview = group('最新已保存配置',notice('请先读取最新配置。'));
          let fresh;
          const adopt = button('在最新配置上继续编辑', () => {
            if (!fresh || !ctx.current() || !box.isConnected) return;
            box.mutationVersion = fresh.config?.version || 0;
            box.conflictPending = false; submit.disabled = !execute;
            feedback.textContent = '已更新保存目标，当前草稿已保留，可修正后提交。';
            adopt.disabled = true;
          }, {disabled:true});
          const reload = button('核对最新配置', event => void ctx.mutate(event.currentTarget, async () => {
            const result = await ctx.api(`${base}/workspace`);
            if (!ctx.current() || !box.isConnected) return;
            if (current.config?.id && result.config?.id !== current.config.id) throw new Error('配置目标已变化，请保留草稿并重新选择实例。');
            fresh = result;
            preview.querySelector('.a-disclosure-body').replaceChildren(code(result.config?.content || '尚无已保存配置'));
            preview.open = true; adopt.disabled = false;
          }));
          const recovery = stack(feedback,preview,actions(reload,adopt));
          recovery.dataset.conflictRecovery = ''; form.append(recovery);
        }
      } else if (!error.status || error.status === 200 || error.status === 408 || error.status >= 500 || error.status === 409) {
        const form = box.querySelector('form'), submit = form.querySelector('button[type=submit]');
        box.submissionPending = true;
        submit.addEventListener('mutationend', () => { submit.disabled = true; }, { once: true });
        form.append(notice('保存结果需重新核对，草稿已保留。请勿重复提交。'), actions(button(error.recoverConfigSubmission?'核对提交结果':'重新读取并核对', async event => {
          if(error.recoverConfigSubmission){await ctx.mutate(event.currentTarget,async()=>{const result=await error.recoverConfigSubmission();if(ctx.current()&&box.isConnected)await committed(result,box);});return;}
          if (!await ctx.confirm('重新读取已保存配置并放弃此弹窗草稿？请先核对任务记录，避免重复执行。', '重新读取配置') || !ctx.current()) return;
          await ctx.mutate(event.currentTarget, async () => { ctx.setDirty(false); box.close(); await reloadSaved(); });
        })));
      }
      throw error;
    }
  }
  async function committed(result, box) {
    if (!ctx.current()) return;
    current = { ...current, config: result.config, source_content:result.config.content, snapshot_task_id:undefined };
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
  function restrictions(target) {
    return preparedDialog(ctx, '访问限制', ['访问策略', '允许的 IP / CIDR', '允许的域名'], local => restrictionsLoaded(local, target));
  }
  async function restrictionsLoaded(ctx, target) {
    if (current.snapshot_task_id) throw new Error('读取的节点配置尚未保存，请先保存并部署，再设置访问限制。');
    if (dirty()) throw new Error('配置源码有未保存修改，请先保存，再设置入站访问限制。');
    if (!await latest(ctx) || !current.config) return;
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
  function inboundEdit(input, operation = 'modify') {
    return preparedDialog(ctx, operation === 'add' ? '添加入站' : `修改入站 · ${input.tag}`, ['协议', '入站标签', '监听地址', '监听端口', '传输与 TLS'], local => inboundEditLoaded(local, input, operation));
  }
  async function inboundEditLoaded(ctx, input, operation = 'modify') {
    if (!await latest(ctx)) return;
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
          const updated = read();
          const rotateMLDSA = Boolean(updated.reality_mldsa65_seed || updated.reality_mldsa65_verify);
          for (const key of ['credential','secondary_credential','reality_private_key','reality_public_key','reality_short_id','reality_mldsa65_seed','reality_mldsa65_verify','wireguard_server_private_key','wireguard_server_public_key','wireguard_client_private_key','wireguard_client_public_key','wireguard_preshared_key','vless_decryption','vless_encryption','sudoku_client_key','snell_shadow_tls_password'])
            if (Object.hasOwn(generated,key) && (rotateMLDSA || !key.startsWith('reality_mldsa65_'))) updated[key] = generated[key];
          render(updated);ctx.setDirty(true);
        }
      });
    });
    const native = input && operation === 'modify' && button('完整入站配置',async event=>{
      const control = event.currentTarget;
      if (!ctx.current()) return;
      await ctx.mutate(control,()=>inboundSource(input,box,()=>ctx.setDirty(JSON.stringify(read())!==JSON.stringify(baseline))));
    });
    const form = ctx.form([protocolSelect,area,actions(regenerate,native),protocolHint,intents(),feedback],'保存并部署',async values=>{
      if(!protocol || !baseline || protocol.key !== form.elements.protocol.value) throw new Error('请等待方案加载');
      const payload=read();
      if(!await preparing(values.intent))return;
      const result=await write(`${base}/server-inbounds`,{operation,original_tag:operation==='add'?'':input.tag,expected_version:current.config?.version||0,name:current.config?.name||`${agent.name} ${engine}`,description:current.config?.description||'',intent:values.intent,input:payload,snapshot_task_id:current.snapshot_task_id,preserve_ss_rust_globals:engine==='ss-rust'},box);
      await committed(result,box);
    });
    function read() {
      const values = new Map(Object.entries(baseline || {}).filter(([key]) => !form.elements.namedItem(key)).map(([key,value]) =>
        [key, typeof value === 'boolean' ? value ? '1' : '0' : typeof value === 'object' ? JSON.stringify(value) : String(value ?? '')]));
      for (const control of form.querySelectorAll('[name]')) if (!control.disabled && (control.type !== 'checkbox' || control.checked)) values.set(control.name, control.value);
      const result = { ...baseline, ...readServerPlanInput(form, protocol, values) };
      if (!protocol.supports_udp_443_block) delete result.block_udp_443;
      return result;
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
      const control=key=>field(key==='credential'?protocol.credential_label||labels[key]:labels[key]||key,key,key==='tls_static_page'?(plan.tls_static_html?(protocol.static_pages||[]).find(page=>page.html===plan.tls_static_html)?.key||'custom':''):key==='panel_certificate_id'?certificateID:typeof plan[key]==='object'?JSON.stringify(plan[key]||{},null,2):plan[key]??(booleans.has(key)?false:''),{...(booleans.has(key)?{type:'checkbox',value:'1'}:numeric.has(key)?{type:'number',min:0,max:key.includes('port')?65535:undefined}:multiline.has(key)?{multiline:4,spellcheck:false}:/credential|private_key|password|auth_key|preshared_key|seed|decryption/.test(key)?{type:'password',autocomplete:'new-password'}:{}),...(options(key)?{options:options(key)}:{}),required:['tag','listen','port'].includes(key),readonly:key==='listen'&&engine==='sing-box'&&protocol.uses_wireguard,maxlength:multiline.has(key)?undefined:key==='reality_mldsa65_verify'?2603:2000});
      const sections=[node('div',{className:'a-form-grid'},keys.map(control))];
      if(protocol.transport_config)sections.push(group('传输',node('div',{className:'a-form-grid'},['transport','transport_path','transport_host','transport_http_method','flow'].map(control)),true));
      if(protocol.supports_tls||protocol.requires_tls)sections.push(group('TLS',node('div',{className:'a-form-grid'},['tls_enabled','tls_server_name','tls_alpn','tls_client_alpn','certificate_path','private_key_path','panel_certificate_id','tls_client_certificate',...(protocol.supports_managed_tls?['tls_mode','tls_acme_email','tls_public_port',...(protocol.key==='vless-xhttp-tls'?['tls_static_page','tls_static_html']:[])]:[])].map(control)),Boolean(plan.tls_enabled||protocol.requires_tls)));
      if(isXHTTPGroup(protocol.key))sections.push(group('TLS/CDN 入口',grid(...['tls_server_name','tls_alpn','tls_client_alpn','certificate_path','private_key_path','panel_certificate_id'].map(control)),true));
      if(protocol.uses_reality||protocol.key==='vless') {
        const reality = [node('div',{className:'a-form-grid'},['reality_enabled','reality_private_key','reality_public_key','reality_short_id','reality_server_name','reality_target','reality_server_names',...(protocol.supports_reality_mldsa?['reality_min_client_ver','reality_mldsa65_seed','reality_mldsa65_verify']:[])].map(control))];
        if (protocol.supports_udp_443_block) reality.push(stack(control('block_udp_443'),muted('仅限此入站的 UDP 目标端口 443')));
        sections.push(group('Reality',stack(...reality),protocol.uses_reality));
      }
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
          const generated = await ctx.api(`${base}/plans`, { method: 'POST', body: { protocol: selected.key, input: read(), ...(name === 'port' ? {generate_port:true} : {}) } });
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
      form.dataset.configReady = "false";
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
        feedback.textContent = ''; form.dataset.configReady = "true"; submit.disabled = !execute;
      } catch (error) {
        if (epoch !== request || !ctx.current() || !box.isConnected || error.name === 'AbortError') return;
        baseline = previous;
        if (protocol && baseline) {
          form.elements.protocol.value = protocol.key;
          area.disabled = regenerate.disabled = false;
          if (native) native.disabled = false;
          form.dataset.configReady = "true"; submit.disabled = !execute;
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
  function inboundSource(target,parent,restoreDirty) {
    return preparedDialog(ctx, `${target.tag} · 入站源码`, ['入站源码'], local => inboundSourceLoaded(local, target, parent, restoreDirty));
  }
  async function inboundSourceLoaded(ctx, target,parent,restoreDirty) {
    if (!await latest(ctx) || !current.config) return;
    const config=current.config, draft=getDraft();
    const child = await editInboundSource(ctx,{target,agent,engine,base,workspace:current,preparing,write,committed:async(result,editor)=>{parent?.close();await committed(result,editor);},track,isCurrent:()=>ctx.current()&&current.config===config&&getDraft()===draft});
    child?.addEventListener('close',()=>{if(parent?.isConnected&&parent.open)restoreDirty?.();},{once:true});
  }
  function files() {
    return preparedDialog(ctx, '展开源码', ['完整配置'], local => filesLoaded(local));
  }
  async function filesLoaded(ctx) {
    if(!await latest(ctx)||!current.config)return;
    const editor=field('完整配置','content',readableConfigSource(current.source_content ?? current.config.content,engine),{multiline:24,spellcheck:false,readonly:!editable});
    const form=ctx.form([node('div',{className:'a-editor'},editor)], '保存并部署',async()=>{
      if(!await preparing('deploy'))return;
      const result=await write(`${base}/source`,{name:current.config.name,description:current.config.description,version:current.config.version,content:form.elements.content.value,intent:'deploy'},box);
      await committed(result,box);
    });
    const box=ctx.dialog('展开源码',form,{wide:true});
    bindJSONEditor(form.elements.content, engine==='mihomo'?'YAML':'JSON', ctx.signal);
    track(form,box);
    if(!editable)form.querySelector('button[type=submit]').remove();

  }
  function commonEditor(inbound) {
    return preparedDialog(ctx, inbound ? `${inbound.tag} · 端口配置项` : '通用配置项', ['操作', '配置项', '字段片段'], local => commonEditorLoaded(local, inbound));
  }
  async function commonEditorLoaded(ctx, inbound) {
    if (!await latest(ctx) || !current.config) return;
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
      selected=key; selectedOperation=operation; ready=false; form.dataset.configReady='false'; form.querySelector('button[type=submit]').disabled=true;
      feedback.textContent=key?'正在读取配置项…':operation==='add'?'没有可添加的配置项':'尚无可操作的配置项';
      fragment.hidden=!key; if(!key)return;
      try {
        const value=await ctx.api(`${base}/fields/${pathID(key)}${inbound?`?inbound=${pathID(inbound.tag)}`:''}`);
        if(expected!==epoch||!ctx.current()||!box.isConnected)return;
        if(value.version!==undefined&&value.version!==config.version)throw new Error('配置已变化，请关闭并重新打开');
        if((operation==='add')===Boolean(value.present))throw new Error('配置项状态已变化，请重新读取');
        baseline.set(key,value.fragment||'');
        form.elements.fragment.value=operation==='delete'?value.fragment||'':drafts.get(key)??value.fragment??'';
        form.elements.fragment.readOnly=operation==='delete'; feedback.textContent='';ready=true;form.dataset.configReady='true';
        form.querySelector('button[type=submit]').disabled=!execute;
        feedback.textContent='';
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
  function outbound(inbound, operation) {
    return preparedDialog(ctx, `${inbound.tag} · 出站`, ['出站类型', '名称', '出站配置', '绑定目标'], async local => {
      if (dirty()) throw new Error('配置源码有未保存修改，请先保存，再操作出站。');
      if (!await latest(local) || !local.current()) return;
      const binding = outboundBinding(inbound);
      const chosen = list(current.managed_objects).find(item => !item.inbound && item.tag === binding);
      if (['modify', 'delete'].includes(operation) && !chosen) throw new Error('当前入站尚未绑定可编辑出站');
      await outboundObjectLoaded(local, ['modify', 'delete'].includes(operation) ? chosen : null, operation, inbound, true);
    });
  }
  function outboundBinding(inbound) {
    if(current.outbound_bindings)return current.outbound_bindings[inbound.tag]||'';
    return engine==='mihomo'?'':inboundOutbound(current.config.content,engine,inbound);
  }
  function outboundObject(object, operation = 'modify', inbound, loaded = false) {
    return preparedDialog(ctx, operation === 'add' ? '添加出站' : operation === 'bind' ? '绑定已有出站' : operation === 'delete' ? '删除出站' : `${object.tag} · 出站`, ['出站类型', '名称', '出站配置', '绑定目标'], local => outboundObjectLoaded(local, object, operation, inbound, loaded));
  }
  async function outboundObjectLoaded(ctx,object,operation='modify',inbound,loaded=false) {
    const box=ctx.dialog(operation==='add'?'添加出站':operation==='bind'?'绑定已有出站':operation==='delete'?'删除出站':`${object.tag} · 出站`,loadingForm(['出站类型', '名称', '出站配置', '绑定目标']),{wide:true});
    let preset;
    box.addEventListener('close',()=>preset?.dispose(),{once:true});
    try {
      if(!loaded&&!await latest(ctx)||!ctx.current()||!box.isConnected||!current.config&&operation!=='add')return;
      const config=current.config||{version:0}, selecting=operation==='bind', deleting=operation==='delete';
      const sections=engine==='mihomo'?[['proxies','代理'],['proxy-groups','代理组']]:[['outbounds','出站']];
      const sectionName=object?.section||sections[0][0];
      let initialObject, content=engine==='mihomo'?`name: ${inbound?`exit-${inbound.port}`:'new-exit'}\ntype: direct\n`:JSON.stringify({tag:inbound?`exit-${inbound.port}`:'new-exit',[engine==='xray'?'protocol':'type']:engine==='xray'?'freedom':'direct'},null,2);
      if(object&&!selecting){
        const selected=await ctx.api(`${base}/outbounds/${pathID(object.section)}/${pathID(object.tag)}${current.snapshot_task_id?`?snapshot=${pathID(current.snapshot_task_id)}`:''}`);
        if(!ctx.current()||!box.isConnected)return;
        if(selected.version!==config.version)throw new Error('配置已变化，请重新读取');
        content=selected.fragment; initialObject=selected.object?JSON.parse(selected.object):undefined;
      }
      const entries=list(current.managed_objects).filter(item=>!item.inbound&&item.enabled&&!['dns','blackhole','block'].includes(item.kind));
      const form=ctx.form([
        inbound&&muted(`${agent.name} · ${engine} · ${agent.shared_allocation?'当前共享实例':'主实例'} · 入站 ${inbound.tag} · ${inbound.port}`),
        selecting?field('已有出站','tag',outboundBinding(inbound),{options:[['','请选择出站'],...entries.map(item=>[item.tag,item.tag])],required:true}):deleting?stack(muted(`删除 ${object.tag}？`),replacementField(object)):field('配置区域','section',sectionName,{options:sections,disabled:Boolean(object)}),
        group('出站配置',field(engine==='mihomo'?'出站 YAML':'出站 JSON','fragment',readableConfigSource(content,engine),{multiline:12,spellcheck:false,readonly:selecting||deleting}), selecting||deleting||sectionName==='proxy-groups'),
        object&&operation==='modify'&&muted('保存会更新所有引用此出站的入站。'),intents(),
      ],'保存并部署',async values=>{
        const raw=!selecting&&!deleting&&activePreset()?await preset.verifiedContent():values.fragment||'';
        if(!selecting&&!deleting&&engine!=='mihomo'){try{JSON.parse(raw);}catch(error){throw new Error('出站 JSON 格式无效，请检查后重试。',{cause:error});}}
        if(object&&operation==='modify'&&!await ctx.confirm(`更新“${object.tag}”及其所有引用？`,'更新出站'))return;
        if(!await preparing(values.intent))return;
        const result=await write(`${base}/outbounds`,{version:config.version,snapshot_task_id:current.snapshot_task_id,intent:values.intent,section:object?.section||values.section||sectionName,tag:selecting?values.tag:object?.tag||'',fragment:deleting||selecting?'':raw,preset_base:operation==='modify'&&activePreset()?preset.baseline():'',replacement_tag:values.replacement_tag||'',operation,
          ...(['add','bind'].includes(operation)&&inbound?{inbound_tag:inbound.tag,inbound_port:inbound.port}:{})},box);
        await committed(result,box);
      });
      form.elements.fragment.setAttribute('aria-label',engine==='mihomo'?'出站配置 YAML':'出站配置 JSON');
      bindJSONEditor(form.elements.fragment, engine==='mihomo'?'YAML':'JSON', ctx.signal);
      if(deleting)form.querySelector('button[type=submit]').classList.add('danger');
      const activePreset=()=>preset&&(form.elements.section?.value||sectionName)!=='proxy-groups';
      box.setContent(form);track(form,box);
      if(selecting){
        let epoch=0;
        const preview=async()=>{
          const tag=form.elements.tag.value, selected=entries.find(item=>item.tag===tag), request=++epoch;
          form.elements.fragment.value='';if(!selected)return;
          try {
            const result=await ctx.api(`${base}/outbounds/${pathID(selected.section)}/${pathID(tag)}${current.snapshot_task_id?`?snapshot=${pathID(current.snapshot_task_id)}`:''}`);
            if(!ctx.current()||!box.isConnected||request!==epoch)return;
            if(result.version!==config.version)throw new Error('配置已变化，请重新读取');
            form.elements.fragment.value=result.fragment;
          }catch(error){if(ctx.current()&&box.isConnected&&request===epoch)ctx.announce(error.message,'error');}
        };
        form.elements.tag.addEventListener('change',()=>void preview());void preview();
      }else if(!deleting){
        const host=outboundPresetFields(engine);host.dataset.outboundPresetHost='';
        preset=bindOutboundPresetController({form:form.querySelector('fieldset'),input:form.elements.fragment,engine,agentId:agent.id,
          api:ctx.api,canReadPeers:ctx.can('client-access.read'),active:()=>ctx.current()&&box.isConnected,
          initialMode:sectionName==='proxy-groups'?'json':object?'auto':'preset',initialObject,onChange:()=>form.dispatchEvent(new Event('input',{bubbles:true})),host});
        const drafts=new Map([['proxy-groups','name: new-group\ntype: select\nproxies: [DIRECT]\n']]);let previous=sectionName;
        const choose=()=>{
          drafts.set(previous,form.elements.fragment.value);previous=form.elements.section.value;
          host.hidden=previous==='proxy-groups';
          if(previous==='proxy-groups')form.elements.fragment.closest('details').open=true;
          form.elements.fragment.value=drafts.get(previous)||'';
          form.elements.fragment.readOnly=previous==='proxy-groups'?false:host.querySelector('[data-outbound-mode]').value!=='json';
          if(previous!=='proxy-groups')preset.refresh();
        };
        form.elements.section?.addEventListener('change',choose);
        if(sectionName==='proxy-groups')host.hidden=true;
      }
    }catch(error){if(ctx.current()&&box.isConnected&&error.name!=='AbortError')throw error;}
  }
  function removeInbound(target) {
    return preparedDialog(ctx, '删除入站', ['入站', '监听端口'], local => removeInboundLoaded(local, target));
  }
  async function removeInboundLoaded(ctx, target) {
    if (!await latest(ctx) || !current.config) return;
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
  function replacementField(object) {
    const choices=list(current.managed_objects).filter(item=>!item.inbound&&item.enabled&&item.tag!==object.tag);
    return field('替代出站','replacement_tag','',{options:[['','保留现有引用；被引用时提示调整'],...choices.map(item=>[item.tag,item.tag])]});
  }
  function draw() {
    deployment.resetPorts();
    const inbounds=list(current.inbounds),targets=list(current.inbound_targets);
    const managed=list(current.managed_objects);
    const toggle=object=>button(object.enabled?'停用':'启用',()=>preparedDialog(ctx, object.enabled?'停用配置对象':'启用配置对象', ['配置对象', '替代出站'], async local=>{
      if(!await latest(local))return;
      const fresh=list(current.managed_objects).find(item=>item.section===object.section&&item.tag===object.tag);
      if(!fresh||fresh.enabled!==object.enabled)throw new Error('配置状态已变化，请重新选择');
      const disabled=list(current.management?.disabled).filter(item=>item.section!==object.section||item.tag!==object.tag);
      const expectedVersion=current.config.version;
      if(object.enabled)disabled.push({section:object.section,tag:object.tag});
      const form=ctx.form([muted(`${object.enabled?'停用':'启用'} ${object.tag}`),object.enabled&&!object.inbound&&replacementField(object),intents()], '保存并部署',async values=>{
        if(!await preparing(values.intent))return;
        const result=await write(`${base}/management`,{version:expectedVersion,disabled,intent:values.intent,outbound_tag:object.tag,replacement_tag:values.replacement_tag||''},box,'PUT');
        await committed(result,box);
      });
      const box=local.dialog(object.enabled?'停用配置对象':'启用配置对象',form); track(form, box);
    }));
    const controls=writable&&button('添加入站',()=>inboundEdit(null,'add'),{tone:'primary'});
    const status=deployment.element;
    const rows=targets.map(target=>{
      const input=inbounds.find(input=>input.tag===target.tag&&input.port===target.port);
      const object=managed.find(item=>item.inbound&&item.tag===target.tag);
      return [target.port||'无固定端口',stack(target.tag,muted([object?.listen||input?.listen,object?.network].filter(Boolean).join(' · '))),target.kind,
        deployment.inboundState(target.port,object?.enabled),actions(
        ctx.can('agent-config.read')&&button('访问限制',()=>restrictions(target)),
        writable&&object&&toggle(object),
        writable&&button('修改',()=>input?inboundEdit(input):inboundSource(target)),
        ctx.can('client-access.read')&&input&&button('复制入站连接',event=>void ctx.mutate(event.currentTarget,()=>copyInbound(input))),
        writable&&button('删除',()=>removeInbound(target),{tone:'danger'}),
        writable&&['xray','sing-box','mihomo'].includes(engine)&&button('出站',()=>{
          let binding='';try{binding=outboundBinding(target);}catch(error){ctx.announce(error.message,'error');return;}
          const chooser=ctx.dialog(`${target.tag} · 出站操作`,actions(['add','bind','modify','delete'].map((operation,index)=>button(['添加并绑定','绑定已有出站','修改绑定出站','删除绑定出站'][index],()=>{chooser.close();void outbound(target,operation);},{disabled:['modify','delete'].includes(operation)&&!binding}))));
        }),
        writable&&engine==='ss-rust'&&button('端口配置项',()=>fieldChooser(target)),
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
    surface.replaceChildren(...[receipt,inboundSection,
      ['xray','sing-box','mihomo'].includes(engine)&&section('出站配置',table(['名称','类型','目标状态','操作'],outboundObjects.map(object=>[object.tag,object.kind||object.section,badge(object.enabled?'启用':'停用'),writable&&actions(toggle(object),button('修改',()=>outboundObject(object)),button('删除',()=>outboundObject(object,'delete'),{tone:'danger'}))]),{label:'出站配置'}),writable&&button('添加出站',()=>outboundObject(null,'add'))),
    ].filter(Boolean));
    sourceTools?.replaceChildren(...(current.config?[
      button('展开源码',files),
      writable&&button('通用配置项',()=>fieldChooser()),
    ].filter(Boolean):[]));
  }
  function fieldChooser(inbound) {
    return commonEditor(inbound);
  }
  draw();return surface;
}
