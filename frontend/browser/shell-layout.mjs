import { assert, waitFor, delay } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';

export async function testShellLayoutRuntime({ mode }) {
  const mobile = mode.endsWith('mobile');
  assert.ok(matchMedia(mobile ? '(pointer:coarse)' : '(pointer:fine)').matches, '布局回归输入设备不符');
  assert.ok(innerWidth <= 820, '外壳回归没有窄窗口');
  await startNativeNodes();
  const layout = document.querySelector('.a-layout'), main = document.querySelector('.a-main'), top = document.querySelector('.a-top');
  const toggle = document.querySelector('[aria-label=切换导航]');
  toggle.focus(); toggle.click();
  const drawer = await waitFor(() => document.querySelector('dialog.a-drawer[open]'), '窄窗口导航未打开');
  assert.ok(drawer.matches(':modal'), '窄窗口导航不是原生模态');
  const link = drawer.querySelector('a[href="#client-access"]');
  link.focus(); link.click();
  await waitFor(() => document.querySelector('[data-client-agent]') && !drawer.isConnected, '导航未打开客户端或关闭抽屉');
  assert.equal(document.querySelector('.a-layout'), layout, '导航重建页面外壳');
  assert.equal(document.querySelector('.a-main'), main, '导航替换稳定的内容容器');
  assert.equal(document.querySelector('.a-top'), top, '导航重建顶栏');
  assert.equal(document.querySelector('.a-layout > .a-rail a[href="#client-access"]')?.getAttribute('aria-current'), 'page', '导航未更新选中状态');
  const root = document.documentElement, original = root.style.fontSize;
  try {
    for (const [hash, selector, title] of [
      ['#node-settings', '.a-probe-grid', '节点'], ['#settings-node-alpha', 'dialog[open] [data-refresh-key=mihomo]', '节点'],
      ['#client-access', '[data-client-agent]', '客户端接入'], ['#settings-engines', '#panel-settings-form', '面板设置'],
      ['#archive-config', '.a-main .a-card', '配置档案'],
    ]) {
      location.hash = hash;
      await waitFor(() => document.querySelector(selector) && document.querySelector('.a-top-title')?.textContent === title && !document.querySelector('.a-main [aria-busy=true]'), `路由未加载：${hash}`);
      for (const scale of ['90%', '100%', '110%', '135%']) {
        root.style.fontSize = scale;
        for (const animation of document.getAnimations()) if (Number.isFinite(animation.effect?.getComputedTiming().endTime)) animation.finish();
        assert.ok(root.scrollWidth <= innerWidth + 1, `${hash}/${scale} 页面横向溢出`);
        for (const panel of document.querySelectorAll('.a-layout,.a-work,.a-main,.a-probe,.a-dialog[open],.a-card,.a-filterbar')) {
          if (!panel.checkVisibility()) continue;
          assert.ok(panel.scrollWidth <= panel.clientWidth + 1, `${hash}/${scale} 内容被裁切：${panel.className}`);
        }
      }
      root.style.fontSize = original;
    }
    const create = [...document.querySelectorAll('.a-top button')].find(button => button.textContent === '新建配置');
    create.click();
    await waitFor(() => document.querySelector('dialog[open] textarea[name=content]'), '配置编辑器未打开');
    assert.ok(document.querySelector('dialog[open]').scrollWidth <= document.querySelector('dialog[open]').clientWidth + 1, '配置编辑器横向溢出');
    document.querySelector('dialog[open] [aria-label=关闭]').click();
    await delay(20);
  } finally { root.style.fontSize = original; }
}
