import { assert, delay, waitFor } from "./assertions.mjs";

export async function testEnrollmentLayoutRuntime({ mode, testAPI }) {
const open = async () => {
    const launcher = await waitFor(() => document.querySelector("[data-open-enrollment]"), "添加节点入口没有载入");
    launcher.click();
    const dialog = await waitFor(() => document.querySelector(".enrollment-create-dialog"), "添加节点弹窗没有打开");
    await Promise.all(dialog.getAnimations().map(animation => animation.finished));
    return dialog;
  };
  const assertFits = (dialog) => {
    const rect = dialog.getBoundingClientRect();
    assert.ok(rect.left >= -1 && rect.right <= innerWidth + 1 && rect.top >= -1 && rect.bottom <= innerHeight + 1,
      "添加节点弹窗超出视口");
    for (const selector of [".enrollment-dialog-body", "form", ".enrollment-visibility", ".enrollment-form-actions", ".enrollment-history"]) {
      const element = dialog.querySelector(selector);
      assert.ok(element.scrollWidth <= element.clientWidth + 1, `添加节点内容横向溢出：${selector}`);
    }
    const checkbox = dialog.querySelector('[name="admin_hidden"]').getBoundingClientRect();
    const title = dialog.querySelector("#enrollment-visibility-title").getBoundingClientRect();
    assert.ok(title.left > checkbox.right && Math.abs(title.top - checkbox.top) < 3,
      "权限复选框与说明不在同一行");
    assert.equal(checkbox.width, 18, "权限复选框被普通文本输入框样式拉伸");
    assert.equal(checkbox.height, 18, "权限复选框高度异常");
    const [cancel, submit] = [...dialog.querySelectorAll(".enrollment-form-actions .button")].map(button => button.getBoundingClientRect());
    assert.ok(cancel.right <= submit.left && Math.abs(cancel.top - submit.top) < 1, "表单按钮未同排对齐");
  };
  const dialog = await open();
  assert.equal(dialog.querySelector(".eyebrow"), null, "添加节点不应重复展示部署标题");
  assert.equal(dialog.querySelector(".enrollment-security-note"), null, "添加节点不应重复展示命令页的凭据说明");
  assert.equal(dialog.querySelector("#enrollment-dialog-description").textContent, "命令仅供复制，不会自动执行。");
  const history = dialog.querySelector(".enrollment-history-disclosure");
  assert.ok(history && !history.open, "空的添加记录应默认收起");
  assert.equal(history.querySelector("[data-enrollment-history-count]").textContent, "0");
  history.querySelector("summary").click();
  assert.ok(history.open && history.querySelector(".enrollment-history-empty").getClientRects().length,
    "仍应能展开查看空记录说明");
  history.querySelector("summary").click();
  const checkbox = dialog.querySelector('[name="admin_hidden"]');
  assert.equal(checkbox.checked, false, "优化布局不能改变管理员可见性默认值");
  dialog.querySelector("#enrollment-visibility-title").click();
  assert.equal(checkbox.checked, true, "点击权限说明应切换复选框");

  if (mode.endsWith("-mobile")) {
    assert.equal(innerWidth, 390);
    assert.ok(matchMedia("(pointer:coarse)").matches, "手机回归必须使用真实触控视口");
  }
  const root = document.documentElement;
  const previousTheme = root.dataset.theme;
  const previousScale = root.style.getPropertyValue("--ui-font-scale");
  for (const theme of ["light", "dark"]) {
    root.dataset.theme = theme;
    for (const scale of ["1", "1.35"]) {
      root.style.setProperty("--ui-font-scale", scale);
      await delay(350);
      assertFits(dialog);
    }
  }
  if (previousTheme) root.dataset.theme = previousTheme;
  else delete root.dataset.theme;
  if (previousScale) root.style.setProperty("--ui-font-scale", previousScale);
  else root.style.removeProperty("--ui-font-scale");
  if (new URLSearchParams(location.search).has("preview")) await new Promise(() => {});

  const form = dialog.querySelector("form");
  const submit = form.querySelector('[type="submit"]');
  form.elements.name.value = "browser-node";
  testAPI.enrollmentFailure = true;
  form.requestSubmit(submit);
  await waitFor(() => !submit.disabled, "生成失败后按钮未恢复");
  assert.ok(checkbox.checked && form.elements.name.value === "browser-node", "失败后丢失了节点名称或可见性选择");
  testAPI.enrollmentFailure = false;
  form.requestSubmit(submit);
  let command = await waitFor(() => document.querySelector(".deploy-command-modal:not(.enrollment-dialog)"), "部署命令未显示");
  assert.equal(JSON.stringify(testAPI.lastEnrollmentRequest), JSON.stringify({ name: "browser-node", admin_hidden: true }),
    "勾选后的权限值没有提交");
  assert.ok(command.querySelector("[data-command]").readOnly, "命令必须保持只读复制模式");
  assert.equal(testAPI.calls.some(call => call.path === "/tasks" && call.method === "POST"), false,
    "生成命令不能自动执行远程任务");
  command.querySelector("[data-close]").click();

  const next = await open();
  assert.equal(next.querySelector('[name="admin_hidden"]').checked, false, "重新打开不能继承上次的权限选择");
  next.querySelector('[name="name"]').value = "browser-node";
  next.querySelector("form").requestSubmit();
  command = await waitFor(() => document.querySelector(".deploy-command-modal:not(.enrollment-dialog)"), "未勾选时不能生成命令");
  assert.equal(testAPI.lastEnrollmentRequest.admin_hidden, false, "未勾选时权限值被改变");
  command.querySelector("[data-close]").click();

  testAPI.enrollmentRecords = [{
    id: "enr-alpha", name: `上海边缘节点-${"long-name-".repeat(9)}`,
    reusable: true, used_count: 2, command_available: true,
  }];
  location.hash = "#dashboard";
  await waitFor(() => document.querySelector(".dashboard-head"), "未离开节点页");
  location.hash = "#node-settings";
  const populated = await open();
  const records = populated.querySelector(".enrollment-history-disclosure");
  assert.ok(records.open, "已有添加记录应默认展开");
  assert.equal(records.querySelector("[data-enrollment-history-count]").textContent, "1");
  assert.equal(records.querySelectorAll("article").length, 1, "已有记录不能丢失");
  assertFits(populated);
  records.querySelector("summary").click();
  const summary = records.querySelector("summary");
  const close = populated.querySelector("[data-close]");
  summary.focus();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
  assert.equal(document.activeElement, close, "折叠记录后 Tab 不能跳出弹窗");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
  assert.equal(document.activeElement, summary, "折叠记录后的焦点不能落到隐藏按钮");
  summary.click();
  assert.ok(records.querySelector("[data-view-enrollment-record]").getClientRects().length,
    "重新展开后原记录操作应仍可见");
  close.click();
}
