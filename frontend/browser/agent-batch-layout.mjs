import { assert, delay, waitFor } from "./assertions.mjs";

export async function testAgentBatchLayout() {
  const launch = await waitFor(() => document.querySelector("[data-node-batch-toggle]"), "批量入口未渲染");
  launch.click();
  const form = await waitFor(() => document.querySelector("#batch-form"), "批量表单未渲染");
  form.elements.action.value = "install";
  form.elements.action.dispatchEvent(new Event("change", { bubbles: true }));
  form.elements.release_channel.value = "custom";
  form.elements.release_channel.dispatchEvent(new Event("change", { bubbles: true }));
  form.elements.custom_version.value = "v1.19.29";
  form.querySelector("[data-batch-select-all]").click();
  await delay(100);
  const bar = form.querySelector(".node-batch-bar");
  const bounds = bar.getBoundingClientRect();
  assert.ok(bounds.left >= 0 && bounds.right <= innerWidth, "批量操作栏超出屏幕宽度");
  assert.ok(bounds.top >= 0 && bounds.bottom <= innerHeight, "批量操作栏超出屏幕高度");
  assert.ok(bar.scrollWidth <= bar.clientWidth + 1, "批量操作栏产生横向滚动");
  for (const field of form.querySelectorAll('.batch-controls label:not([hidden]), .batch-submit-row button')) {
    const box = field.getBoundingClientRect();
    assert.ok(box.width >= 100, "版本选项或执行按钮被挤压");
    assert.ok(box.left >= bounds.left && box.right <= bounds.right, "批量控件超出容器");
  }
  const workspace = document.querySelector(".workspace-main");
  workspace.scrollTo({ top: workspace.scrollHeight, behavior: "instant" });
  window.scrollTo({ top: document.scrollingElement.scrollHeight, behavior: "instant" });
  await delay(100);
  const cards = [...form.querySelectorAll("[data-node-batch-card]")];
  assert.ok(cards.at(-1).getBoundingClientRect().bottom <= bounds.top, "最后一个节点被批量操作栏遮挡");
  for (const [action, title, tone] of [
    ["upgrade-agent", "批量更新 Agent", "primary"],
    ["start", "批量启动服务", "primary"],
    ["stop", "批量停止服务", "danger"],
    ["restart", "批量重启服务", "danger"],
    ["status", "批量查询状态", "primary"],
    ["install", "批量更新内核", "primary"],
  ]) {
    form.elements.action.value = action;
    form.elements.action.dispatchEvent(new Event("change", { bubbles: true }));
    const all = form.querySelector("[data-batch-select-all]");
    if (!all.checked) all.click();
    assert.equal(form.querySelector("[data-batch-action-title]").textContent, title);
    form.requestSubmit(form.querySelector('[type="submit"]'));
    const dialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "原有批量动作未打开确认弹窗");
    assert.equal(dialog.querySelector("[data-confirm-title]").textContent, title);
    assert.equal(dialog.dataset.tone, tone);
    assert.ok(dialog.querySelector(".confirm-targets").children.length >= 2, "未展示将要执行的节点");
    assert.equal(document.activeElement, dialog.querySelector("[data-confirm-cancel]"), "确认弹窗应默认聚焦取消");
    const box = dialog.getBoundingClientRect();
    assert.ok(box.left >= 0 && box.right <= innerWidth && box.top >= 0 && box.bottom <= innerHeight, "确认弹窗超出屏幕");
    assert.ok(dialog.scrollWidth <= dialog.clientWidth + 1, "确认弹窗横向溢出");
    if (action === "stop") assert.match(dialog.textContent, /连接会立即中断/);
    if (action === "status") assert.match(dialog.textContent, /不会重启或中断/);
    dialog.querySelector("[data-confirm-cancel]").click();
    await delay(20);
    assert.equal(form.dataset.busy, undefined, "取消确认不应提交任务");
  }

}
