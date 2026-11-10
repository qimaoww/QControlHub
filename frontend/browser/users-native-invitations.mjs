import { startAnalytics } from '../analytics/app.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testNativeInvitations() {
  window.dispatchEvent(new Event('pagehide'));
  const nativeFetch = globalThis.fetch;
  let session = { role: 'user', user_id: 'recipient', username: 'recipient', permissions: ['agent-access.read'], csrf_token: 'invitation-fixture' };
  let access = { revision: 4, isolated: true, shares: [
    { id: 'pending', agent_id: 'shared', agent_name: '<img src=x onerror=alert(1)>', owner_username: 'owner <img src=x onerror=alert(1)>', enabled: true, status: 'pending', invitation_revision: 2, engines: ['mihomo', 'xray'], ports: Array.from({ length: 101 }, (_, i) => 21000 + i), limit_bytes: 1024 ** 3, used_bytes: 64 },
    { id: 'revoked', agent_id: 'revoked', agent_name: '已撤销节点', enabled: false, status: 'pending', invitation_revision: 8, engines: [], ports: [], limit_bytes: 0, used_bytes: 0 },
  ] };
  const writes = [];
  let hold = false, release, failRead = false, conflict = false, holdRead = false, releaseRead, retainedFailures = 0;
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (url, options = {}) => {
    if (!String(url).startsWith('/api/v1')) return nativeFetch(url, options);
    const path = String(url).slice(7), method = options.method || 'GET';
    if (path === '/auth/session') return reply(session);
    if (path === '/tasks/failures' && method === 'POST') {
      assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), session.csrf_token, '失败留存缺少 CSRF');
      return reply({id:`fixture-failure-${++retainedFailures}`});
    }
    if (path === '/agent-access') {
      if (failRead) return reply({ error: '邀请读取失败' }, 503);
      const value = structuredClone(access);
      if (holdRead) await new Promise(resolve => { releaseRead = resolve; });
      return reply(value);
    }
    if (path === '/agent-access/pending/response' && method === 'POST') {
      assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), session.csrf_token, '邀请响应缺少 CSRF');
      const body = JSON.parse(options.body); writes.push(body);
      const target = access;
      if (hold) await new Promise(resolve => { release = resolve; });
      if (conflict || body.revision !== target.shares[0].invitation_revision) return reply({ error: '共享邀请已变更' }, 409);
      target.shares[0].status = body.decision === 'accept' ? 'accepted' : 'rejected';
      target.shares[0].invitation_revision++; target.revision++;
      return reply(target);
    }
    throw new Error(`Unexpected invitation API: ${method} ${path}`);
  };
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const dialog = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const cards = () => document.querySelector('.a-main');
  const boot = async () => {
    window.dispatchEvent(new Event('pagehide'));
    document.body.replaceChildren(Object.assign(document.createElement('div'), { id: 'native-invitations-test' }));
    history.replaceState(null, '', '#my-quota');
    startAnalytics({ app: document.querySelector('#native-invitations-test'), session });
    await waitFor(() => cards()?.textContent.includes('我使用的共享与额度') && !cards()?.querySelector('[aria-busy=true]'), '邀请页面未加载');
  };
  const open = async () => { button('查看邀请', cards()).click(); await waitFor(() => dialog()?.textContent.includes('共享邀请'), '邀请详情未打开'); };
  const close = async () => { dialog().querySelector('[aria-label=关闭]').click(); await waitFor(() => !dialog(), '邀请未关闭'); };
  const reload = async () => {
    const control = dialog() ? button('重新读取', dialog()) : button('刷新', document.querySelector('.a-top'));
    control.click(); await waitFor(() => !control.disabled, '邀请读取未结束');
  };
  const fits = () => {
    assert.ok(document.documentElement.scrollWidth <= innerWidth, '邀请页面横向溢出');
    if (dialog()) assert.ok(dialog().scrollWidth <= dialog().clientWidth + 1, '邀请弹窗溢出');
  };
  try {
    await boot();
    assert.ok(cards().textContent.includes('待接受') && cards().textContent.includes('已撤销'), '邀请或撤销状态不正确');
    assert.ok(!button('接受邀请', cards()) && !button('拒绝邀请', cards()), '邀请决策未移入详情');
    assert.ok(!cards().querySelector('img'), '节点名称未转义');
    const revoked = [...cards().querySelectorAll('.a-card')].find(card => card.querySelector('h2')?.textContent === '已撤销节点');
    assert.ok(revoked && !button('查看邀请', revoked) && !button('共享详情', revoked), '已撤销邀请仍可以接受或使用');
    assert.ok(cards().textContent.includes('21000-21100'), '连续端口未压缩');
    if (new URLSearchParams(location.search).get('preview') === 'invitations') { access.shares[0].agent_name = '香港 · 边缘节点'; access.shares[0].owner_username = 'alice'; access.shares[0].limit_bytes = 100 * 1024 ** 3; access.shares[0].used_bytes = 6.4 * 1024 ** 3; await reload(); }
    await open(); fits();
    if (new URLSearchParams(location.search).get('preview') === 'invitations') await new Promise(() => {});
    assert.ok(dialog().textContent.includes('mihomo / xray') && !dialog().querySelector('img'), '邀请条款内核或转义不正确');
    const stableDialog = dialog(), stableTerms = dialog().querySelector('dl'), stableCard = cards().querySelector('.a-grid > .a-card');
    const range = document.createRange(); range.selectNodeContents(stableTerms.querySelector('dd'));
    getSelection().removeAllRanges(); getSelection().addRange(range); const selected = getSelection().toString();
    button('刷新', document.querySelector('.a-top')).click(); await delay(30);
    assert.equal(dialog(), stableDialog, '页面刷新替换邀请弹窗');
    assert.equal(dialog().querySelector('dl'), stableTerms, '页面刷新重建邀请条款');
    assert.equal(cards().querySelector('.a-grid > .a-card'), stableCard, '页面刷新重建邀请卡片');
    assert.equal(getSelection().toString(), selected, '页面刷新丢失条款文本选择');
    await close(); assert.equal(writes.length, 0, '关闭邀请产生决策');
    await open(); dialog().dispatchEvent(new Event('cancel', { cancelable: true }));
    await waitFor(() => !dialog(), 'Escape 未关闭邀请'); assert.equal(writes.length, 0, 'Escape 改变了邀请');
    for (const [ports, label] of [[[0], '无限制'], [[], '未分配']]) {
      access.shares[0].ports = ports; access.shares[0].invitation_revision++;
      await reload(); await open();
      assert.ok(dialog().textContent.includes(label), '邀请丢失空端口或无限制权限模式');
      await close();
    }
    await open(); hold = true;
    button('接受邀请', dialog()).click();
    await waitFor(() => release, '邀请响应未提交');
    button('拒绝邀请', dialog()).click();
    assert.equal(writes.length, 1, '允许重复邀请响应');
    assert.ok(button('接受邀请', dialog()).disabled && button('拒绝邀请', dialog()).disabled && button('重新读取', dialog()).disabled, '邀请响应未锁定');
    dialog().querySelector('[aria-label=关闭]').click(); assert.ok(dialog(), '提交期间关闭邀请');
    const submittingDialog = dialog();
    button('刷新', document.querySelector('.a-top')).click(); await delay(20);
    assert.equal(dialog(), submittingDialog, '页面刷新中断了正在提交的邀请');
    release(); hold = false;
    await waitFor(() => !dialog() && cards().textContent.includes('已接受'), '接受未更新');
    button('共享详情', cards()).click();
    await waitFor(() => button('退出共享', dialog()), '已接受共享缺少退出入口');
    button('退出共享', dialog()).click();
    await waitFor(() => dialog()?.querySelector('h2')?.textContent === '退出共享', '退出无确认');
    button('取消', dialog()).click(); await delay(20);
    assert.equal(writes.length, 1, '取消退出仍写入');
    button('退出共享', dialog()).click();
    await waitFor(() => dialog()?.querySelector('h2')?.textContent === '退出共享', '退出确认未重开');
    button('退出共享', dialog()).click();
    await waitFor(() => !dialog() && cards().textContent.includes('已拒绝'), '退出共享未更新');
    access.shares[0].status = 'pending'; access.shares[0].invitation_revision++; await reload(); await open();
    conflict = true; button('接受邀请', dialog()).click();
    await waitFor(() => dialog()?.textContent.includes('共享邀请已变更'), '过期邀请缺少错误');
    assert.ok(!button('接受邀请', dialog()).disabled, '冲突未解锁');
    conflict = false; access.shares[0].ports = [21002]; access.shares[0].invitation_revision++;
    holdRead = true; button('重新读取', dialog()).click(); await waitFor(() => releaseRead, '邀请重新读取未开始');
    const before = writes.length; button('接受邀请', dialog()).click();
    button('拒绝邀请', dialog()).click();
    const reading = dialog(); reading.querySelector('[aria-label=关闭]').click();
    assert.ok(!dialog(), '普通读取阻止关闭邀请弹窗');
    assert.equal(writes.length, before, '读取期间对旧条款作出决策');
    holdRead = false; releaseRead();
    await waitFor(() => cards().textContent.includes('21002'), '新条款未更新卡片');
    assert.ok(!dialog(), '迟到读取重新打开了邀请弹窗');
    await open();
    await waitFor(() => dialog()?.textContent.includes('21002') && !button('重新读取', dialog()).disabled, '新条款未显示');
    button('拒绝邀请', dialog()).click();
    await waitFor(() => !dialog() && cards().textContent.includes('已拒绝'), '待接受邀请不能明确拒绝');
    assert.ok(!button('查看邀请', cards()) && !button('共享详情', cards()), '拒绝后仍提供共享操作');
    access.shares[0].status = 'pending'; access.shares[0].engines = []; access.shares[0].invitation_revision++; await reload(); await open();
    assert.ok(button('接受邀请', dialog()).disabled && !button('拒绝邀请', dialog()).disabled, '空内核邀请可以接受');
    failRead = true; await reload();
    assert.ok(dialog().textContent.includes('邀请读取失败') && button('接受邀请', dialog()).disabled && !button('拒绝邀请', dialog()).disabled, '读取失败清除条款或解除接受限制');
    failRead = false; await close();
    access.shares[0].engines = ['mihomo']; access.shares[0].invitation_revision++; await reload(); await open();
    hold = true; release = null; button('接受邀请', dialog()).click(); await waitFor(() => release, '账号切换测试未开始响应');
    session = { ...session, user_id: 'another', username: 'another' }; access = { revision: 1, shares: [] };
    await boot(); release(); hold = false; await delay(40);
    assert.ok(!dialog() && !cards().textContent.includes('21002') && cards().textContent.includes('暂无共享记录'), '旧账号响应污染新账号'); fits();
    access = { revision: 1, shares: [{ id: 'pending', agent_id: 'old-read-node', agent_name: '旧账号节点', enabled: true, status: 'pending', invitation_revision: 1, engines: ['mihomo'], ports: [22000] }] };
    await boot(); await open(); holdRead = true; releaseRead = null;
    button('重新读取', dialog()).click(); await waitFor(() => releaseRead, '旧账号读取未开始');
    session = { ...session, user_id: 'third', username: 'third' }; access = { revision: 1, shares: [] }; holdRead = false;
    await boot(); releaseRead(); await delay(30);
    assert.ok(!dialog() && !cards().textContent.includes('旧账号节点') && cards().textContent.includes('暂无共享记录'), '旧账号读取恢复过期邀请');
    const count = writes.length; location.hash = '#users';
    await waitFor(() => cards()?.textContent.includes('没有访问此页面的权限'), '普通用户可以打开用户管理');
    assert.ok(!button('创建用户', cards()), '普通用户暴露创建账号入口');
    assert.equal(writes.length, count, '普通用户访问用户管理发生写入');
  } finally { window.dispatchEvent(new Event('pagehide')); globalThis.fetch = nativeFetch; }
}
