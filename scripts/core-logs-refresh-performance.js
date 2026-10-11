// Run with playwright-cli run-code --filename after opening the loopback fixture.
// Both baseline and candidate must use the same server settings and this script.
async page => {
  const origin = new URL(page.url()).origin;
  if (!['127.0.0.1', 'localhost'].includes(new URL(origin).hostname)) throw new Error('Use the loopback performance fixture');
  const control = change => page.evaluate(async change => {
    await fetch('/perf-control', { method: 'POST', body: JSON.stringify(change) });
  }, change);
  const settle = () => page.waitForFunction(() => document.querySelector('[data-core-log-refresh-label]')?.textContent === '自动更新已暂停', null, { timeout: 60000 });
  const frame = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const counts = () => page.evaluate(() => ({ rows: document.querySelectorAll('.core-log-row').length,
    loaded: document.querySelector('.core-log-status')?.textContent }));
  const stats = () => page.evaluate(async () => (await (await fetch('/perf-stats')).json()));
  const waitStats = async predicate => {
    const deadline = Date.now() + 60000;
    while (Date.now() < deadline) {
      if (predicate(await stats())) return;
      await page.waitForTimeout(100);
    }
    throw new Error('HTTP fixture requests did not settle');
  };
  const result = { steps: [] };
  const step = async (name, action, wait) => {
    const began = await page.evaluate(() => performance.now());
    await action();
    if (wait) await wait();
    await frame();
    result.steps.push({ name, ms: Math.round(await page.evaluate(() => performance.now()) - began) });
  };
  await page.addInitScript(() => { localStorage.clear(); sessionStorage.clear(); });
  await control({ revision: 0, failure: 0, reset: true });
  const began = Date.now();
  await page.goto(origin + '/?mode=logs&run=' + Date.now() + '#core-logs');
  await page.waitForSelector('.core-log-row', { timeout: 60000 });
  result.firstVisibleMs = Date.now() - began;
  await page.locator('[data-toggle-core-log-refresh]').click();
  await settle();
  result.initialCompleteMs = Date.now() - began;
  await step('window8000', () => page.locator('select[name=limit]').selectOption('2000'), settle);
  result.window = await counts();
  if (!result.window.loaded.includes('已加载 8000 条')) throw new Error('Incomplete full window');
  await step('searchFullWindow', () => page.locator('input[name=q]').fill('pressure entry 1999'),
    () => page.waitForFunction(() => document.querySelectorAll('.core-log-row').length === 4));
  await step('reset', () => page.locator('[data-reset-core-logs]').click());
  await step('page2', () => page.locator('.core-log-result-toolbar [data-core-log-page-index="1"]').click(),
    () => page.waitForFunction(() => document.querySelector('.core-log-pagination')?.textContent.includes('第 2 / 40 页')));
  await step('coldNodeComplete', () => page.locator('[data-core-log-agent="bravo"]').click(), settle);
  await page.locator('[data-core-log-agent=""]').click();
  await settle();
  result.warmSelection = await page.evaluate(() => {
    const began = performance.now();
    document.querySelector('[data-core-log-agent="bravo"]').click();
    return { ms: performance.now() - began, synchronousRows: document.querySelectorAll('.core-log-row').length,
      status: document.querySelector('[data-core-log-refresh-label]')?.textContent };
  });
  await settle();
  result.navigationAndSwitch = { ...await stats(), ...await page.evaluate(() => ({
    longTasks: window.__longTasks, bodyReads: window.__bodyReads, phases: window.__phaseMetrics,
    resources: performance.getEntriesByType('resource').filter(entry => entry.name.includes('/perf-core-logs')).map(entry => ({
      limit: new URL(entry.name).searchParams.get('limit'), ttfbMs: entry.responseStart - entry.requestStart,
      transferMs: entry.responseEnd - entry.responseStart, durationMs: entry.duration,
      encodedBytes: entry.encodedBodySize, decodedBytes: entry.decodedBodySize,
    })),
  })) };

  // Accelerate only the production polling/backoff timers; request delay and
  // streamed bodies remain real wall-clock HTTP transfers in the fixture.
  await page.evaluate(() => {
    window.__nativeTimeout = window.setTimeout;
    window.setTimeout = (callback, delay, ...args) => window.__nativeTimeout(callback,
      delay >= 10000 && delay <= 60000 ? 100 : delay, ...args);
    const search = document.querySelector('input[name=q]');
    search.focus(); search.setSelectionRange(0, 0);
    window.__streamMutations = 0;
    window.__streamObserver = new MutationObserver(records => { window.__streamMutations += records.length; });
    window.__streamObserver.observe(document.querySelector('.core-log-stream'), { childList: true, subtree: true, characterData: true });
    window.__longTasks = []; window.__phaseMetrics = []; window.__bodyReads = []; window.__httpResponses = [];
  });
  await control({ reset: true });
  const refreshAt = Date.now();
  await page.evaluate(() => document.querySelector('[data-toggle-core-log-refresh]').click());
  await waitStats(stats => stats.requests.filter(request => request.completed).length >= 3);
  await page.waitForFunction(() => window.__bodyReads.filter(read => read.status === 200).length +
    window.__httpResponses.filter(response => response.status === 304).length >= 3, null, { timeout: 60000 });
  await frame();
  await page.evaluate(() => document.querySelector('[data-toggle-core-log-refresh]').click());
  await settle();
  result.unchanged = { elapsedMs: Date.now() - refreshAt, ...await stats(), ...await page.evaluate(() => ({
    streamMutations: window.__streamMutations, longTasks: window.__longTasks,
    bodyReads: window.__bodyReads, httpResponses: window.__httpResponses, phases: window.__phaseMetrics,
    focusPreserved: document.activeElement === document.querySelector('input[name=q]'),
  })) };
  if (result.unchanged.streamMutations || !result.unchanged.focusPreserved) throw new Error('Unchanged polling disturbed the stream or search focus');
  await page.evaluate(() => { window.__streamObserver.disconnect(); });

  await page.locator('.core-log-result-toolbar [data-core-log-page-index="1"]').click();
  await page.evaluate(() => {
    const main = document.querySelector('.workspace-main');
    window.__reader = main.scrollHeight > main.clientHeight ? main : document.scrollingElement;
    window.__reader.scrollTo({ top: 1200, behavior: 'instant' });
  });
  await page.waitForTimeout(200);
  await page.evaluate(() => {
    const top = window.__reader === document.scrollingElement ? 0 : Math.max(0, window.__reader.getBoundingClientRect().top);
    window.__anchor = [...document.querySelectorAll('.core-log-row')].find(row => row.getBoundingClientRect().bottom > top);
    window.__anchorTop = window.__anchor.getBoundingClientRect().top;
  });
  const oldFirstKey = await page.locator('.core-log-row').first().getAttribute('data-refresh-key');
  await control({ revision: 1, reset: true });
  await step('changedRefresh', () => page.evaluate(() => document.querySelector('[data-toggle-core-log-refresh]').click()),
    async () => {
      await waitStats(stats => stats.requests.some(request => request.status === 200 && request.completed));
      await page.waitForFunction(oldFirstKey => document.querySelector('.core-log-row')?.dataset.refreshKey !== oldFirstKey,
        oldFirstKey, { timeout: 60000 });
    });
  await page.evaluate(() => document.querySelector('[data-toggle-core-log-refresh]').click());
  await settle();
  result.changed = { ...await stats(), ...await page.evaluate(() => ({
    firstKey: document.querySelector('.core-log-row')?.dataset.refreshKey,
    anchorRetained: window.__anchor.isConnected,
    anchorDelta: window.__anchor.getBoundingClientRect().top - window.__anchorTop,
    pageRetained: document.querySelector('.core-log-pagination')?.textContent.includes('第 2 / 40 页'),
  })) };
  result.changed.previousFirstKey = oldFirstKey;
  if (!result.changed.anchorRetained || Math.abs(result.changed.anchorDelta) >= 2 || !result.changed.pageRetained) throw new Error('Changed refresh lost the reading anchor/page');

  await control({ failure: 503, reset: true });
  await page.evaluate(() => document.querySelector('[data-toggle-core-log-refresh]').click());
  await page.waitForFunction(() => document.querySelector('[data-core-log-refresh-label]')?.textContent.includes('刷新失败'));
  result.failure = await counts();
  if (result.failure.rows !== 200) throw new Error('Failure discarded valid logs');
  await control({ failure: 0 });
  await page.waitForFunction(() => document.querySelector('[data-core-log-refresh-label]')?.textContent === '正在实时更新', null, { timeout: 60000 });
  await page.evaluate(() => document.querySelector('[data-toggle-core-log-refresh]').click());
  await settle();
  result.recovery = await stats();
  await page.evaluate(() => { window.setTimeout = window.__nativeTimeout; });

  // Each immediate selection must clear/reuse only its own scope, even while
  // old preview/full requests are still streaming on a slow connection.
  result.rapidSwitch = [];
  for (const agent of ['charlie', 'bravo', 'charlie']) {
    result.rapidSwitch.push(await page.evaluate(agent => {
      const began = performance.now(); document.querySelector(`[data-core-log-agent="${agent}"]`).click();
      const messages = [...document.querySelectorAll('.core-log-row pre')].map(row => row.textContent);
      return { agent, ms: performance.now() - began,
        active: document.querySelector(`[data-core-log-agent="${agent}"]`).classList.contains('active'),
        consistent: messages.every(message => message.startsWith(agent + ' ')) };
    }, agent));
    await page.waitForTimeout(50);
  }
  await settle();
  result.finalSelection = await page.evaluate(() => ({ active: document.querySelector('[data-core-log-agent="charlie"]').classList.contains('active'),
    consistent: [...document.querySelectorAll('.core-log-row pre')].every(row => row.textContent.startsWith('charlie ')) }));
  if (result.rapidSwitch.some(step => !step.active || !step.consistent) || !result.finalSelection.active || !result.finalSelection.consistent) throw new Error('Stale logs overwrote the selected node');
  result.browser = await page.evaluate(() => ({ ua: navigator.userAgent, width: innerWidth, height: innerHeight }));
  return result;
}
