import { assert, delay, waitFor } from './assertions.mjs';

export async function testAgentTimeZoneLayout({ testAPI, mode }) {
  const styles = document.createElement('link'); styles.rel = 'stylesheet'; styles.href = '/assets/analytics/theme.css';
  const styled = new Promise((resolve, reject) => { styles.onload = resolve; styles.onerror = reject; }); document.head.append(styles); await styled;
  location.hash = '#settings-node-alpha';
  const session = await (await fetch('/api/v1/auth/session')).json();
  (await import('../analytics/app.js')).startAnalytics({ app: document.querySelector('#app'), session });
  const form = await waitFor(() => document.querySelector('[data-agent-timezone-form="alpha"]'), 'Agent 时区设置未加载');
  const section = form.closest('[data-agent-timezone]'), dialog = form.closest('dialog');
  const input = form.elements.time_zone, modeInput = form.elements.mode, submit = form.querySelector('[data-timezone-submit]');
  const status = () => section.querySelector('[data-timezone-status]').textContent;
  const error = () => form.querySelector(':scope > .a-notice').textContent;
  const clock = () => [section.querySelector('[data-clock-sync-status]').textContent, section.querySelector('[data-clock-sync-offset]').textContent];
  const agent = testAPI.agents.find(item => item.id === 'alpha');
  const refresh = async () => {
    const before = testAPI.calls.filter(call => call.path === '/agents' && call.method === 'GET').length;
    document.dispatchEvent(new Event('visibilitychange'));
    await waitFor(() => testAPI.calls.filter(call => call.path === '/agents' && call.method === 'GET').length > before, '节点未请求刷新'); await delay(100);
  };
  const settle = async () => { await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '时区提交未结束'); await delay(30); };
  const change = (control, value) => { control.value = value; control.dispatchEvent(new Event('input', { bubbles: true })); control.dispatchEvent(new Event('change', { bubbles: true })); };
  const applies = () => testAPI.calls.filter(call => call.method === 'PUT' && call.path === '/agents/alpha/time-zone').length;
  assert.equal(section.querySelector('h2').textContent, 'Agent 进程时区'); assert.equal(status(), '已生效');
  assert.equal(section.querySelector('[data-timezone-applied]').hidden, true, '已生效时重复显示时区');
  assert.equal(clock().join('|'), '已同步|-1.375 ms', '未显示系统时钟及有符号偏差');
  assert.equal(input.getAttribute('aria-label'), 'IANA 时区'); assert.match(modeInput.closest('.a-field').querySelector('label').textContent, /模式/);
  assert.equal(modeInput.value, 'custom');
  if (mode.endsWith('-readonly')) {
    await refresh(); assert.ok(modeInput.disabled && input.disabled && submit.disabled, '只读时区可写');
    form.requestSubmit(); await delay(40); assert.equal(applies(), 0, '只读用户发出时区写入');
    agent.can_manage = false; await refresh(); await waitFor(() => !dialog.open, '只读主实例切换为借用范围后仍保留原设置'); return;
  }
  input.focus();
  agent.time_zone = { ...agent.time_zone, time_zone: 'Europe/Paris', applied_time_zone: 'Europe/Paris', revision: 3 };
  await refresh(); assert.equal(input.value, 'Europe/Paris', '未编辑时区未跟随远端更新'); assert.equal(form.dataset.timezoneRevision, '3'); assert.equal(document.activeElement, input, '刷新丢失焦点');
  agent.time_zone = { ...agent.time_zone, time_zone: 'Europe/Berlin', applied_time_zone: 'Europe/Berlin', revision: 4 };
  agent.capabilities = [...agent.capabilities, 'ss-rust']; await refresh();
  assert.ok(dialog.querySelector('[data-engine-capability="ss-rust"]').checked, '未刷新内核能力');
  assert.ok(dialog.querySelector('[data-refresh-key="ss-rust"]'), '新增内核缺少运行状态');
  assert.equal(input.value, 'Europe/Berlin'); assert.equal(form.dataset.timezoneRevision, '4'); assert.equal(document.activeElement, input);
  const beforeInvalid = applies(); change(input, ''); form.requestSubmit(); await delay(40);
  assert.equal(applies(), beforeInvalid, '空时区进入 API'); assert.equal(form.querySelectorAll('.a-validation').length, 1, '缺少字段校验提示'); assert.equal(error(), '', '同一校验错误重复显示'); assert.match(form.querySelector('.a-validation').textContent, /请输入 IANA 时区/);
  change(input, 'Europe/Berlin'); assert.equal(form.querySelectorAll('.a-validation').length, 0, '修正后保留失效校验提示');
  change(modeInput, 'auto'); assert.ok(input.disabled, '自动模式允许编辑时区'); change(modeInput, 'custom'); assert.ok(!input.disabled);
  change(input, 'Pacific/Apia'); input.focus();
  agent.metrics.clock_sync = { status: 'unsynchronized', offset_milliseconds: 0.25 };
  agent.time_zone = { ...agent.time_zone, time_zone: 'Pacific/Apia', applied_time_zone: 'America/New_York', status: 'applying' }; await refresh();
  assert.equal(input.value, 'Pacific/Apia'); assert.equal(document.activeElement, input);
  assert.equal(section.querySelector('[data-timezone-applied]').textContent, '当前生效：America/New_York'); assert.ok(!section.querySelector('[data-timezone-applied]').hidden);
  assert.equal(clock().join('|'), '未同步|+0.250 ms');

  let finishSave; testAPI.timeZoneSaveGate = new Promise(resolve => { finishSave = resolve; });
  form.requestSubmit(); await waitFor(() => testAPI.timeZoneSaves.length === 1, '时区请求未提交');
  agent.capabilities = [...agent.capabilities, 'xray']; await refresh();
  assert.ok(dialog.querySelector('[data-engine-capability="xray"]').checked); assert.ok(modeInput.disabled && input.disabled && submit.disabled, '刷新解除提交锁'); assert.equal(status(), '保存中');
  form.requestSubmit(); assert.equal(applies(), 1, '重复写入时区');
  agent.time_zone = { ...agent.time_zone, time_zone: 'Europe/Oslo', applied_time_zone: 'Europe/Oslo', status: 'applied', revision: agent.time_zone.revision + 1 };
  agent.capabilities = agent.capabilities.filter(engine => engine !== 'ss-rust'); agent.metrics.clock_sync = { status: 'synchronized', offset_milliseconds: -1.375 }; await refresh();
  assert.ok(!dialog.querySelector('[data-engine-capability="ss-rust"]').checked); assert.equal(dialog.querySelector('[data-refresh-key="ss-rust"]'), null, '已移除内核仍显示运行状态'); finishSave(); testAPI.timeZoneSaveGate = null; await settle();
  assert.equal(JSON.stringify(testAPI.timeZoneSaves[0]), JSON.stringify({ agentID: 'alpha', mode: 'custom', time_zone: 'Pacific/Apia', revision: 4, retry: false }));
  assert.equal(input.value, 'Europe/Oslo', '迟到响应覆盖较新设置'); assert.equal(status(), '已生效'); assert.equal(Number(form.dataset.timezoneRevision), agent.time_zone.revision); assert.equal(clock().join('|'), '已同步|-1.375 ms');

  let finishAcknowledged; testAPI.timeZoneSaveGate = new Promise(resolve => { finishAcknowledged = resolve; });
  modeInput.focus(); form.requestSubmit(); await waitFor(() => testAPI.timeZoneSaves.length === 2, '第二次提交未开始');
  agent.time_zone = { ...agent.time_zone, status: 'applied', applied_time_zone: agent.time_zone.time_zone }; agent.capabilities.push('ss-rust'); await refresh();
  finishAcknowledged(); testAPI.timeZoneSaveGate = null; await settle(); assert.equal(status(), '已生效', '迟到保存响应覆盖同修订生效回执'); assert.equal(document.activeElement, modeInput, '提交完成丢失键盘焦点');
  testAPI.timeZoneSaveFailure = true; change(input, 'Asia/Tokyo'); input.focus(); form.requestSubmit(); await settle();
  assert.match(status(), /服务暂不可用/); assert.equal(input.value, 'Asia/Tokyo'); assert.equal(document.activeElement, input); assert.ok(!submit.disabled);
  testAPI.timeZoneSaveFailure = false; change(modeInput, 'auto'); form.requestSubmit(); await waitFor(() => testAPI.timeZoneSaves.length === 3, '自动时区未提交'); await settle();
  assert.equal(testAPI.timeZoneSaves[2].mode, 'auto'); assert.equal(testAPI.timeZoneSaves[2].time_zone, ''); assert.equal(testAPI.timeZoneSaves[2].retry, true);
  const previous = { ...agent.time_zone }; agent.time_zone = { ...previous, time_zone: 'Asia/Tokyo', applied_time_zone: 'Asia/Tokyo', status: 'applied', revision: previous.revision + 1 }; await refresh();
  assert.equal(input.value, 'Asia/Tokyo'); assert.ok(input.disabled); assert.equal(submit.textContent, '重新检测');
  const latest = { ...agent.time_zone }; agent.time_zone = previous; await refresh(); assert.equal(input.value, 'Asia/Tokyo', '旧轮询回退时区'); assert.equal(status(), '已生效'); agent.time_zone = latest;
  change(modeInput, 'custom'); change(input, 'Australia/Sydney'); input.focus(); input.setSelectionRange(2, 8);
  agent.time_zone = { ...latest, time_zone: 'Europe/London', applied_time_zone: 'Europe/London', revision: latest.revision + 1 };
  const draftRevision = form.dataset.timezoneRevision; agent.capabilities = agent.capabilities.filter(engine => engine !== 'xray'); await refresh();
  assert.equal(document.querySelector('[data-agent-timezone-form="alpha"]'), form); assert.equal(modeInput.value, 'custom'); assert.equal(input.value, 'Australia/Sydney'); assert.equal(document.activeElement, input); assert.equal(input.selectionStart, 2); assert.equal(input.selectionEnd, 8); assert.equal(form.dataset.timezoneRevision, draftRevision, '轮询升级草稿修订');
  form.requestSubmit(); await settle(); assert.match(status(), /冲突/); assert.match(error(), /冲突/); assert.equal(input.value, 'Australia/Sydney'); assert.equal(document.activeElement, input); assert.equal(testAPI.timeZoneSaves.length, 3);
  agent.capabilities.push('xray'); await refresh(); assert.match(status(), /冲突/, '刷新清除冲突反馈'); assert.equal(form.dataset.timezoneRevision, draftRevision);

  for (const [state, label] of [['failed', '测试时区应用失败'], ['unsupported', '请升级 Agent'], ['applying', '应用中'], ['pending', '待生效']]) {
    change(input, ''); change(input, 'Australia/Sydney'); agent.time_zone = { ...agent.time_zone, status: state, error: state === 'failed' ? label : '' }; await refresh(); assert.equal(status(), label);
  }
  agent.status = 'offline'; await refresh(); assert.equal(status(), '离线，待生效');
  agent.metrics.clock_sync = { status: 'unavailable' }; await refresh(); assert.equal(clock().join('|'), '不可用|');
  const rect = section.getBoundingClientRect(), modeRect = modeInput.getBoundingClientRect(), inputRect = input.getBoundingClientRect(), buttonRect = submit.getBoundingClientRect();
  assert.ok(rect.width > 0 && inputRect.width >= 100, '时区输入被挤压');
  assert.ok(inputRect.right <= rect.right + 1 && modeRect.right <= rect.right + 1 && buttonRect.right <= rect.right + 1, '时区控件溢出');
  assert.ok(Math.abs(inputRect.height - modeRect.height) <= 1, '模式与输入高度不一致');
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1 && dialog.scrollWidth <= dialog.clientWidth + 1, '节点时区布局溢出');
  if (matchMedia('(pointer: coarse)').matches) for (const control of [input, modeInput, submit]) assert.ok(control.getBoundingClientRect().height >= 44, '触摸控件不足44像素');
  window.agentTimeZoneMeasurements = { mode, cssViewport: { width: innerWidth, height: innerHeight }, devicePixelRatio, section: { width: rect.width }, dialog: { width: dialog.clientWidth, scrollWidth: dialog.scrollWidth } };
  if (new URLSearchParams(location.search).has('preview')) await new Promise(() => {});
  agent.owner_id = 'different-owner'; await refresh(); await waitFor(() => !dialog.open, '所有者变更后仍保留旧设置及草稿');
}
