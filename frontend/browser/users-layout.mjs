import { startAnalytics } from '../analytics/app.js';
import { assert, delay, waitFor } from './assertions.mjs';

export async function testUsersLayoutRuntime({ mode, testAPI }) {
  window.dispatchEvent(new Event('pagehide'));
  document.querySelectorAll('link[rel=stylesheet]').forEach(link => link.remove());
  const stylesheet = Object.assign(document.createElement('link'), { rel: 'stylesheet', href: '/assets/analytics/theme.css' });
  document.head.append(stylesheet);
  await new Promise((resolve, reject) => { stylesheet.onload = resolve; stylesheet.onerror = reject; });
  document.body.replaceChildren(Object.assign(document.createElement('div'), { id: 'app' }));
  history.replaceState(null, '', '#users');
  startAnalytics({ app: document.querySelector('#app'), session: await (await fetch('/api/v1/auth/session')).json() });
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const dialog = () => document.querySelector('dialog[data-user-allocation][open]');
  const form = () => dialog()?.querySelector('form');
  const row = id => [...dialog().querySelectorAll('tbody tr')].find(item => item.cells[0]?.textContent === testAPI.agents.find(agent => agent.id === id)?.name);
  const status = id => row(id)?.cells[3].textContent;
  const change = (element, value) => { element.value = value; element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })); };
  const idle = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), '共享操作未结束');
  const openUser = async id => {
    await idle();
    const user = [...document.querySelectorAll('.a-main tbody tr')].find(item => item.cells[0]?.textContent === id);
    button('共享资源', user).click();
    await waitFor(() => dialog()?.dataset.userAllocation === id, '用户分配未打开');
    await idle();
  };
  const edit = async id => { await idle(); button('编辑分配', row(id)).click(); };
  const fits = async () => {
    await Promise.all(dialog().getAnimations({ subtree: true }).filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, '用户页横向溢出');
    assert.ok(dialog().scrollWidth <= dialog().clientWidth + 1, '分配弹窗横向溢出');
    if (innerWidth <= 640 && !form()) {
      const table = dialog().querySelector('.a-table-scroll');
      assert.ok(table.scrollWidth <= table.clientWidth + 1, '手机分配结果的操作依赖横向滚动');
    }
    if (form()) {
      const submit = form().querySelector('[type=submit]'); submit.scrollIntoView({ block: 'nearest' });
      const bounds = submit.getBoundingClientRect();
      assert.ok(bounds.left >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight + 1, '分配保存操作不可达');
    }
  };
  const save = async () => {
    const before = testAPI.allocationWrites.length;
    form().requestSubmit();
    await waitFor(() => testAPI.allocationWrites.length === before + 1 && !form(), '分配未保存并返回列表');
    await idle(); return testAPI.allocationWrites.at(-1);
  };
  await waitFor(() => document.querySelector('.a-main tbody tr'), '新主题用户页面未载入');
  await openUser('cdn');
  assert.equal(form(), null, '已保存分配仍是批量编辑表单');
  assert.equal(dialog().querySelectorAll('tbody tr').length, 2, '已保存分配数量不正确');
  assert.ok(row('alpha').textContent.includes('未分配'), '空端口没有显示未分配');
  assert.equal(dialog().querySelector('input'), null, '已保存结果包含未提交编辑');
  assert.ok(!row('delta'), '自有节点被列入共享分配');
  const owned = dialog().querySelector('details');
  assert.ok(owned && !owned.open, '自有节点没有单独折叠');
  owned.querySelector('summary').click();
  assert.ok(owned.textContent.includes('自有节点'), '自有节点缺失');
  owned.querySelector('summary').click();
  if (mode.endsWith('-mobile')) {
    assert.equal(innerWidth, 390);
    assert.ok(matchMedia('(pointer:coarse)').matches, '手机测试没有启用触摸模拟');
  }
  await fits(); await edit('bravo');
  assert.equal(form().elements.limit.value, '50', '额度没有恢复为 GiB');
  assert.ok(dialog().querySelector('h2').textContent.includes('Tokyo Edge'), '编辑未标识目标节点');
  change(form().elements.limit, '75');
  button('取消', dialog()).click();
  assert.equal(testAPI.allocationWrites.length, 0, '取消保存了草稿');
  assert.ok(/50\sGB/.test(row('bravo').textContent) && !/75\sGB/.test(row('bravo').textContent), '取消后结果包含草稿额度');
  button('分配节点', dialog()).click();
  assert.ok(form().querySelector('[type=submit]').disabled, '没有选择节点时允许发送邀请');
  assert.ok(form().elements.engines === undefined, '尚未选择节点却展示内核授权');
  assert.equal(form().elements.agent_id.querySelector('[value=delta]'), null, '自有节点可以被重复分配');
  assert.equal(form().elements.agent_id.querySelector('[value=alpha]'), null, '已分配节点可以重复添加');
  change(form().elements.agent_id, 'charlie');
  assert.ok(!form().querySelector('[type=submit]').disabled, '选择节点后没有启用发送操作');
  assert.equal(form().querySelectorAll('[name=engines]:checked').length, 0, '新分配默认扩大内核授权');
  assert.equal(form().elements.limit.value, '0', '新分配的不限量语义改变');
  await fits(); form().requestSubmit();
  await waitFor(() => form().textContent.includes('请至少分配一个内核'), '没有内核时缺少校验');
  assert.equal(testAPI.allocationWrites.length, 0, '无效分配提交到了 API');
  form().querySelector('[name=engines][value=xray]').click();
  change(form().elements.limit, '2.5');
  assert.ok(form().elements.ports.placeholder.includes('21000-21100'), '缺少端口范围示例');
  for (const [ports, error] of [['21000-21256', '256'], ['21000-21002,21001', '重叠'], ['10084-10087', '保留'], ['21100-21000', '起始'], ['0,21001', '混用']]) {
    change(form().elements.ports, ports); form().requestSubmit();
    await waitFor(() => form().textContent.includes(error) && !form().querySelector('[type=submit]').disabled, `范围错误没有明确反馈：${ports}`);
    assert.equal(testAPI.allocationWrites.length, 0, '无效范围被保存');
  }
  change(form().elements.ports, '21000-21100, 22000');
  await fits();
  const saved = await save(), allocation = saved.shares.find(share => share.agent_id === 'charlie');
  assert.ok(saved.isolated && saved.user_id === 'cdn' && saved.revision === 4, '保存改变隔离、用户或并发保护');
  assert.equal(allocation.limit_bytes, 2.5 * 1024 ** 3, '小数 GiB 保存精度丢失');
  assert.equal(allocation.ports.join(','), [...Array.from({ length: 101 }, (_, index) => 21000 + index), 22000].join(','), '端口范围未按包含首尾的完整列表提交');
  assert.equal(allocation.engines.join(','), 'xray', '保存扩大了内核授权');
  assert.equal(saved.shares.find(share => share.agent_id === 'alpha').limit_bytes, 0, '其他分配的不限量语义改变');
  assert.equal(saved.shares.length, 3, '添加一个节点丢失其他分配');
  assert.equal(status('alpha'), '待接受'); assert.equal(status('charlie'), '待接受', '邀请被展示为已接受');
  assert.ok(button('分配节点', dialog()).disabled, '没有候选节点时仍能添加');
  assert.ok(row('charlie').textContent.includes('21000-21100, 22000'), '已保存范围被展开为长列表');
  await edit('charlie');
  assert.equal(form().elements.ports.value, '21000-21100, 22000', '重开丢失范围');
  button('取消', dialog()).click();
  assert.equal(testAPI.allocationWrites.length, 1, '关闭编辑意外保存');
  for (const [text, ports, label] of [['0', [0], '无限制'], ['', [], '未分配'], ['21001-21003', [21001, 21002, 21003], '21001-21003'], ['0', [0], '无限制']]) {
    await edit('charlie'); change(form().elements.ports, text);
    const saved = await save(), share = saved.shares.find(share => share.agent_id === 'charlie');
    assert.equal(JSON.stringify(share.ports), JSON.stringify(ports), '端口权限模式改变');
    assert.equal(share.limit_bytes, 2.5 * 1024 ** 3, '端口模式改变流量额度');
    assert.ok(row('charlie').textContent.includes(label), '端口模式显示错误');
    await edit('charlie'); assert.equal(form().elements.ports.value, text, '重开丢失端口模式');
    await fits(); button('取消', dialog()).click();
  }
  await edit('bravo'); change(form().elements.limit, '75'); await save();
  assert.equal(status('bravo'), '已接受', '修改额度重发了邀请');
  await edit('bravo'); change(form().elements.limit, '0');
  const unlimited = await save();
  assert.equal(unlimited.shares.find(share => share.agent_id === 'bravo').limit_bytes, 0, '不限量未按 API 的零值保存');
  assert.equal(unlimited.shares.find(share => share.agent_id === 'charlie').ports.join(','), '0', '修改额度清除另一节点无限制端口');
  await edit('bravo');
  assert.ok(!form().textContent.includes('用户需重新接受'), '未扩大内核时重复提示邀请');
  form().querySelector('[name=engines][value="sing-box"]').click();
  assert.ok(form().textContent.includes('用户需重新接受'), '扩大内核授权缺少同意说明');
  await save(); assert.equal(status('bravo'), '待接受', '扩大内核跳过同意');
  const beforeRevoke = testAPI.allocationWrites.length;
  button('撤销共享', row('alpha')).click();
  let confirmation = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box.querySelector('h2')?.textContent === '撤销共享'), '撤销缺少独立确认');
  assert.ok(confirmation.textContent.includes('Catixs HK') && confirmation.textContent.includes('端口预留与用量将保留'), '确认缺少对象或保留语义');
  assert.equal(testAPI.allocationWrites.length, beforeRevoke, '确认前已撤销');
  button('取消', confirmation).click(); await delay(20);
  assert.equal(testAPI.allocationWrites.length, beforeRevoke, '取消仍撤销');
  button('撤销共享', row('alpha')).click();
  confirmation = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box.querySelector('h2')?.textContent === '撤销共享'), '撤销确认无法重开');
  button('撤销共享', confirmation).click();
  await waitFor(() => status('alpha') === '已撤销', '确认后没有撤销');
  assert.equal(testAPI.allocationWrites.at(-1).shares.find(share => share.agent_id === 'alpha').enabled, false);
  button('重新邀请', row('alpha')).click();
  assert.equal(testAPI.allocationWrites.length, beforeRevoke + 1, '打开邀请就发送请求');
  await save(); assert.equal(status('alpha'), '待接受', '恢复共享跳过同意');
  const restored = testAPI.allocationWrites.at(-1).shares.find(share => share.agent_id === 'alpha');
  assert.ok(restored.enabled && !restored.reinvite, '恢复待接受共享误用已拒绝邀请标记');
  assert.equal(testAPI.calls.some(call => call.path === '/tasks' && call.method === 'POST'), false, '分配自行执行远程任务');
  dialog().querySelector('[aria-label=关闭]').click();
  await openUser('new-user');
  assert.equal(dialog().querySelectorAll('tbody tr').length, 0, '空用户出现其他账号的分配');
  button('分配节点', dialog()).click(); change(form().elements.agent_id, 'bravo');
  form().querySelector('[name=engines][value=mihomo]').click(); await save();
  assert.equal(testAPI.allocationWrites.at(-1).shares[0].ports.length, 0, '新分配留空隐式授予端口权限');
  assert.ok(row('bravo').textContent.includes('未分配'), '新分配留空没有显示未分配');
  assert.equal(dialog().querySelectorAll('tbody tr').length, 1, '首个邀请未替换空状态');
  assert.equal(testAPI.allocationWrites.at(-1).user_id, 'new-user', '分配写入上一用户');
  await fits();
  if (new URLSearchParams(location.search).has('preview')) await new Promise(() => {});
}
