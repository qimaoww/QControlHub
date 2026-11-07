import { testBatchCoreUpdates } from './agent-batch-core.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';
import { assert, delay, waitFor } from './assertions.mjs';

export const nativeBatchButton = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
export async function openNativeBatch(fixture) {
  await startNativeNodes();
  nativeBatchButton('批量操作').click();
  const card = document.querySelector('article[data-agent-id]');
  if (card) {
    const choice = card.querySelector('input[type=checkbox]');
    card.querySelector('h2').click(); assert.ok(choice.checked, '点击卡片标题未选择节点');
    card.querySelector('.a-probe-body').click(); assert.ok(!choice.checked, '点击卡片内容未取消选择');
    card.querySelector('label').click(); assert.ok(choice.checked, '选择框标签重复切换');
    card.focus(); card.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
    assert.ok(!choice.checked, '空格键未取消卡片选择');
    const dialogs = document.querySelectorAll('dialog[open]').length;
    card.querySelector('.a-probe-footer button').click();
    assert.ok(choice.checked, '多选模式下卡片操作区不能选择');
    assert.equal(document.querySelectorAll('dialog[open]').length, dialogs, '多选点击误打开设置');
    choice.click(); assert.ok(!choice.checked, '直接点击选择框重复切换');
  }
  const all = () => document.querySelector('input[name=select-all]');
  all().click();
  assert.ok(all().checked && !all().indeterminate, '全选状态错误');
  const multiple = document.querySelectorAll('input[name^="select:"]:enabled').length > 1;
  const first = document.querySelector('input[name^="select:"]'); first.click();
  assert.ok(!all().checked && all().indeterminate === multiple, '部分选择状态错误');
  all().click(); assert.ok(all().checked && !all().indeterminate, '混合状态全选失败');
  all().click(); assert.ok(!all().checked && !all().indeterminate, '取消全选状态错误');
  assert.equal(document.querySelectorAll('input[name^="select:"]:checked').length, 0, '取消全选未清空选择');
  all().click();
  nativeBatchButton('执行批量操作', document.querySelector('.a-probe-batch')).click();
  const form = await waitFor(() => document.querySelector('dialog[open] form'), '批量操作未打开');
  if (fixture.mode?.endsWith('mobile')) {
    assert.equal(innerWidth, 390, '批量操作移动视口不正确');
    assert.ok(matchMedia('(pointer:coarse)').matches, '批量操作未使用触摸模拟');
  }
  return form;
}
export async function testAgentBatchActions(fixture) {
  const { testAPI, onlineAgent, populatedAgents } = fixture;
  testAPI.agents = populatedAgents.map(agent => structuredClone(agent));
  const form = await openNativeBatch(fixture);
  const box = form.closest('dialog'), choose = id => form.elements[`batch:${id}`];
  const row = id => choose(id).closest('tr');
  const change = (name, value) => { form.elements[name].value = value; form.elements[name].dispatchEvent(new Event('change', { bubbles: true })); };
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), '批量操作未结束');
  const refresh = async () => { await delay(2100); };
  const writes = () => testAPI.pendingTasks.length;
  const submit = form.querySelector('[type=submit]');
  const confirm = async () => {
    form.requestSubmit();
    return await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(item => item !== box), '批量确认未打开');
  };
  assert.equal(form.querySelectorAll('input[type=checkbox]').length, 4, '批量目标缺失');
  change('action', 'upgrade-agent');
  assert.ok(choose('charlie').disabled && !choose('charlie').checked && row('charlie').textContent.includes('离线'), '离线节点仍可更新');
  assert.ok(choose('delta').disabled && !choose('delta').checked && row('delta').textContent.includes('旧版 Agent'), '旧 Agent 仍可更新');
  assert.ok(choose('alpha').checked && choose('bravo').checked, '合格节点选择丢失');
  testAPI.taskMode = 'deferred';
  const beforeCancel = writes();
  const canceled = await confirm();
  nativeBatchButton('取消', canceled).click(); await idle();
  assert.equal(writes(), beforeCancel, '取消批量确认仍提交任务');
  assert.ok(box.open && !submit.disabled && choose('alpha').checked && choose('bravo').checked, '取消确认丢失选择或未解锁');
  assert.equal(document.querySelectorAll('dialog[open]').length, 1, '取消确认残留额外弹窗');
  // A new authoritative read after confirmation must reject every newly ineligible target.
  for (const [action, mutate] of [
    ['upgrade-agent', agent => { agent.status = 'offline'; }],
    ['upgrade-agent', agent => { agent.features = []; }],
    ['restart', agent => { agent.runtime.mihomo.installed = false; }],
  ]) {
    testAPI.agents = populatedAgents.map(agent => structuredClone(agent));
    await refresh(); change('action', action);
    for (const id of ['alpha', 'bravo', 'delta']) choose(id).checked = id === 'alpha';
    const before = writes(), confirmation = await confirm();
    mutate(testAPI.agents[0]);
    nativeBatchButton('执行批量操作', confirmation).click(); await idle();
    assert.equal(writes(), before, '确认后仍向失效节点提交');
    assert.ok(row('alpha').textContent.includes('离线') || row('alpha').textContent.includes('旧版 Agent') || row('alpha').textContent.includes('未安装'), '失效原因未呈现');
  }
  testAPI.agents = populatedAgents.map(agent => structuredClone(agent)); await refresh(); change('action', 'upgrade-agent');
  choose('alpha').checked = choose('bravo').checked = true;
  let confirmation = await confirm(); form.requestSubmit();
  assert.equal(document.querySelectorAll('dialog[open]').length, 2, '重复提交打开额外确认');
  let next = writes(); nativeBatchButton('执行批量操作', confirmation).click();
  await waitFor(() => writes() === next + 1, '首个串行任务未提交');
  assert.equal(testAPI.pendingTasks[next].payload.agent_id, 'alpha');
  assert.ok(submit.disabled && form.elements.action.matches(':disabled') && form.elements.engine.matches(':disabled') && choose('alpha').matches(':disabled'), '提交没有锁定表单');
  assert.ok(choose('alpha').checked && choose('bravo').checked, '提交清除选择');
  testAPI.pendingTasks[next++].ok({ id: 'task-alpha' });
  await waitFor(() => writes() === next + 1, '第二个任务没有串行提交');
  assert.equal(testAPI.pendingTasks[next].payload.agent_id, 'bravo');
  testAPI.pendingTasks[next++].fail('bravo temporary failure'); await idle();
  assert.ok(form.textContent.includes('已提交 1，提交失败 1'), '部分失败汇总错误');
  assert.ok(nativeBatchButton('重试提交', row('bravo')) && !nativeBatchButton('重试提交', row('bravo')).hidden, '失败节点不能重试');
  nativeBatchButton('重试提交', row('bravo')).click();
  await waitFor(() => writes() === next + 1, '部分失败重试未提交');
  testAPI.pendingTasks[next++].ok({ id: 'task-bravo-retry' }); await idle();
  assert.ok(nativeBatchButton('重试提交', row('bravo')).hidden && form.textContent.includes('已提交 2，提交失败 0'), '重试结果未更新');
  confirmation = await confirm(); nativeBatchButton('执行批量操作', confirmation).click();
  for (const id of ['alpha', 'bravo']) {
    await waitFor(() => writes() === next + 1, '全部失败未依次提交');
    testAPI.pendingTasks[next++].fail(`${id} temporary failure`);
  }
  await idle();
  assert.equal([...form.querySelectorAll('button')].filter(button => button.textContent === '重试提交' && !button.hidden).length, 2, '全部失败未保留两个重试入口');
  if (new URLSearchParams(location.search).has('preview')) {
    document.documentElement.dataset.batchPreview = 'ready';
    await new Promise(() => {});
  }
  const route = location.hash, firstRow = row('alpha'), body = box.querySelector('.a-dialog-body');
  body.scrollTop = 43; const scroll = body.scrollTop;
  const calls = path => testAPI.calls.filter(call => call.method === 'GET' && call.path === path).length;
  const beforePoll = calls('/agents'), beforeOverview = calls('/overview'), beforeEnrollment = calls('/enrollment-tokens');
  const failedResults = ['alpha', 'bravo'].map(id => row(id).lastElementChild.textContent);
  testAPI.agents[0].runtime['sing-box'] = { installed: true, service_status: 'active' };
  await delay(6500);
  assert.ok(calls('/agents') >= beforePoll + 3 && calls('/agents') <= beforePoll + 8, '持续刷新次数失去边界');
  assert.equal(calls('/overview'), beforeOverview, '轮询重复读取仪表盘');
  assert.equal(calls('/enrollment-tokens'), beforeEnrollment, '轮询重复读取接入记录');
  assert.equal(row('alpha'), firstRow, '持续刷新重建批量结果');
  assert.ok(form.isConnected && choose('alpha').checked && choose('bravo').checked, '刷新丢失结果或选择');
  assert.equal(location.hash, route); assert.equal(body.scrollTop, scroll, '刷新改变滚动位置');
  for (const [index, id] of ['alpha', 'bravo'].entries()) assert.equal(row(id).lastElementChild.textContent, failedResults[index], '刷新丢失失败详情');
  nativeBatchButton('重试提交', row('alpha')).click(); nativeBatchButton('重试提交', row('bravo')).click(); form.requestSubmit();
  await waitFor(() => writes() === next + 1, '失败重试未提交'); await delay(30);
  assert.equal(writes(), next + 1, '重试未阻止并行提交');
  assert.ok(form.elements.action.matches(':disabled') && choose('bravo').matches(':disabled') && submit.matches(':disabled'), '重试未锁定所有操作');
  testAPI.pendingTasks[next++].fail('alpha retry still failing'); await idle();
  nativeBatchButton('重试提交', row('alpha')).click();
  await waitFor(() => writes() === next + 1, '重试失败后不能再次重试');
  testAPI.pendingTasks[next++].ok({ id: 'task-alpha-retry' }); await idle();
  testAPI.agents[1].status = 'offline'; await refresh();
  const retry = nativeBatchButton('重试提交', row('bravo'));
  assert.ok(retry.disabled, '离线节点重试未禁用');
  retry.dispatchEvent(new MouseEvent('click')); await idle(); assert.equal(writes(), next, '失效重试仍实际提交');
  testAPI.agents[0].runtime['sing-box'].installed = false;
  await refresh();
  await testBatchCoreUpdates({ testAPI, form });
  const shared = { ...onlineAgent('shared-batch'), can_manage: false, features: ['shared-core-lifecycle-v1'],
    shared_allocation: { enabled: true, status: 'accepted', owner_id: 'owner-one' } };
  shared.runtime.mihomo.instance = { id: 'instance-one', created: true, deployed_version: 1 };
  testAPI.agents = [shared];
  const sharedForm = await openNativeBatch(fixture);
  sharedForm.elements.action.value = 'restart'; sharedForm.elements.action.dispatchEvent(new Event('change', { bubbles: true }));
  const sharedBefore = writes(); sharedForm.requestSubmit();
  const sharedConfirm = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(item => item !== sharedForm.closest('dialog')), '共享批量操作没有确认');
  shared.runtime.mihomo.instance.id = 'instance-two';
  nativeBatchButton('执行批量操作', sharedConfirm).click(); await idle();
  assert.equal(writes(), sharedBefore, '确认后把操作转到另一个共享实例');
  assert.ok(sharedForm.textContent.includes('目标实例已变化'), '实例变化没有提示');
  const current = await openNativeBatch(fixture);
  for (const action of ['upgrade-agent', 'install']) {
    current.elements.action.value = action; current.elements.action.dispatchEvent(new Event('change', { bubbles: true }));
    assert.ok(current.elements['batch:shared-batch'].disabled, '共享批量操作允许管理宿主');
    current.requestSubmit(); await idle(); assert.equal(writes(), sharedBefore, '共享批量操作提交宿主任务');
  }
}
