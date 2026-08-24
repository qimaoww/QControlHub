const fail = (message) => {
  throw new Error(message);
};
const assert = {
  ok(value, message = "expected a truthy value") {
    if (!value) fail(message);
  },
  equal(actual, expected, message = "values are not equal") {
    if (!Object.is(actual, expected))
      fail(`${message}: actual=${String(actual)} expected=${String(expected)}`);
  },
  notEqual(actual, expected, message = "values unexpectedly match") {
    if (Object.is(actual, expected))
      fail(`${message}: actual=${String(actual)}`);
  },
  match(actual, pattern, message = "value does not match") {
    if (!pattern.test(String(actual))) fail(`${message}: ${String(actual)}`);
  },
  fail,
};

const mode = new URLSearchParams(location.search).get("mode") || "admin";
const onlineAgent = (id, features = ["agent-self-upgrade-v1"]) => ({
  id,
  name: id.toUpperCase(),
  os: "linux",
  arch: "amd64",
  status: "online",
  version: "1.2.3",
  capabilities: ["mihomo"],
  features,
  labels: {},
  metrics: {},
  runtime: { mihomo: { installed: true, service_status: "running" } },
  last_seen: "2026-08-24T00:00:00Z",
  enrolled_at: "2026-08-24T00:00:00Z",
});
const populatedAgents = [
  onlineAgent("alpha"),
  onlineAgent("bravo"),
  { ...onlineAgent("charlie"), status: "offline" },
  onlineAgent("delta", []),
];

const testAPI = {
  calls: [],
  pendingTasks: [],
  enrollmentFailure: false,
  taskMode: "immediate",
  agents: populatedAgents,
};
window.__agentsBrowserTestAPI = testAPI;

const json = (value, status = 200) =>
  new Response(value === null ? null : JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });

window.fetch = async (input, options = {}) => {
  const url = new URL(input instanceof Request ? input.url : input, location.href);
  const path = url.pathname.replace(/^\/api\/v1/, "");
  const method = String(options.method || (input instanceof Request ? input.method : "GET")).toUpperCase();
  testAPI.calls.push({ method, path });
  if (!["GET", "HEAD", "OPTIONS"].includes(method))
    assert.equal(
      new Headers(options.headers).get("X-QControlHub-CSRF"),
      "browser-test-csrf",
      `mutation ${method} ${path} 缺少 CSRF 头`,
    );
  if (method === "GET" && path === "/auth/session")
    return json({ role: mode === "readonly" ? "readonly" : "admin", csrf_token: "browser-test-csrf" });
  if (method === "GET" && path === "/overview")
    return json({ agents: mode === "empty" ? 0 : populatedAgents.length, agents_online: mode === "empty" ? 0 : 3 });
  if (method === "GET" && path === "/settings")
    return json({ panel_name: "QControlHub Browser Smoke" });
  if (method === "GET" && path === "/agents")
    return json(mode === "empty" ? [] : testAPI.agents);
  if (method === "GET" && path === "/enrollment-tokens") return json([]);
  if (method === "GET" && /^\/agents\/[^/]+\/configs$/.test(path)) return json([]);
  if (method === "GET" && path.startsWith("/metrics/")) return json([]);
  if (method === "POST" && path === "/enrollment-tokens") {
    if (testAPI.enrollmentFailure) return json({ error: "temporary enrollment failure" }, 503);
    return json({ token: "browser-test-enrollment", name: "browser-node" });
  }
  if (method === "DELETE" && path.startsWith("/enrollment-tokens/")) return json(null, 204);
  if (method === "POST" && path === "/tasks") {
    const payload = JSON.parse(String(options.body || "{}"));
    if (testAPI.taskMode !== "deferred") return json({ id: `task-${payload.agent_id}` });
    return await new Promise((resolve) => {
      testAPI.pendingTasks.push({
        payload,
        ok: (value) => resolve(json(value)),
        fail: (message) => resolve(json({ error: message }, 503)),
      });
    });
  }
  if (method === "POST" && path === "/auth/logout") return json(null, 204);
  return json([]);
};

const delay = (milliseconds = 0) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(predicate, message) {
  for (let attempt = 0; attempt < 400; attempt += 1) {
    const value = predicate();
    if (value) return value;
    await delay(10);
  }
  assert.fail(message);
}

function assertNoPersistentEnrollment() {
  assert.equal(document.querySelector(".enrollment-sheet"), null);
  assert.equal(document.querySelector("#enrollment"), null);
}

function responsiveDialogRuleExists() {
  const visit = (rules) => {
    for (const rule of rules) {
      if (rule instanceof CSSMediaRule) {
        if (
          rule.conditionText.includes("max-width: 620px") &&
          rule.conditionText.includes("pointer: coarse") &&
          [...rule.cssRules].some(
            (child) =>
              child.selectorText === ".modal-backdrop" &&
              child.style.alignItems === "end" &&
              child.style.padding === "0px",
          )
        )
          return true;
        if (visit(rule.cssRules)) return true;
      }
    }
    return false;
  };
  return [...document.styleSheets].some((sheet) => visit(sheet.cssRules));
}

async function testAdminRuntime() {
  await waitFor(() => document.querySelector("#batch-form"), "聚合页没有渲染真实批量表单");
  assertNoPersistentEnrollment();
  const launcher = document.querySelector("[data-open-enrollment]");
  assert.ok(launcher, "有权限的 populated 聚合页缺少添加节点入口");
  const originalHash = location.hash;
  const workspace = document.querySelector(".workspace-main");
  workspace.scrollTop = 37;
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
  await waitFor(() => document.querySelector(".modal-backdrop") === backdrop, "同 route 刷新丢失了打开的 modal");
  await delay();
  assert.equal(document.querySelector(".desktop-app").inert, true, "刷新后的背景没有继续 inert");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  assert.equal(document.querySelector(".modal-backdrop"), null);
  assert.equal(document.querySelector(".desktop-app").inert, false);
  assert.equal(document.body.style.overflow, "");
  assert.equal(document.activeElement, document.querySelector("[data-open-enrollment]"));
  assert.equal(document.querySelector(".workspace-main").scrollTop, 37);

  document.querySelector("[data-open-enrollment]").click();
  const enrollment = await waitFor(() => document.querySelector(".enrollment-dialog"), "无法重新打开 enrollment modal");
  testAPI.enrollmentFailure = true;
  enrollment.querySelector('input[name="name"]').value = "browser-node";
  const enrollmentForm = enrollment.querySelector("form");
  const enrollmentSubmit = enrollmentForm.querySelector('button[type="submit"]');
  enrollmentForm.requestSubmit(enrollmentSubmit);
  await waitFor(() => !enrollmentSubmit.disabled, "enrollment 失败后提交按钮未恢复");
  assert.equal(document.querySelector(".enrollment-dialog"), enrollment);
  assert.match(document.body.textContent, /temporary enrollment failure/);
  testAPI.enrollmentFailure = false;
  enrollment.querySelector("[data-close]").click();

  let form = document.querySelector("#batch-form");
  let all = form.querySelector("[data-batch-select-all]");
  let inputs = [...form.querySelectorAll("[data-batch-checkbox]")];
  let [alpha, bravo, charlie, delta] = inputs;
  let count = form.querySelector("[data-batch-count]");
  let submit = form.querySelector('button[type="submit"]');
  const readCurrentBatchDOM = () => {
    form = document.querySelector("#batch-form");
    all = form.querySelector("[data-batch-select-all]");
    inputs = [...form.querySelectorAll("[data-batch-checkbox]")];
    [alpha, bravo, charlie, delta] = inputs;
    count = form.querySelector("[data-batch-count]");
    submit = form.querySelector('button[type="submit"]');
  };
  const replaceAgent = (agentID, update) => {
    testAPI.agents = testAPI.agents.map((agent) =>
      agent.id === agentID ? update(agent) : agent,
    );
  };
  const refreshAgents = async (predicate, message) => {
    document.querySelector("[data-agent-refresh]")?.click();
    await waitFor(predicate, message);
    await delay(50);
    if (document.querySelector("#batch-form") !== form) {
      readCurrentBatchDOM();
      await waitFor(predicate, message);
    }
  };
  assert.equal(charlie.disabled, true);
  assert.match(charlie.closest("label").textContent, /离线/);
  assert.equal(delta.disabled, true);
  assert.match(delta.closest("label").textContent, /旧版 Agent/);

  all.click();
  assert.equal(alpha.checked, true);
  assert.equal(bravo.checked, true);
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  bravo.click();
  assert.equal(all.checked, false);
  assert.equal(all.indeterminate, true);
  assert.equal(all.getAttribute("aria-checked"), "mixed");
  all.click();
  assert.equal(alpha.checked && bravo.checked, true);
  all.click();
  assert.equal(alpha.checked || bravo.checked, false, "取消全选未清空合格节点");
  all.click();

  testAPI.taskMode = "deferred";

  replaceAgent("alpha", (agent) => ({ ...agent, status: "offline" }));
  await refreshAgents(
    () => alpha.disabled && !alpha.checked,
    "刷新后没有撤销刚变离线的节点",
  );
  assert.equal(bravo.checked, true, "刷新不应清除仍合格节点的选择");
  assert.equal(count.textContent, "已选择 1 个节点 · 当前可选 1 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  replaceAgent("alpha", () => onlineAgent("alpha"));
  await refreshAgents(() => !alpha.disabled, "恢复在线快照后节点仍不可选");

  bravo.click();
  alpha.click();
  form.requestSubmit(submit);
  let confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "离线二次校验没有进入确认流程");
  replaceAgent("alpha", (agent) => ({ ...agent, status: "offline" }));
  await refreshAgents(
    () => alpha.disabled && !alpha.checked && /离线/.test(alpha.closest("label").textContent),
    "刷新后没有立即撤销已离线节点的选择",
  );
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 0, "确认后仍向已离线节点提交任务");

  replaceAgent("alpha", () => onlineAgent("alpha"));
  await refreshAgents(() => !alpha.disabled, "恢复在线快照后节点仍不可选");
  bravo.click();
  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "feature 二次校验没有进入确认流程");
  replaceAgent("bravo", (agent) => ({ ...agent, features: [] }));
  await refreshAgents(
    () => bravo.disabled && !bravo.checked && /旧版 Agent/.test(bravo.closest("label").textContent),
    "刷新后没有立即撤销缺少升级 feature 的节点选择",
  );
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 0, "确认后仍向缺少升级 feature 的节点提交任务");

  replaceAgent("bravo", () => onlineAgent("bravo"));
  await refreshAgents(() => !bravo.disabled, "恢复 feature 后节点仍不可选");
  form.elements.action.value = "restart";
  form.elements.action.dispatchEvent(new Event("change", { bubbles: true }));
  alpha.click();
  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "runtime 二次校验没有进入确认流程");
  replaceAgent("alpha", (agent) => ({
    ...agent,
    runtime: { ...agent.runtime, mihomo: { installed: false, service_status: "stopped" } },
  }));
  await refreshAgents(
    () => alpha.disabled && !alpha.checked && /未安装/.test(alpha.closest("label").textContent),
    "刷新后没有立即撤销 runtime 不可用节点的选择",
  );
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 0, "确认后仍向 runtime 不可用节点提交任务");

  replaceAgent("alpha", () => onlineAgent("alpha"));
  await refreshAgents(() => !alpha.disabled, "恢复 runtime 后节点仍不可选");
  form.elements.action.value = "upgrade-agent";
  form.elements.action.dispatchEvent(new Event("change", { bubbles: true }));
  all.click();
  form.requestSubmit(submit);
  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "批量提交没有进入确认流程");
  assert.equal(testAPI.pendingTasks.length, 0, "确认前不应提交任务");
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.pendingTasks.length === 1, "首个串行任务未提交");
  assert.equal(testAPI.pendingTasks.length, 1, "确认防重入产生了重复任务");
  assert.equal(alpha.checked && bravo.checked, true, "busy 不应清除选中状态");
  assert.equal(alpha.disabled && bravo.disabled, true, "busy 应锁定节点选择控件");
  assert.equal(all.disabled, true, "busy 应锁定全选控件");
  assert.equal(form.querySelector("[data-batch-clear]").disabled, true, "busy 应锁定清空控件");
  assert.equal(submit.disabled, true, "busy 应锁定提交控件");
  assert.equal(form.elements.action.disabled, true, "busy 应锁定动作控件");
  assert.equal(form.elements.engine.disabled, true, "busy 应锁定内核控件");
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");

  testAPI.pendingTasks[0].ok({ id: "task-alpha" });
  await waitFor(() => testAPI.pendingTasks.length === 2, "第二个任务没有在首个完成后串行提交");
  assert.equal(testAPI.pendingTasks[1].payload.agent_id, "bravo");
  testAPI.pendingTasks[1].fail("bravo temporary failure");
  await waitFor(() => form.dataset.busy !== "1", "部分失败后 busy 未恢复");
  let rows = [...form.querySelectorAll(".batch-result-row")];
  assert.equal(rows.length, 2);
  assert.equal(rows.filter((row) => row.classList.contains("ok")).length, 1);
  assert.equal(rows.filter((row) => row.classList.contains("error")).length, 1);
  let retry = form.querySelector("[data-batch-retry]");
  assert.equal(retry.dataset.batchRetry, "bravo", "部分失败只应重试失败节点");
  retry.click();
  await waitFor(() => testAPI.pendingTasks.length === 3, "部分失败项重试未提交");
  testAPI.pendingTasks[2].ok({ id: "task-bravo-retry" });
  await waitFor(() => !form.querySelector("[data-batch-retry]"), "成功重试后仍残留重试入口");

  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "第二轮批量提交没有进入确认流程");
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.pendingTasks.length === 4, "第二轮首个任务未提交");
  testAPI.pendingTasks[3].fail("alpha temporary failure");
  await waitFor(() => testAPI.pendingTasks.length === 5, "第二轮任务没有串行提交");
  testAPI.pendingTasks[4].fail("bravo temporary failure");
  await waitFor(() => form.dataset.busy !== "1", "全部失败后 busy 未恢复");
  rows = [...form.querySelectorAll(".batch-result-row")];
  assert.equal(rows.filter((row) => row.classList.contains("error")).length, 2);
  let retries = [...form.querySelectorAll("[data-batch-retry]")];
  assert.equal(retries.length, 2, "两个失败节点均应保留重试入口");
  retries[0].click();
  retries[1].click();
  form.requestSubmit(submit);
  alpha.click();
  await waitFor(() => testAPI.pendingTasks.length === 6, "失败项重试未提交");
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 6, "共享 busy 未阻止并行 retry 或主提交");
  assert.equal(retries.every((button) => button.disabled), true, "retry busy 未锁定全部重试控件");
  assert.equal(alpha.disabled && bravo.disabled && all.disabled, true, "retry busy 未锁定选择控件");
  assert.equal(form.querySelector("[data-batch-clear]").disabled, true, "retry busy 未锁定清空控件");
  assert.equal(submit.disabled && form.elements.action.disabled && form.elements.engine.disabled, true, "retry busy 未锁定动作控件");
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.getAttribute("aria-checked"), "true");
  testAPI.pendingTasks[5].fail("alpha retry still failing");
  await waitFor(() => form.dataset.busy !== "1", "retry 失败后共享 busy 未释放");
  retries = [...form.querySelectorAll("[data-batch-retry]")];
  assert.equal(retries.every((button) => !button.disabled), true, "retry 失败后控件未恢复");

  retries[0].click();
  await waitFor(() => testAPI.pendingTasks.length === 7, "失败 retry 未允许再次重试");
  testAPI.pendingTasks[6].ok({ id: "task-alpha-retry" });
  await waitFor(() => form.querySelectorAll("[data-batch-retry]").length === 1, "成功 retry 后失败项未原位更新");

  const remainingRetry = form.querySelector("[data-batch-retry]");
  replaceAgent("bravo", (agent) => ({ ...agent, status: "offline" }));
  await refreshAgents(() => remainingRetry.disabled, "刷新后不合格 retry 未禁用");
  const pendingBeforeRetry = testAPI.pendingTasks.length;
  await remainingRetry.onclick();
  assert.equal(testAPI.pendingTasks.length, pendingBeforeRetry, "retry 实际 POST 前未使用最新离线快照 fail closed");
}

async function testEmptyRuntime() {
  await waitFor(() => document.querySelector(".empty.large"), "空列表没有渲染 empty state");
  assertNoPersistentEnrollment();
  assert.ok(document.querySelector("[data-open-enrollment]"), "空列表有权限用户缺少添加节点入口");
  assert.equal(document.querySelector("#batch-form"), null);
}

async function testReadonlyRuntime() {
  await waitFor(() => document.querySelector(".workspace-main"), "只读聚合页没有完成渲染");
  assertNoPersistentEnrollment();
  assert.equal(document.querySelector("[data-open-enrollment]"), null);
  assert.equal(document.querySelector("#batch-form"), null);
  assert.equal(
    testAPI.calls.some((call) => call.path === "/enrollment-tokens"),
    false,
    "无 enrollment.manage 权限不应读取添加记录",
  );
}

try {
  await import("./app.js");
  if (mode === "admin") await testAdminRuntime();
  else if (mode === "empty") await testEmptyRuntime();
  else await testReadonlyRuntime();
  document.documentElement.dataset.browserSmoke = "passed";
  document.body.innerHTML = `<pre id="browser-smoke-result">PASS ${mode}</pre>`;
} catch (error) {
  document.documentElement.dataset.browserSmoke = "failed";
  document.body.innerHTML = `<pre id="browser-smoke-result"></pre>`;
  document.querySelector("#browser-smoke-result").textContent = String(error?.stack || error);
  console.error(error);
}
