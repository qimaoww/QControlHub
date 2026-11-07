import { assert, waitFor, delay } from './assertions.mjs';
import { startNativeNodes, openCoreSettings } from './agent-detail-actions.mjs';

const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
const fits = root => {
  assert.ok(document.documentElement.scrollWidth <= innerWidth, '节点页面横向溢出');
  const rect = root.getBoundingClientRect();
  assert.ok(rect.left >= 0 && rect.right <= innerWidth + 1 && root.scrollWidth <= root.clientWidth + 1, '节点或弹窗被裁切');
};
const settings = async id => {
  button('查看设置', document.querySelector(`.a-probe[data-agent-id="${id}"]`)).click();
  return await waitFor(() => modal()?.textContent.includes('节点设置') && modal(), '节点设置未加载');
};
const close = async box => { box.querySelector('[aria-label=关闭]').click(); await waitFor(() => !box.isConnected, '节点设置未关闭'); };

export async function testEmptyRuntime() {
  await startNativeNodes();
  assert.ok(document.querySelector('.a-empty')?.textContent.includes('暂无节点'), '空节点缺少空状态');
  assert.ok(button('添加节点'), '有权限用户缺少添加节点入口');
  assert.equal(document.querySelector('dialog'), null, '空列表自动展开添加节点');
  assert.equal(document.querySelector('.a-probe-batch form'), null, '空列表直接显示批量表单');
  fits(document.querySelector('.a-main'));
}

export async function testSharedNodeRuntime({ mode, testAPI, populatedAgents }) {
  await startNativeNodes();
  const shared = document.querySelector('.a-probe[data-agent-id="alpha"]');
  assert.ok(shared.textContent.includes('本人共享实例') && shared.textContent.includes('在线'), '共享节点缺少归属或状态');
  assert.equal([...document.querySelectorAll('.a-probe')].filter(card => card.textContent.includes('本人共享实例')).length, 1, '自有节点被标记为共享');
  assert.ok(button('添加节点'), '共享接收者无法添加自有节点');
  if (mode.endsWith('mobile')) { assert.equal(innerWidth, 390); assert.ok(matchMedia('(pointer:coarse)').matches); }
  fits(shared);
  const box = await settings('alpha'); fits(box);
  assert.equal(box.querySelectorAll('[data-core-installed]').length, 1, '共享详情显示未授权内核');
  openCoreSettings(box);
  const core = box.querySelector('[data-refresh-key=mihomo]');
  assert.ok(core.querySelector('a[href*="live-config"]'), '共享内核缺少独立配置入口');
  assert.ok(!button('重启服务', core).disabled && !button('停止服务', core).disabled, '共享接收者不能操作本人实例');
  assert.ok(core.textContent.includes('运行 · 有修改待部署'), '共享详情丢失保存与运行配置差异');
  assert.ok(!box.querySelector('[data-engine-capability],input[name=name],[data-agent-timezone]'), '共享详情暴露宿主设置');
  for (const label of ['国家／地区', '卸载内核', '升级 Agent', '删除节点', '查看安装命令', '保存名称']) assert.ok(!button(label, box), `共享详情暴露宿主管理：${label}`);
  assert.ok(box.textContent.includes('所有者') && box.textContent.includes('端口'), '共享详情缺少授权范围');
  await close(box);
  const own = await settings('bravo'); fits(own); openCoreSettings(own);
  assert.ok(own.querySelector('input[name=name]') && button('卸载内核', own), '自有节点丢失管理入口');
  assert.ok(!own.textContent.includes('本人共享实例'), '自有节点显示共享身份');
  await close(own);
  let reopened = await settings('alpha'); openCoreSettings(reopened);
  const beforeTasks = testAPI.submittedTasks.length;
  button('重启服务', reopened.querySelector('[data-refresh-key=mihomo]')).click();
  const confirmation = await waitFor(() => modal() !== reopened && modal()?.textContent.includes('重启服务') && modal(), '共享实例重启没有确认');
  testAPI.agents[0].runtime.mihomo.instance.id = 'reallocated-instance';
  button('重启服务', confirmation).click();
  await waitFor(() => !reopened.isConnected && !document.querySelector('#app[aria-busy=true]'), '实例变化后仍保留旧设置');
  assert.equal(testAPI.submittedTasks.length, beforeTasks, '旧设置操作了重新分配的共享实例');
  reopened = await settings('alpha');
  testAPI.agents = [populatedAgents[1]];
  button('刷新', document.querySelector('.a-top')).click();
  await waitFor(() => !reopened.isConnected && !document.querySelector('.a-probe[data-agent-id="alpha"]'), '撤销后仍保留共享详情或操作');
}

export async function testReadonlyRuntime({ testAPI }) {
  await startNativeNodes();
  assert.ok(!button('添加节点') && !button('批量操作'), '只读用户可添加或批量操作节点');
  const box = await settings('alpha'); openCoreSettings(box);
  assert.equal(box.querySelector('input[name=name]'), null, '只读用户可修改节点名称');
  assert.ok(button('卸载内核', box)?.disabled, '只读用户可卸载内核');
  assert.ok(!button('国家／地区', box), '只读用户可修改国家／地区');
  for (const form of box.querySelectorAll('form')) form.requestSubmit();
  await delay(20);
  assert.ok(!testAPI.calls.some(call => call.method === 'PUT' && /\/(name|time-zone)$/.test(call.path)), '只读用户提交节点设置');
  assert.ok(!testAPI.calls.some(call => call.path === '/enrollment-tokens'), '无权限时读取添加记录');
  await close(box);
  location.hash = '#client-access';
  await waitFor(() => document.querySelector('[data-client-agent]'), '只读客户端页未渲染');
  assert.ok(!button('修改显示参数'), '只读用户可修改客户端参数');
  await waitFor(() => document.querySelector('[data-client-agent] .a-probe-flag img[src="/api/v1/region-flags/cn"]'), '只读客户端国家／地区旗帜缺失');
  fits(document.querySelector('.a-main'));
}
