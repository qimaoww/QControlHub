import { assert, delay, waitFor } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';

export async function testEnrollmentLayoutRuntime({ mode, testAPI }) {
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), '节点操作未结束');
  const open = async () => {
    await idle(); button('添加节点').focus(); button('添加节点').click();
    await waitFor(() => modal()?.querySelector('h2')?.textContent === '添加节点' && modal().querySelector('summary')?.textContent.includes('·'), '添加节点未加载');
    return modal();
  };
  const close = async () => { modal().querySelector('[aria-label=关闭]').click(); await waitFor(() => !modal(), '添加弹窗未关闭'); };
  const fits = box => {
    const rect = box.getBoundingClientRect();
    assert.ok(rect.left >= -1 && rect.right <= innerWidth + 1 && rect.top >= -1 && rect.bottom <= innerHeight + 1, '添加节点弹窗超出视口');
    assert.ok(box.scrollWidth <= box.clientWidth + 1 && document.documentElement.scrollWidth <= innerWidth + 1, '添加节点横向溢出');
    const submit = box.querySelector('[type=submit]'); submit.scrollIntoView({ block: 'nearest' });
    const bounds = submit.getBoundingClientRect();
    assert.ok(bounds.bottom <= innerHeight && bounds.left >= 0 && bounds.right <= innerWidth, '添加节点提交不可达');
    const checkbox = box.querySelector('[name=admin_hidden]');
    assert.ok(checkbox.getBoundingClientRect().width <= 24 && checkbox.getBoundingClientRect().height <= 24, '复选框被文本样式拉伸');
  };
  const hash = location.hash, launcher = button('添加节点');
  let box = await open(), form = box.querySelector('form');
  assert.equal(location.hash, hash, '添加节点改变路由');
  assert.ok(box.matches(':modal') && box.getAttribute('aria-labelledby'), '添加节点没有模态语义与标题');
  assert.equal(document.activeElement, form.elements.name, '添加节点未聚焦名称');
  assert.equal(box.querySelectorAll('h2').length, 1, '添加节点重复标题');
  const preview = new URLSearchParams(location.search).has('preview');
  const key = async (key, shift = false) => {
    window.__enrollmentKey = { key, shift };
    await waitFor(() => !window.__enrollmentKey, '真实键盘输入未完成');
  };
  const checkKeyboard = async box => {
    const controls = [...box.querySelectorAll('button:not(:disabled),input:not(:disabled),summary')].filter(control => control.getClientRects().length && !control.closest('[hidden]'));
    controls.at(-1).focus(); await key('Tab');
    assert.equal(document.activeElement, controls[0], 'Tab 离开模态弹窗');
    controls[0].focus(); await key('Tab', true);
    assert.equal(document.activeElement, controls.at(-1), 'Shift+Tab 离开模态弹窗');
  };
  if (!preview) {
    await checkKeyboard(box);
    launcher.focus(); assert.ok(box.contains(document.activeElement), '背景抢走模态焦点');
    const scroll = document.scrollingElement.scrollTop;
    await key('Escape'); await waitFor(() => !modal(), 'Escape 未关闭添加节点');
    assert.equal(document.activeElement, launcher, 'Escape 未恢复入口焦点');
    assert.equal(document.scrollingElement.scrollTop, scroll, '关闭弹窗改变滚动');
    box = await open(); form = box.querySelector('form');
  }
  const history = box.querySelector('details');
  assert.ok(!history.open && history.querySelector('summary').textContent.includes('0'), '空记录未默认收起');
  history.querySelector('summary').click(); assert.ok(history.textContent.includes('暂无记录'), '展开空记录无提示'); history.querySelector('summary').click();
  const checkbox = form.elements.admin_hidden;
  assert.equal(checkbox.checked, false, '默认隐藏权限被改变');
  checkbox.closest('label').click(); assert.ok(checkbox.checked, '点击权限标签未切换');
  if (mode.endsWith('mobile')) { assert.equal(innerWidth, 390); assert.ok(matchMedia('(pointer:coarse)').matches); }
  const original = document.documentElement.style.fontSize;
  for (const scale of ['90%', '100%', '110%', '135%']) {
    document.documentElement.style.fontSize = scale;
    for (const animation of document.getAnimations()) if (Number.isFinite(animation.effect?.getComputedTiming().endTime)) animation.finish();
    fits(box);
  }
  document.documentElement.style.fontSize = original;
  form.elements.name.value = 'browser-node'; form.elements.name.dispatchEvent(new Event('input', { bubbles: true }));
  button('刷新', document.querySelector('.a-top')).click(); await delay(30);
  assert.equal(modal(), box, '刷新关闭节点添加'); assert.equal(form.elements.name.value, 'browser-node', '刷新清除草稿');
  testAPI.enrollmentFailure = true; form.requestSubmit();
  await waitFor(() => form.textContent.includes('服务暂不可用') && !form.querySelector('[type=submit]').disabled, '生成失败未反馈或解锁');
  assert.ok(checkbox.checked && form.elements.name.value === 'browser-node', '失败丢失名称或权限');
  testAPI.enrollmentFailure = false;
  let release; testAPI.enrollmentGate = new Promise(resolve => { release = resolve; });
  const before = testAPI.calls.filter(call => call.method === 'POST' && call.path === '/enrollment-tokens').length;
  form.requestSubmit(); form.requestSubmit();
  assert.equal(testAPI.calls.filter(call => call.method === 'POST' && call.path === '/enrollment-tokens').length, before + 1, '重复生成凭据');
  assert.ok(checkbox.matches(':disabled'), '生成期间权限未锁定');
  release(); testAPI.enrollmentGate = null;
  await waitFor(() => modal()?.querySelector('h2')?.textContent === 'QAgent 安装命令', '安装命令未显示');
  assert.equal(document.querySelectorAll('dialog[open]').length, 1, '命令叠加节点添加弹窗');
  assert.equal(JSON.stringify(testAPI.lastEnrollmentRequest), JSON.stringify({ name: 'browser-node', admin_hidden: true }), '权限未提交');
  assert.match(modal().querySelector('pre').textContent, /browser-test-enrollment/);
  assert.ok(!modal().querySelector('input,textarea,[contenteditable=true]'), '安装命令可以编辑');
  assert.ok(!testAPI.calls.some(call => call.path === '/tasks' && call.method === 'POST'), '生成命令自动执行远程任务');
  await idle();
  if (!preview) {
    await checkKeyboard(modal()); await key('Escape');
    await waitFor(() => !modal(), 'Escape 未关闭命令弹窗');
    assert.equal(document.activeElement, launcher, '关闭命令未恢复入口焦点');
  } else await close();
  box = await open(); form = box.querySelector('form');
  assert.equal(form.elements.admin_hidden.checked, false, '重开继承隐藏权限');
  form.elements.name.value = 'browser-node'; form.requestSubmit();
  await waitFor(() => modal()?.querySelector('h2')?.textContent === 'QAgent 安装命令', '未勾选时不能生成命令');
  assert.equal(testAPI.lastEnrollmentRequest.admin_hidden, false, '未勾选权限改变'); await idle(); await close();
  testAPI.enrollmentRecords = [{ id: 'enr-alpha', name: `上海边缘节点-${'long-name-'.repeat(9)}`, reusable: true, used_count: 2, command_available: true }];
  box = await open(); assert.ok(box.querySelector('details').open, '已有记录未默认展开');
  assert.equal(box.querySelectorAll('tbody tr').length, 1, '已有记录丢失'); fits(box);
  const reads = testAPI.calls.filter(call => call.path === '/enrollment-tokens/enr-alpha/command').length;
  const created = testAPI.calls.filter(call => call.method === 'POST' && call.path === '/enrollment-tokens').length;
  let previous;
  for (let index = 0; index < 3; index++) {
    if (index) box = await open();
    button('安装命令', box).click();
    await waitFor(() => modal()?.querySelector('h2')?.textContent === 'QAgent 安装命令', '已有命令未显示');
    const command = modal().querySelector('pre').textContent;
    if (previous) assert.equal(command, previous, '重复查看更换凭据');
    previous = command;
    assert.equal(document.querySelectorAll('dialog[open]').length, 1, '查看已有命令叠加弹窗');
    await idle(); await close();
  }
  assert.equal(testAPI.calls.filter(call => call.path === '/enrollment-tokens/enr-alpha/command').length, reads + 3, '重复查看未使用读取接口');
  assert.equal(testAPI.calls.filter(call => call.method === 'POST' && call.path === '/enrollment-tokens').length, created, '查看已有命令创建新凭据');
  assert.equal(document.activeElement, launcher, '关闭命令未恢复添加入口焦点');
  const savedRecords = structuredClone(testAPI.enrollmentRecords);
  const agentsBefore = JSON.stringify(testAPI.agents);
  const deletionCount = () => testAPI.calls.filter(call => call.method === 'DELETE' && call.path === '/enrollment-tokens/enr-alpha').length;
  const nodeDeletesBefore = testAPI.calls.filter(call => call.method === 'DELETE' && call.path.startsWith('/agents/')).length;
  const tasksBefore = testAPI.calls.filter(call => call.method === 'POST' && call.path === '/tasks').length;
  box = await open();
  const remove = button('删除记录', box);
  const deletesBefore = deletionCount();
  const deletionConfirmation = async () => {
    remove.scrollIntoView({ block: 'nearest' });
    if (!preview) {
      const rect = remove.getBoundingClientRect();
      window.__enrollmentKey = { key: 'click', x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      await waitFor(() => !window.__enrollmentKey, '真实删除按钮点击未完成');
    } else { remove.focus(); remove.click(); }
    return await waitFor(() => modal() !== box && modal()?.querySelector('h2')?.textContent === '删除记录' && modal(), '删除添加记录未确认');
  };
  let confirmation = await deletionConfirmation();
  assert.ok(confirmation.textContent.includes('已接入的节点不会被卸载'), '删除确认未区分凭据与节点');
  button('取消', confirmation).click(); await delay(20);
  assert.equal(deletionCount(), deletesBefore, '取消确认仍删除凭据');
  assert.equal(modal(), box, '取消确认关闭添加弹窗');
  assert.equal(box.querySelectorAll('tbody tr').length, 1, '取消删除丢失记录');
  assert.equal(document.activeElement, remove, '取消确认未恢复记录操作焦点');
  if (!preview) {
    await deletionConfirmation(); await key('Escape');
    await waitFor(() => modal() === box, 'Escape 未取消凭据删除');
    assert.equal(deletionCount(), deletesBefore, 'Escape 仍删除凭据');
  }
  testAPI.enrollmentDeleteFailure = true;
  confirmation = await deletionConfirmation(); button('删除记录', confirmation).click();
  await waitFor(() => deletionCount() === deletesBefore + 1, '失败删除未开始'); await idle();
  assert.equal(deletionCount(), deletesBefore + 1, '失败删除未请求正确记录');
  assert.ok(!remove.disabled && remove.isConnected && box.querySelectorAll('tbody tr').length === 1, '删除失败丢失记录或不能重试');
  assert.ok(document.body.textContent.includes('服务暂不可用'), '删除失败没有反馈');
  testAPI.enrollmentDeleteFailure = false;
  let releaseDelete; testAPI.enrollmentDeleteGate = new Promise(resolve => { releaseDelete = resolve; });
  confirmation = await deletionConfirmation(); button('删除记录', confirmation).click();
  await waitFor(() => deletionCount() === deletesBefore + 2 && remove.disabled, '凭据删除未锁定');
  remove.click(); await delay(20);
  assert.equal(deletionCount(), deletesBefore + 2, '删除期间重复提交凭据');
  assert.equal(document.querySelectorAll('dialog[open]').length, 1, '删除期间打开额外确认');
  box.querySelector('[aria-label=关闭]').click();
  assert.equal(modal(), box, '提交期间关闭添加弹窗');
  releaseDelete(); testAPI.enrollmentDeleteGate = null;
  await idle(); await waitFor(() => box.querySelector('summary').textContent.includes('· 0'), '删除后未重读凭据记录');
  assert.ok(box.textContent.includes('暂无记录') && !box.querySelector('tbody tr'), '删除成功未显示空状态');
  await close(); box = await open();
  assert.ok(box.querySelector('summary').textContent.includes('· 0') && !box.querySelector('details').open, '重开恢复已删除凭据');
  await close();
  assert.equal(JSON.stringify(testAPI.agents), agentsBefore, '删除凭据修改已接入节点');
  assert.equal(testAPI.calls.filter(call => call.method === 'DELETE' && call.path.startsWith('/agents/')).length, nodeDeletesBefore, '删除凭据调用节点删除');
  assert.equal(testAPI.calls.filter(call => call.method === 'POST' && call.path === '/tasks').length, tasksBefore, '删除凭据执行远程任务');
  testAPI.enrollmentRecords = savedRecords;
  window.__enrollmentKeysDone = true;
  if (new URLSearchParams(location.search).has('preview')) { await open(); document.documentElement.dataset.enrollmentPreview = 'ready'; await new Promise(() => {}); }
}
