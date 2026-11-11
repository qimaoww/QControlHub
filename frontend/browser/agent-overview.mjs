import { assert, waitFor, delay } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';

export async function testAgentOverview({ testAPI }) {
  await startNativeNodes();
  await testNativeAddressRefresh(testAPI);
  const button = text => [...document.querySelectorAll('button')].find(item => item.textContent === text);
  const card = id => document.querySelector(`.a-probe[data-agent-id="${id}"]`);
  const flag = await waitFor(() => card('alpha').querySelector('img[src="/api/v1/region-flags/cn"]'), '节点地区没有统一 SVG 旗帜');
  await waitFor(() => flag.complete && flag.naturalWidth > 0, '节点旗帜没有载入');
  assert.equal(flag.parentNode.getAttribute('aria-label'), '中国台湾', '节点地区无可读名称');
  assert.equal(flag.parentNode.title, '中国台湾 (TW)', '节点地区缺少名称及代号');
  await waitFor(() => card('bravo').querySelector('img[src="/api/v1/region-flags/sg"]'), '内联地区未渲染');
  assert.ok(!testAPI.calls.some(call => call.path === '/agents/bravo/region'), '已有内联地区仍逐节点查询');
  const network = card('alpha').querySelector('.a-probe-network');
  assert.ok(network.textContent.includes('/s') && network.textContent.includes('累计'), '节点丢失速率或累计流量');
  const stableCard = card('alpha'), search = document.querySelector('input[name=search]');
  search.focus(); search.value = 'ALPHA'; search.dispatchEvent(new Event('input', { bubbles: true }));
  search.setSelectionRange(1, 3);
  assert.equal(document.activeElement, search, `筛选前未取得焦点：${document.activeElement?.outerHTML}`);
  const filteredCard = card('alpha');
  testAPI.agents[0].metrics.network_rx_bps = 2048;
  testAPI.agents[0].metrics.network_rx_bytes = 2 ** 30;
  button('刷新').click();
  await waitFor(() => card('alpha').querySelector('.a-probe-network').textContent.includes('2\u00a0KB/s'), '网络速率未刷新');
  assert.equal(card('alpha'), filteredCard, '刷新重建节点卡片');
  assert.equal(document.activeElement, search, `刷新丢失搜索焦点：${document.activeElement?.outerHTML}`);
  assert.equal(search.selectionStart, 1, '刷新丢失文本选择');
  assert.equal(search.value, 'ALPHA', '刷新重置筛选');
  assert.ok(card('alpha').textContent.includes('累计 1\u00a0GB'), '累计收发量未刷新');
  assert.ok(stableCard.isConnected, '搜索重建已存在卡片');
  search.value = ''; search.dispatchEvent(new Event('input', { bubbles: true }));
  button('列表').click();
  await waitFor(() => document.querySelector('.a-probe-list'), '列表视图未切换');
  assert.ok(document.querySelector('.a-probe-list').textContent.includes('累计接收'), '列表缺少累计流量');
  await testNetworkLayout(testAPI);
  await testNodePageSizes(testAPI);
  await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '视图保存未结束');
  testAPI.nodeView.revision++;
  button('卡片').click();
  await waitFor(() => !document.querySelector('#app[aria-busy=true]') && document.querySelector('.a-toast.error')?.textContent.includes('冲突'), '视图并发冲突未反馈');
  assert.ok(document.querySelector('.a-probe-list'), '冲突未采用服务端当前视图');
  button('卡片').click(); await waitFor(() => card('alpha'), '卡片视图未恢复');
  assert.equal(testAPI.nodeView.view, 'cards', '视图未保存到 API');
  const stable = card('alpha');
  testAPI.agentsFailure = true; button('刷新').click();
  await waitFor(() => document.querySelector('[role=status]')?.textContent.includes('更新失败'), '读取失败未保留状态');
  assert.equal(card('alpha'), stable, '失败清空有效节点');
  await waitFor(() => !button('刷新').disabled, '失败后刷新未解锁');
  testAPI.agentsFailure = false; button('刷新').click();
  await waitFor(() => document.querySelector('[role=status]')?.textContent === '正在实时更新', '节点读取未恢复');
  await delay(20);
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, '节点列表横向溢出');
}

async function testNativeAddressRefresh(testAPI) {
  const original = testAPI.agents[0];
  const clipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
  let copied;
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async value => { copied = value; } } });
  const card = () => document.querySelector('.a-probe[data-agent-id="alpha"]');
  const row = (root, family) => root.querySelector(`[data-ip-family="${family}"]`);
  const address = (root, family) => row(root, family)?.querySelector('[dir=ltr]').textContent;
  const update = async (labels, metrics, ready) => {
    testAPI.agents[0] = { ...original, labels, metrics: { ...original.metrics, ...metrics }, features: [...original.features, 'public-ip-probe-v1'] };
    await waitFor(ready, 'native public address refresh did not converge');
  };
  let settings;
  try {
    const stable = card();
    const hostname = 'node-' + 'edge'.repeat(18) + '.example.invalid';
    await update({ ...original.labels, client_address: hostname }, { public_ipv4: '198.35.26.96', public_ipv6: '' }, () => card().textContent.includes(`手动连接地址：${hostname}`));
    assert.equal(card(), stable, 'address refresh replaced the node card');
    assert.ok(card().scrollWidth <= card().clientWidth + 1, 'long manual hostname overflows its node card');
    assert.equal(address(card(), 'v4'), '198.35.26.96', 'manual hostname replaced the verified IP family');
    assert.ok(!row(card(), 'v6'), 'unavailable IPv6 rendered as a detected address');
    assert.equal(row(card(), 'v4').title, 'Agent 本地直连探测', 'probe origin missing');
    card().querySelector('[aria-label="复制 ALPHA 的 IPv4：198.35.26.96"]').click();
    await waitFor(() => copied === '198.35.26.96', 'native copy used the hostname instead of IPv4');
    [...card().querySelectorAll('button')].find(button => button.textContent === '查看设置').click();
    settings = await waitFor(() => document.querySelector('dialog[open] input[name=name]')?.closest('dialog'), 'node address details missing');
    assert.ok(!settings.querySelector('[data-ip-family],.a-probe-network,.a-probe-meters'), '节点设置重复列表信息');
    settings.querySelector('[aria-label=关闭]').click();
    await waitFor(() => !settings.isConnected, 'node settings did not close'); settings = null;
    await update({ ...original.labels, client_address: '93.184.216.34', public_ip: '2001:4860:4860::8888' }, { public_ipv4: '198.35.26.96', public_ipv6: '' }, () => address(card(), 'v4') === '93.184.216.34' && address(card(), 'v6') === '2001:4860:4860::8888');
    assert.equal(row(card(), 'v4').title, '手动设置', 'manual source not identified');
    assert.equal(row(card(), 'v6').title, '节点公网 IP');
    assert.ok(!card().textContent.includes('手动连接地址：'), 'manual literal duplicated as a hostname note');
    row(card(), 'v4').querySelector('button').click();
    await waitFor(() => copied === '93.184.216.34', 'in-place copy retained the old IP');
    assert.ok(row(card(), 'v4').querySelector('button').getAttribute('aria-label').endsWith('93.184.216.34'), 'copy accessible label retained stale IP');
    const stableAddress = row(card(), 'v4');
    await update(original.labels, { public_ipv4: '::ffff:0101:0101', public_ipv6: '' }, () => address(card(), 'v4') === '1.1.1.1');
    assert.equal(row(card(), 'v4'), stableAddress, 'address refresh replaced the existing family row');
    await update(original.labels, { public_ipv4: '172.69.135.152', observed_public_ip: '93.184.216.34', public_ipv6: '' }, () => address(card(), 'v4') === '93.184.216.34');
    assert.equal(row(card(), 'v4').title, '已验证连接来源', 'relay probe did not use the verified connection fallback');
    await update(original.labels, { public_ipv4: '104.22.17.83', observed_public_ip: '', public_ipv6: '' }, () => !row(card(), 'v4') && !row(card(), 'v6'));
    assert.ok(card().textContent.includes('暂无公网 IP'), 'relay-only addresses masquerade as public IPs');
    assert.ok(!card().querySelector('.a-ip-copy'), 'unavailable address retains a stale copy target');
    await update(original.labels, { public_ipv4: '198.35.26.96', public_ipv6: '2001:4860:4860::8888' }, () => address(card(), 'v4') === '198.35.26.96' && !!row(card(), 'v6'));
    assert.equal(row(card(), 'v6').title, 'Agent 本地直连探测', 'IPv6 source missing');
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1 && card().scrollWidth <= card().clientWidth + 1, 'address content overflows');
  } finally {
    settings?.close();
    testAPI.agents[0] = original;
    if (clipboard) Object.defineProperty(navigator, 'clipboard', clipboard); else delete navigator.clipboard;
  }
  const refresh = [...document.querySelectorAll('.a-top button')].find(button => button.textContent === '刷新');
  refresh.click();
  await waitFor(() => !refresh.disabled && address(card(), 'v4') === original.metrics.public_ipv4, 'address fixture did not restore');
}

async function testNetworkLayout(testAPI) {
  const original = structuredClone(testAPI.agents[0]);
  const rates = () => document.querySelector('.a-probe-rates');
  const stable = rates(), row = stable.closest('tr');
  const geometry = () => [row.getBoundingClientRect().height, ...[...row.cells].map(cell => cell.getBoundingClientRect().width)];
  const initial = geometry();
  const refresh = async () => {
    document.dispatchEvent(new Event('visibilitychange'));
    await delay(150);
  };
  for (const value of [0, 999, 1024, 1024 ** 3, 2 ** 64 - 1]) {
    Object.assign(testAPI.agents[0].metrics, { network_rx_bps: value, network_tx_bps: value, network_rx_bytes: value, network_tx_bytes: value });
    await refresh();
    await waitFor(() => rates().textContent.includes(value === 0 ? '0\u00a0B' : value === 999 ? '999\u00a0B' : value === 1024 ? '1\u00a0KB' : value === 1024 ** 3 ? '1\u00a0GB' : 'TB'), '收发单位或数值未更新');
    assert.equal(rates(), stable, '数值更新替换网络统计');
    assert.equal(JSON.stringify(geometry()), JSON.stringify(initial), '数值增长导致表格布局跳动');
    assert.equal(rates().children.length, 4, '缺少收发统计');
    const edges = [...rates().children].map(pair => {
      assert.equal(pair.children.length, 2, '统计标签与数值未分开');
      const [label, value] = [...pair.children].map(item => item.getBoundingClientRect());
      assert.ok(label.right <= value.left, '统计标签与数值重叠');
      assert.ok(value.right <= rates().getBoundingClientRect().right + 1, '统计数值溢出');
      return value.right;
    });
    assert.ok(edges.every(edge => Math.abs(edge - edges[0]) < 1), '速率与累计数值未对齐');
  }
  testAPI.agents[0].metrics.network_rx_bytes = undefined;
  testAPI.agents[0].metrics.network_tx_bytes = 0;
  await refresh();
  await waitFor(() => rates().children[2].lastChild.textContent === '—', '缺失累计值被当作零');
  assert.equal(rates().children[3].lastChild.textContent, '0\u00a0B', '真实零值被当作缺失');
  testAPI.agents[0].status = 'offline';
  await refresh();
  await waitFor(() => rates().children[0].lastChild.textContent === '—', '离线节点仍显示实时速率');
  assert.equal(rates().children[3].lastChild.textContent, '0\u00a0B', '离线清空已上报累计值');
  testAPI.agents[0].metrics.network_available = false;
  await refresh();
  await waitFor(() => [...rates().children].every(pair => pair.lastChild.textContent === '—'), '不可用指标仍显示累计值');
  testAPI.agents[0] = original;
  await refresh();
  await waitFor(() => rates().children[2].lastChild.textContent !== '—', '收发测试未恢复数据');
}

async function testNodePageSizes(testAPI) {
  const original = testAPI.agents;
  const button = text => [...document.querySelectorAll('button')].find(control => control.textContent === text && control.checkVisibility());
  const picker = () => document.querySelector('.a-main [name=page_size]');
  const rows = () => [...document.querySelectorAll('.a-probe-list tbody tr')];
  const refresh = async count => {
    button('刷新').click();
    await waitFor(() => !button('刷新').disabled && rows().length === count, 'node page size did not settle');
  };
  await waitFor(() => !picker().disabled, 'node preference did not load');
  assert.equal([...picker().options].map(value => value.value).join(','), '30,50,100,200');
  for (const count of [0,1,29,30,31,61]) {
    testAPI.agents = Array.from({length:count}, (_,index) => ({...structuredClone(original[0]), id:`page-node-${index}`, name:`分页节点 ${index}`}));
    await refresh(Math.min(30,count));
    assert.ok(picker().checkVisibility(), 'single or empty node page hides page size');
    if (count > 30) {
      const first = rows().map(row => row.dataset.refreshKey);
      button('下一页').click();
      assert.equal(rows().length, Math.min(30,count-30));
      assert.ok(rows().every(row => !first.includes(row.dataset.refreshKey)), 'node pages repeat records');
      button('上一页').click();
    }
  }
  button('下一页').click(); button('下一页').click();
  assert.equal(rows().length, 1, 'node final page wrong');
  testAPI.agents = testAPI.agents.slice(0,29); await refresh(29);
  const choose = async size => {
    picker().value=String(size); picker().dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(() => !picker().disabled, 'node preference save did not settle');
  };
  await choose(50); assert.equal(testAPI.pageSizes.nodes.page_size,50);
  testAPI.pageSizeFailure=true; await choose(100); assert.equal(picker().value,'50');
  testAPI.pageSizeFailure=false; testAPI.pageSizes.nodes.revision++;
  await choose(100); assert.equal(picker().value,'50','conflict overwrote newer preference');
  await choose(100); assert.equal(testAPI.pageSizes.nodes.page_size,100);
  testAPI.agents=original;
  await startNativeNodes();
  await waitFor(() => document.querySelector('.a-probe-list') && picker().value==='100', 'node preference did not survive reentry');
  await choose(30);
}
