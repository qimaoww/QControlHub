import { assert, waitFor } from "./assertions.mjs";
export async function testAgentRenameActions({ testAPI, populatedAgents }) {
  const renameForm = document.querySelector('[data-agent-name-form="alpha"]');
  assert.ok(renameForm, "节点身份页缺少改名表单");
  const renameInput = renameForm.elements.namedItem("name");
  const renameButton = renameForm.querySelector('button[type="submit"]');
  const renameCalls = () => testAPI.calls.filter((call) => call.method === "PUT" && call.path === "/agents/alpha/name").length;
  const renameBefore = renameCalls();
  renameInput.value = "   ";
  renameForm.requestSubmit(renameButton);
  assert.equal(renameCalls(), renameBefore, "空白名称不应提交");
  testAPI.renameFailure = true;
  renameInput.value = "香港 & Tokyo <edge>";
  renameForm.requestSubmit(renameButton);
  await waitFor(() => !renameButton.disabled, "改名失败后保存按钮未恢复");
  assert.equal(renameInput.value, "香港 & Tokyo <edge>", "改名失败后丢失输入");
  assert.match(document.body.textContent, /服务暂不可用，请稍后重试/);
  testAPI.renameFailure = false;
  renameForm.requestSubmit(renameButton);
  await waitFor(() => document.querySelector(".node-operations-title h2")?.textContent === "香港 & Tokyo <edge>", "改名后标题未更新");
  assert.equal(document.querySelector(".node-operations-title edge"), null, "节点名称未进行 HTML 转义");
  assert.equal(document.querySelector('[data-agent-name-form="alpha"] input').value, "香港 & Tokyo <edge>");
  document.querySelector("[data-agent-refresh]").click();
  await waitFor(() => !document.querySelector("[data-agent-refresh]").disabled, "改名后节点刷新未完成");
  assert.equal(document.querySelector(".node-operations-title h2").textContent, "香港 & Tokyo <edge>", "指标刷新覆盖自定义名称");
  let completeRename;
  testAPI.renameGate = new Promise((resolve) => { completeRename = resolve; });
  const pendingForm = document.querySelector('[data-agent-name-form="alpha"]');
  const pendingInput = pendingForm.elements.namedItem("name");
  const pendingButton = pendingForm.querySelector('button[type="submit"]');
  const pendingCalls = renameCalls();
  pendingInput.value = "正在保存的名称";
  pendingForm.requestSubmit(pendingButton);
  await waitFor(() => renameCalls() === pendingCalls + 1, "改名请求未发出");
  pendingInput.value = "保存期间继续编辑的名称";
  pendingForm.requestSubmit();
  assert.equal(renameCalls(), pendingCalls + 1, "重复提交产生并行改名请求");
  completeRename();
  await waitFor(() => document.querySelector(".node-operations-title h2")?.textContent === "正在保存的名称", "延迟改名未完成");
  assert.equal(document.querySelector('[data-agent-name-form="alpha"] input').value, "保存期间继续编辑的名称", "迟到的保存响应覆盖了新草稿");
  testAPI.renameGate = null;
  testAPI.agents = testAPI.agents.filter((agent) => agent.id !== "alpha");
  document.querySelector("[data-agent-refresh]").click();
  await waitFor(() => document.querySelector("[data-node-missing]"), "删除当前节点后未渲染详情缺失状态");
  assert.equal(document.querySelector("[data-open-enrollment]"), null, "删除当前节点后不得回退到添加入口");
  assert.equal(document.querySelector("#batch-form"), null, "删除当前节点后不得回退到批量表单");
  assert.equal(document.querySelector(".node-batch-bar"), null, "删除当前节点后不得回退到底部批量栏");
  testAPI.agents = populatedAgents.slice();
  location.hash = "#settings-node-unknown";
  await waitFor(() => document.querySelector("[data-node-missing]"), "未知节点详情路由未渲染缺失状态");
  assert.equal(document.querySelector("[data-open-enrollment]"), null, "未知节点详情不得渲染添加入口");
  assert.equal(document.querySelector("#batch-form"), null, "未知节点详情不得渲染批量表单");
  location.hash = "#settings-node-bravo";
  await waitFor(
    () => document.querySelector('.node-operations-workspace[data-agent-node="bravo"]'),
    "无可恢复命令的单节点详情没有完成渲染",
  );
  assert.equal(document.querySelector('[data-view-enrollment-command="bravo"]'), null, "无可恢复命令的节点不得渲染查看入口");
}
