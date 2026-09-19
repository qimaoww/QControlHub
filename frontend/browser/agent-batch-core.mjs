import { assert, delay, waitFor } from "./assertions.mjs";

export async function testBatchCoreUpdates({ testAPI }) {
  const form = document.querySelector("#batch-form");
  const change = (name, value) => {
    form.elements[name].value = value;
    form.elements[name].dispatchEvent(new Event("change", { bubbles: true }));
  };
  const submit = form.querySelector('[type="submit"]');
  const alpha = form.querySelector('[data-batch-checkbox][value="alpha"]');
  form.querySelector("[data-batch-clear]").click();
  change("action", "install");
  change("engine", "mihomo");
  assert.equal(form.querySelector("[data-batch-version-wrap]").hidden, false);
  assert.equal(form.elements.custom_version.disabled, true);
  assert.equal(form.querySelector('[data-batch-checkbox][value="charlie"]').disabled, true);
  change("engine", "sing-box");
  assert.equal(alpha.disabled, true, "批量更新不能安装尚未安装的内核");
  change("engine", "mihomo");
  alpha.click();

  const confirm = async (versionLabel) => {
    form.requestSubmit(submit);
    const dialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "内核更新没有确认弹窗");
    assert.ok(dialog.textContent.includes(versionLabel), "确认弹窗没有显示目标版本");
    assert.match(dialog.textContent, /重启/);
    dialog.querySelector("[data-confirm-accept]").click();
  };
  let next = testAPI.pendingTasks.length;
  for (const channel of ["stable", "development"]) {
    change("release_channel", channel);
    await confirm(channel === "stable" ? "最新稳定版" : "最新开发版");
    await waitFor(() => testAPI.pendingTasks.length === next + 1, "版本任务未提交");
    assert.equal(JSON.stringify(testAPI.pendingTasks[next].payload), JSON.stringify({ agent_id: "alpha", action: "install", engine: "mihomo", core_version: channel }));
    assert.equal(form.elements.release_channel.disabled, true, "提交时未锁定版本选项");
    testAPI.pendingTasks[next++].ok({ id: `core-${channel}` });
    await waitFor(() => form.dataset.busy !== "1", "版本提交后未解除 busy");
    assert.equal(form.querySelector("[data-batch-result-details]").hidden, true, "全部提交成功后应收起明细");
    form.querySelector("[data-batch-results-toggle]").click();
    assert.equal(form.querySelector("[data-batch-result-details]").hidden, false, "成功结果应可重新展开");
  }
  change("release_channel", "custom");
  assert.equal(form.elements.custom_version.required, true);
  assert.equal(form.checkValidity(), false, "指定版本为空时应阻止提交");
  change("custom_version", "   ");
  form.requestSubmit(submit);
  await delay(20);
  assert.equal(testAPI.pendingTasks.length, next, "空白版本号不能提交");
  assert.equal(document.querySelector("[data-confirm-dialog][open]"), null);
  change("custom_version", " v1.19.29 ");
  await confirm("v1.19.29");
  await waitFor(() => testAPI.pendingTasks.length === next + 1, "指定版本未提交");
  const original = { ...testAPI.pendingTasks[next].payload };
  assert.equal(original.core_version, "v1.19.29");
  assert.equal(form.elements.custom_version.disabled, true);
  testAPI.pendingTasks[next++].fail("temporary core update failure");
  await waitFor(() => form.dataset.busy !== "1", "失败后未解除 busy");
  change("custom_version", "v9.9.9");
  change("action", "restart");
  assert.equal(form.elements.release_channel.disabled, true);
  assert.equal(form.elements.custom_version.required, false);
  form.querySelector("[data-batch-retry]").click();
  await waitFor(() => testAPI.pendingTasks.length === next + 1, "更新失败重试未提交");
  assert.equal(JSON.stringify(testAPI.pendingTasks[next].payload), JSON.stringify(original), "重试必须保留原内核、动作和版本");
  testAPI.pendingTasks[next++].ok({ id: "core-retry" });
  await waitFor(() => form.dataset.busy !== "1", "重试后未解除 busy");
  await confirm("重启");
  await waitFor(() => testAPI.pendingTasks.length === next + 1, "服务操作未提交");
  assert.equal(JSON.stringify(testAPI.pendingTasks[next].payload), JSON.stringify({ agent_id: "alpha", action: "restart", engine: "mihomo" }), "普通操作不应携带版本参数");
  testAPI.pendingTasks[next].ok({ id: "restart-after-core" });
  await waitFor(() => form.dataset.busy !== "1", "服务操作后未解除 busy");
}
