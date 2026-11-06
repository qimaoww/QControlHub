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

export async function testWriteOnlyCoreUninstall({ testAPI }) {
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  button('查看设置', document.querySelector('[data-agent-id="alpha"]')).click();
  const settings = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box.textContent.includes('节点设置')), '节点设置未打开');
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
  const { testAPI, onlineAgent } = fixture;
  testAPI.agents = [onlineAgent('native-guard')];
  testAPI.taskMode = 'deferred';
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  button('查看设置').click();
  const settings = await waitFor(() => modal()?.textContent.includes('节点设置') && modal(), '节点设置未打开');
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
  await waitFor(() => !document.querySelector('#app[aria-busy=true]') && !settings.querySelector('[data-refresh-key=xray]'), '关闭未安装内核未移除管理入口');
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
