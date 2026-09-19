import { assert, delay, waitFor } from "./assertions.mjs";

export async function testAgentBatchLayout({ testAPI, onlineAgent }) {
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
  for (const field of form.querySelectorAll('.batch-controls label:not([hidden]), .batch-operation-row > button')) {
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
    assert.equal(form.querySelector("[data-batch-action-title]"), null, "动作说明不应重复展示");
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
    if (action === "status" || action === "start") assert.equal(dialog.querySelector("[data-confirm-message]").hidden, true, "无额外影响时不应重复动作说明");
    dialog.querySelector("[data-confirm-cancel]").click();
    await delay(20);
    assert.equal(form.dataset.busy, undefined, "取消确认不应提交任务");
  }

  // A large selection must keep full names available without pushing buttons off-screen.
  form.querySelector("[data-close-node-batch]").click();
  await waitFor(() => !document.querySelector("#batch-form"), "批量模式未关闭");
  const longName = "节点名称".repeat(24) + "<img src=x onerror=alert(1)>";
  testAPI.agents.push(...Array.from({ length: 24 }, (_, index) => ({
    ...onlineAgent(`extra-${index}`), name: `${longName}-${index}`,
  })));
  document.querySelector("[data-node-batch-toggle]").click();
  const expanded = await waitFor(() => document.querySelector("#batch-form"), "批量模式未重新打开");
  expanded.querySelector("[data-batch-select-all]").click();
  expanded.requestSubmit(expanded.querySelector('[type="submit"]'));
  const manyDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "大批量确认未打开");
  const targets = manyDialog.querySelector(".confirm-targets");
  assert.ok(targets.children.length >= 24);
  assert.ok(targets.textContent.includes(longName), "长名称不能丢失");
  assert.equal(targets.querySelector("img"), null, "节点名称不能被当作 HTML");
  assert.ok(targets.scrollHeight > targets.clientHeight, "大量节点应限制列表高度");
  assert.equal(targets.tabIndex, 0, "节点列表必须可用键盘滚动");
  assert.ok(manyDialog.scrollWidth <= manyDialog.clientWidth + 1, "长名称导致弹窗横向溢出");
  const acceptBox = manyDialog.querySelector("[data-confirm-accept]").getBoundingClientRect();
  assert.ok(acceptBox.bottom <= innerHeight && acceptBox.top >= 0, "大量节点遮挡了确认按钮");
  manyDialog.querySelector("[data-confirm-cancel]").click();

}
