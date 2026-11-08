import { assert, delay, waitFor } from './assertions.mjs';
import { nativeBatchButton, selectBatch } from './agent-batch-actions.mjs';

export async function testBatchCoreUpdates({ testAPI, form }) {
  const box = form.closest('dialog');
  const change = (name, value) => { form.elements[name].value = value; form.elements[name].dispatchEvent(new Event('change', { bubbles: true })); };
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), '版本提交未结束');
  const alpha = form.elements['batch:alpha'];
  for (const input of form.querySelectorAll('input[type=checkbox]')) selectBatch(input, false);
  change('action', 'install'); change('engine', 'sing-box');
  assert.ok(alpha.disabled, '批量更新不能安装未安装的内核');
  change('engine', 'mihomo'); selectBatch(alpha);
  assert.ok(form.elements.custom_version.closest('.a-field').hidden, '稳定版不应显示指定版本');
  assert.ok(form.elements['batch:charlie'].disabled, '离线节点仍允许更新内核');
  const confirm = async label => {
    form.requestSubmit();
    const dialog = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(item => item !== box), '内核更新没有确认');
    assert.ok(dialog.textContent.includes(label), '确认没有目标版本');
    if (form.elements.action.value === 'install') assert.ok(dialog.textContent.includes('重启'), '更新未提示重启');
    nativeBatchButton('执行批量操作', dialog).click();
  };
  let next = testAPI.pendingTasks.length;
  for (const channel of ['stable', 'development']) {
    change('release_channel', channel); await confirm(channel === 'stable' ? '最新稳定版' : '最新开发版');
    await waitFor(() => testAPI.pendingTasks.length === next + 1, '版本任务未提交');
    const expected = { agent_id: 'alpha', action: 'install', engine: 'mihomo', core_version: channel, ...(channel === 'development' ? { core_source: 'official' } : {}), expected_instance_id: '' };
    assert.equal(JSON.stringify(testAPI.pendingTasks[next].payload), JSON.stringify(expected), '版本任务参数错误');
    assert.ok(form.elements.release_channel.matches(':disabled'), '版本选项未锁定');
    testAPI.pendingTasks[next++].ok({ id: `core-${channel}` }); await idle();
    assert.ok(form.textContent.includes('已提交 1，提交失败 0'), '提交汇总错误');
  }
  change('release_channel', 'custom');
  assert.ok(form.elements.custom_version.required && !form.checkValidity(), '空指定版本未阻止提交');
  change('custom_version', '   '); form.requestSubmit(); await idle(); await delay(20);
  assert.equal(testAPI.pendingTasks.length, next, '空白版本仍提交');
  assert.equal(document.querySelectorAll('dialog[open]').length, 1, '空白版本仍打开确认');
  change('custom_version', ' v1.19.29 '); await confirm('v1.19.29');
  await waitFor(() => testAPI.pendingTasks.length === next + 1, '指定版本未提交');
  const original = { ...testAPI.pendingTasks[next].payload };
  assert.equal(original.core_version, 'v1.19.29');
  assert.ok(form.elements.custom_version.matches(':disabled'), '指定版本未锁定');
  testAPI.pendingTasks[next++].fail('temporary core update failure'); await idle();
  change('custom_version', 'v9.9.9'); change('action', 'restart');
  assert.ok(form.elements.release_channel.closest('.a-field').hidden && !form.elements.custom_version.required, '普通服务操作仍要求版本');
  const row = alpha.closest('tr'); nativeBatchButton('重试提交', row).click();
  await waitFor(() => testAPI.pendingTasks.length === next + 1, '更新失败重试未提交');
  assert.equal(JSON.stringify(testAPI.pendingTasks[next].payload), JSON.stringify(original), '重试未保留原动作及版本');
  testAPI.pendingTasks[next++].ok({ id: 'core-retry' }); await idle();
  await confirm('重启'); await waitFor(() => testAPI.pendingTasks.length === next + 1, '服务操作未提交');
  assert.equal(JSON.stringify(testAPI.pendingTasks[next].payload), JSON.stringify({ agent_id: 'alpha', action: 'restart', engine: 'mihomo', expected_instance_id: '' }), '普通服务操作携带版本参数');
  testAPI.pendingTasks[next].ok({ id: 'restart-after-core' }); await idle();
}
