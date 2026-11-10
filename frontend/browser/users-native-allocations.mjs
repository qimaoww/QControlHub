import { assert, waitFor, delay } from './assertions.mjs';

export async function testNativeAllocations({ button, dialog, input, row, modal, confirm, fits, restart, session }) {
  const nativeFetch = globalThis.fetch;
  const agent = id => ({ id, name: `${id} 节点`, capabilities: ['mihomo', 'xray'] });
  const directory = ['owned', 'shared', 'other', 'new'].map(agent);
  let access = { revision: 4, isolated: true, owned_agent_ids: ['owned'], shares: [
    { id: 's1', agent_id: 'shared', enabled: true, status: 'accepted', engines: ['mihomo', 'ss-rust'], ports: [31001], limit_bytes: 1024 ** 3, used_bytes: 123, http_domains: ['example.test'] },
    { id: 's2', agent_id: 'other', enabled: false, status: 'rejected', engines: ['mihomo'], ports: [31002], limit_bytes: 15250032, used_bytes: 87, http_domains: ['other.test'] },
  ] };
  const same = (actual, expected, message) => assert.equal(JSON.stringify(actual), JSON.stringify(expected), message);
  const writes = [];
  let failRead = false, failBobRead = false, failSave = false, hold = false, release, holdRead = false, releaseRead;
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (url, options = {}) => {
    const path = String(url).replace('/api/v1', '');
    if (path === '/agent-directory') return reply(directory);
    if (path === '/users/bob/agent-access') return failBobRead ? reply({ error: '另一用户读取失败' }, 500) : reply({ revision: 1, owned_agent_ids: [], shares: [] });
    if (path !== '/users/alice/agent-access') return nativeFetch(url, options);
    if (!options.method || options.method === 'GET') {
      const result = structuredClone(access);
      if (holdRead) await new Promise(resolve => { releaseRead = resolve; });
      return failRead ? reply({ error: '分配读取失败' }, 500) : reply(result);
    }
    const body = JSON.parse(options.body); writes.push(body);
    if (hold) await new Promise(resolve => { release = resolve; });
    if (failSave || body.revision !== access.revision) return reply({ error: '分配已变更' }, 409);
    access = { ...access, ...body, revision: access.revision + 1, shares: body.shares.map(value => {
      const old = access.shares.find(share => share.agent_id === value.agent_id);
      const consent = value.reinvite || !old?.enabled && value.enabled || value.engines.some(engine => !old?.engines.includes(engine));
      return { ...old, ...value, status: consent ? 'pending' : old?.status, id: old?.id || 's3' };
    }) };
    return reply(access);
  };
  const allocationRow = id => [...dialog().querySelectorAll('tbody tr')].find(item => item.cells[0]?.textContent === `${id} 节点`);
  const form = () => dialog()?.querySelector('form');
  const close = async () => { dialog().querySelector('[aria-label=关闭]').click(); await waitFor(() => !dialog(), '共享弹窗未关闭'); };
  const idle = () => waitFor(() => !document.querySelector('#native-users-test[aria-busy=true]'), '前次写入未结束');
  const revoke = async id => {
    await idle();
    const control = await waitFor(() => {
      const target = allocationRow(id);
      const control = target && button('撤销共享', target);
      return control && !control.disabled && control;
    }, `撤销共享未就绪：${id}`);
    control.click();
  };
  const open = async () => { await idle(); button('共享资源', row('alice')).click(); await waitFor(() => dialog()?.dataset.userAllocation === 'alice', '共享资源未打开'); };
  try {
    await open();
    const savedDialog = dialog(), savedUserRow = row('alice');
    button('刷新', document.querySelector('.a-top')).click(); await delay(30);
    assert.equal(dialog(), savedDialog, '刷新关闭共享资源弹窗');
    assert.equal(row('alice'), savedUserRow, '无变化刷新替换用户行');
    assert.ok(dialog().textContent.includes('自有节点') && allocationRow('other').textContent.includes('已撤销'), '已保存结果未区分自有节点或撤销状态');
    button('分配节点', dialog()).click();
    same([...form().elements.agent_id.options].map(option => option.value), ['', 'new'], '已分配或自有节点可以重复分配');
    button('取消', dialog()).click();
    await idle(); button('编辑分配', allocationRow('shared')).click(); fits();
    if (new URLSearchParams(location.search).get('preview') === '1') await new Promise(() => {});
    assert.ok(form().querySelector('[name=engines][value="ss-rust"]:checked'), '不可用的已有内核被静默删除');
    input(form().elements.ports, '31007, invalid'); form().requestSubmit();
    await waitFor(() => form().textContent.includes('请填写单个端口'), '端口错误未反馈');
    const draftForm = form(), portInput = form().elements.ports;
    input(form().elements.limit, '2.5');
    form().querySelector('[name=engines][value=xray]').click();
    portInput.focus(); portInput.setSelectionRange(1, 4);
    button('刷新', document.querySelector('.a-top')).click(); await delay(30);
    assert.equal(form(), draftForm, '刷新替换共享编辑器');
    assert.equal(document.activeElement, portInput, '刷新丢失分配输入焦点');
    assert.equal(portInput.selectionStart, 1, '刷新丢失文本选择');
    await close(); await open();
    assert.equal(form().elements.ports.value, '31007, invalid', '重开丢失分配草稿');
    assert.equal(form().elements.limit.value, '2.5', '重开丢失小数额度草稿');
    assert.ok(form().querySelector('[name=engines][value=xray]').checked, '重开丢失内核草稿');
    await close(); failBobRead = true; await idle(); button('共享资源', row('bob')).click();
    await waitFor(() => !document.querySelector('#native-users-test[aria-busy=true]') && [...document.querySelectorAll('dialog[open] [role=alert]')].some(item => item.textContent.includes('另一用户读取失败')), '另一用户读取失败没有反馈');
    assert.ok(dialog()?.querySelector('h2').textContent.includes('bob') && !dialog().querySelector('[name=ports]'), '读取失败显示了其他用户的编辑器');
    await close();
    failBobRead = false; await open();
    assert.equal(form().elements.ports.value, '31007, invalid', '另一用户读取失败清除当前草稿');
    await close(); await idle(); button('共享资源', row('bob')).click();
    await waitFor(() => dialog()?.dataset.userAllocation === 'bob', '另一用户共享未打开');
    assert.ok(!form() && dialog().textContent.includes('暂无记录'), '未分配用户继承其他账号的结果或草稿');
    assert.ok(!dialog().textContent.includes('false') && !dialog().querySelector('details'), '没有自有节点时出现布尔值或空的自有节点区域');
    button('分配节点', dialog()).click();
    same([...form().elements.agent_id.options].map(option => option.value), ['', 'owned', 'shared', 'other', 'new'], '候选节点沿用了其他用户的归属或分配');
    assert.ok(!form().querySelector('[name=engines]:checked'), '新分配默认扩大内核授权');
    await close(); await open();
    assert.equal(form().elements.ports.value, '31007, invalid', '跨用户切换丢失无效草稿');
    assert.equal(form().elements.limit.value, '2.5', '跨用户切换丢失小数额度');
    assert.ok(form().querySelector('[name=engines][value=xray]').checked, '跨用户切换丢失内核授权草稿');
    input(form().elements.ports, '31007');
    access.revision++; access.shares[0].ports = [31008];
    form().requestSubmit();
    await waitFor(() => form().textContent.includes('分配已变更'), '旧草稿未保留 CAS');
    assert.equal(writes.at(-1).revision, 4, '草稿采用了新版本');
    button('重新读取', dialog()).click(); await modal('重新读取'); button('取消', dialog()).click(); await delay(20);
    assert.equal(form().elements.ports.value, '31007', '取消重新读取丢弃草稿');
    assert.ok(!form().querySelector('[type=submit]').disabled, '取消重新读取没有解锁');
    failRead = true; button('重新读取', dialog()).click(); await confirm('重新读取');
    await waitFor(() => dialog().textContent.includes('分配读取失败'), '读取失败未反馈');
    assert.equal(form().elements.ports.value, '31007', '读取失败丢失输入');
    failRead = false; button('重新读取', dialog()).click(); await confirm('重新读取');
    await waitFor(() => !form(), '重新读取未释放编辑器');
    assert.ok(allocationRow('shared').textContent.includes('31008'), '读取后结果未更新');
    assert.ok(!dialog().querySelector('.a-dialog-body').textContent.includes('分配读取失败'), '读取成功保留旧错误');
    await idle(); button('编辑分配', allocationRow('shared')).click();
    input(form().elements.limit, '2.5');
    failSave = true; form().requestSubmit();
    await waitFor(() => form().textContent.includes('分配已变更'), '保存失败未反馈');
    assert.equal(form().elements.limit.value, '2.5', '保存失败丢失额度');
    failSave = false; hold = true; release = null; form().requestSubmit(); form().requestSubmit();
    await waitFor(() => release, '分配保存未发出'); const count = writes.length;
    dialog().querySelector('[aria-label=关闭]').click(); assert.ok(form(), '提交期间关闭表单');
    const submitting = dialog(); location.hash = '#my-quota'; await delay(20);
    assert.equal(dialog(), submitting, '导航中断正在保存的分配');
    assert.ok(form().querySelector('[type=submit]').disabled, '导航解锁正在保存的分配');
    release(); hold = false;
    await waitFor(() => !form(), '分配保存未完成');
    assert.equal(writes.length, count, '重复提交分配');
    const changed = access.shares[0], untouched = access.shares[1];
    assert.equal(changed.limit_bytes, 2.5 * 1024 ** 3, '小数 GiB 额度未精确保存');
    assert.equal(changed.status, 'accepted', '普通修改重发邀请');
    assert.equal(changed.used_bytes, 123, '保存清除了用量');
    same(changed.engines, ['mihomo', 'ss-rust'], '保存丢失不可用内核');
    same(untouched.http_domains, ['other.test'], '单节点编辑丢失其他节点域名');
    assert.equal(untouched.enabled, false, '单节点编辑重新启用了其他节点');
    assert.equal(untouched.limit_bytes, 15250032, '单节点编辑改变另一节点的精确额度');
    assert.equal(untouched.used_bytes, 87, '单节点编辑清除另一节点的用量');
    assert.ok(!writes.at(-1).shares.find(share => share.agent_id === 'other').reinvite, '单节点编辑重发其他节点的邀请');
    await idle(); button('编辑分配', allocationRow('shared')).click();
    const xray = form().querySelector('[name=engines][value=xray]'); xray.checked = true; xray.dispatchEvent(new Event('change', { bubbles: true }));
    assert.ok(form().textContent.includes('用户需重新接受'), '授权扩大缺少同意说明');
    form().requestSubmit(); await waitFor(() => !form(), '授权扩大未保存');
    assert.equal(access.shares[0].status, 'pending', '授权扩大跳过同意');
    failSave = true;
    await idle(); button('撤销共享', allocationRow('shared')).click(); await confirm('撤销共享');
    await waitFor(() => dialog()?.textContent.includes('分配已变更') && !button('撤销共享', allocationRow('shared')).disabled, '撤销失败未反馈或未解锁');
    assert.ok(access.shares[0].enabled, '撤销失败更改共享状态');
    failSave = false; hold = true; release = null;
    await idle(); button('撤销共享', allocationRow('shared')).click(); await confirm('撤销共享');
    await waitFor(() => release, '撤销未提交');
    const revoking = dialog(), revocationWrites = writes.length;
    button('刷新', document.querySelector('.a-top')).click(); await delay(20);
    assert.equal(dialog(), revoking, '刷新中断正在撤销的共享');
    button('撤销共享', allocationRow('shared')).click();
    assert.equal(writes.length, revocationWrites, '撤销允许重复提交');
    release(); hold = false;
    await waitFor(() => allocationRow('shared').textContent.includes('已撤销'), '撤销结果未显示');
    assert.ok(!dialog().querySelector('.a-dialog-body').textContent.includes('分配已变更'), '撤销成功保留旧错误');
    same(access.shares[0].ports, [31008], '撤销丢失端口预留');
    assert.equal(access.shares[0].used_bytes, 123, '撤销清除了用量');
    for (const previousStatus of ['pending', 'accepted']) {
      access.shares[0].enabled = true; access.shares[0].status = previousStatus; access.revision++;
      await idle(); button('重新读取', dialog()).click();
      await waitFor(() => !button('重新读取', dialog()).disabled, '恢复邀请场景未重读');
      const previous = structuredClone(access.shares[0]);
      await revoke('shared'); await confirm('撤销共享');
      await waitFor(() => allocationRow('shared').textContent.includes('已撤销'), '原邀请无法撤销');
      const beforeRestore = writes.length, revision = access.revision;
      button('重新邀请', allocationRow('shared')).click();
      assert.equal(writes.length, beforeRestore, '打开恢复邀请立即写入');
      await close(); button('共享资源', row('bob')).click();
      await waitFor(() => dialog()?.dataset.userAllocation === 'bob', '恢复邀请无法切换到另一用户');
      assert.ok(!form(), '另一用户继承恢复邀请');
      await close(); await open();
      assert.ok(dialog().querySelector('h2').textContent.includes('重新邀请'), '关闭重开丢失恢复邀请意图');
      form().requestSubmit(); await waitFor(() => !form(), '恢复邀请未保存');
      const restored = access.shares[0], request = writes.at(-1);
      assert.ok(request.revision === revision && request.shares[0].enabled && request.shares[0].reinvite === false, '恢复非拒绝邀请使用错误的版本或重发标记');
      assert.equal(restored.status, 'pending', '恢复邀请跳过用户同意');
      for (const name of ['ports', 'engines', 'used_bytes', 'limit_bytes', 'http_domains']) same(restored[name], previous[name], `恢复 ${previousStatus} 邀请改变 ${name}`);
    }
    await idle(); button('编辑分配', allocationRow('other')).click(); input(form().elements.limit, '3');
    form().requestSubmit(); await waitFor(() => !form(), '被拒绝的分配不能编辑');
    assert.equal(access.shares[1].status, 'rejected', '普通修改重新发送被拒绝邀请');
    assert.ok(!writes.at(-1).shares[1].reinvite, '普通修改带上重新邀请标记');
    await idle(); button('重新邀请', allocationRow('other')).click();
    assert.ok(form().textContent.includes('用户接受邀请后'), '重新邀请缺少同意说明');
    form().requestSubmit(); await waitFor(() => !form(), '重新邀请未保存');
    assert.equal(access.shares[1].status, 'pending', '重新邀请未等待用户同意');
    assert.ok(writes.at(-1).shares.find(share => share.agent_id === 'other').reinvite, '重新邀请未显式发送');
    assert.ok(!writes.at(-1).shares.find(share => share.agent_id === 'shared').reinvite, '重新邀请污染其他节点');
    await revoke('shared'); await confirm('撤销共享');
    await waitFor(() => allocationRow('shared').textContent.includes('已撤销'), '释放端口前未撤销');
    button('编辑分配', allocationRow('shared')).click(); input(form().elements.ports, '');
    form().requestSubmit(); await waitFor(() => !form(), '撤销后无法释放端口');
    assert.ok(!access.shares[0].enabled && access.shares[0].ports.length === 0, '释放端口自动恢复共享');
    for (const [text, ports, label] of [['0', [0], '无限制'], ['', [], '未分配'], ['21001-21003', [21001, 21002, 21003], '21001-21003']]) {
      await idle(); button('编辑分配', allocationRow('shared')).click();
      input(form().elements.ports, text); form().requestSubmit();
      await waitFor(() => !form(), '端口模式未保存');
      same(access.shares[0].ports, ports, '端口模式提交改变了空值、无限制或范围语义');
      assert.ok(allocationRow('shared').textContent.includes(label), '已保存列表未显示端口模式');
      assert.equal(access.shares[0].limit_bytes, 2.5 * 1024 ** 3, '端口模式改变额度');
      assert.ok(writes.at(-1).isolated, '保存关闭了资源隔离');
    }
    await idle(); button('编辑分配', allocationRow('shared')).click();
    for (const [text, message] of [['21000-21256', '256'], ['21000-21002,21001', '重叠'], ['10084-10087', '保留'], ['21100-21000', '起始'], ['0,21001', '混用']]) {
      const before = writes.length;
      input(form().elements.ports, text); form().requestSubmit();
      await waitFor(() => form().textContent.includes(message) && !form().querySelector('[type=submit]').disabled, `无效端口范围缺少提示：${text}`);
      assert.equal(writes.length, before, '无效端口范围提交到 API');
    }
    button('取消', dialog()).click();
    const beforeRevoke = writes.length;
    await revoke('other'); await modal('撤销共享');
    button('取消', dialog()).click(); await delay(20);
    assert.equal(writes.length, beforeRevoke, '取消撤销仍提交');
    assert.ok(access.shares[1].enabled, '取消撤销使共享失效');
    const beforeStaleConfirm = writes.length;
    await revoke('other'); await modal('撤销共享');
    const staleConfirm = button('撤销共享', dialog());
    await restart({ ...session, user_id: 'confirmation-admin' });
    staleConfirm.dispatchEvent(new MouseEvent('click')); await delay(20);
    assert.equal(writes.length, beforeStaleConfirm, '旧账号确认撤销了以前的共享');
    await open();
    await idle(); fits(); await close();
    directory[1].name = `香港边缘节点 · ${'long-node-name-without-spaces'.repeat(6)}`;
    access.shares[0].http_domains = [`${'verylonghostname'.repeat(12)}.example.test`];
    await open(); fits();
    const savedTable = dialog().querySelector('.a-table-scroll');
    if (innerWidth <= 640) assert.ok(savedTable.scrollWidth <= savedTable.clientWidth + 1, '手机分配列表仍需横向滚动');
    button('编辑分配', dialog().querySelector('tbody tr')).click(); fits();
    assert.ok(dialog().querySelector('[aria-label=关闭]').getBoundingClientRect().right <= innerWidth, '长节点标题挤走关闭按钮');
    assert.equal(form().elements.domains.value, access.shares[0].http_domains[0], '长域名被截断或改变');
    await close();

    // A completed read from a previous browser lifetime must not replace a
    // new editor, even when both administrators are editing the same user.
    await open(); holdRead = true; releaseRead = null;
    button('重新读取', dialog()).click();
    await waitFor(() => releaseRead, '延迟重新读取未开始');
    button('重新读取', dialog()).click();
    assert.ok(button('重新读取', dialog()).disabled, '读取期间允许重复请求');
    const readingDialog = dialog(); readingDialog.querySelector('[aria-label=关闭]').click();
    assert.equal(dialog(), readingDialog, '读取期间关闭了当前分配');
    await restart({ ...session, user_id: 'another-admin' }); holdRead = false;
    await open(); button('编辑分配', dialog().querySelector('tbody tr')).click();
    input(form().elements.ports, '31111'); const newEditor = form();
    releaseRead(); await delay(30);
    assert.equal(form(), newEditor, '旧账号读取替换当前编辑器');
    assert.equal(form().elements.ports.value, '31111', '旧账号读取覆盖当前草稿');
    button('取消', dialog()).click(); await close();


    await open(); button('编辑分配', dialog().querySelector('tbody tr')).click();
    input(form().elements.limit, '3'); hold = true; release = null; form().requestSubmit();
    await waitFor(() => release, '延迟保存未开始');
    await restart(session); hold = false;
    await open(); button('编辑分配', dialog().querySelector('tbody tr')).click();
    input(form().elements.ports, '31222'); const nextEditor = form();
    const notices = document.querySelectorAll('.a-toast').length;
    release(); await delay(30);
    assert.equal(form(), nextEditor, '旧账号保存替换当前编辑器');
    assert.equal(form().elements.ports.value, '31222', '旧账号保存清除当前草稿');
    assert.equal(document.querySelectorAll('.a-toast').length, notices, '旧账号保存泄漏通知');
    button('取消', dialog()).click(); await close();
    await open(); button('编辑分配', dialog().querySelector('tbody tr')).click();
    input(form().elements.ports, '31222, draft-before-user-delete'); await close();
    const allocationFetch = globalThis.fetch;
    return async () => {
      const previousFetch = globalThis.fetch; globalThis.fetch = allocationFetch;
      try {
        await open();
        assert.ok(!form(), '删除用户后恢复了旧分配草稿');
        await close();
      } finally { globalThis.fetch = previousFetch; }
    };
  } finally { globalThis.fetch = nativeFetch; }
}
