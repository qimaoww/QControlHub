import { assert, delay, waitFor } from "./assertions.mjs";

export async function testUsersLayoutRuntime({ mode, testAPI }) {
await waitFor(() => document.querySelector(".user-admin-workspace"), "用户分配页未载入");
  if (new URLSearchParams(location.search).has("preview")) await new Promise(() => {});
  const workspace = () => document.querySelector(".user-admin-workspace");
  const form = () => document.querySelector("[data-user-access-form]");
  const dialog = () => document.querySelector("[data-allocation-dialog]");
  const row = id => document.querySelector(`[data-allocation-agent="${id}"]`);
  const status = id => row(id)?.querySelector("[data-share-status]").textContent;
  const change = (element, value) => {
    element.value = value;
    element.dispatchEvent(new Event(element.tagName === "SELECT" ? "change" : "input", { bubbles: true }));
  };
  const assertFits = async () => {
    await Promise.all(dialog().getAnimations().map(animation => animation.finished));
    for (const element of workspace().querySelectorAll(".users-toolbar, .user-allocation-head, .user-allocation-row, .user-allocation-terms, .user-owned-nodes, .user-allocation-dialog[open], .user-allocation-dialog .traffic-edit-body, .user-allocation-dialog .shared-engine-options, .user-quota-options")) {
      if (!element.getClientRects().length) continue;
      assert.ok(element.scrollWidth <= element.clientWidth + 1, `用户分配内容横向溢出：${element.className}`);
      const bounds = element.getBoundingClientRect();
      assert.ok(bounds.left >= -1 && bounds.right <= innerWidth + 1, "用户分配内容超出视口");
    }
    if (dialog().open) {
      const footer = form().querySelector("footer").getBoundingClientRect();
      const body = form().querySelector(".traffic-edit-body").getBoundingClientRect();
      assert.ok(footer.bottom <= innerHeight + 1 && footer.top >= body.bottom - 1, `分配弹窗的保存按钮被裁切或覆盖：${footer.bottom} / ${innerHeight}`);
      if (mode.endsWith("-mobile")) {
        const bounds = dialog().getBoundingClientRect();
        assert.ok(Math.abs(bounds.width - innerWidth) <= 1 && Math.abs(bounds.height - innerHeight) <= 1, "手机分配不是整页弹层");
      }
    }
  };
  const save = async () => {
    const before = testAPI.allocationWrites.length;
    form().requestSubmit();
    await waitFor(() => testAPI.allocationWrites.length === before + 1 && !form(), "分配未保存并返回列表");
    return testAPI.allocationWrites.at(-1);
  };
  assert.equal(form(), null, "分配结果仍是未保存的勾选表单");
  assert.equal(workspace().querySelectorAll("[data-allocation-agent]").length, 2);
  assert.equal(workspace().querySelector("[data-share-count]").textContent, "2");
  assert.ok(row("alpha").textContent.includes("未分配"), "空端口应显示为未分配");
  assert.equal(workspace().querySelector("[data-share-empty]"), null, "有分配时不应显示空状态");
  assert.equal(workspace().querySelector(".user-isolation, .settings-section-number, .settings-savebar"), null, "用户分配保留了重复说明或全局保存栏");
  assert.ok(workspace().querySelector(".user-allocation-head [data-allocation-add]"), "分配入口应集中在标题栏");
  const owned = workspace().querySelector(".user-owned-nodes");
  assert.ok(owned && !owned.open && !row("delta"), "自有节点未与共享分配分开");
  owned.querySelector("summary").click();
  assert.equal(owned.querySelector("li").textContent, "自有节点");
  owned.querySelector("summary").click();
  if (mode.endsWith("-mobile")) {
    assert.equal(innerWidth, 390);
    assert.ok(matchMedia("(pointer:coarse)").matches, "分配页手机测试未使用触控视口");
    assert.ok(document.querySelector("[data-user-mobile-select]").getClientRects().length, "手机缺少用户选择器");
  }
  const root = document.documentElement;
  const previousTheme = root.dataset.theme;
  const previousScale = root.style.getPropertyValue("--ui-font-scale");
  row("bravo").querySelector("[data-allocation-edit]").click();
  assert.ok(dialog().open && form().textContent.includes("Tokyo Edge"));
  assert.equal(form().elements.limit_gib.value, "50");
  assert.ok(form().querySelector('[name="quota_mode"][value="limited"]').checked);
  for (const theme of ["light", "dark"]) {
    root.dataset.theme = theme;
    for (const scale of ["1", "1.35"]) {
      root.style.setProperty("--ui-font-scale", scale);
      await delay(350);
      await assertFits();
    }
  }
  if (previousTheme) root.dataset.theme = previousTheme;
  else delete root.dataset.theme;
  if (previousScale) root.style.setProperty("--ui-font-scale", previousScale);
  else root.style.removeProperty("--ui-font-scale");
  change(form().elements.limit_gib, "75");
  assert.ok(row("bravo").textContent.includes("50 GiB") && !row("bravo").textContent.includes("75 GiB"), "列表提前展示未保存的额度");
  form().querySelector("[data-allocation-close]").click();
  assert.equal(testAPI.allocationWrites.length, 0, "取消编辑不能保存");
  assert.ok(!dialog().open && document.activeElement.matches('[data-allocation-edit="bravo"]'), "取消编辑没有恢复操作焦点");
  document.querySelector("[data-allocation-add]").click();
  assert.ok(dialog().open && form().querySelector('[type="submit"]').disabled, "未选择节点时应禁用发送邀请");
  assert.ok(form().querySelector("[data-allocation-fields]").hidden, "未选择节点就显示了无关的内核说明");
  assert.equal(form().querySelector('[data-share-agent] option[value="delta"]'), null, "自有节点不能被重复分配");
  assert.equal(form().querySelector('[data-share-agent] option[value="alpha"]'), null, "已有共享不能重复添加");
  change(form().querySelector("[data-share-agent]"), "charlie");
  assert.ok(!form().querySelector('[type="submit"]').disabled && !form().querySelector("[data-allocation-fields]").hidden);
  assert.equal(workspace().querySelector("[data-share-count]").textContent, "2", "未发送的邀请提前进入分配结果");
  assert.equal(form().querySelectorAll('[name="engines"]:checked').length, 0, "新分配不能默认扩大内核授权");
  assert.ok(form().querySelector('[name="quota_mode"][value="unlimited"]').checked && form().elements.limit_gib.disabled, "不限量仍要求填写魔法数字");
  await assertFits();
  form().requestSubmit();
  await waitFor(() => !form().querySelector("[data-user-error]").hidden, "未选内核应提示错误");
  assert.equal(testAPI.allocationWrites.length, 0, "无效分配不能提交");
  form().querySelector('[name="engines"][value="xray"]').click();
  form().querySelector('[name="quota_mode"][value="limited"]').click();
  change(form().elements.limit_gib, "0");
  form().requestSubmit();
  assert.ok(form().querySelector("[data-user-error]").textContent.includes("大于 0"), "限量选项把零额度静默解释为不限量");
  assert.equal(testAPI.allocationWrites.length, 0);
  change(form().elements.limit_gib, "2.5");
  assert.ok(form().elements.ports.placeholder.includes("21000-21100"), "端口输入没有范围示例");
  for (const [ports, error] of [["21000-21256", "256"], ["21000-21002,21001", "重叠"], ["10084-10087", "保留"], ["21100-21000", "起始"], ["0,21001", "混用"]]) {
    change(form().elements.ports, ports);
    form().requestSubmit();
    assert.ok(form().querySelector("[data-user-error]").textContent.includes(error), `无效范围没有明确提示：${ports}`);
    assert.equal(testAPI.allocationWrites.length, 0, "无效端口范围不能提交");
  }
  change(form().elements.ports, "21000-21100, 22000");
  await assertFits();
  const saved = await save();
  assert.ok(saved.isolated && saved.user_id === "cdn" && saved.revision === 4, "保存改变了账号隔离或覆盖保护");
  const allocation = saved.shares.find(share => share.agent_id === "charlie");
  assert.equal(allocation.limit_bytes, 2.5 * 1024 ** 3);
  assert.equal(allocation.ports.join(","), [...Array.from({ length: 101 }, (_, index) => 21000 + index), 22000].join(","),
    "范围没有按首尾包含的完整端口列表提交");
  assert.equal(allocation.engines.join(","), "xray", "保存扩大了内核授权");
  assert.equal(saved.shares.find(share => share.agent_id === "alpha").limit_bytes, 0, "零额度的不限量语义改变");
  assert.equal(saved.shares.length, 3, "添加一个节点时遗漏了既有分配");
  assert.equal(status("alpha"), "待接受", "待接受的共享不能提前生效");
  assert.equal(status("charlie"), "待接受", "发送邀请被展示为已接受");
  assert.ok(document.querySelector("[data-allocation-add]").disabled, "没有剩余节点时分配入口应禁用");
  assert.ok(row("charlie").textContent.includes("21000-21100, 22000"), "已保存的端口范围展开成了长列表");
  row("charlie").querySelector("[data-allocation-edit]").click();
  assert.equal(form().elements.ports.value, "21000-21100, 22000", "重新编辑没有保留完整范围");
  form().querySelector("[data-allocation-close]").click();
  assert.equal(testAPI.allocationWrites.length, 1, "关闭范围编辑意外保存");
  for (const [text, ports, label] of [["0", [0], "无限制"], ["", [], "未分配"], ["21001-21003", [21001, 21002, 21003], "21001-21003"], ["0", [0], "无限制"]]) {
    row("charlie").querySelector("[data-allocation-edit]").click();
    change(form().elements.ports, text);
    const saved = await save(), share = saved.shares.find(share => share.agent_id === "charlie");
    assert.equal(JSON.stringify(share.ports), JSON.stringify(ports), "端口权限模式未正确保存");
    assert.equal(share.limit_bytes, 2.5 * 1024 ** 3, "端口模式改变了流量额度");
    assert.ok(row("charlie").textContent.includes(label), "端口权限模式未正确显示");
    row("charlie").querySelector("[data-allocation-edit]").click();
    assert.equal(form().elements.ports.value, text, "重新编辑丢失端口模式");
    await assertFits();
    form().querySelector("[data-allocation-close]").click();
  }
  await assertFits();
  row("bravo").querySelector("[data-allocation-edit]").click();
  change(form().elements.limit_gib, "75");
  await save();
  assert.equal(status("bravo"), "已接受", "仅增加额度不应重新邀请");
  row("bravo").querySelector("[data-allocation-edit]").click();
  form().querySelector('[name="quota_mode"][value="unlimited"]').click();
  const unlimited = await save();
  assert.equal(unlimited.shares.find(share => share.agent_id === "bravo").limit_bytes, 0, "不限量选择没有转成 API 的零额度");
  assert.equal(unlimited.shares.find(share => share.agent_id === "charlie").ports.join(","), "0", "编辑其他节点清除了无限制端口授权");
  row("bravo").querySelector("[data-allocation-edit]").click();
  assert.ok(form().querySelector("[data-allocation-consent]").hidden, "未改变内核时重复提示邀请");
  form().querySelector('[name="engines"][value="sing-box"]').click();
  assert.ok(!form().querySelector("[data-allocation-consent]").hidden, "增加内核没有提示重新接受");
  await save();
  assert.equal(status("bravo"), "待接受", "增加内核跳过用户同意");
  const beforeRevoke = testAPI.allocationWrites.length;
  row("alpha").querySelector("[data-allocation-revoke]").click();
  let confirmation = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "撤销共享没有独立确认");
  assert.ok(confirmation.textContent.includes("Catixs HK") && confirmation.textContent.includes("端口预留会保留"), "撤销确认缺少对象或保留说明");
  assert.equal(testAPI.allocationWrites.length, beforeRevoke, "确认前已撤销");
  confirmation.querySelector("[data-confirm-cancel]").click();
  await waitFor(() => !row("alpha").querySelector("[data-allocation-revoke]").disabled, "取消撤销未解锁");
  assert.equal(testAPI.allocationWrites.length, beforeRevoke);
  row("alpha").querySelector("[data-allocation-revoke]").click();
  confirmation = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "再次撤销未确认");
  confirmation.querySelector("[data-confirm-accept]").click();
  await waitFor(() => status("alpha") === "已撤销", "确认后未撤销分配");
  assert.equal(testAPI.allocationWrites.at(-1).shares.find(share => share.agent_id === "alpha").enabled, false);
  row("alpha").querySelector("[data-allocation-invite]").click();
  assert.equal(testAPI.allocationWrites.length, beforeRevoke + 1, "打开重新邀请弹窗就发送了请求");
  await save();
  assert.equal(status("alpha"), "待接受", "恢复共享跳过用户同意");
  const restored = testAPI.allocationWrites.at(-1).shares.find(share => share.agent_id === "alpha");
  assert.equal(restored.enabled, true, "恢复共享没有重新启用");
  assert.equal(restored.reinvite, false, "恢复撤销前待接受的共享误传了仅适用于已拒绝共享的标记");
  assert.equal(testAPI.calls.some(call => call.path === "/tasks" && call.method === "POST"), false, "分配页面不能自行执行远程任务");

  if (mode.endsWith("-mobile")) change(document.querySelector("[data-user-mobile-select]"), "new-user");
  else document.querySelector('[data-user-select="new-user"]').click();
  await waitFor(() => document.querySelector(".users-toolbar h2").textContent === "未分配用户", "未切换到空分配用户");
  assert.ok(workspace().querySelector("[data-share-empty]"));
  assert.equal(workspace().querySelector("[data-share-count]").textContent, "0");
  document.querySelector("[data-allocation-add]").click();
  change(form().querySelector("[data-share-agent]"), "bravo");
  form().querySelector('[name="engines"][value="mihomo"]').click();
  await save();
  assert.equal(testAPI.allocationWrites.at(-1).shares[0].ports.length, 0, "新分配留空时隐式授予了端口权限");
  assert.ok(row("bravo").textContent.includes("未分配"), "新分配留空时没有显示未分配");
  assert.equal(workspace().querySelector("[data-share-empty]"), null, "发送首个邀请后空状态未隐藏");
  assert.equal(workspace().querySelector("[data-share-count]").textContent, "1");
  assert.equal(testAPI.allocationWrites.at(-1).user_id, "new-user", "分配被写入上一个账号");
}
