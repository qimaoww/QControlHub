import { assert } from "./assertions.mjs";

function assertVisibleBounds(element, label) {
  const rect = element.getBoundingClientRect();
  assert.ok(rect.width > 0 && rect.height > 0, `${label}不可见`);
  assert.ok(rect.left >= -1 && rect.right <= innerWidth + 1, `${label}超出视口宽度`);
  assert.ok(rect.top >= -1 && rect.bottom <= innerHeight + 1, `${label}超出视口高度`);
}

export async function assertTCPDialogLayout(dialog) {
  await Promise.all(dialog.getAnimations().map((animation) => animation.finished.catch(() => {})));
  assertVisibleBounds(dialog, "TCP 弹窗");
  assert.ok(dialog.scrollWidth <= dialog.clientWidth + 1, "TCP 弹窗出现横向溢出");
  dialog.querySelectorAll("footer button, .bbr-preset > button").forEach((button) => {
    assertVisibleBounds(button, button.textContent);
    if (matchMedia("(pointer: coarse)").matches)
      assert.ok(button.getBoundingClientRect().height >= 44, "移动弹窗按钮触摸区域过小");
  });
}

export async function testSystemTCPLayout({ card, editor, refresh, testAPI }) {
  await assertTCPDialogLayout(editor());
  const preset = editor().querySelector(".bbr-preset");
  assert.equal(preset.querySelector("p"), null, "正常预设不应堆叠解释段落");
  assert.ok(preset.textContent.length < 70, "预设说明不够精简");
  editor().querySelector("[data-bbr-dialog-close]").click();

  const agent = testAPI.agents.find((entry) => entry.id === "alpha");
  const name = agent.name, kernel = agent.metrics.bbr.kernel_release;
  try {
    agent.name = `EDGE-${"very-long-node-name".repeat(8)}`;
    agent.metrics.bbr.kernel_release = `6.12-${"long-kernel-build".repeat(6)}`;
    await refresh();
    const bounds = card().getBoundingClientRect();
    const title = card().querySelector("h2").getBoundingClientRect();
    assert.ok(title.left >= bounds.left && title.right <= bounds.right, "长节点名撑出了卡片");
    assert.ok(card().scrollWidth <= card().clientWidth + 1, "长节点名或内核版本撑宽了卡片");
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "TCP 页面出现横向溢出");
  } finally {
    agent.name = name;
    agent.metrics.bbr.kernel_release = kernel;
    await refresh();
  }

  const buttons = [...card().querySelectorAll(".bbr-card-tools > button")];
  assert.equal(buttons.length, 2, "可编辑节点缺少并排入口");
  assert.equal(buttons[0].getBoundingClientRect().top, buttons[1].getBoundingClientRect().top, "详情和编辑入口未并排");
  for (const button of buttons) {
    assert.ok(button.getAttribute("aria-label").includes(button.querySelector("span").firstChild.textContent.trim()), "入口可访问名称不包含可见文本");
    if (matchMedia("(pointer: coarse)").matches)
      assert.ok(button.getBoundingClientRect().height >= 44, "移动卡片入口触摸区域过小");
  }
  const values = [...card().querySelectorAll(".bbr-primary-values > div")].map((entry) => entry.getBoundingClientRect());
  assert.ok(values.every((entry) => entry.top === values[0].top), "默认算法、队列和持久配置未保持一行");
  card().querySelector('[data-bbr-dialog-open="bbr-editor-alpha"]').click();
  await assertTCPDialogLayout(editor());
}
