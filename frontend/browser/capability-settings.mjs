import { testSettingsAPIKeysRuntime } from './settings-api-keys.mjs';
import { assert, delay, waitFor } from './assertions.mjs';

export async function testCapabilitySettingsRuntime({ mode, testAPI }) {
  const styles = document.createElement('link'); styles.rel = 'stylesheet'; styles.href = '/assets/analytics/theme.css';
  const styled = new Promise((resolve, reject) => { styles.onload = resolve; styles.onerror = reject; }); document.head.append(styles); await styled;
  const session = await (await fetch('/api/v1/auth/session')).json();
  (await import('../analytics/app.js')).startAnalytics({ app: document.querySelector('#app'), session });
  const form = await waitFor(() => document.querySelector('#panel-settings-form'), '面板设置未加载');
  if (!mode.endsWith("-readonly")) await testSiteBranding(testAPI);
  const inputs = [...form.querySelectorAll('[name^="engine:"]')];
  const save = document.querySelector('.a-top [form="panel-settings-form"]');
  const status = () => form.querySelector('[role=status]').textContent;
  const changed = (name, value) => {
    const input = form.elements.namedItem(name); input.value = value; input.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const settled = async () => { await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '设置提交未结束'); await delay(30); };
  const writes = () => testAPI.calls.filter(call => call.path === '/settings' && call.method === 'PUT').length;
  assert.equal(inputs.length, 4, '缺少默认内核能力'); assert.ok(inputs[0].checked && !inputs[1].checked, '默认内核状态错误');
  assert.equal(form.querySelectorAll('[role=status]').length, 1, '重复保存状态');
  const acme = form.elements.acme_email;
  assert.ok(acme && acme.type === 'email' && !acme.required, '缺少可选证书邮箱');
  assert.equal(form.querySelector('[name*="komari"]'), null, '仍保留 Komari 字段');
  assert.ok(!document.querySelector('.a-main').textContent.includes('修订'), '设置泄露内部修订标识');
  const page = document.querySelector('.a-main');
  assert.match(page.textContent, /控制面发行版本/); assert.match(page.textContent, /QAgent 安装包版本/); assert.match(page.textContent, /可信代理/);
  if (mode.endsWith('-readonly')) {
    assert.equal(document.querySelector('#site-branding-form'), null, '普通账号出现站点品牌入口');
    assert.ok(inputs.every(input => input.disabled), '只读用户可改内核'); assert.ok(acme.disabled, '只读用户可改邮箱'); assert.equal(save, null, '只读用户出现保存入口');
    form.requestSubmit(); await delay(40); assert.equal(writes(), 0, '只读表单提交触发写入'); return;
  }
  assert.ok(save && save.form === form && !form.contains(save), '顶部保存按钮未关联完整表单');
  assert.equal(document.querySelectorAll('button[form="panel-settings-form"]').length, 1, '同一设置重复保存入口');
  assert.ok(save.disabled, '未修改时仍要求保存'); assert.equal(status(), '已保存');
  for (const ratio of [0, 0.5, 1]) {
    window.scrollTo(0, (document.documentElement.scrollHeight - innerHeight) * ratio); await delay(70);
    const rect = save.getBoundingClientRect();
    assert.ok(rect.top >= 0 && rect.bottom <= innerHeight && rect.left >= 0 && rect.right <= innerWidth + 1, '滚动后保存不可达');
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, '设置布局横向溢出');
  }
  window.scrollTo(0, 0);
  const ipv4 = form.elements.cnip_ipv4_url, ipv6 = form.elements.cnip_ipv6_url;
  assert.ok(ipv4 && ipv6 && !form.querySelector('[name=cnip_format]'), '地址库未保留双栈与自动格式');
  changed('panel_name', '暂存名称'); assert.ok(!save.disabled); changed('panel_name', testAPI.settings.panel_name); assert.ok(save.disabled, '改回原值后仍要求保存');
  changed('acme_email', 'not-an-email'); assert.ok(!acme.checkValidity(), '邮箱格式未验证');
  const beforeInvalid = writes(); form.requestSubmit(); await delay(50); assert.equal(writes(), beforeInvalid, '非法邮箱进入 API'); assert.equal(document.activeElement, acme, '邮箱校验未聚焦字段');
  changed('acme_email', 'certs@example.test'); changed('cnip_ipv4_url', 'https://example.com/cn.mmdb'); changed('cnip_ipv6_url', 'https://example.com/cn6.srs'); changed('client_connection_retention_days', '45');
  changed('agent_heartbeat_interval_seconds', '30');
  assert.ok(!form.elements.agent_offline_threshold_seconds.checkValidity(), '缺少心跳与离线阈值联动校验');
  form.requestSubmit(); await delay(40); assert.equal(writes(), beforeInvalid, '无效节点上报策略进入 API'); changed('agent_heartbeat_interval_seconds', '15');
  inputs[1].click(); assert.ok(!save.disabled, '切换未标记待保存');
  testAPI.settingsFailure = true; form.requestSubmit();
  await waitFor(() => page.textContent.includes('数据已发生变化或存在冲突'), '失败未显示中文错误'); await settled();
  assert.ok(!save.disabled && inputs[1].checked, '失败丢失草稿或锁死按钮'); assert.match(status(), /保存失败/);
  testAPI.settingsFailure = false; for (const input of inputs) if (input.checked) input.click();
  form.requestSubmit(); await waitFor(() => testAPI.settings.revision === 2, '默认能力未保存'); await settled();
  assert.equal(testAPI.settings.client_connection_retention_days, 45, '连接保留期未保存'); assert.equal(testAPI.settings.acme_email, 'certs@example.test', '邮箱未保存'); assert.equal(testAPI.settings.default_agent_engines.length, 0, '不允许关闭所有默认内核');
  assert.equal(testAPI.settings.cnip_source.ipv4_url, ipv4.value); assert.equal(testAPI.settings.cnip_source.ipv6_url, ipv6.value); assert.equal(testAPI.settings.cnip_source.format, 'auto');
  assert.ok(save.disabled, '保存后仍要求保存'); assert.equal(status(), '已保存');
  changed('ui_font_scale', '110');
  changed('acme_email', ''); form.requestSubmit(); await waitFor(() => testAPI.settings.revision === 3, '邮箱清空未保存'); await settled(); assert.equal(testAPI.settings.acme_email, '');
  assert.equal(testAPI.settings.ui_font_scale, 110, '文字大小未按数字保存');
  assert.equal(document.documentElement.style.fontSize, '110%', '已保存文字大小没有应用到原生界面');
  assert.equal([...form.elements.ui_font_scale.options].map(option => Number(option.value)).join(','), '90,100,110', '文字大小选项缺失');
  const beforePending = writes(); let release;
  testAPI.settingsGate = new Promise(resolve => { release = resolve; });
  changed('panel_description', '正在提交'); form.requestSubmit(); form.requestSubmit();
  await waitFor(() => form.getAttribute('aria-busy') === 'true', '未进入提交状态');
  assert.equal(writes(), beforePending + 1, '重复提交产生多个请求'); assert.ok(save.disabled && form.querySelector('fieldset').disabled, '提交未锁定表单'); assert.equal(status(), '正在保存…');
  // A queued input arriving after the captured request must remain a draft.
  changed('panel_description', '后续输入'); release(); await waitFor(() => testAPI.settings.revision === 4, '延迟保存未结束'); await settled(); testAPI.settingsGate = null;
  assert.equal(testAPI.settings.panel_description, '正在提交', '请求没有保留提交快照'); assert.equal(form.elements.panel_description.value, '后续输入', '完成请求覆盖后续输入'); assert.ok(!save.disabled, '后续输入被误判为已保存');
  form.requestSubmit(); await waitFor(() => testAPI.settings.revision === 5, '后续草稿未保存'); await settled(); assert.equal(testAPI.settings.panel_description, '后续输入'); assert.ok(save.disabled);
  const refresh = async () => {
    const before = testAPI.calls.filter(call => call.method === 'GET' && call.path === '/settings').length;
    [...document.querySelectorAll('.a-top button')].find(button => button.textContent === '刷新').click();
    await waitFor(() => testAPI.calls.filter(call => call.method === 'GET' && call.path === '/settings').length > before, '设置刷新未读取 API');
    await delay(30);
  };
  changed('panel_description', '保留的刷新草稿');
  await waitFor(()=>!document.getAnimations().some(animation=>animation.id.startsWith('analytics-')), '设置反馈未结束');
  const description = form.elements.panel_description;
  description.focus(); description.setSelectionRange(1, 4); window.scrollTo({top:120,behavior:'instant'});
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))); const scroll = scrollY;
  testAPI.settings.panel_description = '其他会话的设置'; testAPI.settings.revision++;
  await refresh();
  assert.equal(document.querySelector('#panel-settings-form'), form, '刷新重建设置表单');
  assert.equal(description.value, '保留的刷新草稿', '刷新覆盖设置草稿');
  assert.equal(document.activeElement, description, '刷新丢失设置焦点'); assert.equal(description.selectionStart, 1); assert.equal(description.selectionEnd, 4);
  assert.equal(scrollY, scroll, '刷新改变设置滚动位置');
  assert.ok(page.textContent.includes('设置已由其他会话修改'), '其他会话修改没有提示');
  form.requestSubmit(); await settled(); assert.ok(status().includes('保存失败'), '草稿刷新偷偷采用新修订编号');
  changed('panel_description', '后续输入'); await refresh();
  assert.equal(description.value, '其他会话的设置', '无草稿刷新未同步当前设置'); assert.ok(save.disabled);
  await testSettingsAPIKeysRuntime();
  if (new URLSearchParams(location.search).has('preview')) await new Promise(() => {});
}

async function testSiteBranding(testAPI) {
  const form=await waitFor(()=>document.querySelector('#site-branding-form'),'Logo settings did not load');
  const save=form.querySelector('[type=submit]'), file=form.elements.logo_file;
  const select=async(name,blob)=>{
    const transfer=new DataTransfer();transfer.items.add(new File([blob],name,{type:blob.type}));file.files=transfer.files;
    file.dispatchEvent(new Event('change',{bubbles:true}));await delay(50);
  };
  const canvas=document.createElement('canvas');canvas.width=180;canvas.height=36;
  const context=canvas.getContext('2d');context.fillStyle='#7c3aed';context.fillRect(0,0,180,36);
  const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
  assert.ok(save.disabled&&!document.querySelector('.a-brand img'),'default brand unavailable');
  await select('brand.png',blob);await waitFor(()=>!save.disabled,'Logo preview not ready');
  assert.ok(form.querySelector('.a-mark img')&&!document.querySelector('.a-brand img'),'unsaved preview became public');
  testAPI.brandingFailure=true;form.requestSubmit();await waitFor(()=>form.textContent.includes('保存失败'),'Logo failure missing');
  assert.ok(!save.disabled&&form.querySelector('.a-mark img')&&!document.querySelector('.a-brand img'),'Logo failure lost preview');
  testAPI.brandingFailure=false;form.requestSubmit();await waitFor(()=>testAPI.branding.revision===2,'Logo save failed');
  await waitFor(()=>document.querySelector('.a-brand img')?.complete&&save.disabled,'saved logo not applied');
  const image=document.querySelector('.a-brand img');assert.equal(getComputedStyle(image).objectFit,'contain');assert.equal(form.querySelector(".a-mark img").getBoundingClientRect().width,32);
  const current=testAPI.branding.logo;
  await select('invalid.png',new Blob(['broken'],{type:'image/png'}));
  assert.equal(testAPI.branding.logo,current);assert.ok(form.textContent.includes('图片内容无效'),'invalid image accepted');
  const reset=[...form.querySelectorAll('button')].find(control=>control.textContent==='恢复默认');reset.click();
  assert.ok(!form.querySelector('.a-mark img')&&document.querySelector('.a-brand img'),'reset preview prematurely applied');
  form.requestSubmit();await waitFor(()=>testAPI.branding.revision===3,'default not saved');
  await waitFor(()=>!document.querySelector('.a-brand img')&&save.disabled,'default brand not restored');
}
