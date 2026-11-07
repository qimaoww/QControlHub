import assert from 'node:assert/strict';

export async function checkNodeAddress({ page, fixture, evaluate, wait, screenshot, api, identity, record, mode, call }) {
  const id = fixture.analyticsAgent;
  await evaluate(page, `location.hash='#settings-node-${id}'`);
  await wait(page, `!!document.querySelector('[data-node-address="${id}"]') && !document.querySelector('[aria-busy=true]')`);
  const set = async (mode, address = '') => {
    await evaluate(page, `(() => {const form=document.querySelector('[data-node-address="${id}"]');form.elements.node_address_mode.value=${JSON.stringify(mode)};form.elements.node_address_mode.dispatchEvent(new Event('change',{bubbles:true}));form.elements.node_address.value=${JSON.stringify(address)};form.elements.node_address.dispatchEvent(new Event('input',{bubbles:true}));form.requestSubmit();})()`);
    await wait(page, "document.querySelectorAll('dialog[open]').length===2");
    await evaluate(page, "[...document.querySelectorAll('dialog[open]')].at(-1).querySelector('.a-dialog-body .a-actions button:last-child').click()");
    await wait(page, "document.querySelectorAll('dialog[open]').length===1 && !document.querySelector('[aria-busy=true]')");
  };
  await set('manual', 'manual.example.test');
  let agent = (await api(page, identity, 'GET', '/agents')).find(agent => agent.id === id);
  assert.equal(agent.labels.client_address, 'manual.example.test');
  const entries = await api(page, identity, 'GET', '/client-access');
  assert.ok(entries.some(entry => entry.agent_id === id && entry.profiles.some(profile => profile.address === 'manual.example.test')));
  for (const [width, height] of [[1920,1080],[3840,2160],[1440,1000],[960,800],[390,844],[844,390],[768,1024],[1024,768]]) {
    await call('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 1100 }, page);
    await evaluate(page, 'new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
    assert.ok(await evaluate(page, `(() => {const form=document.querySelector('[data-node-address="${id}"]');return form.scrollWidth<=form.clientWidth+1&&document.documentElement.scrollWidth<=innerWidth+1})()`), `${width}x${height}: address form overflow`);
  }
  await call('Emulation.setDeviceMetricsOverride', { width: mode === 'mobile' ? 390 : 1920, height: mode === 'mobile' ? 844 : 1080, deviceScaleFactor: 1, mobile: mode === 'mobile' }, page);
  const capture = async label => {
    await evaluate(page, `(() => {document.querySelectorAll('.a-toast [aria-label=关闭通知]').forEach(button=>button.click());const heading=document.querySelector('[data-node-address="${id}"]').previousElementSibling;const box=heading.closest('dialog');box.scrollTop+=heading.getBoundingClientRect().top-box.getBoundingClientRect().top-100;})()`);
    await wait(page, "!document.querySelector('.a-toast,.a-motion-tail')");
    await screenshot(page, label);
  };
  await capture('node-address-manual');
  await evaluate(page, "document.querySelector('dialog[open]').close();location.hash='#traffic'");
  await wait(page, "!document.querySelector('[aria-busy=true]') && document.querySelector('.a-top h1')?.textContent==='流量'");
  await evaluate(page, `location.hash='#settings-node-${id}'`);
  await wait(page, `document.querySelector('[data-node-address="${id}"] [name=node_address]')?.value==='manual.example.test'`);
  await set('auto');
  agent = (await api(page, identity, 'GET', '/agents')).find(agent => agent.id === id);
  assert.ok(!agent.labels.client_address && !agent.labels.public_host && !agent.labels.public_ip);
  assert.ok(await evaluate(page, `document.querySelector('[data-node-address="${id}"]').textContent.includes('自动获取')`));
  await capture('node-address-auto');
  await evaluate(page, "document.querySelector('dialog[open]').close()");
  await wait(page, "!document.querySelector('dialog[open],.a-motion-tail')");
  await call('Emulation.setDeviceMetricsOverride', { width: mode === 'mobile' ? 390 : 1440, height: mode === 'mobile' ? 844 : 1000, deviceScaleFactor: 1, mobile: mode === 'mobile' }, page);
  record('node address: real API manual save, exported client inheritance, route remount and automatic reset passed');
}
