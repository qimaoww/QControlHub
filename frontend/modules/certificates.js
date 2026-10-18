import { bindDialogBackdrop } from "./popup.js";
import { createCertificateView } from "./certificate-view.js";
import { certificateForm, certificateAccountForm } from "./certificate-forms.js";

export function installCertificates(ctx) {
 const {api:requestAPI,state,esc,shell,notify,confirmAction}=ctx;
 // The shared transport accepts Fetch BodyInit, as do the existing pages.
 const api=(path,options={})=>requestAPI(path,{...options,...(options.body!==undefined?{body:JSON.stringify(options.body)}:{})});
 const view=createCertificateView(ctx);
 let items=[],accounts=[],agents=[],httpGrants=[],httpNodes=[],managedSources=[],dialog=null,accountData=null;
 let certificateDraft=null,lastPageMarkup="",dialogReturnFocus=null;
 let pageRead=0,modalRead=0;
 const filters={search:"",source:"",expiry:"",status:""};
 const current=()=>state.route==="certificates"&&state.data===accountData;
 function close(preserveDraft=false){modalRead++;if(!preserveDraft)certificateDraft=null;const previous=dialog;dialog=null;previous?.close();previous?.remove();if(!preserveDraft){dialogReturnFocus?.focus?.({preventScroll:true});dialogReturnFocus=null;}}
 function modalContent(html){const content=document.createElement("div");content.innerHTML=html;const heading=content.querySelector("h2");const title=heading?.textContent||"证书";heading?.remove();return{title,body:content.innerHTML,editor:Boolean(content.querySelector(":scope > form[data-cert-form], :scope > form[data-cert-account-form]"))};}
 function modal(html,saved={}) {
  const trigger=dialog?dialogReturnFocus:document.activeElement;
  close(true);dialogReturnFocus=trigger;
  const content=modalContent(html);
  const editor=content.editor;
  dialog=document.createElement("dialog");
  dialog.className=editor?"traffic-edit-dialog user-allocation-dialog cert-dialog":"config-inbound-dialog config-common-dialog config-outbound-dialog cert-dialog";
  dialog.setAttribute("aria-label",content.title);
  dialog.innerHTML=`<header ${editor?"":'class="config-inbound-heading"'}><div${editor?"":' class="notice-message"'}><h2>${esc(content.title)}</h2></div><button class="${editor?"deploy-command-close":"config-access-close"}" type="button" data-cert-close aria-label="关闭证书弹窗">×</button></header>${editor?content.body:`<div class="config-inbound-body" data-cert-modal-content>${content.body}</div>`}`;
  document.body.append(dialog);bindDialogBackdrop(dialog,()=>close());dialog.showModal();
  const opened=dialog;trapFocus(opened);
  opened.addEventListener("close",()=>{opened.remove();if(dialog===opened){dialog=null;certificateDraft=null;dialogReturnFocus?.focus?.({preventScroll:true});dialogReturnFocus=null;}});
  restore(dialog.querySelector("form"),saved);bind(dialog);
 }


 function trapFocus(opened){opened.addEventListener("keydown",event=>{if(event.key!=="Tab"||event.defaultPrevented)return;const controls=[...opened.querySelectorAll('button,input:not([type="hidden"]),select,textarea,a[href],summary,[tabindex]')].filter(e=>!e.disabled&&e.tabIndex>=0&&e.getClientRects().length&&!e.closest('[inert]'));const first=controls[0],last=controls.at(-1);if(!first){event.preventDefault();return}if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}})}

 async function resources(request){
  const data=state.data;
  const result=await Promise.all([api("/certificate-accounts"),api("/certificate-http-grants"),api("/certificate-http-nodes")]);
  if(data!==state.data||request!==modalRead||!current())return false;
  [accounts,httpGrants,httpNodes]=result;
  agents=httpNodes.map(node=>({id:node.id,name:node.name}));
  return true;
 }
 async function detail(id){const session=state.data,request=++modalRead;const data=await api(`/certificates/${id}`);if(request===modalRead&&session===state.data&&current()){modal(view.detail(data));dialog.dataset.certDetailId=id;}}
 async function share(id){const session=state.data,request=++modalRead;const data=await api(`/certificates/${id}`);if(request===modalRead&&session===state.data&&current())modal(view.sharing(data.certificate,data.grants,data.bindings));}
 async function create(source="acme",method="dns-api",replaceID="",saved={}){const request=++modalRead;if(!await resources(request))return;modal(certificateForm({esc,accounts,agents,httpGrants,httpNodes,source,method,replaceID}),saved);}
 function restore(form,saved){for(const [name,value]of Object.entries(saved)){const input=form?.elements.namedItem(name);if(!input||input.type==="file")continue;if(input.type==="checkbox")input.checked=value===true;else input.value=value;}}
 function read(form){const value=Object.fromEntries(new FormData(form));for(const input of form.querySelectorAll('[type="checkbox"]'))value[input.name]=input.checked;return value;}
 function accountForm(kind="acme",provider="cloudflare",ca="letsencrypt-staging",saved={}){modal(certificateAccountForm(esc,kind,provider,ca),saved);}
 async function accountsPage(){
  const request=++modalRead;if(!await resources(request))return;
  modal(`<h2>账号管理</h2><div class="settings-form">${certificateDraft?'<div class="user-allocation-tools"><button class="button small" data-cert-return-draft>返回证书申请</button></div>':""}
   ${view.panel("签发与 DNS 账号",accounts.map(a=>`<div class="agent-share-recipient"><div class="agent-share-recipient-head"><div class="user-allocation-node"><strong>${esc(a.name)}</strong><span class="agent-share-usage">${esc(a.kind==="acme"?`${a.ca} · ${a.email}`:a.provider)}</span></div><button class="button small danger-button" data-cert-delete-account="${a.id}">删除</button></div>${a.kind==="dns"?`<div class="user-allocation-tools"><button class="button small" data-cert-update-account="${a.id}">更新凭据</button><button class="button small" data-cert-check-account="${a.id}">连接检查</button></div>`:""}</div>`).join("")||'<p class="settings-hint">尚无账号</p>','<button class="button primary small" data-cert-new-account>新增 ACME 账号</button><button class="button small" data-cert-new-dns>新增 DNS 账号</button>')}
   ${view.panel("共享节点 HTTP-01 授权",`<p class="settings-hint">也可在<a href="#node-settings">节点共享设置</a>中配置允许域名。</p><form class="settings-form" data-cert-http-grant><div class="agent-share-fields"><label class="settings-field"><span>验证节点</span><select name="agent_id" required><option value="">选择节点</option>${agents.filter(a=>httpNodes.some(n=>n.id===a.id&&n.owned)).map(a=>`<option value="${esc(a.id)}">${esc(a.name)}</option>`).join("")}</select></label><label class="settings-field"><span>接收用户的账号名</span><input name="username" required autocomplete="off"></label></div><label class="settings-field"><span>域名</span><input name="domain" required placeholder="auth.example.com"></label><div class="user-allocation-tools"><button class="button primary small">添加授权</button></div><p class="alert error notice-message" data-cert-form-error role="alert" hidden></p></form><div>${httpGrants.map((g,index)=>`<div class="agent-share-recipient"><div class="agent-share-recipient-head"><div class="user-allocation-node"><strong>${esc(g.domain)}</strong><span class="agent-share-usage">${esc(agents.find(a=>a.id===g.agent_id)?.name||"节点不可用")} · ${esc(g.username||"共享用户")}</span></div>${agents.some(a=>a.id===g.agent_id&&httpNodes.some(n=>n.id===a.id&&n.owned))?`<button class="button small danger-button" data-cert-http-revoke="${index}">撤销</button>`:""}</div></div>`).join("")}</div>`)}
  </div>`);
 }
 async function sourcePage(){
  const session=state.data,request=++modalRead;const sources=await api("/certificate-managed-sources");if(request!==modalRead||session!==state.data||!current())return;managedSources=sources;
  modal(`<h2>Caddy 自动同步</h2><div class="settings-form"><p class="settings-hint">Caddy 自动续期，面板同步新版本。</p>${managedSources.map(s=>view.panel(esc(s.domain),view.terms([
   ["来源节点",esc(s.agent_name)], ["内核",esc(s.engine)], ["状态",view.badge(s.status)], ["最近成功同步",s.last_synced?esc(ctx.date(s.last_synced)):"尚未同步"]
  ])+(s.error?`<p class="alert error notice-message" role="status">${esc(s.error)}</p>`:"")+`<div class="user-allocation-tools">${s.certificate_id?`<button class="button small" data-cert-detail="${esc(s.certificate_id)}">查看证书</button>`:""}${s.status!=="deleted"?`<button class="button small" data-cert-source-action="${s.status==="paused"||s.status==="detached"?"resume":"pause"}" data-source="${esc(s.id)}">${s.status==="paused"||s.status==="detached"?"恢复同步":"暂停同步"}</button><button class="button small danger-button" data-cert-source-action="detach" data-source="${esc(s.id)}">解除来源关联</button>`:'<span class="settings-hint">已停止自动导入</span>'}</div>`)).join("")||'<p class="user-share-empty">暂无 Caddy 来源，部署后自动同步。</p>'}<div class="user-allocation-tools"><button class="button small" data-cert-sources>重新检查状态</button></div></div>`);
 }
 async function submit(form,work){const button=form.querySelector('button:not([type="button"])');const error=form.querySelector("[data-cert-form-error]");if(button?.disabled)return;form.setAttribute("aria-busy","true");if(button)button.disabled=true;if(error){error.textContent="";error.hidden=true;}try{await work();if(!current())return;notify("操作已完成");}catch(e){if(error&&form.isConnected){error.textContent=e.message;error.hidden=false;error.tabIndex=-1;error.focus();}else if(current())notify(e.message,"error");}finally{if(button)button.disabled=false;form.removeAttribute("aria-busy");}}
 function bind(root){
  root.querySelectorAll("[data-cert-close]").forEach(b=>b.onclick=()=>close());
  root.querySelectorAll("[data-cert-sources]").forEach(b=>b.onclick=()=>sourcePage().catch(e=>notify(e.message,"error")));
  root.querySelectorAll("[data-cert-source-action]").forEach(b=>b.onclick=async()=>{const action=b.dataset.certSourceAction;if(action==="detach"&&!await confirmAction("停止自动导入和版本同步？现有证书和绑定保留，可恢复同步。","解除来源关联"))return;b.disabled=true;try{await api(`/certificate-managed-sources/${b.dataset.source}`,{method:"PUT",body:{action}});await sourcePage()}catch(e){notify(e.message,"error");b.disabled=false}});
  root.querySelectorAll("[data-cert-create]").forEach(b=>b.onclick=()=>create().catch(e=>notify(e.message,"error")));
  root.querySelectorAll("[data-cert-accounts],[data-cert-open-accounts]").forEach(b=>b.onclick=()=>{if(b.hasAttribute("data-cert-open-accounts"))certificateDraft=read(b.closest("form"));accountsPage().catch(e=>notify(e.message,"error"))});
  root.querySelectorAll("[data-cert-return-draft]").forEach(b=>b.onclick=()=>create(certificateDraft.source,certificateDraft.method,"",certificateDraft));
  root.querySelectorAll("[data-cert-detail],[data-cert-reload]").forEach(b=>b.onclick=()=>detail(b.dataset.certDetail||b.dataset.certReload).catch(e=>notify(e.message,"error")));
  root.querySelectorAll("[data-cert-share]").forEach(b=>b.onclick=()=>share(b.dataset.certShare).catch(e=>notify(e.message,"error")));
  root.querySelectorAll("[data-cert-copy]").forEach(b=>b.onclick=async()=>{try{await navigator.clipboard.writeText(b.dataset.certCopy);notify("已复制")}catch{notify("复制失败，请手动选择文本","error")}});
  root.querySelectorAll("[data-cert-replace]").forEach(b=>b.onclick=()=>create("import","dns-api",b.dataset.certReplace));
  root.querySelectorAll("[data-cert-refresh]").forEach(b=>b.onclick=()=>page().catch(e=>notify(e.message,"error")));
  root.querySelectorAll("[data-cert-new-account]").forEach(b=>b.onclick=()=>accountForm());
  root.querySelectorAll("[data-cert-new-dns]").forEach(b=>b.onclick=()=>accountForm("dns"));
  for(const [attribute,action]of [["certRenew","renew"],["certAuto","auto-renew"],["certDelete",""]])root.querySelectorAll(`[data-${attribute.replace(/[A-Z]/g,c=>"-"+c.toLowerCase())}]`).forEach(b=>b.onclick=async()=>{if(action===""&&!await confirmAction("删除此证书及其版本？","删除证书",{title:"删除证书？"}))return;b.disabled=true;try{const id=b.dataset[attribute];await api(`/certificates/${id}${action?"/"+action:""}`,{method:action===""?"DELETE":action==="auto-renew"?"PUT":"POST",...(action==="auto-renew"?{body:{enabled:b.dataset.enabled==="true"}}:{})});close();await page();if(action==="renew")await detail(id);}catch(e){notify(e.message,"error");b.disabled=false;}});
  root.querySelectorAll("[data-cert-continue],[data-cert-cancel]").forEach(b=>b.onclick=async()=>{b.disabled=true;try{const id=b.dataset.certContinue||b.dataset.certCancel;await api(`/certificates/${id}/orders/${b.dataset.order}${b.dataset.certContinue?"/continue":""}`,{method:b.dataset.certContinue?"POST":"DELETE"});await detail(id);}catch(e){notify(e.message,"error");b.disabled=false;}});
  root.querySelectorAll("[data-cert-revoke]").forEach(b=>b.onclick=async()=>{if(!await confirmAction("撤销后停止分发和同步，现有部署保留。","撤销授权",{title:"撤销使用授权？"}))return;try{await api(`/certificates/${b.dataset.certRevoke}/grants`,{method:"DELETE",body:{username:b.dataset.username}});await share(b.dataset.certRevoke);}catch(e){notify(e.message,"error")}});
  const grant=root.querySelector("[data-cert-grant-form]");if(grant)grant.onsubmit=e=>{e.preventDefault();submit(grant,async()=>{await api(`/certificates/${grant.dataset.certGrantForm}/grants`,{method:"POST",body:read(grant)});await share(grant.dataset.certGrantForm)})};
  root.querySelectorAll("[data-cert-update-account]").forEach(b=>b.onclick=()=>{const a=accounts.find(a=>a.id===b.dataset.certUpdateAccount);accountForm("dns",a.provider,"",{name:a.name});const f=dialog.querySelector("form");f.dataset.update=a.id;f.elements.kind.disabled=true;f.elements.provider.disabled=true;dialog.querySelector("h2").textContent="更新 DNS 凭据";});
  root.querySelectorAll("[data-cert-check-account]").forEach(b=>b.onclick=async()=>{b.disabled=true;try{await api(`/certificate-accounts/${b.dataset.certCheckAccount}/check`,{method:"POST"});notify("DNS 读取连接正常")}catch(e){notify(e.message,"error")}finally{b.disabled=false}});
  root.querySelectorAll("[data-cert-delete-account]").forEach(b=>b.onclick=async()=>{try{await api(`/certificate-accounts/${b.dataset.certDeleteAccount}`,{method:"DELETE"});await accountsPage()}catch(e){notify(e.message,"error")}});
  root.querySelectorAll("[data-cert-http-revoke]").forEach(b=>b.onclick=async()=>{try{await api("/certificate-http-grants",{method:"DELETE",body:httpGrants[Number(b.dataset.certHttpRevoke)]});await accountsPage()}catch(e){notify(e.message,"error")}});
  const httpForm=root.querySelector("[data-cert-http-grant]");if(httpForm)httpForm.onsubmit=e=>{e.preventDefault();submit(httpForm,async()=>{await api("/certificate-http-grants",{method:"POST",body:read(httpForm)});await accountsPage()})};
  const form=root.querySelector("[data-cert-form]");if(form){
   for(const control of form.querySelectorAll("[data-cert-form-source],[data-cert-form-method]"))control.onchange=()=>{const data={...(certificateDraft||{}),...read(form)};certificateDraft=data;create(data.source,data.method||"dns-api","",data)};
   const nodes=form.querySelector('[name="agent_id"]');if(nodes){const update=()=>{const node=httpNodes.find(n=>n.id===nodes.value);const text=form.querySelector('[data-cert-node-scope]');text.textContent=node?(node.owned?"自有节点":"允许域名："+node.domains.join(" · ")):""};nodes.onchange=update;update();}
   const methodControl=form.elements.namedItem("method");const domainsControl=form.elements.namedItem("domains");if(methodControl?.value==="http-01"&&domainsControl){const validate=()=>domainsControl.setCustomValidity(domainsControl.value.split(/[\n,，]+/).some(v=>v.trim().startsWith("*."))?"HTTP-01 不支持泛域名，请选择 DNS API 或手动 DNS。":"");domainsControl.addEventListener("input",validate);validate();}
   form.querySelectorAll("[data-cert-pick-file]").forEach(button=>button.onclick=()=>form.querySelector(`[data-cert-file="${button.dataset.certPickFile}"]`).click());
   form.querySelectorAll("[data-cert-file]").forEach(input=>input.onchange=async()=>{const file=input.files[0];if(!file)return;if(file.size>256*1024){const error=form.querySelector("[data-cert-form-error]");error.hidden=false;error.textContent="文件不得超过 256 KiB";return};form.elements.namedItem(input.dataset.certFile).value=await file.text();form.querySelector(`[data-cert-file-name="${input.dataset.certFile}"]`).textContent=file.name;});
   form.onsubmit=e=>{e.preventDefault();submit(form,async()=>{const data=read(form);const payload=form.dataset.replace?{certificate:data.certificate,private_key:data.private_key}:{name:data.name,source:data.source,...(data.source==="import"?{certificate:data.certificate,private_key:data.private_key}:{domains:data.domains.split(/[\n,，]+/).map(v=>v.trim()).filter(Boolean),key_type:data.key_type,...(data.source==="self-signed"?{days:Number(data.days)}:{method:data.method,account_id:data.account_id,dns_account_id:data.dns_account_id||"",agent_id:data.agent_id||"",auto_renew:Boolean(data.auto_renew)})})};const cert=await api(form.dataset.replace?`/certificates/${form.dataset.replace}/replace`:"/certificates",{method:"POST",body:payload});close();await page();await detail(cert.id)})};
  }
  const account=root.querySelector("[data-cert-account-form]");if(account){for(const control of account.querySelectorAll("[data-cert-account-kind],[data-cert-account-provider],[data-cert-account-ca]"))control.onchange=()=>{const data=read(account);accountForm(data.kind,data.provider||"cloudflare",data.ca||"letsencrypt-staging",data)};account.onsubmit=e=>{e.preventDefault();submit(account,async()=>{const data=read(account);await api(account.dataset.update?`/certificate-accounts/${account.dataset.update}/credentials`:"/certificate-accounts",{method:account.dataset.update?"PUT":"POST",body:{name:data.name,kind:data.kind,...(data.kind==="acme"?{ca:data.ca,email:data.email,eab_id:data.eab_id||"",eab_key:data.eab_key||"",accept_terms:data.accept_terms}:{provider:data.provider,credentials:{zone:data.zone,...Object.fromEntries(["token","access_key_id","access_key_secret","secret_id","secret_key"].filter(k=>data[k]).map(k=>[k,data[k]]))}})}});await accountsPage()})}}
 }
 async function page(silent=false){const request=++pageRead;accountData=state.data;const session=state.data;const data=await api("/certificates");if(request!==pageRead||session!==state.data||!current())return;items=data.items;const markup=view.page(items,filters,data.encryption_ready);if(silent&&markup===lastPageMarkup)return;const focused=document.activeElement;const key=["search","source","expiry","status"].find(k=>focused?.hasAttribute?.(`data-cert-${k}`));const start=focused?.selectionStart,end=focused?.selectionEnd,scroll=window.scrollY;lastPageMarkup=markup;shell(markup,"证书中心");bind(document.querySelector(".workspace-main"));for(const [attr,key]of [["search","search"],["source","source"],["expiry","expiry"],["status","status"]]){const control=document.querySelector(`[data-cert-${attr}]`);if(control)control.onchange=()=>{filters[key]=control.value;page().catch(e=>notify(e.message,"error"))}}if(key){const control=document.querySelector(`[data-cert-${key}]`);control?.focus({preventScroll:true});if(key==="search"&&start!==null)control?.setSelectionRange(start,end)}window.scrollTo({top:scroll,behavior:"instant"})}

 // Poll only the read view. Account/navigation invalidation also closes any
 // secret form and releases its in-memory input values.
 let poll;
 const dispose=()=>{pageRead++;clearInterval(poll);close();items=[];accounts=[];agents=[];httpGrants=[];httpNodes=[];managedSources=[];certificateDraft=null;lastPageMarkup="";accountData=null;Object.assign(filters,{search:"",source:"",expiry:"",status:""});};
 const load=async()=>{
  dispose();
  const session=state.data,signal=state.routeSignal;
  signal?.addEventListener("abort",dispose,{once:true});
  await page();
  if(signal?.aborted||session!==state.data||!current())return;
  poll=setInterval(async()=>{if(session!==state.data||!current()){dispose();return}if(document.hidden||dialog?.querySelector("form"))return;try{const request=modalRead,id=dialog?.dataset.certDetailId;if(id){const data=await api(`/certificates/${id}`);if(request===modalRead&&session===state.data&&current()&&dialog?.dataset.certDetailId===id){const content=dialog.querySelector("[data-cert-modal-content]");const scroll=content.scrollTop;const expanded=[...content.querySelectorAll("details")].map(d=>d.open);const rendered=modalContent(view.detail(data));dialog.querySelector("header h2").textContent=rendered.title;if(content.innerHTML===rendered.body)return;const focused=document.activeElement;const focusKey=focused?.getAttribute("data-cert-reload")||focused?.getAttribute("data-cert-copy");content.innerHTML=rendered.body;if(focusKey)[...content.querySelectorAll("button")].find(b=>b.dataset.certReload===focusKey||b.dataset.certCopy===focusKey)?.focus({preventScroll:true});content.querySelectorAll("details").forEach((d,i)=>d.open=expanded[i]||false);bind(content);content.scrollTop=scroll;}}else if(!dialog)await page(true)}catch{/* Keep the existing view; explicit refresh reports errors. */}},4000);
 };
 return Object.assign(load,{dispose});

}
