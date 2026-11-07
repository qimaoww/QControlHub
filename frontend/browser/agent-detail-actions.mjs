import { assert, waitFor } from "./assertions.mjs";
import { testAgentOverview } from './agent-overview.mjs';
import { testAgentRenameActions } from './agent-rename-actions.mjs';
import { testAgentDeleteActions } from './agent-delete-actions.mjs';
export async function testAgentDetailActions(fixture) {
  const { testAPI, populatedAgents } = fixture;
  testAPI.agents = populatedAgents.map(agent => structuredClone(agent));
  await startNativeNodes();
  location.hash = '#settings-node-alpha';
  const box = await waitFor(() => document.querySelector('dialog[open] input[name=name]')?.closest('dialog'), '单节点设置未打开');
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  assert.equal(box.querySelector('.a-probe-batch,form[name=enrollment]'), null, '单节点设置包含聚合操作');
  await testSettingsPanels(box, testAPI);
  const reads = testAPI.calls.filter(call => call.path === '/agents/alpha/enrollment-command').length;
  const credentials = testAPI.calls.filter(call => /enrollment-token/.test(call.path) && call.method === 'POST').length;
  let previous;
  for (let index = 0; index < 2; index++) {
    button('查看安装命令', box).click();
    const command = await waitFor(() => [...document.querySelectorAll('dialog[open] pre')].at(-1), '节点安装命令未打开');
    assert.match(command.textContent, /browser-test-enrollment/);
    if (previous) assert.equal(command.textContent, previous, '重复查看生成不同安装凭据');
    previous = command.textContent;
    await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '命令读取未结束');
    command.closest('dialog').querySelector('[aria-label=关闭]').click();
    await waitFor(() => document.querySelectorAll('dialog[open]').length === 1, '关闭命令后节点设置丢失');
  }
  assert.equal(testAPI.calls.filter(call => call.path === '/agents/alpha/enrollment-command').length, reads + 2);
  assert.equal(testAPI.calls.filter(call => /enrollment-token/.test(call.path) && call.method === 'POST').length, credentials, '读取节点命令创建新凭据');
  box.querySelector('[aria-label=关闭]').click();
  testAPI.agents = populatedAgents.map(agent => structuredClone(agent));
  await testNativeNodeTaskLifecycle(fixture);
}

async function testSettingsPanels(box, testAPI) {
  const nodePanel = box.querySelector('[data-settings-panel=node]'), corePanel = box.querySelector('[data-settings-panel=core]');
  const name = box.querySelector('input[name=name]'), original = name.value;
  assert.ok(!nodePanel.hidden && corePanel.hidden, '旧节点深链接未进入节点设置');
  assert.ok(!nodePanel.querySelector('[data-core-installed],[data-engine-capability]'), '节点设置混入内核操作');
  assert.ok(!corePanel.querySelector('[name=name],[data-agent-timezone]'), '内核设置混入节点表单');
  name.value = '未提交的节点名称'; name.focus(); name.setSelectionRange(1, 3);
  const top = box.scrollTop;
  testAPI.agents[0].metrics.network_rx_bps = 1024;
  await waitFor(() => document.querySelector('.a-probe-network').textContent.includes('1\u00a0KB/s'), '节点未实时更新');
  assert.ok(!nodePanel.querySelector('.a-probe-network,.a-probe-meters,[data-ip-family]'), '节点设置重复列表概览');
  assert.equal(document.activeElement, name, '设置刷新丢失焦点'); assert.equal(name.selectionStart, 1, '设置刷新丢失选择'); assert.equal(box.scrollTop, top, '设置刷新重置滚动');
  openCoreSettings(box);
  const capability = corePanel.querySelector('[data-engine-capability=xray]');
  const before = capability.checked; capability.click();
  const stable = capability.closest('form');
  testAPI.agents[0].runtime.mihomo.version = 'v1.2.3';
  await waitFor(() => corePanel.textContent.includes('v1.2.3'), '内核设置未实时更新');
  assert.equal(capability.closest('form'), stable, '刷新重建能力表单'); assert.equal(capability.checked, !before, '刷新覆盖能力草稿');
  assert.equal(corePanel.hidden, false, '刷新重置当前设置分区');
  for (const engine of ['mihomo', 'xray', 'sing-box', 'ss-rust']) {
    const card = corePanel.querySelector(`[data-refresh-key="${engine}"]`);
    assert.ok(card.querySelector(`[data-engine-capability="${engine}"]`), '能力设置与对应内核未归组');
    assert.equal([...card.querySelectorAll('button')].filter(control => control.textContent === '保存能力').length, 1, '相同内核重复能力保存入口');
  }
  [...box.querySelectorAll('button')].find(control => control.textContent === '节点设置').click();
  assert.equal(name.value, '未提交的节点名称', '分区切换丢失名称草稿');
  openCoreSettings(box); assert.equal(capability.checked, !before, '分区切换丢失能力草稿'); capability.click();
  [...box.querySelectorAll('button')].find(control => control.textContent === '节点设置').click(); name.value = original;
}

export async function startNativeNodes() {
  window.dispatchEvent(new Event('pagehide'));
  document.querySelectorAll('link[rel=stylesheet]').forEach(link => link.remove());
  const style = Object.assign(document.createElement('link'), { rel: 'stylesheet', href: '/assets/analytics/theme.css' });
  document.head.append(style);
  await new Promise((resolve, reject) => { style.onload = resolve; style.onerror = reject; });
  document.body.replaceChildren(Object.assign(document.createElement('div'), { id: 'app' }));
  history.replaceState(null, '', '#node-settings');
  const session = await (await fetch('/api/v1/auth/session')).json();
  const { startAnalytics } = await import('../analytics/app.js');
  startAnalytics({ app: document.querySelector('#app'), session });
  await waitFor(() => document.querySelector('.a-probe-toolbar'), '新主题节点未加载');
}

export function openCoreSettings(box) {
  [...box.querySelectorAll('button')].find(control => control.textContent === '内核设置').click();
  assert.ok(!box.querySelector('[data-settings-panel=core]').hidden && box.querySelector('[data-settings-panel=node]').hidden, '节点与内核设置未独立切换');
}

export async function testWriteOnlyCoreUninstall({ testAPI }) {
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  button('查看设置', document.querySelector('[data-agent-id="alpha"]')).click();
  const settings = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box.textContent.includes('节点设置')), '节点设置未打开');
  openCoreSettings(settings);
  const service = settings.querySelector('[data-refresh-key=mihomo]');
  const uninstall = await waitFor(() => button('卸载内核', service), '只有执行权的节点所有者不能卸载内核');
  assert.ok(!uninstall.disabled, '只有执行权的所有者卸载入口不可用');
  testAPI.taskMode = 'deferred';
  uninstall.click();
  const confirm = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box.querySelector('h2')?.textContent === '卸载内核'), '卸载确认弹窗未出现');
  button('卸载内核', confirm).click();
  const request = await waitFor(() => testAPI.pendingTasks.find(item => item.payload.action === 'uninstall'), '没有提交卸载任务');
  assert.equal(uninstall.disabled, true, '卸载请求尚未返回时按钮没有锁定');
  request.ok({ id: 'uninstall-writeonly-task' });
  await waitFor(() => !uninstall.disabled, '无法读取任务状态后卸载按钮没有恢复');
  const reads = testAPI.calls.filter(call => call.path === '/agents').length;
  button('刷新', document.querySelector('.a-top')).click();
  await waitFor(() => testAPI.calls.filter(call => call.path === '/agents').length > reads, '节点状态没有刷新');
  assert.equal(uninstall.disabled, false, '刷新后卸载按钮再次卡在等待状态');
  assert.equal(testAPI.calls.some(call => call.path === '/tasks/uninstall-writeonly-task'), false, '没有 tasks.read 权限时仍轮询受保护的任务详情');
  assert.ok(!settings.querySelector('input[name=name], [data-engine-capability]'), '执行任务权限扩大为 Agent 设置修改权限');
  const editable = [...settings.querySelectorAll('[data-agent-timezone-form] input, [data-agent-timezone-form] select, [data-agent-timezone-form] button[type=submit]')].filter(control => !control.matches(':disabled'));
  assert.equal(editable.length, 0, `执行任务权限扩大为时区修改权限：${editable.map(control => control.outerHTML).join('')}`);
}

export async function testNativeNodeTaskLifecycle(fixture) {
  if (fixture.mode.endsWith('mobile')) {
    assert.equal(innerWidth, 390, '节点任务移动回归没有使用 390px 视口');
    assert.ok(matchMedia('(pointer:coarse)').matches, '节点任务移动回归未启用触摸模拟');
  }
  await testAgentOverview(fixture);
  if (fixture.mode.startsWith('node-task-lifecycle')) {
    await testNativeInstallSource(fixture);
    await testNativeRuntimeRefresh(fixture);
  }
  const { testAPI, onlineAgent } = fixture;
  testAPI.agents = [onlineAgent('native-guard')];
  testAPI.taskMode = 'deferred';
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  button('查看设置').click();
  const settings = await waitFor(() => modal()?.textContent.includes('节点设置') && modal(), '节点设置未打开');
  openCoreSettings(settings);
  const core = () => settings.querySelector('[data-refresh-key=mihomo]');
  const uninstall = () => button('卸载内核', core());
  const writes = () => testAPI.submittedTasks.filter(task => task.action === 'uninstall');
  const before = writes().length;
  uninstall().click(); uninstall().click();
  await waitFor(() => modal()?.querySelector('h2')?.textContent === '卸载内核', '卸载没有确认');
  assert.equal(document.querySelectorAll('dialog[open]').length, 2, '卸载确认允许重入');
  button('取消', modal()).click();
  await waitFor(() => !uninstall().disabled, '取消卸载未解锁');
  assert.equal(writes().length, before, '取消卸载仍提交');
  uninstall().click();
  await waitFor(() => modal()?.querySelector('h2')?.textContent === '卸载内核', '离线测试无确认');
  testAPI.agents[0].status = 'offline';
  button('卸载内核', modal()).click();
  await waitFor(() => core() && uninstall().disabled && settings.textContent.includes('离线'), '确认后未复查最新在线状态');
  assert.equal(writes().length, before, '确认期间离线仍卸载');
  testAPI.agents[0].status = 'online';
  button('刷新', document.querySelector('.a-top')).click();
  await waitFor(() => !uninstall().disabled, '上线未恢复卸载');
  for (const [id, terminal] of [['native-uninstall-failed', 'failed'], ['native-uninstall-success', 'succeeded']]) {
    uninstall().click();
    await waitFor(() => modal()?.querySelector('h2')?.textContent === '卸载内核', '重试缺少确认');
    button('卸载内核', modal()).click();
    const request = await waitFor(() => testAPI.pendingTasks.find(task => task.payload.action === 'uninstall' && !task.nativeConsumed), '卸载未提交');
    request.nativeConsumed = true;
    request.ok({ id });
    await waitFor(() => modal()?.querySelector('h2')?.textContent === '任务详情', '卸载未显示任务结果');
    modal().querySelector('[aria-label=关闭]').click();
    await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '提交锁未释放');
    assert.ok(uninstall().disabled, '任务等待期间允许再次卸载');
    const count = writes().length; uninstall().click(); assert.equal(writes().length, count, '等待期间重复卸载');
    testAPI.taskStates.set(id, { id, status: terminal });
    if (terminal === 'succeeded') testAPI.agents[0].runtime.mihomo.installed = false;
    button('刷新', document.querySelector('.a-top')).click();
    await waitFor(() => terminal === 'failed' ? uninstall() && !uninstall().disabled : core()?.dataset.coreInstalled === '0' && !uninstall(), '任务终态未解除等待或更新安装状态');
  }
  testAPI.agents[0].runtime.mihomo.installed = true;
  testAPI.agents[0].capabilities = ['sing-box'];
  button('刷新', document.querySelector('.a-top')).click();
  await waitFor(() => core()?.dataset.capabilityEnabled === '0', '关闭能力丢失已安装内核');
  assert.ok(uninstall() && !uninstall().disabled, '关闭能力后无法卸载已安装内核');
  assert.ok(button('切换版本', core()).disabled, '关闭能力后仍允许版本安装');
  for (const label of ['查询状态', '启动服务', '停止服务', '重启服务']) assert.ok(button(label, core()).disabled, '关闭能力后仍可执行服务操作');
  assert.ok(core().textContent.includes('能力已关闭') && core().textContent.includes('版本未上报'), '内核能力或版本状态误报');
  testAPI.agents[0].features = [];
  button('刷新', document.querySelector('.a-top')).click();
  await waitFor(() => uninstall().disabled, '旧版 Agent 仍允许卸载');
  testAPI.agents[0].features = ['core-uninstall-v1'];
  testAPI.agents[0].capabilities.push('mihomo');
  button('刷新', document.querySelector('.a-top')).click();
  const capability = engine => settings.querySelector(`[data-engine-capability="${engine}"]`);
  await waitFor(() => capability('mihomo').checked && !uninstall().disabled, '恢复能力未更新');
  const toggle = async engine => {
    const input = capability(engine); input.click(); input.closest('form').requestSubmit();
    await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '能力保存未结束');
    if (modal() !== settings) {
      assert.equal(modal().querySelector('h2').textContent, '任务详情', '启停任务结果未显示');
      modal().querySelector('[aria-label=关闭]').click();
    }
  };
  const complete = async success => {
    const agent = testAPI.agents[0], transition = agent.capability_transitions.mihomo;
    transition.status = success ? 'succeeded' : 'failed';
    if (success) {
      agent.capabilities = agent.capabilities.filter(engine => engine !== 'mihomo');
      if (transition.enabled) agent.capabilities.push('mihomo');
      agent.runtime.mihomo.service_status = transition.enabled ? 'running' : 'inactive';
    }
    button('刷新', document.querySelector('.a-top')).click();
    await waitFor(() => !capability('mihomo').disabled && capability('mihomo').checked === agent.capabilities.includes('mihomo'), '能力终态未更新或解锁');
  };
  await toggle('xray');
  await waitFor(() => capability('xray').checked && settings.querySelector('[data-refresh-key=xray]'), '开启未安装内核丢失管理入口');
  testAPI.capabilityFailure = true; await toggle('xray');
  assert.ok(capability('xray').closest('form').textContent.includes('冲突'), '能力保存失败没有反馈');
  assert.ok(testAPI.agents[0].capabilities.includes('xray'), '保存失败改变真实能力');
  testAPI.capabilityFailure = false;
  // The failed attempt retains the chosen disabled draft for retry.
  capability('xray').closest('form').requestSubmit();
  await waitFor(() => !document.querySelector('#app[aria-busy=true]') && settings.querySelector('[data-refresh-key=xray]')?.dataset.capabilityEnabled === '0', '关闭未安装内核未更新能力状态');
  for (const success of [false, true, false, true]) {
    const wasEnabled = testAPI.agents[0].capabilities.includes('mihomo');
    await toggle('mihomo');
    assert.ok(capability('mihomo').disabled && capability('mihomo').checked === wasEnabled, '启停任务完成前提前修改能力状态');
    assert.ok(capability('mihomo').closest('form').textContent.includes('排队中'), '启停等待状态未显示');
    await complete(success);
    if (!success) assert.ok(capability('mihomo').closest('form').textContent.includes('失败或取消'), '启停失败缺少可见提示');
    if (!testAPI.agents[0].capabilities.includes('mihomo')) {
      assert.ok(core() && uninstall() && !uninstall().disabled, '停止能力后丢失已安装内核卸载入口');
      assert.ok(button('切换版本', core()).disabled, '能力关闭后允许切换版本');
    }
  }
  await testAgentRenameActions(fixture);
  await testAgentDeleteActions(fixture);
}

async function testNativeInstallSource({ testAPI, onlineAgent }) {
  testAPI.agents = [onlineAgent('source-guard')];
  testAPI.agents[0].features = [];
  testAPI.taskMode = 'deferred';
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(control => control.textContent === text);
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), 'installation did not unlock');
  button('查看设置').click();
  const settings = await waitFor(() => modal()?.querySelector('input[name=name]') && modal(), 'settings missing');
  openCoreSettings(settings);
  button('切换版本', settings.querySelector('[data-refresh-key=mihomo]')).click();
  const box = modal(), form = box.querySelector('form');
  const change = (name, value) => { form.elements[name].value = value; form.elements[name].dispatchEvent(new Event('change', { bubbles: true })); };
  const source = form.elements.core_source.closest('.a-field'), custom = form.elements.custom_version.closest('.a-field');
  const mirror = form.elements.core_source.querySelector('[value=mirror]');
  assert.ok(source.hidden && custom.hidden, 'stable channel exposes development/custom fields');
  change('release_channel', 'development');
  assert.ok(!source.hidden && mirror.disabled && !source.querySelector('.a-field-hint').hidden, 'legacy Agent mirror is not disabled/explained');
  change('release_channel', 'stable'); assert.ok(source.hidden, 'stable channel retains development source');
  change('release_channel', 'custom');
  assert.ok(!custom.hidden && form.elements.custom_version.required && !form.checkValidity(), 'custom version is not required');
  change('custom_version', 'v9.8.7-draft');
  testAPI.agents[0].features.push('mihomo-development-source-v1');
  await waitFor(() => !mirror.disabled, 'live Agent feature upgrade did not enable mirror');
  assert.equal(form.elements.custom_version.value, 'v9.8.7-draft', 'feature refresh overwrote draft');
  change('release_channel', 'development');
  assert.ok(source.querySelector('.a-field-hint').hidden, 'supported Agent retains upgrade warning');
  for (const selected of ['official', 'mirror']) {
    change('core_source', selected);
    const count = testAPI.pendingTasks.length;
    form.requestSubmit();
    const confirm = await waitFor(() => modal() !== box && modal()?.querySelector('h2').textContent === '提交安装' && modal(), 'install confirmation missing');
    button('提交安装', confirm).click();
    const request = await waitFor(() => testAPI.pendingTasks[count], 'development source install not submitted');
    assert.equal(JSON.stringify(request.payload), JSON.stringify({ agent_id: 'source-guard', engine: 'mihomo', action: 'install', core_version: 'development', core_source: selected }), 'development source payload changed');
    assert.ok(form.elements.release_channel.matches(':disabled'), 'pending install leaves fields editable');
    request.ok({ id: `source-${selected}`, status: 'pending' });
    await waitFor(() => modal()?.querySelector('h2').textContent === '任务详情', 'task result did not open automatically');
    modal().querySelector('[aria-label=关闭]').click(); await idle();
  }
  const count = testAPI.pendingTasks.length;
  form.requestSubmit();
  await waitFor(() => modal()?.querySelector('h2').textContent === '提交安装', 'feature downgrade confirmation missing');
  testAPI.agents[0].features = [];
  button('提交安装', modal()).click(); await idle();
  assert.equal(testAPI.pendingTasks.length, count, 'feature downgrade during confirmation still submitted mirror');
  assert.ok(box.textContent.includes('使用第三方镜像需先升级 Agent'), 'mirror rejection lost recovery guidance');
  assert.equal(form.elements.core_source.value, 'mirror', 'rejection overwrote selected source');
  change('core_source', 'official'); form.requestSubmit();
  await waitFor(() => modal()?.querySelector('h2').textContent === '提交安装', 'offline confirmation missing');
  testAPI.agents[0].status = 'offline';
  button('提交安装', modal()).click(); await idle();
  assert.equal(testAPI.pendingTasks.length, count, 'offline node installed after confirmation');
  assert.ok(box.textContent.includes('节点离线'), 'offline reason missing');
  testAPI.agents[0].status = 'online'; form.requestSubmit();
  await waitFor(() => modal()?.querySelector('h2').textContent === '提交安装', 'ownership confirmation missing');
  testAPI.agents[0].owner_id = 'other-owner';
  button('提交安装', modal()).click(); await idle();
  assert.equal(testAPI.pendingTasks.length, count, 'changed owner installed after confirmation');
  box.querySelector('[aria-label=关闭]').click(); settings.querySelector('[aria-label=关闭]').click();
}

async function testNativeRuntimeRefresh({ testAPI, onlineAgent }) {
  testAPI.agents = [onlineAgent('runtime-parity')];
  testAPI.agents[0].capabilities = ['mihomo', 'xray', 'sing-box', 'ss-rust'];
  Object.assign(testAPI.agents[0].runtime, {
    'sing-box': { installed: false, existing_config_available: false, existing_config_unsupported_reason: 'unsupported wrapper' },
    'ss-rust': { installed: true, existing_config_available: true },
  });
  testAPI.agents[0].features.push('core-uninstall-v1');
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(control => control.textContent === text);
  button('查看设置').click();
  const box = await waitFor(() => document.querySelector('dialog[open] input[name=name]')?.closest('dialog'), 'runtime details missing');
  const core = engine => box.querySelector(`[data-refresh-key="${engine}"]`);
  const stable = core('mihomo'), draft = box.querySelector('input[name=name]');
  for (const engine of ['mihomo', 'xray', 'sing-box', 'ss-rust']) {
    const entry = [...core(engine).querySelectorAll('a')].find(link => link.textContent === '编辑配置');
    assert.equal(entry?.getAttribute('href'), `#live-config?agent=runtime-parity&engine=${engine}`, 'engine configuration entry missing or targets another core');
  }
  assert.ok(core('sing-box').textContent.includes('unsupported wrapper'), 'optional import refusal reason missing');
  assert.ok(![...core('sing-box').querySelectorAll('a')].some(link => link.textContent === '导入系统配置'), 'unsupported system configuration can be imported');
  const importEntry = [...core('ss-rust').querySelectorAll('a')].find(link => link.textContent === '导入系统配置');
  assert.equal(importEntry?.getAttribute('href'), '#live-config?agent=runtime-parity&engine=ss-rust&source=import', 'optional import replaced the managed entry or targets another core');
  assert.ok(!box.textContent.includes('请先手动导入'), 'optional import became a configuration prerequisite');

  draft.value = '未保存的名称'; draft.focus(); draft.setSelectionRange(1, 3);
  assert.equal(core('xray').dataset.coreInstalled, 'unknown', 'unreported runtime is reported as uninstalled');
  assert.ok(core('xray').textContent.includes('安装状态未知'), 'initial unknown installation is not identified');
  for (const installed of [false, true, undefined]) {
    testAPI.agents[0].runtime = installed === undefined ? {} : { mihomo: { installed, service_status: installed ? 'running' : 'unknown' }, xray: { installed: false } };
    const marker = installed === undefined ? 'unknown' : installed ? '1' : '0';
    await waitFor(() => core('mihomo')?.dataset.coreInstalled === marker, 'live runtime transition did not converge');
    assert.equal(core('mihomo'), stable, 'runtime transition rebuilt the existing engine section');
    assert.ok(core('mihomo').textContent.includes(installed === undefined ? '安装状态未知' : installed ? '版本未上报' : '尚未安装'), 'installation state text is inaccurate');
    if (installed !== true) {
      assert.ok(!button('卸载内核', core('mihomo')), 'unknown/uninstalled core retains uninstall action');
      for (const name of ['查询状态', '启动服务', '停止服务', '重启服务']) assert.ok(button(name, core('mihomo')).disabled, 'unknown/uninstalled core permits a service task');
    }
    assert.equal(draft.value, '未保存的名称', 'runtime poll replaced the name draft');
    assert.equal(document.activeElement, draft, 'runtime poll stole input focus');
    assert.equal(draft.selectionStart, 1, 'runtime poll reset text selection');
  }
  const stableCard = document.querySelector('[data-agent-id=runtime-parity]');
  testAPI.agents.push(onlineAgent('newly-enrolled'));
  await waitFor(() => document.querySelector('[data-agent-id=newly-enrolled]'), 'new Agent did not appear through polling');
  assert.equal(document.querySelector('[data-agent-id=runtime-parity]'), stableCard, 'new Agent discovery rebuilt an existing card');
  assert.ok(box.open && draft.value === '未保存的名称', 'new Agent discovery lost open settings or draft');
  const reads = testAPI.calls.filter(call => call.path === '/agents').length;
  testAPI.agents.reverse();
  await waitFor(() => testAPI.calls.filter(call => call.path === '/agents').length > reads, 'reordered roster was not polled');
  assert.equal(document.querySelector('[data-agent-id=runtime-parity]'), stableCard, 'API response ordering rebuilt an existing node');
  assert.equal(core('mihomo'), stable, 'API response ordering rebuilt an existing engine');
  assert.ok(box.open && draft.value === '未保存的名称' && document.activeElement === draft, 'API response ordering lost settings, draft or focus');
  button('查看指标历史', box).click();
  const history = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(dialog => dialog.querySelector('h2')?.textContent.includes('指标历史')), 'native metric history did not open');
  await waitFor(() => history.textContent.includes('暂无指标记录'), 'empty metric history missing');
  assert.equal(testAPI.calls.filter(call => call.path.startsWith('/metrics/')).at(-1)?.path, '/metrics/runtime-parity', 'metric history read targets another node');
  testAPI.metricSamples = Array.from({ length: 36 }, (_, index) => ({ sampled_at: new Date(Date.UTC(2026, 10, 7, 0, index)).toISOString(), cpu_percent: index, memory_percent: 50, rx_rate_bps: 1024 * (index + 1), tx_rate_bps: 512 * (index + 1) }));
  const refreshHistory = button('刷新指标', history);
  refreshHistory.click();
  await waitFor(() => history.querySelector('svg.a-chart') && !refreshHistory.disabled, 'metric history did not recover from empty data');
  assert.ok(history.querySelector('svg.a-chart').getAttribute('aria-label').includes('最近 24 小时网络速率'), 'history chart lacks scoped accessible description');
  assert.equal(history.querySelectorAll('tbody tr').length, 24, 'metric history lost bounded pagination');
  button('下一页', history).click();
  assert.equal(history.querySelectorAll('tbody tr').length, 12, 'metric history cannot read older samples');
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  const chart = history.querySelector('svg.a-chart');
  testAPI.metricFailure = true; refreshHistory.click();
  await waitFor(() => !refreshHistory.disabled, 'failed metric history read left retry locked');
  assert.equal(history.querySelector('svg.a-chart'), chart, 'failed metric history read discarded valid chart');
  assert.equal(history.querySelectorAll('tbody tr').length, 12, 'failed metric history read changed historical page');
  testAPI.metricFailure = false; testAPI.metricSamples = [];
  refreshHistory.click();
  await waitFor(() => !history.querySelector('svg.a-chart') && history.textContent.includes('暂无指标记录') && !refreshHistory.disabled, 'metric history retry retained obsolete samples');
  history.querySelector('[aria-label=关闭]').click();
  assert.ok(box.open && draft.value === '未保存的名称', 'closing metric history lost parent settings or draft');


  box.querySelector('[aria-label=关闭]').click();
  const originalFetch = window.fetch;
  window.fetch = (input, options) => String(input).endsWith('/auth/session')
    ? Promise.resolve(Response.json({ role: 'user', user_id: 'roster-only', permissions: ['agents.read'], csrf_token: 'browser-test-csrf' }))
    : originalFetch(input, options);
  try {
    await startNativeNodes();
    testAPI.agents.push(onlineAgent('without-metrics-permission'));
    await waitFor(() => document.querySelector('[data-agent-id=without-metrics-permission]'), 'node discovery incorrectly requires metrics.read');
    assert.ok(!button('添加节点') && !button('批量操作'), 'roster read grant exposes mutation controls');
    button('查看设置').click();
    const details = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(dialog => dialog.querySelector('h2')?.textContent.includes('节点设置')), 'readonly node details missing');
    assert.ok(!button('查看指标历史', details), 'metric history exposed without metrics.read');
    details.querySelector('[aria-label=关闭]').click();

  } finally { window.fetch = originalFetch; }
}
