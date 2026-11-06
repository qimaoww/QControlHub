import { assert, waitFor } from './assertions.mjs';

// Exercise the production theme's controls while source grants and result
// counts change. Legacy button geometry becomes native field alignment;
// authorization, exact counts, local filtering and focus assertions remain.
export async function testCoreLogFilterLayout({ testAPI }) {
  location.hash = '#core-logs';
  await waitFor(() => document.querySelector('[data-log-filters]') && !document.querySelector('.a-main [aria-busy=true]'), '日志筛选未加载');
  const root = document.querySelector('.a-main');
  const form = root.querySelector('[data-log-filters]');
  const button = text => [...form.querySelectorAll('button')].find(item => item.textContent === text);
  if (button('暂停更新')) button('暂停更新').click();
  button('清除筛选').click();
  const originalSources = testAPI.logSources, originalCounts = testAPI.logCounts;
  const engines = ['mihomo', 'xray', 'sing-box', 'ss-rust'];
  const select = form.elements.engine;
  const frame = () => new Promise(resolve => requestAnimationFrame(resolve));
  const refresh = async () => {
    const before = testAPI.calls.length;
    button('刷新').click();
    await waitFor(() => testAPI.calls.length > before && !root.querySelector('[aria-busy=true]'), '日志筛选刷新未完成');
  };
  const change = (control, value) => { control.value = value; control.dispatchEvent(new Event('change', { bubbles: true })); };
  const geometry = () => [...form.querySelectorAll('.a-form-grid > .a-field')].map(field => {
    const control = field.querySelector('input,select');
    const bounds = control.getBoundingClientRect();
    return [bounds.x, bounds.y, bounds.width, bounds.height];
  });
  try {
    testAPI.longLogs = false;
    testAPI.logSources = engines.map(engine => ({ agent_id: 'alpha', name: 'Alpha', instance_id: 'main', engine, status: 'active', grant_revision: 'layout' }));
    testAPI.logCounts = [317, 6, 11, 1000];
    await refresh();
    assert.equal([...select.options].map(option => option.textContent.split(' · ').at(-1)).join(','), '1334,317,6,11,1000', '内核筛选丢失精确结果数量');
    change(select, 'mihomo');
    const reads = testAPI.calls.length;
    change(form.elements.level, 'error');
    change(form.elements.level, '');
    assert.equal(testAPI.calls.length, reads, '本地内核和级别筛选请求了远端数据库');
    await Promise.all(root.getAnimations({ subtree: true }).filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
    for (const value of [innerWidth]) {
      await frame();
      const fields = [...form.querySelectorAll('.a-form-grid > .a-field')];
      const container = form.getBoundingClientRect();
      for (const field of fields) {
        const label = field.querySelector('label').getBoundingClientRect();
        const control = field.querySelector('input,select').getBoundingClientRect();
        assert.ok(control.left >= container.left - 1 && control.right <= container.right + 1, `筛选控件超出容器：${value}`);
        assert.ok(Math.abs(control.left - label.left) < 1 && control.top >= label.bottom, '字段标签与控件未对齐');
        assert.ok(field.scrollWidth <= field.clientWidth + 1, '字段文字或控件溢出');
      }
      const geometryBefore = JSON.stringify(geometry());
      select.focus({ preventScroll: true });
      const scroll = document.scrollingElement.scrollTop;
      for (const count of [0, 1, 9, 10, 99, 100, 999, 1000]) {
        testAPI.logCounts = [0, 0, 0, count];
        await refresh(); await frame();
        assert.equal(document.activeElement, select, '数量更新丢失筛选焦点');
        assert.equal(select.value, 'mihomo', '数量更新丢失内核筛选');
        assert.equal(document.scrollingElement.scrollTop, scroll, '数量更新改变阅读位置');
        assert.equal(JSON.stringify(geometry()), geometryBefore, '一至四位数量使筛选控件移动或变宽');
        assert.ok(select.options[4].textContent.endsWith(` · ${count}`), '内核数量未随数据更新');
      }
    }
    for (const available of [[], ['xray'], ['xray', 'ss-rust'], engines]) {
      testAPI.logSources = available.map(engine => ({ agent_id: 'alpha', name: 'Alpha', instance_id: 'main', engine, status: 'active', grant_revision: available.join(':') }));
      testAPI.logCounts = [0, 0, 0, 0];
      await refresh();
      assert.equal([...select.options].map(option => option.value).join(','), ['', ...available].join(','), '零日志时筛选项未遵守授权来源');
      change(select, available.at(-1) || '');
      assert.equal(select.selectedOptions.length, 1, '内核选择出现多个选中项');
      button('清除筛选').click();
      assert.equal(select.value, '', '清除筛选没有恢复全部获授权内核');
    }
    const longUser = 'shared-user-without-spaces'.repeat(4);
    const longNode = '长节点名称'.repeat(30);
    testAPI.logSources = engines.map(engine => ({ agent_id: 'alpha', name: longNode, instance_id: 'shared-long', owner_id: 'recipient', username: longUser, engine, status: 'active', grant_revision: 'long-shared' }));
    testAPI.logCounts = [1, 1, 1, 1];
    await refresh();
    change(form.elements.instance_id, 'shared-long');
    await waitFor(() => !root.querySelector('[aria-busy=true]') && root.querySelector('[data-log-entry]')?.textContent.includes(longUser), '共享实例日志没有显示真实使用用户');
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, '长节点或用户名撑破日志页面');
    for (const heading of root.querySelectorAll('[data-log-entry] p')) assert.ok(heading.scrollWidth <= heading.clientWidth + 1, '长日志来源标签溢出');
    root.querySelector('[data-source-status]').click();
    const detail = document.querySelector('dialog[data-log-sources]');
    await Promise.all(detail.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {})));
    assert.ok(detail.textContent.includes(longUser) && detail.textContent.includes(longNode), '采集状态丢失共享用户或长节点名称');
    assert.ok(detail.scrollWidth <= detail.clientWidth + 1, '长来源信息撑破弹窗');
    assert.ok([...detail.querySelectorAll('.a-table-scroll')].every(table => table.scrollWidth <= table.clientWidth + 1), '来源状态和时间需横向滚动才能查看');
    detail.querySelector('[aria-label=关闭]').click();
  } finally {
    testAPI.logSources = originalSources;
    testAPI.logCounts = originalCounts;
  }
}
