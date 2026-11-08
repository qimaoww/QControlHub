import { assert, delay, waitFor } from './assertions.mjs';
import { nativeBatchButton, openNativeBatch, selectBatch } from './agent-batch-actions.mjs';

export async function testAgentBatchLayout(fixture) {
  const { testAPI, onlineAgent, populatedAgents } = fixture;
  const submittedBefore = testAPI.submittedTasks.length;
  testAPI.agents = populatedAgents.map(agent => structuredClone(agent));
  let form = await openNativeBatch(fixture);
  const check = dialog => {
    const bounds = dialog.getBoundingClientRect();
    assert.ok(bounds.left >= 0 && bounds.right <= innerWidth + 1 && bounds.top >= 0 && bounds.bottom <= innerHeight + 1, '批量弹窗超出屏幕');
    assert.ok(dialog.scrollWidth <= dialog.clientWidth + 1, '批量弹窗横向溢出');
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, '页面横向溢出');
  };
  const change = (name, value) => { form.elements[name].value = value; form.elements[name].dispatchEvent(new Event('change', { bubbles: true })); };
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), '取消确认没有解锁');
  for (const action of ['upgrade-agent', 'start', 'stop', 'restart', 'install']) {
    change('action', action);
    if (action === 'install') { change('release_channel', 'custom'); change('custom_version', 'v1.19.29'); }
    for (const input of form.querySelectorAll('input[type=checkbox]')) if (!input.disabled) selectBatch(input);
    check(form.closest('dialog'));
    for (const control of form.querySelectorAll('.a-field:not([hidden]) input:not([type=checkbox]),.a-field:not([hidden]) select'))
      assert.ok(control.getBoundingClientRect().width >= 100, '批量控件被挤压');
    const submit = form.querySelector('[type=submit]'); submit.scrollIntoView({ block: 'nearest' });
    const bounds = submit.getBoundingClientRect();
    assert.ok(bounds.top >= 0 && bounds.bottom <= innerHeight, '执行按钮不可达');
    form.requestSubmit();
    const confirm = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box !== form.closest('dialog')), '批量动作没有确认');
    check(confirm);
    assert.equal(document.activeElement, nativeBatchButton('取消', confirm), '确认未默认聚焦取消');
    assert.ok(confirm.textContent.includes('个节点'), '确认没有目标数量');
    if (action === 'stop') assert.ok(confirm.textContent.includes('连接会立即中断'), '停止缺少连接中断提示');
    if (action === 'install') assert.ok(confirm.textContent.includes('v1.19.29') && confirm.textContent.includes('重启'), '更新缺少版本或重启提示');
    nativeBatchButton('取消', confirm).click(); await idle();
  }
  assert.equal(testAPI.submittedTasks.length, submittedBefore, '取消仍提交任务');
  form.closest('dialog').querySelector('[aria-label=关闭]').click();
  const longName = '节点名称'.repeat(24) + '<img src=x onerror=alert(1)>';
  testAPI.agents.push(...Array.from({ length: 24 }, (_, index) => ({ ...onlineAgent(`extra-${index}`), name: `${longName}-${index}` })));
  form = await openNativeBatch(fixture); change('action', 'upgrade-agent');
  check(form.closest('dialog')); assert.equal(form.querySelector('td img'), null, '长节点名称未转义');
  form.requestSubmit();
  const confirm = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box !== form.closest('dialog')), '大量节点缺少确认');
  check(confirm); assert.ok(confirm.textContent.includes('26 个节点'), '大量节点确认数量错误');
  assert.ok(!confirm.textContent.includes(longName), '大量确认重复列出所有节点名称');
  const accept = nativeBatchButton('执行批量操作', confirm).getBoundingClientRect();
  assert.ok(accept.top >= 0 && accept.bottom <= innerHeight, '大量节点遮挡确认按钮');
  nativeBatchButton('取消', confirm).click(); await idle(); await delay(20);
  const batchBox = form.closest('dialog');
  const all = () => nativeBatchButton('全选', form) || nativeBatchButton('取消全选', form);
  const count = () => form.querySelector('[role=status]').textContent;
  assert.ok(count().includes('已选 26 / 可选 26'), '跨分页计数未包含全部节点');
  const next = () => nativeBatchButton('下一页', form);
  const previous = () => nativeBatchButton('上一页', form);
  next().click();
  assert.ok([...form.querySelectorAll('[name^="batch:"]')].every(input => input.checked), '第二页没有保留全选');
  const choice = form.querySelector('[name^="batch:"]'), choiceID = choice.name;
  choice.click(); assert.ok(count().includes('已选 25 / 可选 26'), '第二页取消未更新总计');
  previous().click(); next().click();
  assert.ok(!form.elements[choiceID].checked, '翻页丢失单项取消状态');
  all().click(); assert.ok(count().includes('已选 26 / 可选 26'), '第二页全选未覆盖其他分页');
  all().click(); previous().click();
  assert.ok(count().includes('已选 0 / 可选 26') && [...form.querySelectorAll('[name^="batch:"]')].every(input => !input.checked), '跨分页取消全选失败');
  all().click();
  change('action', 'install'); change('release_channel', 'custom'); change('custom_version', 'draft-version');
  const draft = form.elements.custom_version; draft.focus(); draft.setSelectionRange(1, 4);
  const page = form.querySelector('.a-pagination').textContent, scroll = batchBox.scrollTop;
  testAPI.agents.push(onlineAgent('new-batch-node'));
  testAPI.agents.find(agent => agent.id === 'charlie').status = 'online';
  await delay(2200);
  assert.equal(form.elements.custom_version, draft, '节点刷新替换参数');
  assert.equal(draft.value, 'draft-version'); assert.equal(document.activeElement, draft);
  assert.equal(draft.selectionStart, 1); assert.equal(draft.selectionEnd, 4);
  assert.equal(form.querySelector('.a-pagination').textContent.replace(/共 \d+ 条/, ''), page.replace(/共 \d+ 条/, ''), '刷新重置当前分页');
  assert.equal(batchBox.scrollTop, scroll, '刷新重置弹窗滚动');
  assert.ok(!form.elements['batch:charlie'].checked, '恢复在线的节点被自动加入选择');
  assert.equal(all().textContent, '全选', '新增节点被自动加入原全选');
  const originalFetch = window.fetch;
  window.fetch = async (url, options) => String(url).endsWith('/api/v1/agents') ? Response.json({error:'temporary refresh failure'}, {status:503}) : originalFetch(url, options);
  const selectedBefore = count();
  try {
    await delay(2200);
    assert.equal(count(), selectedBefore, '刷新失败清除有效选择'); assert.equal(draft.value, 'draft-version');
    assert.ok(form.textContent.includes('暂时无法核实'), '刷新失败没有明确状态');
  } finally { window.fetch = originalFetch; }
  testAPI.agents = testAPI.agents.filter(agent => agent.id !== 'alpha');
  await delay(2200);
  assert.ok(form.elements['batch:alpha'].disabled && !form.elements['batch:alpha'].checked, '撤权目标仍可选择');
  assert.ok(!form.textContent.includes('ALPHA'), '撤权仍显示旧节点名称');
  batchBox.querySelector('[aria-label=关闭]').click();
  // Both parent views keep their regular controls and ignore parent search.
  for (const view of ['列表', '卡片']) {
    nativeBatchButton(view).click(); await idle();
    const search = document.querySelector('.a-main [name=search]');
    search.value = '不存在的节点'; search.dispatchEvent(new Event('input', {bubbles:true})); search.dispatchEvent(new Event('change', {bubbles:true}));
    const launcher = nativeBatchButton('批量操作'); launcher.focus(); launcher.click();
    const reopened = await waitFor(() => document.querySelector('dialog[open] form'), '两种视图未直接打开弹窗');
    assert.ok(!document.querySelector('.a-main input[type=checkbox],.a-probe-batch'), '父视图残留旧选择');
    assert.ok(reopened.textContent.includes('全部授权节点') && reopened.querySelector('[name^="batch:"]'), '父页面搜索隐藏了执行范围');
    assert.ok(reopened.querySelector('[type=submit]').disabled, '重开残留旧选择');
    reopened.closest('dialog').querySelector('[aria-label=关闭]').click();
    assert.equal(document.activeElement, launcher, '关闭批量弹窗未恢复入口焦点');
    search.value = ''; search.dispatchEvent(new Event('input', {bubbles:true})); search.dispatchEvent(new Event('change', {bubbles:true}));
  }
  testAPI.agents = [];
  await delay(2200);
  nativeBatchButton('批量操作').click();
  const empty = document.querySelector('dialog[open] form');
  assert.ok(empty.textContent.includes('暂无授权节点') && nativeBatchButton('全选', empty).disabled && empty.querySelector('[type=submit]').disabled, '空节点仍能执行');
  empty.closest('dialog').querySelector('[aria-label=关闭]').click();
  testAPI.agents = [{...onlineAgent('offline-only'), status:'offline'}];
  await delay(2200); nativeBatchButton('批量操作').click();
  const unavailable = document.querySelector('dialog[open] form');
  assert.ok(unavailable.textContent.includes('已选 0 / 可选 0') && nativeBatchButton('全选', unavailable).disabled, '全部不可选时全选未禁用');

}
