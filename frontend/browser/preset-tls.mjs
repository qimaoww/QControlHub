import { assert, waitFor } from "./config-fixture.mjs";
import { createPresetEditor } from "./preset-controls.mjs";
import { readServerPlanInput } from "../modules/inbound-options-model.js";

const certificate = {id:"crt_fixture",name:"原生 TLS 测试证书",domains:["proxy.example.test"],source:"import",owned:true,version:1,not_after:"2099-01-01T00:00:00Z"};
export async function testManagedTLSRuntime() {
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
