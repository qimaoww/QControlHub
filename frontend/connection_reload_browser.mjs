// Use the repository's native CDP convention and existing HTTP fixture. No
// browser framework or production service is needed for real reload coverage.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as pause } from 'node:timers/promises';
const root = dirname(fileURLToPath(import.meta.url)), children = [], pending = new Map();
const profile = await mkdtemp(join(tmpdir(), 'qch-connection-reload-'));
const artifacts = process.env.QCH_CONNECTION_ARTIFACT_DIR;
let socket, sequence = 0;
const start = (command, args, env = {}) => {
  const child = spawn(command, args, { stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env, ...env } }); children.push(child); return child;
};
const endpoint = (child, pattern) => new Promise((resolve, reject) => {
  let output = '';
  for (const stream of [child.stdout, child.stderr]) stream.on('data', chunk => { output = (output + chunk).slice(-8192); const match = output.match(pattern); if (match) resolve(match[1]); });
  child.once('error', reject); child.once('exit', code => reject(new Error(`fixture/browser exited ${code}: ${output}`)));
});
const deadline = setTimeout(() => { for (const child of children) child.kill('SIGKILL'); }, 120_000);
try {
  const server = start(process.execPath, [join(root, 'agents_browser_smoke.mjs')], { QCH_BROWSER_SMOKE_SERVE_ONLY: '1' });
  const base = await endpoint(server, /(http:\/\/127\.0\.0\.1:\d+)\//);
  const chrome = start(process.env.QCH_CHROME_BIN || 'chromium', ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--no-first-run', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank']);
  socket = new WebSocket(await endpoint(chrome, /DevTools listening on (ws:\/\/\S+)/));
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(data), entry = pending.get(message.id);
    if (!entry) return;
    pending.delete(message.id); clearTimeout(entry.timer);
    message.error ? entry.reject(new Error(`${entry.method}: ${JSON.stringify(message.error)}`)) : entry.resolve(message.result);
  });
  const call = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++sequence, timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 10_000);
    pending.set(id, { method, timer, resolve, reject }); socket.send(JSON.stringify({ id, method, params, sessionId }));
  });
  const { targetId } = await call('Target.createTarget', { url: 'about:blank' });
  const { sessionId: page } = await call('Target.attachToTarget', { targetId, flatten: true });
  const evaluate = async expression => {
    const result = await call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, page);
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const wait = async expression => {
    for (let i = 0; i < 240; i++) {
      try { const value = await evaluate(expression); if (value) return value; } catch (error) { if (!/context|Cannot find|navigated/.test(error.message)) throw error; }
      await pause(25);
    }
    throw new Error(`condition timed out: ${expression}\n${await evaluate('document.body.innerText')}`);
  };
  const screenshot = async label => {
    if (!artifacts) return;
    await wait(`!document.getAnimations().some(animation=>animation.id.startsWith('analytics-'))`);
    await mkdir(artifacts, { recursive: true });
    const { cssContentSize } = await call('Page.getLayoutMetrics', {}, page);
    const { data } = await call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width: cssContentSize.width, height: cssContentSize.height, scale: 1 } }, page);
    await writeFile(join(artifacts, `${label}.png`), Buffer.from(data, 'base64'));
  };
  const configure = values => evaluate(`connectionFixture.configure(${JSON.stringify(values)})`);
  const counts = () => evaluate('connectionFixture.counts()');
  const rows = `document.querySelectorAll('td[data-column="客户端来源 IP"] code').length`;
  const ready = `document.querySelector('.a-main')?.textContent.includes('已读取') && !document.querySelector('[aria-busy=true]')`;
  const refresh = () => evaluate(`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click()`);
  const change = (name, value) => evaluate(`(() => {const el=document.querySelector('.a-main form').elements[${JSON.stringify(name)}];el.value=${JSON.stringify(value)};el.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  const reload = async () => {
    const previous = await evaluate('connectionFixture.document');
    await call('Page.reload', { ignoreCache: true }, page);
    await wait(`window.connectionFixture && connectionFixture.document!==${JSON.stringify(previous)} && document.querySelector('.a-main,.a-login')`);
  };
  await call('Page.enable', {}, page); await call('Network.enable', {}, page);
  await call('Emulation.setDeviceMetricsOverride', { width: 1920, height: 1080, deviceScaleFactor: 1, mobile: false }, page);
  await call('Page.navigate', { url: `${base}/agents-browser-smoke.html?mode=connections-reload&preview=1#client-connections` }, page);
  await wait(ready); assert.equal(await evaluate(rows), 3);
  const cold = await evaluate('({visibleMs:performance.now(),...connectionFixture.counts()})');
  await reload(); await wait(ready);
  const restored = await evaluate('({visibleMs:performance.now(),...connectionFixture.counts()})');
  assert.equal(restored.reads, cold.reads, 'real reload repeated fresh full history');
  await screenshot('restored-1920x1080');
  await wait(`document.querySelector('[name=country_code] option[value=CN]')`);
  assert.equal(await evaluate(`document.querySelector('td[data-column="国家 / 地区"] [data-region-code=US] img').getAttribute('src')`), '/api/v1/region-flags/us');
  await change('country_code', 'CN'); await wait(ready);
  assert.equal(await evaluate(rows), 2);
  assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('独立 IP 2')`));
  const countryRead = await counts(); await reload(); await wait(ready);
  assert.equal((await counts()).reads, countryRead.reads, 'reload repeated a fresh country query');
  assert.equal(await evaluate(`document.querySelector('[name=country_code]').value`), 'CN');
  assert.ok(await evaluate(`[...document.querySelectorAll('td[data-column="国家 / 地区"]')].every(cell=>cell.textContent.includes('中国') && cell.querySelector('[data-region-code=CN]'))`));
  await change('country_code', 'TW'); await wait(ready);
  assert.equal(await evaluate(rows), 1);
  assert.equal(await evaluate(`document.querySelector('td[data-column="国家 / 地区"] [data-region-code=TW] img').getAttribute('src')`), '/api/v1/region-flags/cn');
  assert.ok(await evaluate(`document.querySelector('td[data-column="国家 / 地区"]').textContent.includes('中国台湾')`));
  await screenshot('country-taiwan-1920x1080');
  await change('country_code', 'ST'); await wait(ready);
  assert.equal(await evaluate(rows), 0);
  assert.equal(await evaluate(`document.querySelector('[name=country_code]').title`), '圣多美和普林西比');
  await screenshot('country-empty-1920x1080');
  await evaluate(`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='清除筛选').click()`); await wait(ready);
  assert.equal(await evaluate(`document.querySelector('[name=country_code]').value`), '');
  await evaluate(`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='下一页').click()`); await wait(ready);
  await change('country_code', 'CN'); await wait(ready);
  assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('第 1 页')`), 'country filter retained a previous query cursor');
  assert.equal(await evaluate(rows), 2);
  await evaluate(`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='清除筛选').click()`); await wait(ready);
  await evaluate(`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='下一页').click()`); await wait(ready);
  assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('第 2 页')`));
  const paged = await counts(); await reload(); await wait(ready);
  assert.equal((await counts()).reads, paged.reads, 'reload lost the cursor page');
  assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('第 2 页')`));
  await configure({ order: ['bravo', 'alpha'] }); await reload(); await wait(ready);
  assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('第 1 页')`), 'changed authenticated node order reused an old cursor');
  assert.equal((await counts()).reads, paged.reads + 1, 'changed node order reused an incompatible page');
  await evaluate(`[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='下一页').click()`); await wait(ready);
  await configure({ order: ['alpha', 'bravo'], failure: 503 }); await refresh();
  await wait(`document.querySelector('.a-main').textContent.includes('连接查询暂不可用')`);
  await configure({ failure: 0 }); await refresh(); await wait(ready);
  assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('第 1 页')`), 'failed order revalidation retained the previous order cursor on retry');
  await change('agent_id', 'alpha'); await wait(ready);
  await change('engine', 'xray'); await wait(ready);
  await change('client_ip', '8.8.8.1'); await wait(ready);
  const filtered = await counts(); await reload(); await wait(ready);
  assert.equal((await counts()).reads, filtered.reads, 'reload first queried the default filters');
  assert.equal(await evaluate(`document.querySelector('[name=client_ip]').value`), '8.8.8.1');
  await configure({ offset: 31_000 }); await reload();
  await wait(`document.querySelector('.a-main')?.textContent.includes('上次结果')`);
  assert.equal(await evaluate(rows), 1, 'stale reload cleared usable rows'); await screenshot('updating-1920x1080');
  await wait(ready);
  const updated = await counts(); assert.equal(updated.reads, filtered.reads + 1);
  await configure({ failure: 503 }); await refresh();
  await wait(`document.querySelector('.a-main').textContent.includes('更新失败，保留上次结果')`);
  assert.equal(await evaluate(rows), 1); await screenshot('failed-1920x1080');
  await configure({ failure: 0, empty: true }); await refresh(); await wait(ready);
  assert.equal(await evaluate(rows), 0); assert.ok(await evaluate(`document.querySelector('.a-main').textContent.includes('独立 IP 0')`));
  const empty = await counts(); await reload(); await wait(ready); assert.equal((await counts()).reads, empty.reads, 'empty success did not replace old snapshot');
  await screenshot('empty-1920x1080');
  await configure({ empty: false }); await refresh(); await wait(ready);
  await configure({ scopeFailure: 503 }); await reload();
  await wait(`document.querySelector('.a-main')?.textContent.includes('访问范围暂不可用')`);
  assert.equal(await evaluate(rows), 0, 'unverified scope exposed stored history');
  await configure({ scopeFailure: 0, scope: 2, empty: true }); await reload();
  assert.equal(await evaluate(rows), 0, 'changed authority briefly exposed an old snapshot'); await wait(ready);
  await configure({ scope: 3, empty: false, offset: 332_000 }); await reload();
  assert.equal(await evaluate(rows), 0, 'expired snapshot was painted'); await wait(ready);
  for (const bad of ['{', '{"version":999}']) {
    await evaluate(`sessionStorage.setItem('qcontrolhub:connections:v1:index',${JSON.stringify(bad)})`);
    // Corrupt before startup (pagehide intentionally flushes the live cache).
    await call('Page.addScriptToEvaluateOnNewDocument', { source: `sessionStorage.setItem('qcontrolhub:connections:v1:index',${JSON.stringify(bad)});` }, page).then(async script => {
      const before = await counts(); await reload(); await wait(ready); assert.equal((await counts()).reads, before.reads + 1);
      await call('Page.removeScriptToEvaluateOnNewDocument', { identifier: script.identifier }, page);
    });
  }
  for (const storage of ['quota', 'disabled']) {
    await configure({ storage }); await reload(); await wait(ready);
    await refresh(); await wait(ready); assert.ok(await evaluate(rows) > 0, 'storage failure prevented API fallback');
  }
  await configure({ storage: '', failure: 403 }); await refresh();
  await wait(`document.querySelector('.a-main').textContent.includes('连接查看权限已撤销')`);
  assert.equal(await evaluate(rows), 0, 'revoked permission retained IP history');
  await configure({ failure: 0 }); await refresh(); await wait(ready);
  await reload(); await wait(ready); // Re-enable session storage after the disabled-storage document.
  await configure({ regionsFailure: 503 }); await reload(); await wait(ready);
  await wait(`document.querySelector('[name=country_code]').textContent.includes('国家/地区加载失败')`);
  assert.ok(await evaluate(rows) > 0, 'country catalog failure blocked history');
  await configure({ regionsFailure: 0 }); await refresh(); await wait(ready);
  await wait(`document.querySelector('[name=country_code] option[value=CN]')`);
  const beforeDouble = await counts(); await refresh(); await refresh(); await wait(ready);
  assert.equal((await counts()).reads, beforeDouble.reads + 1, 'repeated force refresh duplicated an in-flight query');
  await evaluate(`document.querySelector('.a-main [name=client_ip]').value='unsubmitted-search';document.querySelector('.a-main [name=client_ip]').focus();document.activeElement.setSelectionRange(2,5);window.savedInput=document.activeElement;`);
  await refresh(); await wait(ready);
  assert.ok(await evaluate(`document.activeElement===savedInput && savedInput.value==='unsubmitted-search' && savedInput.selectionStart===2 && savedInput.selectionEnd===5`), 'refresh disturbed input focus, selection or unsubmitted edits');
  await call('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 }, page);
  await call('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 }, page);
  assert.ok(await evaluate(`document.activeElement!==savedInput`), 'keyboard navigation is blocked');
  await change('client_ip', ''); await wait(ready); await change('engine', ''); await wait(ready); await change('agent_id', ''); await wait(ready);
  await change('country_code', 'US'); await wait(ready);
  for (const [width, height, mobile] of [[1920,1080,false],[3840,2160,false],[1280,900,false],[960,800,false],[600,900,false],[390,844,true],[844,390,true],[768,1024,true],[1024,768,true]]) {
    await call('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile }, page);
    await call('Emulation.setTouchEmulationEnabled', { enabled: mobile, maxTouchPoints: 5 }, page);
    await evaluate(`document.activeElement?.blur();window.scrollTo(0,0)`);
    await pause(80);
    assert.ok(await evaluate(`document.documentElement.scrollWidth<=innerWidth+1`), `${width}x${height}: viewport overflow`);
    await screenshot(`result-${width}x${height}`);
    if (width === 390) {
      await configure({ offset: 365_000 }); await reload(); await wait(`document.querySelector('.a-main')?.textContent.includes('上次结果')`);
      assert.equal(await evaluate(rows), 3); await screenshot('updating-390x844'); await wait(ready); await screenshot('restored-390x844');
      await call('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 200, y: 700 }] }, page);
      await call('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 200, y: 350 }] }, page);
      await call('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] }, page);
    }
  }
  // Same browser, real logout/login UI, with old history pending at the boundary.
  await configure({ delay: 800 }); await refresh();
  await evaluate(`document.querySelector('[title=退出登录]').click()`); await wait(`document.querySelector('.a-login form')`);
  assert.equal(await evaluate(`Object.keys(sessionStorage).filter(key=>key.startsWith('qcontrolhub:connections:')).length`), 0, 'logout left restorable history');
  await configure({ role: 'user' });
  await evaluate(`(() => {const form=document.querySelector('.a-login form');form.elements.username.value='shared';form.elements.token.value='fixture';form.requestSubmit();})()`);
  await wait(ready); await pause(850);
  assert.ok(await evaluate(`[...document.querySelectorAll('td[data-column=节点]')].every(cell=>cell.textContent.includes('Bravo'))`), 'late owner response leaked into a shared account');
  await reload(); await wait(ready);
  assert.ok(await evaluate(`[...document.querySelectorAll('td[data-column=节点]')].every(cell=>cell.textContent.includes('Bravo'))`), 'shared account restored owner history');
  const report = { cold, restored, staleUpdateReads: updated.reads - filtered.reads, viewports: '1920x1080,3840x2160,1280x900,960x800,600x900,390x844,844x390,768x1024,1024x768; CSS pixels; DPR 1; scale 1; browser emulation' };
  process.stdout.write(`Connection reload lifecycle passed: ${JSON.stringify(report)}\n`);
  if (artifacts) await writeFile(join(artifacts, 'measurements.json'), JSON.stringify(report, null, 2));
  await call('Browser.close');
} finally {
  clearTimeout(deadline);
  for (const entry of pending.values()) clearTimeout(entry.timer);
  socket?.close();
  for (const child of children) { child.kill('SIGTERM'); await Promise.race([new Promise(resolve => child.once('exit', resolve)), pause(1000)]); if (child.exitCode === null) child.kill('SIGKILL'); }
  await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
