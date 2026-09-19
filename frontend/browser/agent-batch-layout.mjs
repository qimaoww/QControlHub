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
}
