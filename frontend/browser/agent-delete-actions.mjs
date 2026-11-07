import { assert, waitFor, delay } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';

export async function testAgentDeleteActions({ testAPI, onlineAgent }) {
  testAPI.agents = [{ ...onlineAgent('alpha'), status: 'offline' }, onlineAgent('bravo')];
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), '删除未结束');
  const open = async id => { await idle(); button('查看设置', document.querySelector(`.a-probe[data-agent-id="${id}"]`)).click(); return await waitFor(() => modal()?.querySelector('h2')?.textContent.includes('节点设置') && modal(), '删除节点设置未打开'); };
  let settings = await open('alpha');
  assert.ok(settings.textContent.includes('离线'), '离线删除测试状态错误');
  const remove = () => button('删除节点', settings);
  const writes = () => testAPI.calls.filter(call => call.method === 'DELETE' && call.path === '/agents/alpha').length;
  const confirm = async () => {
    remove().click();
    await waitFor(() => modal()?.querySelector('h2')?.textContent === '删除节点', '删除缺少确认');
    assert.ok(modal().textContent.includes('QAgent 不会被远程卸载'), '删除确认误导远程卸载');
    return modal();
  };
  const before = writes();
  button('取消', await confirm()).click(); await waitFor(() => !remove().disabled, '取消删除未解锁');
  assert.equal(writes(), before, '取消删除仍发送请求');
  testAPI.deleteFailure = true; button('删除节点', await confirm()).click();
  await waitFor(() => document.querySelector('.a-toast.error')?.textContent.includes('服务暂不可用') && !remove().disabled, '失败未反馈或解锁');
  assert.ok(testAPI.agents.some(agent => agent.id === 'alpha'), '失败移除节点');
  let release; testAPI.deleteGate = new Promise(resolve => { release = resolve; });
  const originalControl = remove(); button('删除节点', await confirm()).click();
  await waitFor(() => writes() === before + 2, '删除重试未发送');
  assert.ok(originalControl.disabled && originalControl.getAttribute('aria-busy') === 'true', '删除期间未锁定');
  assert.ok(originalControl.textContent.includes('正在删除'), '删除进行状态未显示');
  originalControl.dispatchEvent(new MouseEvent('click'));
  assert.equal(document.querySelectorAll('dialog[open]').length, 1, '删除期间再次打开确认');
  button('刷新', document.querySelector('.a-top')).click();
  location.hash = '#client-access'; await delay(30);
  assert.equal(modal(), settings, '进行中的删除被刷新或导航中断');
  assert.equal(writes(), before + 2, '重复点击发送额外删除');
  release(); testAPI.deleteGate = null; await idle();
  assert.ok(!originalControl.disabled && testAPI.agents.some(agent => agent.id === 'alpha'), '迟到失败未恢复重试');
  testAPI.deleteFailure = false;
  let releaseRead; testAPI.agentsGate = new Promise(resolve => { releaseRead = resolve; });
  button('删除节点', await confirm()).click();
  await waitFor(() => !settings.isConnected && [...document.querySelectorAll('.a-toast')].some(item => item.textContent.includes('节点已删除')), '刷新阻塞时删除成功没有立即呈现');
  assert.ok(!document.querySelector('.a-probe[data-agent-id=alpha]'), '成功删除仍显示节点');
  assert.ok(originalControl.disabled && !originalControl.hasAttribute('aria-busy'), '成功删除仍在提交或恢复按钮');
  releaseRead(); testAPI.agentsGate = null; await idle(); assert.equal(writes(), before + 3);
  settings = await open('bravo');
  const control = remove(), timeout = AbortSignal.timeout;
  let duration; testAPI.deleteHang = true;
  AbortSignal.timeout = milliseconds => { duration = milliseconds; return timeout.call(AbortSignal, 50); };
  try {
    button('删除节点', await confirm()).click();
    await waitFor(() => document.querySelector('.a-toast.error')?.textContent.includes('请求超时') && !control.disabled, '无响应删除未超时或解锁');
    assert.ok(duration > 30000 && duration < 60000, '删除期限未协调服务端与连接期限');
    assert.ok(document.querySelector('.a-toast.error').textContent.includes('刷新确认结果'), '超时未提示核实结果');
    assert.ok(testAPI.agents.some(agent => agent.id === 'bravo'), '超时被当作成功');
  } finally { AbortSignal.timeout = timeout; testAPI.deleteHang = false; }
  testAPI.agentsFailure = true;
  button('删除节点', await confirm()).click();
  await waitFor(() => [...document.querySelectorAll('.a-toast.error')].some(item => item.textContent.includes('节点已删除，但列表刷新失败')), '刷新失败掩盖删除成功');
  assert.ok(!settings.open && control.disabled && !control.hasAttribute('aria-busy'), '成功后刷新失败仍允许重复删除');
  // close() removes modal access synchronously; its close event, which removes
  // the retired dialog from the document, runs in a later browser task.
  await waitFor(() => !settings.isConnected, '删除成功后已关闭的节点设置未移除');
  assert.ok(!testAPI.agents.some(agent => agent.id === 'bravo'), '刷新失败恢复已删除节点');
  testAPI.agentsFailure = false;
}
