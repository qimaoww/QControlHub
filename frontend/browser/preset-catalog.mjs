import { assert, waitFor } from "./config-fixture.mjs";
import { createPresetEditor } from "./preset-controls.mjs";

// Round-trip the backend-generated catalog through the actual native editor.
export async function testPresetCatalogRuntime(catalog) {
  const covered=new Set();
  const cases=[...catalog,...catalog.filter(entry=>entry.engine==='xray'&&entry.plan.transport==='xhttp').map(entry=>({...entry,shared:true}))];
  for(const actual of cases) {
    const test=await createPresetEditor(actual.engine,{protocol:actual.protocol.key,shared:actual.shared});
    const {form,control,edit,open,save}=test;
    covered.add(actual.engine);
    try {
      await open('add');
      if(actual.protocol.supports_managed_tls) {
        assert(control('tls_mode').value==='caddy'&&control('tls_public_port').value==='443','managed TLS defaults lost');
        assert(control('listen').value==='127.0.0.1'&&control('listen').readOnly,'managed backend is not loopback');
        assert(control('tls_server_name').required&&!control('tls_server_name').checkValidity(),'managed domain is not required');
        assert(!control('tls_acme_email').disabled&&control('tls_acme_email').type==='email','Caddy ACME email is not editable');
        edit('tls_server_name','tls.example.test');
        assert(form().checkValidity()&&form().textContent.includes(`tls.example.test:443${control('transport_path').value}`),'domain-only Caddy flow or route is invalid');
        edit('tls_mode','native');
        assert(control('tls_mode').closest('details').open&&!control('certificate_path').disabled&&control('listen').value==='0.0.0.0','native TLS fields were not revealed');
        edit('tls_mode','caddy');
        assert(control('listen').value==='127.0.0.1'&&control('certificate_path').disabled,'Caddy retained native fields');
        edit('tls_acme_email','certs@example.test');edit('tls_public_port','8443');
        const proxyMode=actual.plan.transport==='http'?'external':'nginx',automatic=actual.protocol.key==='vless-ws-tls';
        edit('tls_mode',proxyMode);
        assert(control('tls_acme_email').disabled===(!automatic||proxyMode==='external'),'ACME email does not follow selected TLS provider');
        assert(control('certificate_path').disabled&&control('private_key_path').disabled,'proxy certificate paths remain enabled');
        assert(form().textContent.includes(proxyMode==='external'?'自行配置':automatic?'TCP 80':'自签证书'),'provider guidance does not follow selected TLS mode');
        const version=test.saved().version;
        test.portConflict='tls_public_port';test.submit('deploy');
        await waitFor(()=>!test.root.hasAttribute('aria-busy')&&form().textContent.includes('草稿已保留'),'public port conflict blocked retry');
        assert(test.saved().version===version&&test.writes.length===0&&control('tls_mode').value===proxyMode&&control('tls_public_port').value==='8443'&&control('tls_server_name').value==='tls.example.test','public port conflict lost draft or changed saved version');
        assert(document.activeElement===control('tls_public_port'),'HTTPS port conflict did not focus its field');
        test.portConflict='';
      }
      if(actual.engine==='sing-box') {
        if(control('transport_host')&&!control('transport_host').disabled)edit('transport_host','http.example.test');
        if(actual.plan.transport==='http')edit('transport_http_method','POST');
        if(actual.plan.tls_enabled) {
          edit('tls_server_name','tls.example.test');
          edit('tls_alpn',actual.plan.transport==='http'?'h2':['quic'].includes(actual.plan.transport)||['tuic','hysteria2'].includes(actual.protocol.key)?'h3':'http/1.1');
        }
      }
      const crossCoreWS=['xray','mihomo'].includes(actual.engine)&&['vmess','trojan'].includes(actual.protocol.key);
      if(crossCoreWS) {
        edit('transport','websocket');edit('transport_path','/custom-ws');edit('tls_server_name','tls.example.test');edit('transport_host','host.example.test');
        if(actual.engine==='xray')edit('tls_alpn','http/1.1');
        assert(control('transport_host').labels.length&&control('tls_server_name').labels.length,'WS routing fields need labels');
      }
      const saved=await save();
      assert(saved.protocol===actual.protocol.key,'catalog submitted a different protocol');
      if(crossCoreWS) {
        assert(saved.transport==='websocket'&&saved.transport_path==='/custom-ws'&&saved.tls_server_name==='tls.example.test'&&saved.transport_host==='host.example.test','WS save conflated independent routing');
        assert(control('tls_server_name').value===saved.tls_server_name&&control('transport_host').value===saved.transport_host,'WS reopen lost SNI/Host');
        if(actual.engine==='xray')assert(control('tls_alpn').value==='http/1.1','Xray WS lost ALPN');
        const host=control('transport_host');host.closest('details').open=true;host.scrollIntoView({block:'center',behavior:'instant'});
        const rect=host.getBoundingClientRect();assert(document.elementFromPoint(rect.x+rect.width/2,rect.y+rect.height/2)===host,'save controls obstruct WS Host');
        edit('transport','grpc');assert(control('transport_host').disabled,'gRPC retained WS Host');
        edit('transport','websocket');assert(!control('transport_host').disabled&&control('transport_host').value===saved.transport_host,'transport switch erased WS Host draft');
      }
      if(actual.engine==='sing-box') {
        assert(saved.transport===actual.plan.transport,'native transport changed during save');
        if(actual.protocol.supports_managed_tls)assert(saved.tls_mode===(actual.plan.transport==='http'?'external':'nginx')&&saved.tls_acme_email===(actual.protocol.key==='vless-ws-tls'?'certs@example.test':'')&&saved.tls_public_port===8443&&saved.listen==='127.0.0.1'&&control('tls_public_port').value==='8443','managed TLS lost provider/public port/email on reopen');
        if(actual.plan.tls_enabled)assert(saved.tls_server_name==='tls.example.test'&&control('tls_server_name').value===saved.tls_server_name&&control('tls_alpn').value===saved.tls_alpn,'native TLS values lost on reopen');
        if(control('transport_host')&&!control('transport_host').disabled)assert(saved.transport_host==='http.example.test'&&control('transport_host').value===saved.transport_host,'HTTP Host was conflated with TLS SNI');
        if(saved.transport==='http')assert(saved.transport_http_method==='POST'&&control('transport_http_method').value==='POST','HTTP Method lost on reopen');
        if(['raw','quic'].includes(saved.transport))assert(saved.transport_path===''&&saved.transport_host==='','irrelevant transport fields survived');
        if(actual.protocol.key==='vless-http2-reality')assert(saved.flow===''&&saved.reality_enabled,'H2 Reality carried Vision flow');
      }
      if(actual.engine==='xray'&&actual.plan.transport==='xhttp') {
        assert(!control('transport_host').disabled,'XHTTP Host disabled by HTTP-only visibility');
        edit('client_address','2001:db8::10');edit('client_port','8443');edit('client_host','http.example.test');edit('xhttp_mode','packet-up');
        const group=['vless-xhttp-five','vless-xhttp-tls-reality','vless-xhttp-reality-tls'].includes(actual.protocol.key);
        if(group) {edit('xhttp_tls_address','cdn.example.test');edit('xhttp_tls_client_port','443');edit('xhttp_tls_host','tls.example.test');}
        else {
          const tls=actual.protocol.key==='vless-xhttp-tls';
          const extra={xmux:{maxConnections:0,maxConcurrency:'4-8'},downloadSettings:{address:'203.0.113.20',port:443,network:'xhttp',security:tls?'tls':'reality',sockopt:{tcpKeepAliveIdle:25},
            [tls?'tlsSettings':'realitySettings']:{serverName:tls?control('tls_server_name').value:control('reality_server_name').value},xhttpSettings:{path:control('transport_path').value,host:'download.example.test',mode:'auto',extra:{xmux:{maxConnections:0,maxConcurrency:8}}}}};
          edit('xhttp_extra',JSON.stringify(extra));
          assert(control('xhttp_split').checked&&control('xhttp_download_address').value==='203.0.113.20','pasted downloadSettings did not hydrate controls');
          [...form().querySelectorAll('button')].find(button=>button.textContent==='交换上传与下载地址').click();
          assert(control('client_address').value==='203.0.113.20'&&control('xhttp_download_address').value==='2001:db8::10','IPv6 exchange failed');
        }
        const changed=await save();
        assert(changed.xhttp_mode==='packet-up'&&control('xhttp_mode').value==='packet-up'&&changed.client_host==='http.example.test','XHTTP client values lost on reopen');
        if(group)assert(control('xhttp_tls_address').value==='cdn.example.test'&&changed.xhttp_tls_client_port===443&&changed.xhttp_tls_host==='tls.example.test'&&changed.certificate_path,`group ${actual.protocol.key}: TLS=${control('xhttp_tls_address').value}, port=${changed.xhttp_tls_client_port}, host=${changed.xhttp_tls_host}, certificate=${Boolean(changed.certificate_path)}`);
        else {
          const download=JSON.parse(changed.xhttp_extra).downloadSettings;
          assert(download.sockopt.tcpKeepAliveIdle===25&&download.xhttpSettings.extra.xmux.maxConcurrency===8&&download.address==='2001:db8::10','raw nested download parameters lost');
          edit('xhttp_split',false);const unsplit=await save();
          assert(!JSON.parse(unsplit.xhttp_extra).downloadSettings,'explicit split disable retained old download');
        }
      }
    } finally {test.dispose();}
  }
  assert(covered.size===4&&catalog.length>=33,'not all backend engine presets were covered');
}
