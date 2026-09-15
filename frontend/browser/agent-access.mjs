import { assert, waitFor, assertNoPersistentEnrollment } from "./assertions.mjs";

export async function testEmptyRuntime() {
await waitFor(() => document.querySelector(".empty.large"), "空列表没有渲染 empty state");
  assertNoPersistentEnrollment();
  assert.ok(document.querySelector("[data-open-enrollment]"), "空列表有权限用户缺少添加节点入口");
  assert.equal(document.querySelector("#batch-form"), null);
}

export async function testSharedNodeRuntime({ mode, testAPI, populatedAgents }) {
const assertSharedStatusOrder = (root) => {
    const badge = root.querySelector(".agent-shared-badge");
    const status = root.querySelector("[data-agent-status-label]");
    assert.ok(badge && status, "共享节点缺少共享标记或在线状态");
    const badgeRect = badge.getBoundingClientRect(), statusRect = status.getBoundingClientRect();
    assert.ok(badge.nextElementSibling?.matches("[data-agent-status-dot]"), "共享标记未紧邻在线状态左侧");
    assert.ok(badgeRect.right <= statusRect.left, "共享标记没有显示在在线状态左侧");
    assert.ok(Math.abs((badgeRect.top + badgeRect.bottom - statusRect.top - statusRect.bottom) / 2) <= 1,
      "共享标记与在线状态没有同排对齐");
    if (mode === "shared-node-mobile") {
      assert.equal(innerWidth, 390, "手机回归没有使用真实的 390px 视口");
      assert.ok(matchMedia("(pointer:coarse)").matches, "手机回归没有启用触控设备");
      const rect = root.getBoundingClientRect();
      assert.ok(rect.left >= 0 && rect.right <= innerWidth + 1 && root.scrollWidth <= root.clientWidth + 1,
        "手机共享节点列表或详情被裁切");
    }
  };
  await waitFor(() => document.querySelector('.node-card[data-agent-node="alpha"]'), "共享节点列表未渲染");
  assert.equal(document.querySelectorAll(".node-card .agent-shared-badge").length, 1, "自有和共享节点没有明确区分");
  assertSharedStatusOrder(document.querySelector('.node-card[data-agent-node="alpha"]'));
  assert.ok(document.querySelector("[data-open-enrollment]"), "共享接收者无法添加自有节点");
  location.hash = "#settings-node-alpha";
  await waitFor(() => document.querySelector(".node-operations-workspace"), "共享节点详情未渲染");
  assertSharedStatusOrder(document.querySelector(".node-operations-workspace"));
  assert.equal(document.querySelectorAll(".core-runtime-row").length, 1, "详情显示了未分配的内核");
  assert.equal(document.querySelector(".node-panel-heading h3").textContent, "已分配内核");
  assert.ok(document.querySelector('[data-config="alpha"][data-engine="mihomo"]'), "共享内核没有独立配置入口");
  assert.equal(document.querySelector('[data-open-version-form], [data-version-agent], [data-engine-capability], [data-agent-name-form], [data-komari-form], [data-agent-sharing], [data-upgrade-agent], [data-delete], [data-view-enrollment-command]'), null, "共享详情仍显示宿主管理控件");
  if (mode === "shared-node-mobile") {
    for (const selector of [".node-operations-workspace", ".node-resource-strip", ".core-runtime-row", '[data-config="alpha"]']) {
      const element = document.querySelector(selector), rect = element.getBoundingClientRect();
      assert.ok(rect.left >= 0 && rect.right <= innerWidth + 1 && element.scrollWidth <= element.clientWidth + 1,
        `手机共享详情被裁切：${selector}`);
    }
  }
  document.querySelector('[data-node-tab="agent"]').click();
  assert.ok(document.querySelector('[data-node-panel="agent"] a[href="#my-quota"]'), "共享身份页缺少分配入口");
  assert.ok(document.querySelector(".node-operations-workspace").scrollWidth <= document.querySelector(".node-operations-workspace").clientWidth + 1, "共享节点详情横向溢出");
  location.hash = "#settings-node-bravo";
  await waitFor(() => document.querySelector('[data-agent-name-form="bravo"]'), "自有节点管理未保留");
  assert.ok(document.querySelector('[data-agent-sharing="bravo"]'), "自有节点不能继续分享");
  assert.equal(document.querySelector(".agent-shared-badge"), null, "自有节点被标记为共享");
  location.hash = "#settings-node-alpha";
  await waitFor(() => document.querySelector('[data-config="alpha"]'), "重新打开共享节点失败");
  testAPI.agents = [populatedAgents[1]];
  document.querySelector("[data-agent-refresh]").click();
  await waitFor(() => document.querySelector("[data-node-missing]"), "撤销后仍保留共享详情");
  assert.equal(document.querySelector('[data-config="alpha"]'), null, "撤销后保留旧配置操作入口");
}

export async function testReadonlyRuntime({ testAPI }) {
await waitFor(() => document.querySelector(".workspace-main"), "只读聚合页没有完成渲染");
  assertNoPersistentEnrollment();
  assert.equal(document.querySelector("[data-open-enrollment]"), null);
  assert.equal(document.querySelector("#batch-form"), null);
  location.hash = "#settings-node-alpha";
  const renameForm = await waitFor(() => document.querySelector('[data-agent-name-form="alpha"]'), "只读节点详情未完成渲染");
  assert.equal(renameForm.querySelector("input").disabled, true, "只读用户可编辑节点名称");
  assert.equal(renameForm.querySelector("button").disabled, true, "只读用户可提交改名");
  assert.equal(document.querySelector("[data-region-edit]"), null, "只读用户不应编辑旗帜");
  renameForm.requestSubmit();
  assert.equal(testAPI.calls.some((call) => call.method === "PUT" && call.path.endsWith("/name")), false, "只读用户触发改名请求");
  assert.equal(
    testAPI.calls.some((call) => call.path === "/enrollment-tokens"),
    false,
    "无 enrollment.manage 权限不应读取添加记录",
  );
  location.hash = "#client-access";
  await waitFor(() => document.querySelector(".client-profile-row"), "只读客户端页未渲染");
  assert.equal(document.querySelector("[data-client-display-open]"),null,"只读用户可修改端口名称");
  await waitFor(() => document.querySelector('.client-access-node-card [data-region-avatar] img[src="/api/v1/region-flags/cn"]'), "只读用户客户端旗帜没有显示");
}
