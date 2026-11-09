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
    form.elements.tag.value='wg-renamed';test.confirm=true;test.submit('deploy');await test.settle(1);
    assert(test.writes[0].original_tag==='second'&&test.writes[0].input.wireguard_keepalive===0&&test.writes[0].input.wireguard_client_private_key===client,'endpoint save lost metadata');
    test.select('wg-renamed');test.click('delete');await waitFor(()=>editor()?.elements.intent,'endpoint delete missing');test.submit('deploy');await test.settle(2);assert(test.writes[1].original_tag==='wg-renamed','endpoint deletion changed another target');
    test.click('add');await waitFor(()=>editor()?.elements.protocol,'protocol picker did not load');form=editor();assert(![...form.elements.protocol.options].some(option=>['tailscale','openvpn-server'].includes(option.value)),'incomplete endpoint lifecycle offered');form.elements.protocol.value='wireguard';form.elements.protocol.dispatchEvent(new Event('change',{bubbles:true}));await open();form=editor();layout(form,engine);
    test.submit('deploy');await test.settle(3);assert(test.writes[2].operation==='add'&&test.writes[2].input.protocol==='wireguard','new endpoint wrong protocol');test.dispose();
  }
}
