import assert from 'node:assert/strict';

// Uses the existing native Chromium driver and real password/CSRF/PostgreSQL fixture.
export async function runAPIManagementBrowser({ base, mode, createPage, login, call, evaluate, wait, screenshot, api }) {
  const page = await createPage();
  const identity = await login(page, 'browser-admin');
  await call('Page.navigate', { url: `${base}/#settings-api-keys` }, page);
  await wait(page, "document.querySelector('.a-top h1')?.textContent==='API' && [...document.querySelectorAll('.a-main button')].some(b=>b.textContent==='创建 Key'&&!b.disabled)");
  assert.equal(await evaluate(page, "document.querySelectorAll('#panel-settings-form').length"), 0);
  assert.ok(await evaluate(page, "!!document.querySelector('.a-nav-link[href=\"#api\"]')"));
  await screenshot(page, 'api-empty');
  await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(b=>b.textContent==='创建 Key').click()");
  await wait(page, "!!document.querySelector('dialog[open] form')");
  await evaluate(page, `(() => {const form=document.querySelector('dialog[open] form'); form.elements.name.value='集成测试 API 管理'; form.elements.mode.value='read_write'; form.elements.mode.dispatchEvent(new Event('change',{bubbles:true}));form.elements.ip_allowlist.value='127.0.0.1\\n2001:db8:1234::/48';for(const control of form.elements)if(control.name?.startsWith('permission:'))control.checked=(control.name.endsWith('.read')&&control.name!=='permission:secrets.read')||['permission:overview.read','permission:configs.write'].includes(control.name);})()`);
  await screenshot(page, 'api-create');
  await evaluate(page, "document.querySelector('dialog[open] form').requestSubmit()");
  await wait(page, "!!document.querySelector('dialog[open] [data-sensitive]')");
  await evaluate(page, "window.__managementKey=document.querySelector('dialog[open] [data-sensitive]').textContent;document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await wait(page, "!document.querySelector('dialog[open]') && document.querySelector('.a-main tbody')?.textContent.includes('集成测试 API 管理')");
  const result = await evaluate(page, `(async()=>{
    const key=window.__managementKey;
    const invoke=async(method,path,body)=>{const response=await fetch('/api/v1'+path,{method,headers:{Authorization:'Bearer '+key,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});return {status:response.status,body:await response.json()};};
    const own=await invoke('GET','/api-key');
    const created=await invoke('POST','/configs',{name:'浏览器 API 档案',engine:'mihomo',content:'mixed-port: 21451\\nsecret: isolated-test-secret\\n'});
    const saved=await invoke('PUT','/configs/'+created.body.id,{...created.body,name:'浏览器 API 已保存',content:created.body.content.replace('21451','21452')});
    const listing=await invoke('GET','/configs');
    const denied=await invoke('POST','/users',{});
    window.__managementConfig=saved.body.id;
    return {mode:own.body.mode,created:created.status,saved:saved.status,denied:denied.status,readback:listing.body.some(c=>c.name==='浏览器 API 已保存'&&c.content.includes('21452')&&!c.content.includes('isolated-test-secret'))};
  })()`);
  assert.deepEqual(result, { mode: 'read_write', created: 201, saved: 200, denied: 403, readback: true });
  await evaluate(page, "document.querySelectorAll('.a-toast-close').forEach(b=>b.click())");
  await screenshot(page, 'api-ready');
  const keyList = await api(page, identity, 'GET', '/settings/api-keys');
  assert.equal(keyList.items.length, 1);
  const permissionCount = keyList.items[0].permissions.length;
  assert.ok(permissionCount > 15, 'scope layout fixture lacks a large permission set');
  assert.equal(await evaluate(page, "document.querySelector('.a-main tbody tr').cells[3].textContent"), `${permissionCount} 项权限2 个来源`);
  const viewports = mode === 'desktop' ? [[1920,1080],[3840,2160],[1280,900],[820,900]] : [[390,844],[844,390]];
  for (const [width,height] of viewports) {
    await call('Emulation.setDeviceMetricsOverride', { width,height,deviceScaleFactor:1,mobile:mode !== 'desktop' }, page);
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"), `API keys overflow ${width}`);
    assert.ok(await evaluate(page, "document.querySelector('.a-main tbody tr').cells[3].getBoundingClientRect().height<130"), `scope stretches key row ${width}`);
    assert.ok(await evaluate(page, "(()=>{const cell=document.querySelector('.a-main tbody tr').cells[2];return cell.querySelectorAll('.a-badge').length===1&&cell.querySelector('.a-badge').getBoundingClientRect().width<70&&cell.querySelector('.a-muted').textContent==='读写';})()"), `key status pill stretches or duplicates mode ${width}`);
    if (mode !== 'desktop') await evaluate(page, "document.querySelector('.a-main tbody tr').cells[3].scrollIntoView({block:'nearest',inline:'center'})");
    await screenshot(page, `api-keys-${width}`);
    await evaluate(page, "window.__scopeTrigger=document.querySelector('.a-main tbody tr').cells[3].querySelector('button');window.__scopeTrigger.focus();window.__scopeTrigger.click()");
    await wait(page, "document.querySelector('dialog[open] h2')?.textContent==='授权范围'");
    assert.equal(await evaluate(page, "document.querySelector('dialog[open] .a-card-body .a-actions').children.length"), permissionCount);
    assert.ok(await evaluate(page, "document.querySelector('dialog[open]').textContent.includes('127.0.0.1')&&document.querySelector('dialog[open]').textContent.includes('2001:db8:1234::/48')"));
    assert.ok(await evaluate(page, "[...document.querySelectorAll('dialog[open],dialog[open] .a-card')].every(e=>e.scrollWidth<=e.clientWidth+1)"), `scope dialog overflows ${width}`);
    await screenshot(page, `api-scope-${width}`);
    await call('Input.dispatchKeyEvent', {type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27}, page);
    await call('Input.dispatchKeyEvent', {type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27}, page);
    await wait(page, "!document.querySelector('dialog[open]')");
    assert.ok(await evaluate(page, "document.activeElement===window.__scopeTrigger"), 'scope dialog lost keyboard focus');
  }
  await call('Emulation.setDeviceMetricsOverride', { width:mode === 'desktop'?1440:390,height:mode === 'desktop'?1000:844,deviceScaleFactor:1,mobile:mode !== 'desktop' }, page);
  await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(b=>b.textContent==='编辑授权').click()");
  await wait(page, "!!document.querySelector('dialog[open] form')");
  assert.equal(await evaluate(page, "document.querySelector('dialog[open] form').elements.mode.value"), 'read_write');
  await screenshot(page, 'api-edit');
  await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await call('Page.navigate', { url: `${base}/#api-docs` }, page);
  await wait(page, "document.querySelector('.a-top h1')?.textContent==='API' && document.querySelectorAll('[data-operation]').length>100");
  assert.equal(await evaluate(page, `document.querySelectorAll('.a-nav-link[href="#api-docs"]').length`), 0);
  assert.equal(await evaluate(page, `document.querySelector('.a-nav-link[href="#api"]').getAttribute("aria-current")`), 'page');
  await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(b=>b.textContent==='密钥').click()");
  assert.ok(await evaluate(page, "[...document.querySelectorAll('.a-main button')].some(b=>b.textContent==='创建 Key')"));
  await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(b=>b.textContent==='接口规范').click()");
  await screenshot(page, 'api-docs-list');
  await evaluate(page, "document.querySelector('[name=api-filter]').value='/configs';document.querySelector('[name=api-filter]').dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('.a-main details').open=true");
  await screenshot(page, 'api-docs');
  assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"), 'API docs overflow');
  if (mode === 'desktop') {
    for (const [width,height] of [[1920,1080],[3840,2160],[1280,900],[820,900]]) {
      await call('Emulation.setDeviceMetricsOverride', { width,height,deviceScaleFactor:1,mobile:false }, page);
      await screenshot(page, `api-docs-${width}`);
      assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"), `API docs overflow ${width}`);
    }
    await call('Emulation.setDeviceMetricsOverride', { width:1440,height:1000,deviceScaleFactor:1,mobile:false }, page);
  }
  await api(page, identity, 'DELETE', `/settings/api-keys/${keyList.items[0].id}`);
  assert.equal(await evaluate(page, "fetch('/api/v1/configs',{headers:{Authorization:'Bearer '+window.__managementKey}}).then(r=>r.status)"), 401);
  await evaluate(page, "delete window.__managementKey;delete window.__managementConfig;delete window.__scopeTrigger");
}
