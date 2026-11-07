import { assert, delay, waitFor } from './assertions.mjs';
import { testEnrollmentLayoutRuntime } from './enrollment.mjs';
import { nativeBatchButton } from './agent-batch-actions.mjs';

export async function testAgentEnrollmentActions(fixture) {
  const { testAPI } = fixture;
  testAPI.enrollmentRecords = [];
  await testEnrollmentLayoutRuntime(fixture);
  assert.ok(!document.querySelector('dialog[open]'), '添加或命令弹窗未关闭');
  location.hash = '#dashboard';
  await waitFor(() => document.querySelector('.a-top h1')?.textContent === '仪表盘' && !document.querySelector('#app[aria-busy=true]'), '离开节点未完成');
  location.hash = '#node-settings';
  await waitFor(() => document.querySelector('.a-probe-toolbar') && !document.querySelector('#app[aria-busy=true]'), '节点往返后未恢复');
  assert.ok(!document.querySelector('.a-probe-batch form,dialog[open]'), '路由往返自动打开表单');
  const reads = testAPI.calls.filter(call => call.path === '/enrollment-tokens/enr-alpha/command').length;
  const created = testAPI.calls.filter(call => call.path === '/enrollment-tokens' && call.method === 'POST').length;
  nativeBatchButton('添加节点').click();
  const record = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => nativeBatchButton('安装命令', box)), '往返后添加记录丢失');
  nativeBatchButton('安装命令', record).click();
  const command = await waitFor(() => document.querySelector('dialog[open] pre'), '往返后无法恢复命令');
  assert.match(command.textContent, /browser-test-enrollment/);
  assert.equal(document.querySelectorAll('dialog[open]').length, 1, '恢复命令叠加弹窗');
  await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '恢复命令未结束');
  command.closest('dialog').querySelector('[aria-label=关闭]').click(); await delay(20);
  assert.equal(testAPI.calls.filter(call => call.path === '/enrollment-tokens/enr-alpha/command').length, reads + 1);
  assert.equal(testAPI.calls.filter(call => call.path === '/enrollment-tokens' && call.method === 'POST').length, created, '路由往返恢复命令创建新凭据');
}
