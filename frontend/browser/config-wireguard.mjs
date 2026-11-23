import { assert, pause, waitFor, createConfigFixture } from './config-fixture.mjs';
const editor=()=>[...document.querySelectorAll('dialog[open] form')].at(-1);
const open=()=>waitFor(()=>editor()?.elements.wireguard_preshared_key&&!document.querySelector('[aria-busy=true]'),'WireGuard editor did not load');
function layout(form,engine){
  form.querySelectorAll('details').forEach(section=>section.open=true);
  const dialog=form.closest('dialog'), bounds=dialog.getBoundingClientRect();
  assert(dialog.scrollWidth<=dialog.clientWidth+1,`${engine}: WireGuard dialog overflows`);
  for(const name of ['wireguard_server_private_key','wireguard_server_public_key','wireguard_client_private_key','wireguard_client_public_key','wireguard_preshared_key']){
    const field=form.elements[name],group=field.closest('.a-field');
    const generationField=form.elements[name.replace('_public_key','_private_key')].closest('.a-field');
    assert(field.checkVisibility()&&generationField.querySelector('button[aria-label^="生成 WireGuard "]'),`${engine}: ${name} control is unavailable`);
    assert(group.scrollWidth<=group.clientWidth+1,`${engine}: ${name} actions overflow`);
    const rect=field.getBoundingClientRect();assert(rect.left>=bounds.left&&rect.right<=bounds.right,`${engine}: ${name} exceeds dialog`);
  }
  for(const label of ['生成 WireGuard 服务端密钥对','生成 WireGuard 客户端密钥对','生成 WireGuard 预共享密钥'])
    assert(form.querySelectorAll(`[aria-label="${label}"]`).length===1,`${engine}: duplicated or missing ${label}`);
}
export async function testWireGuardConfigRuntime(){
  for(const engine of ['xray','sing-box']){
    const test=await createConfigFixture(engine,{protocol:'wireguard'}),source=document.querySelector('.a-main [name=content]'),complete=source.value;
    const section=engine==='sing-box'?'endpoints':'inbounds';assert(JSON.parse(complete)[section].some(item=>item.tag==='second'),'complete source lost endpoint identity');
    test.select('second');test.click('modify');await open();let form=editor();layout(form,engine);
    assert(source.value===complete,'opening endpoint replaced source');assert(form.elements.wireguard_keepalive.value==='0','disabled keepalive became 25');
    assert(form.elements.wireguard_server_private_key.type==='password'&&form.elements.wireguard_client_private_key.type==='password','private key exposed by default');
    assert(!form.elements.wireguard_server_public_key.readOnly&&!form.elements.wireguard_client_public_key.readOnly,'custom keys have no matching public input');
    assert(Boolean(form.elements.wireguard_server_address)===(engine==='sing-box'),'Xray exposes ignored address');assert(form.elements.listen.readOnly===(engine==='sing-box'),'endpoint offers unsupported listen address');assert(!form.elements.wireguard_preshared_key.required,'optional PSK mandatory');
    const client=form.elements.wireguard_client_private_key.value,port=form.elements.port.value,server=form.elements.wireguard_server_private_key.value;
    form.elements.wireguard_server_private_key.value='edited';form.elements.wireguard_server_public_key.value='edited';
    form.elements.wireguard_server_private_key.closest('.a-field').querySelector('[aria-label="生成 WireGuard 服务端密钥对"]').click();
    await waitFor(()=>form.elements.wireguard_server_private_key.value===server,'selective server key generation failed');
    assert(form.elements.wireguard_server_public_key.value!=='edited'&&form.elements.wireguard_client_private_key.value===client&&form.elements.port.value===port,'server key generation altered client or port');
    const add = () => [...form.querySelectorAll('button')].find(control => control.textContent === '添加客户端').click();
    add();
    await waitFor(()=>form.elements.wg_0_address, 'new client missing');
    assert(form.elements.wg_0_address.value === '10.66.66.3/32' && form.elements.wg_0_private_key.type === 'password' && form.elements.wg_0_private_key.value !== client, 'new client identity or masking is incorrect');
    const extra = form.elements.wg_0_private_key.value;
    form.elements.wg_0_address.value = '10.66.66.8/32';
    form.elements.tag.value='wg-renamed';test.confirm=true;test.submit('deploy');await test.settle(1);
    assert(test.writes[0].original_tag==='second'&&test.writes[0].input.wireguard_keepalive===0&&test.writes[0].input.wireguard_client_private_key===client,'endpoint save lost metadata');
    const peer = test.writes[0].input.wireguard_additional_clients[0];
    assert(peer.private_key === extra && peer.address === '10.66.66.8/32' && test.writes[0].input.port === Number(port), 'client edit changed the listener or lost the device');
    test.select('wg-renamed');
    const clipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    let copies = 0;
    Object.defineProperty(navigator, 'clipboard', {configurable:true, value:{writeText:async () => { copies++; }}});
    try {
      test.click('复制入站连接');
      await waitFor(()=>editor()?.elements.peer && !document.querySelector('[aria-busy=true]'), 'per-device copy selector missing');
      const picker = editor().elements.peer;
      assert(picker.options.length === 2 && picker.options[1].textContent === peer.address, 'copy selector lost device address');
      picker.value = peer.public_key;
      editor().requestSubmit();
      await waitFor(()=>copies === 1 && !test.topDialog(), 'per-device copy did not finish');
      const request = test.calls.findLast(call=>call.path.includes('/client-access?'));
      assert(new URLSearchParams(request.path.split('?')[1]).get('wireguard_client') === peer.public_key, 'copy requested another device');
    } finally {
      if (clipboard) Object.defineProperty(navigator, 'clipboard', clipboard); else delete navigator.clipboard;
    }
    test.click('modify');await open();form=editor();
    assert(form.elements.wg_0_private_key.value === extra && form.elements.wg_0_address.value === peer.address, 'saved peer did not reopen');
    [...form.querySelectorAll('button')].find(control=>control.textContent === '移除客户端').click();
    assert(!form.elements.wg_0_address && form.elements.wireguard_client_private_key.value === client, 'removing a client changed the primary');
    test.submit('deploy');await test.settle(2);
    assert(test.writes[1].input.wireguard_additional_clients.length === 0, 'removal did not submit an explicit empty list');
    test.select('wg-renamed');test.click('delete');await waitFor(()=>editor()?.elements.intent,'endpoint delete missing');test.submit('deploy');await test.settle(3);assert(test.writes[2].original_tag==='wg-renamed','endpoint deletion changed another target');
    test.click('add');await waitFor(()=>editor()?.elements.protocol,'protocol picker did not load');form=editor();assert(![...form.elements.protocol.options].some(option=>['tailscale','openvpn-server'].includes(option.value)),'incomplete endpoint lifecycle offered');form.elements.protocol.value='wireguard';form.elements.protocol.dispatchEvent(new Event('change',{bubbles:true}));await open();form=editor();layout(form,engine);
    test.submit('deploy');await test.settle(4);assert(test.writes[3].operation==='add'&&test.writes[3].input.protocol==='wireguard','new endpoint wrong protocol');test.dispose();
  }
}
