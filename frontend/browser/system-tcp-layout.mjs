import { assert, delay } from './assertions.mjs';
import { tcpButton, tcpClose } from './system-tcp-controls.mjs';

export async function assertTCPDialogLayout(dialog) {
  // The theme observes newly opened dialogs before starting their entrance.
  // Measure touch targets after that animation, including its children.
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  await Promise.all(dialog.getAnimations({ subtree: true }).filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
  const rect = dialog.getBoundingClientRect();
  assert.ok(rect.width > 0 && rect.height > 0, 'TCP 弹窗不可见');
  assert.ok(rect.left >= -1 && rect.right <= innerWidth + 1 && rect.top >= -1 && rect.bottom <= innerHeight + 1, 'TCP 弹窗超出视口');
  assert.ok(dialog.scrollWidth <= dialog.clientWidth + 1, 'TCP 弹窗横向溢出');
  const submit = dialog.querySelector('[type=submit]');
  if (submit) {
    submit.scrollIntoView({ block: 'center' }); await delay(60);
    const bounds = submit.getBoundingClientRect();
    assert.ok(bounds.top >= rect.top && bounds.bottom <= rect.bottom + 1, 'TCP 提交按钮不可达');
    if (matchMedia('(pointer: coarse)').matches) assert.ok(bounds.height >= 44, `移动端提交按钮触摸区域不足：实际 ${bounds.height}，样式 ${getComputedStyle(submit).height}，弹窗缩放 ${getComputedStyle(dialog).scale}`);
  }
  dialog.scrollTop = 0;
}

export async function testSystemTCPLayout({ card, editor, refresh, testAPI }) {
  await assertTCPDialogLayout(editor());
  const preset = editor().querySelector('[data-tcp-preset]');
  assert.ok(preset.textContent.length < 70, '预设说明不够精简');
  tcpClose(editor());
  const agent = testAPI.agents.find(entry => entry.id === 'alpha'), name = agent.name, kernel = agent.metrics.bbr.kernel_release;
  try {
    agent.name = `EDGE-${'very-long-node-name'.repeat(8)}`;
    agent.metrics.bbr.kernel_release = `6.12-${'long-kernel-build'.repeat(6)}`;
    await refresh();
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, 'TCP 长内容撑破页面');
    const container = card().closest('[role=region]');
    assert.ok(container && container.getBoundingClientRect().right <= innerWidth + 1, 'TCP 表格未在容器内滚动');
  } finally {
    agent.name = name; agent.metrics.bbr.kernel_release = kernel; await refresh();
  }
  const buttons = ['查看参数', '自定义参数'].map(text => tcpButton(card(), text));
  assert.ok(buttons.every(Boolean), '缺少详情或编辑入口');
  if (matchMedia('(pointer: coarse)').matches) for (const button of buttons) assert.ok(button.getBoundingClientRect().height >= 44, '移动端入口触摸区域不足');
  tcpButton(card(), '自定义参数').click();
  await assertTCPDialogLayout(editor());
}
