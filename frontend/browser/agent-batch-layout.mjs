import { assert, delay, waitFor } from './assertions.mjs';
import { nativeBatchButton, openNativeBatch } from './agent-batch-actions.mjs';

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
  for (const action of ['upgrade-agent', 'start', 'stop', 'restart', 'status', 'install']) {
    change('action', action);
    if (action === 'install') { change('release_channel', 'custom'); change('custom_version', 'v1.19.29'); }
    for (const input of form.querySelectorAll('input[type=checkbox]')) if (!input.disabled) input.checked = true;
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
}
