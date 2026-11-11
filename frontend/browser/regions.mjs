import { assert, waitFor, delay } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';
import { nodeRegionFlag } from '../analytics/node-region.js';

export async function testRegionRuntime({ testAPI }) {
  await startNativeNodes();
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const card = () => document.querySelector('.a-probe[data-agent-id=alpha]');
  const trigger = () => card()?.querySelector('.a-region-trigger');
  const flag = (root, code) => root?.querySelector(`img[src="/api/v1/region-flags/${code}"]`);
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const form = () => modal()?.querySelector('form');
  const search = value => { form().elements.region_search.value = value; form().elements.region_search.dispatchEvent(new Event('input', { bubbles: true })); };
  const choice = code => form().querySelector(`[data-region-choice="${code}"]`);
  const navigate = async hash => {
    await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '节点操作未结束');
    await delay(30);
    location.hash = hash;
  };
  const open = async () => {
    trigger().click();
    await waitFor(() => form()?.elements.region_search && !form().querySelector('[type=submit]').disabled, '旗帜选择未打开');
  };
  const close = async changed => {
    modal().querySelector('[aria-label=关闭]').click();
    if (changed) { await waitFor(() => modal()?.querySelector('h2')?.textContent === '放弃更改', '取消未保存选择缺少确认'); button('放弃更改', modal()).click(); }
    await waitFor(() => !modal(), '旗帜选择未关闭');
  };
  await waitFor(() => flag(trigger(), 'cn'), '自动旗帜未显示');
  const initialHash = location.hash;
  await open(); assert.equal(location.hash, initialHash, '点击旗帜改变节点路由');
  const title = modal().querySelector('h2'), closeButton = modal().querySelector('[aria-label=关闭]');
  assert.ok(title.scrollWidth <= title.clientWidth + 1 && title.getBoundingClientRect().right <= closeButton.getBoundingClientRect().left, '标题被挤压或遮挡关闭操作');
  for (const [code, name] of [['BO','玻利维亚'],['ES','西班牙'],['MX','墨西哥'],['RS','塞尔维亚'],['SV','萨尔瓦多']]) {
    search(name); assert.ok(choice(code), `${name}不在搜索结果`); choice(code).click();
    await waitFor(() => [...form().querySelectorAll(`img[src="/api/v1/region-flags/${code.toLowerCase()}"]`)].some(image => image.complete && image.naturalWidth > 0), `${name}图片未加载`);
  }
  search('japan'); assert.equal(form().querySelectorAll('[data-region-choice]').length, 1, '英文搜索未筛选'); choice('JP').click();
  assert.ok(form().textContent.includes('当前选择 · 日本'), '选择预览未更新');
  testAPI.regionSaveFailure = true; form().requestSubmit();
  await waitFor(() => form().textContent.includes('服务暂不可用') && !form().querySelector('[type=submit]').disabled, '保存失败未提示或解锁');
  assert.equal(choice('JP').getAttribute('aria-pressed'), 'true', '保存失败丢失选择');
  assert.ok(flag(trigger(), 'cn'), '失败保存修改现有旗帜');
  testAPI.regionSaveFailure = false;
  let releaseSave; testAPI.regionSaveGate = new Promise(resolve => { releaseSave = resolve; });
  const before = testAPI.calls.filter(call => call.method === 'PUT' && call.path.endsWith('/region')).length;
  form().requestSubmit(); form().requestSubmit();
  assert.ok(choice('JP').matches(':disabled'), '提交未锁定选择');
  assert.equal(testAPI.calls.filter(call => call.method === 'PUT' && call.path.endsWith('/region')).length, before + 1, '重复提交旗帜');
  releaseSave(); await waitFor(() => !modal() && flag(trigger(), 'jp'), '保存旗帜未生效'); testAPI.regionSaveGate = null;
  await open(); assert.equal(choice('JP').getAttribute('aria-pressed'), 'true', '重开丢失已保存选项');
  button('自动识别', modal()).click(); await close(true); assert.ok(flag(trigger(), 'jp'), '取消仍保存自动模式');
  await navigate('#client-access');
  await waitFor(() => flag(document.querySelector('[data-client-agent]'), 'jp'), '客户端未使用手动旗帜');
  const header = document.querySelector('[data-client-agent] h2');
  assert.ok(header.contains(flag(header, 'jp')), '旗帜未放入卡片标题');
  const reads = testAPI.calls.filter(call => call.path === '/client-access').length;
  button('刷新', document.querySelector('.a-top')).click();
  await waitFor(() => testAPI.calls.filter(call => call.path === '/client-access').length > reads && flag(document.querySelector('[data-client-agent]'), 'jp'), '刷新丢失手动旗帜');
  await navigate('#node-settings'); await waitFor(() => trigger(), '未返回节点');
  await open(); button('自动识别', modal()).click(); form().requestSubmit();
  await waitFor(() => !modal() && flag(trigger(), 'cn'), '未恢复自动识别');
  assert.equal(testAPI.agents[0].labels.region_code, undefined, '恢复自动仍保留手动标签');
  await navigate('#client-access'); await waitFor(() => flag(document.querySelector('[data-client-agent]'), 'cn'), '客户端未恢复自动旗帜');
  // Clear only this lifetime's cache, then hold the real automatic lookup.
  window.dispatchEvent(new Event('pagehide'));
  let releaseLookup; testAPI.regionLookupGate = new Promise(resolve => { releaseLookup = resolve; });
  await startNativeNodes(); await open();
  assert.equal(form().elements.region_search.value, 'japan', 'reopened region search did not restore the account preference');
  search(''); choice('SG').click(); form().requestSubmit();
  await waitFor(() => !modal() && flag(trigger(), 'sg'), '延迟查询阻止手动保存');
  releaseLookup(); testAPI.regionLookupGate = null; await delay(100);
  assert.ok(flag(trigger(), 'sg'), '迟到自动查询覆盖手动旗帜');
  trigger().querySelector('img').dispatchEvent(new Event('error'));
  assert.ok(trigger().textContent.includes('SG'), '图片失败未显示地区代号');
  await open(); assert.equal(choice('SG').getAttribute('aria-pressed'), 'true', '图片失败丢失手动设置');
  search('no such region'); assert.ok(form().textContent.includes('没有匹配'), '无匹配缺少提示');
  assert.ok(form().textContent.includes('当前选择 · 新加坡'), '搜索丢失选择');
  search('香港'); assert.equal(form().querySelectorAll('[data-region-choice]').length, 1, '中文搜索未筛选');
  choice('HK').focus(); choice('HK').click(); assert.equal(choice('HK').getAttribute('aria-pressed'), 'true');
  await close(true);
  testAPI.regionCatalogFailure = true; trigger().click();
  await waitFor(() => form()?.textContent.includes('服务暂不可用'), '目录失败缺少提示');
  assert.ok(form().querySelector('[type=submit]').disabled, '目录读取失败允许保存');
  testAPI.regionCatalogFailure = false; button('重试加载', modal()).click();
  search('');
  await waitFor(() => choice('SG') && !form().querySelector('[type=submit]').disabled, '目录失败无法重试');
  assert.equal(choice('SG').getAttribute('aria-pressed'), 'true', '重试丢失选择');
  if (new URLSearchParams(location.search).has('preview')) {
    document.documentElement.dataset.regionPreview = 'ready';
    await new Promise(() => {});
  }
  await close(false);
  await testNativeRegionResolution();
}

async function testNativeRegionResolution() {
  const requests = [], state = { data: {} };
  let current = true, allowed = true, fail = false;
  const ctx = { state, current: () => current, can: () => allowed, api: async path => {
    requests.push(path);
    if (fail) throw new Error('temporary region failure');
    return { country_code: 'US' };
  } };
  const code = flag => flag.dataset.regionCode || '';
  const agents = Array.from({ length: 40 }, (_, index) => ({ id: `inline-${index}`, region_code: '', metrics: { public_ipv4: `8.8.8.${index + 1}` } }));
  for (const agent of agents) assert.equal(code(nodeRegionFlag(ctx, agent)), '', '未解析的内联地区误报旗帜');
  assert.equal(requests.length, 0, '内联空地区触发逐节点查询');
  nodeRegionFlag(ctx, agents[0]); nodeRegionFlag(ctx, { ...agents[0] });
  assert.equal(requests.length, 0, '重建节点重复查询内联结果');
  assert.equal(code(nodeRegionFlag(ctx, { ...agents[0], region_code: 'US' })), 'US', '新内联结果未替换未知地区');
  assert.equal(code(nodeRegionFlag(ctx, { ...agents[0], region_code: 'US', labels: { region_code: 'JP' } })), 'JP', '自动地区覆盖手动旗帜');
  const legacy = { id: 'legacy', metrics: { public_ipv4: '9.9.9.9' } };
  const cached = nodeRegionFlag(ctx, legacy); document.body.append(cached);
  await waitFor(() => code(cached) === 'US', '缺失地区字段未使用兼容查询');
  assert.equal(requests.join(','), '/agents/legacy/region', '兼容查询目标不正确');
  assert.equal(code(nodeRegionFlag(ctx, legacy)), 'US', '重建丢失已解析兼容结果');
  assert.equal(requests.length, 1, '重建重复查询兼容结果');
  assert.equal(code(nodeRegionFlag(ctx, { ...legacy, region_code: '' })), '', '当前空结果继承旧兼容地区');
  assert.equal(code(nodeRegionFlag(ctx, legacy)), 'US', '空字段污染独立兼容缓存');
  allowed = false;
  assert.equal(code(nodeRegionFlag(ctx, { id: 'denied' })), '', '无节点读取权限显示兼容结果');
  assert.equal(requests.length, 1, '无节点读取权限发起兼容查询'); allowed = true;
  state.data = {};
  nodeRegionFlag(ctx, legacy); await delay(0);
  assert.equal(requests.length, 2, '新账号继承旧地区缓存');
  fail = true; const failed = { id: 'retry-region' };
  nodeRegionFlag(ctx, failed); await delay(0); fail = false;
  const retry = nodeRegionFlag(ctx, failed); document.body.append(retry);
  await waitFor(() => code(retry) === 'US', '地区查询失败被永久缓存');
  assert.equal(requests.filter(path => path === '/agents/retry-region/region').length, 2, '地区重试次数错误');
  current = false; const late = nodeRegionFlag(ctx, { id: 'late-region' }); document.body.append(late);
  await delay(0); assert.equal(code(late), '', '旧页面响应仍写入旗帜');
  cached.remove(); retry.remove(); late.remove();
}
