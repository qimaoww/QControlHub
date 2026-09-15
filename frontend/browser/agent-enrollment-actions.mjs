import { assert, delay, waitFor, assertNoPersistentEnrollment, responsiveDialogRuleExists } from "./assertions.mjs";
export async function testAgentEnrollmentActions({ testAPI }) {
  assertNoPersistentEnrollment();
  assert.equal(document.querySelector("#batch-form"), null, "批量操作栏不应常驻聚合页");
  assert.ok(document.querySelector("[data-node-batch-toggle]"), "聚合页顶栏缺少批量操作入口");
  const launcher = document.querySelector("[data-open-enrollment]");
  assert.ok(launcher, "有权限的 populated 聚合页缺少添加节点入口");
  const originalHash = location.hash;
  const workspace = document.querySelector(".workspace-main");
  workspace.scrollTop = 37;
  const originalScrollTop = workspace.scrollTop;
  launcher.focus();
  launcher.click();
  const backdrop = await waitFor(() => document.querySelector(".modal-backdrop"), "添加节点入口没有打开 modal");
  const dialog = backdrop.querySelector('[role="dialog"]');
  assert.equal(dialog.getAttribute("aria-modal"), "true");
  assert.equal(dialog.getAttribute("aria-labelledby"), "enrollment-dialog-title");
  assert.equal(dialog.getAttribute("aria-describedby"), "enrollment-dialog-description");
  assert.equal(location.hash, originalHash, "打开 modal 不应切换 route");
  assert.equal(document.activeElement, dialog.querySelector("input"));
  assert.equal(document.querySelector(".desktop-app").inert, true);
  assert.equal(document.body.style.overflow, "hidden");
  const backdropStyle = getComputedStyle(backdrop);
  assert.equal(backdropStyle.position, "fixed");
  assert.equal(backdropStyle.display, "grid");
  assert.notEqual(backdropStyle.backgroundColor, "rgba(0, 0, 0, 0)");
  assert.equal(getComputedStyle(dialog).display, "flex");
  assert.equal(responsiveDialogRuleExists(), true, "浏览器 CSSOM 未包含窄屏 modal 最终规则");

  const focusable = [...backdrop.querySelectorAll("button:not(:disabled), input:not(:disabled)")];
  focusable.at(-1).focus();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
  assert.equal(document.activeElement, focusable[0], "Tab 没有限制在 modal 内");
  focusable[0].focus();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
  assert.equal(document.activeElement, focusable.at(-1), "Shift+Tab 没有限制在 modal 内");

  window.dispatchEvent(new HashChangeEvent("hashchange"));
  await waitFor(
    () => document.querySelector(".modal-backdrop") === backdrop &&
      !document.querySelector(".workspace-main[aria-busy]"),
    "同 route 刷新未完成或丢失了打开的 modal",
  );
  await delay();
  assert.equal(document.querySelector(".desktop-app").inert, true, "刷新后的背景没有继续 inert");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  assert.equal(document.querySelector(".modal-backdrop"), null);
  assert.equal(document.querySelector(".desktop-app").inert, false);
  assert.equal(document.body.style.overflow, "");
  assert.equal(document.activeElement, document.querySelector("[data-open-enrollment]"));
  assert.equal(document.querySelector(".workspace-main").scrollTop, originalScrollTop);

  document.querySelector("[data-open-enrollment]").click();
  const enrollment = await waitFor(() => document.querySelector(".enrollment-dialog"), "无法重新打开 enrollment modal");
  testAPI.enrollmentFailure = true;
  enrollment.querySelector('input[name="name"]').value = "browser-node";
  const enrollmentForm = enrollment.querySelector("form");
  const enrollmentSubmit = enrollmentForm.querySelector('button[type="submit"]');
  enrollmentForm.requestSubmit(enrollmentSubmit);
  await waitFor(() => !enrollmentSubmit.disabled, "enrollment 失败后提交按钮未恢复");
  assert.equal(document.querySelector(".enrollment-dialog"), enrollment);
  assert.match(document.body.textContent, /服务暂不可用，请稍后重试/);
  assert.equal(document.body.textContent.includes("temporary enrollment failure"), false);
  testAPI.enrollmentFailure = false;
  enrollment.querySelector("[data-close]").click();

  document.querySelector("[data-open-enrollment]").click();
  let recordsDialog = await waitFor(() => document.querySelector(".enrollment-dialog"), "添加记录弹窗无法重新打开");
  let recordButton = document.querySelector('[data-view-enrollment-record="enr-alpha"]');
  assert.ok(recordButton, "可恢复的聚合添加记录缺少查看入口");
  const recordReadsBefore = testAPI.calls.filter(
    (call) => call.method === "POST" && call.path === "/enrollment-tokens/enr-alpha/command",
  ).length;
  recordButton.click();
  const firstRecordDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal:not(.enrollment-dialog)"),
    "聚合添加记录无法打开已有部署命令",
  );
  assert.equal(document.querySelectorAll(".modal-backdrop").length, 1, "查看记录不得叠加第二层 modal");
  assert.equal(document.querySelector(".enrollment-dialog"), null, "查看记录后聚合 modal 必须关闭");
  assert.equal(document.querySelector(".desktop-app").inert, true, "命令 modal 打开时背景必须 inert");
  const recordCommand = firstRecordDialog.querySelector("[data-command]").value;
  const commandFocusable = [...firstRecordDialog.querySelectorAll("button:not(:disabled), textarea:not(:disabled)")];
  commandFocusable.at(-1).focus();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
  assert.equal(document.activeElement, commandFocusable[0], "记录命令 modal 的 Tab 必须保持在顶层 modal 内");
  commandFocusable[0].focus();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
  assert.equal(document.activeElement, commandFocusable.at(-1), "记录命令 modal 的 Shift+Tab 必须保持在顶层 modal 内");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  assert.equal(document.querySelector(".modal-backdrop"), null, "Escape 只能关闭顶层记录命令 modal");
  assert.equal(document.querySelector(".desktop-app").inert, false, "关闭命令 modal 后背景 inert 必须恢复");
  assert.equal(document.activeElement, document.querySelector("[data-open-enrollment]"), "关闭命令 modal 后焦点必须回到聚合入口");
  document.querySelector("[data-open-enrollment]").click();
  recordsDialog = await waitFor(() => document.querySelector(".enrollment-dialog"), "关闭命令后聚合 modal 无法重新打开");
  recordButton = recordsDialog.querySelector('[data-view-enrollment-record="enr-alpha"]');
  recordButton.click();
  const secondRecordDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal:not(.enrollment-dialog)"),
    "聚合添加记录无法重复打开已有部署命令",
  );
  assert.equal(secondRecordDialog.querySelector("[data-command]").value, recordCommand);
  secondRecordDialog.querySelector("[data-close]").click();
  recordsDialog.querySelector("[data-close]").click();
  location.hash = "#dashboard";
  await waitFor(() => document.querySelector(".dashboard-head"), "路由离开后未完成刷新");
  location.hash = "#node-settings";
  await waitFor(() => document.querySelector(".node-card-grid"), "路由往返后聚合页未恢复");
  assert.equal(document.querySelector("#batch-form"), null, "路由往返后批量栏不应自动打开");
  document.querySelector("[data-open-enrollment]").click();
  await waitFor(() => document.querySelector(".enrollment-dialog"), "刷新后添加记录弹窗无法打开");
  assert.ok(document.querySelector('[data-view-enrollment-record="enr-alpha"]'), "刷新后可恢复添加记录缺少查看入口");
  const recordButtonAfterRefresh = document.querySelector('[data-view-enrollment-record="enr-alpha"]');
  recordButtonAfterRefresh.click();
  const refreshedRecordDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal:not(.enrollment-dialog)"),
    "刷新后无法再次打开已有部署命令",
  );
  assert.equal(refreshedRecordDialog.querySelector("[data-command]").value, recordCommand);
  refreshedRecordDialog.querySelector("[data-close]").click();
  const recordReadsAfter = testAPI.calls.filter(
    (call) => call.method === "POST" && call.path === "/enrollment-tokens/enr-alpha/command",
  ).length;
  assert.equal(recordReadsAfter, recordReadsBefore + 3, "聚合命令重复查看应保持同一读取接口，不得创建新凭据");

}
