// playwright-cli run-code --filename=scripts/node-refresh-performance.js
// Start core-logs-performance-server.mjs with QCH_PERF_PAGE=nodes,
// QCH_PERF_TIMING=1 and QCH_PERF_ROOT pointing at either compared frontend.
async page => {
  const origin = new URL(page.url()).origin;
  if (!['127.0.0.1', 'localhost'].includes(new URL(origin).hostname)) throw new Error('Use the isolated fixture');
  const results = [];
  const phases = entries => Object.fromEntries(['draw', 'bytes', 'date'].map(phase => {
    const values = entries.filter(entry => entry.phase === phase).map(entry => entry.ms);
    return [phase, { samples: values.length, totalMs: values.reduce((sum, value) => sum + value, 0), ...(phase === 'draw' ? { values } : {}) }];
  }));
  for (const [nodes, role] of [[0, 'owner'], [1, 'owner'], [32, 'owner'], [256, 'owner'], [32, 'shared']]) {
    const runs = [];
    for (let repeat = 0; repeat < 3; repeat++) {
      const began = Date.now();
      await page.goto(`${origin}/?nodes=${nodes}&role=${role}`);
      await page.waitForSelector(nodes ? 'article[data-agent-id]' : '.a-empty');
      await page.waitForFunction(() => window.__nodePerformanceFixture && document.querySelector('.a-probe-toolbar'));
      const firstVisibleMs = Date.now() - began;
      const views = [];
      for (const view of ['cards', 'list']) {
        await page.getByRole('button', { name: view === 'cards' ? '卡片' : '列表', exact: true }).click();
        await page.waitForFunction(label => [...document.querySelectorAll('button')].find(button => button.textContent === label)?.getAttribute('aria-pressed') === 'true', view === 'cards' ? '卡片' : '列表');
        const unchanged = await page.evaluate(() => {
          const search = document.querySelector('input[name=search]');
          search.focus();
          window.__phaseMetrics = [];
          const observer = new MutationObserver(() => {});
          const region = document.querySelector('.a-probe-grid,.a-probe-list');
          if (region) observer.observe(region, { childList: true, subtree: true, characterData: true });
          for (let i = 0; i < 10; i++) search.dispatchEvent(new Event('input', { bubbles: true }));
          const mutations = observer.takeRecords().length; observer.disconnect();
          return { phases: window.__phaseMetrics, mutations, focusRetained: document.activeElement === search };
        });
        if (unchanged.mutations || !unchanged.focusRetained) throw new Error('Unchanged data disturbed the view');
        const changed = [];
        for (let i = 0; i < 10; i++) {
          await page.evaluate(index => {
            for (const agent of window.testAPI.agents) {
              agent.metrics.cpu_percent = (index + 30) % 100;
              agent.metrics.collected_at = new Date(Date.now() + index).toISOString();
            }
            window.__phaseMetrics = [];
            [...document.querySelectorAll('.a-top button')].find(button => button.textContent === '刷新').click();
          }, i);
          await page.waitForFunction(() => ![...document.querySelectorAll('.a-top button')].find(button => button.textContent === '刷新').disabled);
          changed.push(await page.evaluate(() => window.__phaseMetrics));
        }
        views.push({ view, unchanged: { ...unchanged, phases: phases(unchanged.phases) }, changed: changed.map(phases) });
      }
      const lifecycle = await page.evaluate(async () => {
        const search = document.querySelector('input[name=search]');
        search.focus(); search.setSelectionRange(0, 0);
        const content = document.querySelector('.a-probe-grid,.a-probe-list')?.textContent;
        window.testAPI.agentsFailure = true;
        const refresh = [...document.querySelectorAll('.a-top button')].find(button => button.textContent === '刷新');
        refresh.click();
        while (refresh.disabled) await new Promise(resolve => setTimeout(resolve, 10));
        const retained = content === document.querySelector('.a-probe-grid,.a-probe-list')?.textContent;
        window.testAPI.agentsFailure = false;
        return { failureRetainedData: retained, focusRetained: document.activeElement === search,
          apiCalls: window.testAPI.calls.length, heapBytes: performance.memory?.usedJSHeapSize,
          resources: performance.getEntriesByType('resource').map(entry => ({ name: new URL(entry.name).pathname, transferBytes: entry.transferSize, durationMs: entry.duration })),
          longTasks: window.__longTasks };
      });
      if (!lifecycle.failureRetainedData || !lifecycle.focusRetained) throw new Error('Refresh failure discarded reader state');
      runs.push({ firstVisibleMs, views, lifecycle });
    }
    results.push({ nodes, role, runs });
  }
  return results;
}
