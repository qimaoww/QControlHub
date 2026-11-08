import { assert, waitFor } from "./config-fixture.mjs";
import { createPresetEditor } from "./preset-controls.mjs";
import { readServerPlanInput } from "../modules/inbound-options-model.js";

const certificate = {id:"crt_fixture",name:"原生 TLS 测试证书",domains:["proxy.example.test"],source:"import",owned:true,version:1,not_after:"2099-01-01T00:00:00Z"};
export async function testManagedTLSRuntime() {
  await testNativeFieldRegeneration();
  await testXHTTPTLSRuntime();
  for (const shared of [false,true]) {
    const test = await createPresetEditor("sing-box", {protocol:"vless-ws-tls", shared, certificates:[certificate]});
    const {form,control,edit,open,save}=test;
    try {
      await open("add");
      assert(control("tls_mode").value === "caddy" && control("tls_public_port").value === "443", "domain-only TLS defaults changed");
      assert(control("tls_mode").closest("details").open && form().querySelectorAll('[name=tls_mode]').length === 1,"TLS selector is hidden or duplicated");
      edit("tls_server_name","proxy.example.test");
      const previousPort=control("port").value;
      control("port").closest(".a-field").querySelector("button").click();
      await waitFor(()=>control("port").value !== previousPort && !document.querySelector('#analytics-config-test[aria-busy=true]'),"backend port regeneration stalled");
      assert(control("tls_server_name").value === "proxy.example.test" && form().textContent.includes(`127.0.0.1:${control("port").value}`),"regeneration lost domain or route preview");
      const backendPort=control("port").value;
      let saved=await save("deploy");
      assert(saved.tls_mode === "caddy" && saved.listen === "127.0.0.1" && saved.tls_public_port === 443 && String(saved.port) === backendPort && !saved.certificate_path && !saved.private_key_path,"Caddy payload retained native TLS or wrong backend");
      assert(control("tls_server_name").value === "proxy.example.test" && control("transport_path").value === saved.transport_path && control("credential").value === saved.credential,"Caddy settings lost after reopening");
      edit("tls_mode","nginx"); edit("tls_acme_email","certs@example.test");
      assert(!control("tls_acme_email").disabled && form().textContent.includes("TCP 80") && control("listen").value === "127.0.0.1","Nginx automatic certificate guidance or route missing");
      saved=await save("deploy");
      assert(saved.tls_mode === "nginx" && saved.tls_public_port === 443 && saved.listen === "127.0.0.1" && saved.tls_acme_email === "certs@example.test" && !saved.certificate_path && !saved.private_key_path,"Nginx retained Caddy/native settings");
      assert(control("tls_mode").value === "nginx" && control("tls_server_name").value === "proxy.example.test" && control("transport_path").value === saved.transport_path && control("credential").value === saved.credential,"Nginx settings lost after reopening");
      edit("tls_mode","caddy");
      assert(!control("tls_acme_email").disabled,"Caddy ACME field did not reactivate");
      saved=await save("deploy");
      assert(saved.tls_mode === "caddy" && control("tls_mode").value === "caddy" && !saved.tls_client_certificate,"Caddy retained Nginx trust material");
      edit("tls_mode","nginx"); await save("deploy");
      edit("tls_client_certificate","generated-nginx-trust"); edit("tls_mode","external");
      assert(control("tls_client_certificate").value === "" && form().textContent.includes("自行配置公网 TLS 反代"),"external TLS retained Nginx trust material");
      saved=await save("deploy");
      assert(saved.tls_mode === "external" && saved.listen === "127.0.0.1" && !saved.tls_client_certificate && !saved.certificate_path && !saved.private_key_path,"external TLS retained certificate paths");
      edit("tls_mode","native"); edit("certificate_path","/etc/qcontrolhub/tls/server.crt"); edit("private_key_path","/etc/qcontrolhub/tls/server.key");
      assert(!control("certificate_path").disabled && control("listen").value === "0.0.0.0" && !control("listen").readOnly,"native TLS did not restore listener/certificate editing");
      saved=await save("deploy");
      assert(saved.tls_mode === "native" && saved.tls_public_port === 0 && saved.listen === "0.0.0.0" && saved.certificate_path.endsWith("server.crt") && saved.private_key_path.endsWith("server.key") && !saved.tls_client_certificate,"native TLS retained managed frontend");
      await waitFor(()=>control("panel_certificate_id")?.options.length > 1,"certificate catalog did not load");
      edit("panel_certificate_id","crt_fixture");
      assert(control("tls_mode").value === "native" && !control("panel_certificate_id").disabled && control("certificate_path").readOnly,"native panel certificate cannot be selected");
      assert(readServerPlanInput(form(),{key:"vless-ws-tls",requires_tls:true,supports_managed_tls:true}).panel_certificate_id === "crt_fixture","panel certificate binding lost");
      edit("tls_mode","nginx");
      assert(control("panel_certificate_id").disabled && control("panel_certificate_id").closest(".a-field").hidden,"panel certificate remains active for Nginx");
      saved=await save("deploy");
      assert(saved.tls_mode === "nginx" && !saved.panel_certificate_id && !saved.certificate_path && !saved.private_key_path,"native certificate overrode Nginx selection");
      let release;
      test.planGate=new Promise(resolve=>{release=resolve;});
      const before=test.calls.filter(call=>call.path.endsWith('/plans')).length, credential=control('credential').value, writes=test.writes.length;
      test.clickButton('重新生成凭据',form());
      await waitFor(()=>test.calls.filter(call=>call.path.endsWith('/plans')).length>before,'credential regeneration did not start');
      edit('tls_server_name','draft.example.test');
      release();
      await waitFor(()=>!test.root.hasAttribute('aria-busy'),'credential regeneration did not finish');
      assert(control('tls_server_name').value==='draft.example.test' && control('credential').value===credential && test.writes.length===writes,'late generation replaced a live draft or saved it');
      assert(document.body.textContent.includes('未应用过期生成结果'),'discarded generation did not explain its outcome');
      test.planGate=null;
      for (const all of [false,true]) {
        let release;test.planGate=new Promise(resolve=>{release=resolve;});
        const domain=control('tls_server_name').value,port=control('port').value,credential=control('credential').value;
        const start=test.calls.filter(call=>call.path.endsWith('/plans')).length;
        (all ? [...form().querySelectorAll('button')].find(button=>button.textContent==='重新生成凭据') : control('port').closest('.a-field').querySelector('[aria-label="生成监听端口"]')).click();
        await waitFor(()=>test.calls.filter(call=>call.path.endsWith('/plans')).length>start,'local regeneration did not start');
        edit('tls_server_name','temporary.example.test');edit('tls_server_name',domain);release();
        await waitFor(()=>!test.root.hasAttribute('aria-busy'),'edit-and-restore generation did not release lock');
        assert(control('port').value===port&&control('credential').value===credential&&control('tls_server_name').value===domain,'generation ignored a newer edit-and-restore revision');
        test.planGate=null;
      }
    } finally { test.dispose(); }
  }
  // A delayed list must not prevent opening or change a user's chosen TLS mode.
  const test=await createPresetEditor("sing-box",{protocol:"vless-ws-tls",certificates:[certificate],plan:{tls_mode:"native",listen:"0.0.0.0",tls_server_name:"proxy.example.test",panel_certificate_id:certificate.id,certificate_path:"/etc/qcontrolhub/panel-certificates/crt_fixture/cert.pem",private_key_path:"/etc/qcontrolhub/panel-certificates/crt_fixture/key.pem"}});
  let release;
  test.certificateGate=new Promise(resolve=>{release=resolve;});
  try {
    await test.open();
    test.edit("tls_mode","nginx");release();
    await waitFor(()=>test.control("panel_certificate_id").options.length>1 && !test.form().textContent.includes("正在加载证书…"),"delayed certificate catalog did not load");
    assert(test.control("tls_mode").value === "nginx" && test.control("panel_certificate_id").disabled,"delayed certificate response changed selected TLS mode");
    const saved=await test.save();
    assert(saved.tls_mode === "nginx" && !saved.panel_certificate_id,"delayed certificate binding leaked into proxy payload");
  } finally { release();test.dispose(); }
}

async function testNativeFieldRegeneration() {
  for (const shared of [false, true]) {
    const test = await createPresetEditor('sing-box', { protocol: 'vless-ws-tls', shared, certificates: [certificate] });
    const { control, form, edit } = test;
    const plans = () => test.calls.filter(call => call.path.endsWith('/plans'));
    const fieldButton = name => control(name).closest('.a-field').querySelector('[aria-label^="生成"]');
    try {
      await test.open('add');
      edit('tls_server_name', 'unsaved.example.test'); edit('tls_mode', 'native'); edit('listen', '127.0.0.1');
      edit('certificate_path', '/unsaved/certificate.pem'); edit('private_key_path', '/unsaved/private-key.pem');
      edit('tag', 'unsaved-tag'); edit('port', '24443');
      const credential = control('credential').value;
      const target = control('port'), before = plans().length;
      fieldButton('port').click();
      await waitFor(() => plans().length === before + 1 && !test.root.hasAttribute('aria-busy'), 'native field generation did not finish');
      const request = test.planRequests.at(-1), payload = request.input;
      assert(request.path === '/agents/node/configs/sing-box/plans' && payload.protocol === 'vless-ws-tls', 'generation changed selected node or core');
      assert(payload.input.listen === '127.0.0.1' && payload.input.certificate_path === '/unsaved/certificate.pem' && payload.input.tls_server_name === 'unsaved.example.test', 'generation lost current unsaved inputs');
      assert(control('port') === target && target.value !== '24443' && control('credential').value === credential && control('tag').value === 'unsaved-tag', 'field generation replaced the form or unrelated credentials');
      assert(control('tls_server_name').value === 'unsaved.example.test' && control('listen').value === '127.0.0.1', 'field generation discarded routing choices');
      const port = target.value;
      test.planError = true; const failed = plans().length;
      fieldButton('credential').click();
      await waitFor(() => plans().length === failed + 1 && !test.root.hasAttribute('aria-busy'), 'failed generation did not unlock');
      assert(control('credential').value === credential && target.value === port && document.body.textContent.includes('协议方案读取失败'), 'failed generation lost source or hid error');
      test.planError = false;
      let release; test.planGate = new Promise(resolve => { release = resolve; });
      const count = plans().length;
      fieldButton('credential').click();
      await waitFor(() => plans().length === count + 1 && test.root.hasAttribute('aria-busy'), 'generation did not enter pending state');
      fieldButton('port').click(); form().requestSubmit();
      assert(plans().length === count + 1 && !test.writes.length, 'pending generation allowed another generate or save');
      release(); test.planGate = null;
      await waitFor(() => !test.root.hasAttribute('aria-busy'), 'generation did not release global lock');
      assert(target.value === port && control('tag').value === 'unsaved-tag', 'credential generation changed tag or port');
      let releaseLate; test.planGate = new Promise(resolve => { releaseLate = resolve; });
      const lateCount = plans().length, savedCredential = control('credential').value;
      fieldButton('credential').click();
      await waitFor(() => plans().length === lateCount + 1, 'late generation did not start');
      window.dispatchEvent(new Event('pagehide')); releaseLate(); test.planGate = null;
      await new Promise(resolve => setTimeout(resolve, 30));
      assert(control('credential').value === savedCredential && !test.writes.length, 'retired account/page applied generation or wrote configuration');
    } finally { test.dispose(); }
  }
}

async function testXHTTPTLSRuntime() {
  for (const engine of ['xray','mihomo']) for (const shared of [false,true]) {
    const test = await createPresetEditor(engine,{protocol:'vless-xhttp-tls',shared,certificates:[certificate]});
    const {control,edit,form}=test;
    try {
      await test.open('add');
      assert(control('tls_mode').value==='caddy' && control('tls_server_name').value==='' && control('transport').value==='xhttp','XHTTP defaults were replaced by legacy TLS/Reality');
      assert([...control('tls_mode').options].map(option=>option.value).join(',')==='native,caddy,nginx','XHTTP mode list differs from supported modes');
      edit('tls_server_name','proxy.example.test');
      const html='<!doctype html>\n<title>静态页面</title>\n<p>'+ '长页面内容'.repeat(200)+'</p>';
      edit('tls_static_page','custom');edit('tls_static_html',html);
      for (const key of ['cleanfolio','minimalio','uisual-line']) {
        edit('tls_static_page',key);
        const saved=await test.save();
        assert(saved.tls_static_html.includes('<html') && control('tls_static_page').value===key && control('tls_static_html').disabled,'bundled static page lost API/save binding');
      }
      const bundledHTML=control('tls_static_html').value;
      edit('tls_static_page','custom');
      assert(control('tls_static_html').value===bundledHTML && bundledHTML.includes('<html'),'customizing a bundled page lost its HTML');
      edit('tls_static_page','custom');edit('tls_static_html',html);
      const credential=control('credential').value,path=control('transport_path').value;
      for (const mode of ['caddy','nginx','native','nginx','caddy']) {
        edit('tls_mode',mode);
        const managed=mode!=='native';
        assert(control('tls_static_page').disabled===!managed && control('certificate_path').disabled===managed,'hidden XHTTP fields still active');
        if (!managed) {edit('certificate_path','/etc/qcontrolhub/tls/test.crt');edit('private_key_path','/etc/qcontrolhub/tls/test.key');}
        else {edit('tls_static_page','custom');edit('tls_static_html',html);}
        const saved=await test.save();
        assert(saved.tls_mode===mode && saved.credential===credential && saved.transport_path===path && saved.transport==='xhttp','XHTTP mode switch changed transport identity');
        assert(managed ? saved.tls_static_html===html && !saved.certificate_path && !saved.private_key_path : !saved.tls_static_html && !saved.tls_public_port,'XHTTP static/native fields leaked across modes');
        assert(control('tls_mode').value===mode && control('tls_server_name').value==='proxy.example.test','XHTTP reopen applied defaults over the chosen mode');
      }
      edit('tls_mode','native');edit('port','443');
      edit('tls_mode','caddy');
      await waitFor(()=>control('port').value!=='443' && !test.root.hasAttribute('aria-busy'),'native public port was not replaced by a generated backend');
      assert(control('credential').value===credential && control('transport_path').value===path,'backend handoff generation changed credentials');
      assert(form().scrollWidth<=form().clientWidth+1,'XHTTP editor content overflow');
    } finally {test.dispose();}
  }
}
