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
  capabilities: ["mihomo", "sing-box"],
  features,
  labels: {},
  metrics: {},
  runtime: {
    mihomo: { installed: true, service_status: "running" },
    "sing-box": { installed: false, service_status: "unknown" },
  },
  last_seen: "2026-08-24T00:00:00Z",
  enrolled_at: "2026-08-24T00:00:00Z",
  enrollment_command_available: id === "alpha",
});
const populatedAgents = [
  {
    ...onlineAgent("alpha"),
    labels: { komari_uuid: "komari-alpha" },
    metrics: {
      cpu_available: true,
      cpu_percent: 18.6,
      memory_available: true,
      memory_used_bytes: 1288490189,
      memory_total_bytes: 4294967296,
      disk_available: true,
      disk_used_bytes: 17179869184,
      disk_total_bytes: 53687091200,
      network_available: true,
      network_rx_bps: 1887437,
      network_tx_bps: 645120,
      network_rx_bytes: 9878424781,
      network_tx_bytes: 4617089843,
      collected_at: "2026-08-30T09:00:00Z",
    },
  },
  onlineAgent("bravo"),
  { ...onlineAgent("charlie"), status: "offline" },
  onlineAgent("delta", []),
];

const testAPI = {
  calls: [],
  pendingTasks: [],
  enrollmentFailure: false,
  enrollmentRecords: [
    {
      id: "enr-alpha",
      name: "alpha",
      reusable: true,
      used_count: 1,
      max_uses: 0,
      command_available: true,
    },
  ],
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
  if (method === "GET" && path === "/agents/alpha/komari")
    return json({
      uuid: "komari-alpha",
      server: {
        uuid: "komari-alpha",
        name: "Tokyo edge-01",
        billing_cycle: 30,
        expired_at: "2026-08-27T00:00:00Z",
        traffic_limit: 10737418240,
        traffic_limit_type: "sum",
        traffic_used: 4294967296,
        traffic_used_available: true,
      },
    });
  if (method === "GET" && path === "/enrollment-tokens") return json(testAPI.enrollmentRecords);
  if (method === "GET" && /^\/agents\/[^/]+\/configs$/.test(path)) return json([]);
  if (method === "GET" && path.startsWith("/metrics/")) return json([]);
  if (method === "POST" && path === "/enrollment-tokens") {
    if (testAPI.enrollmentFailure) return json({ error: "temporary enrollment failure" }, 503);
    return json({ token: "browser-test-enrollment", name: "browser-node" });
  }
  if (method === "POST" && /^\/agents\/[^/]+\/enrollment-command$/.test(path))
    return json({ token: "browser-test-enrollment", name: "ALPHA" });
  if (method === "POST" && path === "/enrollment-tokens/enr-alpha/command")
    return json({ token: "browser-test-enrollment", name: "ALPHA" });
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
  await waitFor(() => document.querySelector(".node-card-grid"), "聚合页没有渲染节点卡片");
  const komariInline = await waitFor(
    () => document.querySelector('[data-komari-link="alpha"]'),
    "Komari 流量没有融合进网络资源格",
  );
  assert.equal(komariInline.closest(".node-card-network") !== null, true);
  assert.match(komariInline.querySelector("[data-komari-traffic]").textContent, /本期 4\.0 GB \/ 10\.0 GB/);
  assert.match(
    komariInline.querySelector("[data-komari-cycle]").textContent,
    /\d{1,2}\.\d{1,2}–\d{1,2}\.\d{1,2}/,
  );
  assert.equal(Number(komariInline.querySelector("[data-komari-progress]").value), 40);
  assert.equal(document.querySelector(".node-card-komari"), null, "Komari 不应再渲染为独立卡片");
  assert.ok(
    komariInline.closest(".node-card-network").querySelector("[data-metric-text=download-rate]"),
    "绑定 Komari UUID 后必须保留原实时网络内容",
  );
  assert.equal(
    komariInline.closest(".node-card-network").querySelector("[data-metric-text=download-total]"),
    null,
    "绑定 Komari UUID 后应只替换原累计网络内容",
  );
  assert.ok(
    document.querySelector('[data-agent-node="bravo"] .node-card-network [data-metric-text="download-rate"]'),
    "未绑定 Komari UUID 的节点必须保留原实时网络内容",
  );
  assert.ok(
    document.querySelector('[data-agent-node="bravo"] .node-card-network [data-metric-text="download-total"]'),
    "未绑定 Komari UUID 的节点必须保留原累计网络内容",
  );
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
  await waitFor(() => document.querySelector(".modal-backdrop") === backdrop, "同 route 刷新丢失了打开的 modal");
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
  assert.match(document.body.textContent, /temporary enrollment failure/);
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

  document.querySelector(".enrollment-dialog [data-close]")?.click();
  const batchLauncher = document.querySelector("[data-node-batch-toggle]");
  batchLauncher.click();
  await waitFor(() => document.querySelector("#batch-form"), "顶栏批量操作没有打开选择模式");
  assert.ok(document.querySelector(".node-batch-bar"), "选择模式没有渲染底部操作栏");
  assert.equal(document.querySelectorAll("[data-node-batch-card] [data-batch-checkbox]").length, 4);
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
  assert.match(charlie.closest(".node-card-select").title, /离线/);
  assert.equal(delta.disabled, true);
  assert.match(delta.closest(".node-card-select").title, /旧版 Agent/);

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
    () => alpha.disabled && !alpha.checked && /离线/.test(alpha.closest(".node-card-select").title),
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
    () => bravo.disabled && !bravo.checked && /旧版 Agent/.test(bravo.closest(".node-card-select").title),
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
    () => alpha.disabled && !alpha.checked && /未安装/.test(alpha.closest(".node-card-select").title),
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
  const batchBar = form.querySelector(".node-batch-bar");
  const aggregateWorkspace = document.querySelector(".workspace-main");
  const aggregateHash = location.hash;
  aggregateWorkspace.scrollTop = 43;
  const aggregateScrollTop = aggregateWorkspace.scrollTop;
  const callsFor = (path) =>
    testAPI.calls.filter((call) => call.method === "GET" && call.path === path)
      .length;
  const agentPollsBefore = callsFor("/agents");
  const overviewCallsBefore = callsFor("/overview");
  const enrollmentCallsBefore = callsFor("/enrollment-tokens");
  const alphaCard = document.querySelector('[data-agent-metrics="alpha"]');
  const singBoxChip = alphaCard.querySelector(".service-sing-box");
  const singBoxService = singBoxChip.querySelector('[data-core-service="sing-box"]');
  const installedSummary = alphaCard.querySelector(
    "[data-core-installed-summary]",
  );
  assert.equal(singBoxChip.dataset.coreInstalled, "0");
  assert.equal(singBoxService.textContent, "未安装");
  assert.equal(
    singBoxService.closest(".engine-state").classList.contains("muted"),
    true,
  );
  assert.match(installedSummary.textContent, /1\/2 内核已安装/);
  replaceAgent("alpha", (agent) => ({
    ...agent,
    runtime: {
      ...agent.runtime,
      "sing-box": { installed: true, service_status: "active" },
    },
  }));
  await delay(6500);
  assert.ok(
    callsFor("/agents") >= agentPollsBefore + 3,
    "批量失败结果未经历三轮连续指标 poll",
  );
  assert.ok(
    callsFor("/agents") <= agentPollsBefore + 4,
    "连续指标 poll 请求失去边界",
  );
  assert.equal(
    callsFor("/overview"),
    overviewCallsBefore,
    "指标 poll 不应重复加载 overview",
  );
  assert.equal(
    callsFor("/enrollment-tokens"),
    enrollmentCallsBefore,
    "指标 poll 不应重复加载 enrollment history",
  );
  assert.equal(
    document.querySelector("#batch-form"),
    form,
    "聚合 core chip 的连续 poll 不应替换批量表单",
  );
  assert.equal(batchBar.isConnected, true, "连续 poll 不应关闭底部批量操作栏");
  assert.equal(location.hash, aggregateHash, "连续 poll 不应改变 route");
  assert.equal(
    aggregateWorkspace.scrollTop,
    aggregateScrollTop,
    "连续 poll 不应改变聚合页滚动位置",
  );
  assert.equal(
    form.querySelector("[data-batch-results]").hidden,
    false,
    "连续 poll 不应隐藏批量结果",
  );
  assert.equal(
    form.querySelectorAll(".batch-result-row.error").length,
    2,
    "连续 poll 后两个失败结果必须保留",
  );
  retries = [...form.querySelectorAll("[data-batch-retry]")];
  assert.equal(retries.length, 2, "连续 poll 后两个 retry 必须保留");
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  assert.equal(singBoxChip.dataset.coreInstalled, "1");
  assert.equal(singBoxService.textContent, "运行中");
  assert.equal(
    singBoxService.closest(".engine-state").classList.contains("ok"),
    true,
  );
  assert.match(installedSummary.textContent, /2\/2 内核已安装/);
  replaceAgent("alpha", (agent) => ({
    ...agent,
    runtime: {
      ...agent.runtime,
      "sing-box": { installed: false, service_status: "unknown" },
    },
  }));
  await waitFor(
    () => singBoxChip.dataset.coreInstalled === "0",
    "compact chip 没有原位同步卸载转换",
  );
  assert.equal(document.querySelector("#batch-form"), form);
  assert.equal(form.querySelectorAll(".batch-result-row.error").length, 2);
  assert.equal(form.querySelectorAll("[data-batch-retry]").length, 2);
  assert.equal(singBoxService.textContent, "未安装");
  assert.equal(
    singBoxService.closest(".engine-state").classList.contains("muted"),
    true,
  );
  assert.match(installedSummary.textContent, /1\/2 内核已安装/);
  assert.equal(batchBar.isConnected, true);
  assert.equal(location.hash, aggregateHash);
  assert.equal(aggregateWorkspace.scrollTop, aggregateScrollTop);
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
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

  location.hash = "#settings-node-alpha";
  await waitFor(
    () => document.querySelector('.node-operations-workspace[data-agent-node="alpha"]'),
    "单节点详情没有完成渲染",
  );
  assert.equal(document.querySelector("[data-open-enrollment]"), null, "单节点详情不得渲染添加节点入口");
  assert.equal(document.querySelector("[data-node-batch-toggle]"), null, "单节点详情不得渲染批量操作入口");
  assert.equal(document.querySelector("#batch-form"), null, "单节点详情不得渲染批量操作表单");
  assert.equal(document.querySelector(".node-batch-bar"), null, "单节点详情不得保留批量操作栏");
  const commandButton = document.querySelector('[data-view-enrollment-command="alpha"]');
  assert.ok(commandButton, "有权限的单节点详情缺少查看已有部署命令入口");
  const commandReadsBefore = testAPI.calls.filter(
    (call) => call.method === "POST" && call.path === "/agents/alpha/enrollment-command",
  ).length;
  commandButton.click();
  const firstCommandDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal"),
    "单节点详情无法打开已有部署命令",
  );
  const firstCommand = firstCommandDialog.querySelector("[data-command]").value;
  assert.match(firstCommand, /browser-test-enrollment/);
  firstCommandDialog.querySelector("[data-close]").click();
  commandButton.click();
  const secondCommandDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal"),
    "单节点详情无法重复打开已有部署命令",
  );
  assert.equal(secondCommandDialog.querySelector("[data-command]").value, firstCommand);
  secondCommandDialog.querySelector("[data-close]").click();
  const commandReadsAfter = testAPI.calls.filter(
    (call) => call.method === "POST" && call.path === "/agents/alpha/enrollment-command",
  ).length;
  assert.equal(commandReadsAfter, commandReadsBefore + 2, "重复查看部署命令应只读取而不创建凭据");
  assert.equal(
    testAPI.calls.some((call) => call.method === "POST" && /\/enrollment-token$/.test(call.path)),
    false,
    "单节点详情不得调用创建 enrollment credential 的接口",
  );
  for (const tab of ["cores", "metrics", "agent"]) {
    document.querySelector(`[data-node-tab="${tab}"]`).click();
    await waitFor(
      () => document.querySelector(`[data-node-panel="${tab}"]:not([hidden])`),
      `${tab} 标签页没有完成切换`,
    );
    assert.equal(document.querySelector("[data-open-enrollment]"), null, `${tab} 标签页不得渲染添加节点入口`);
    assert.equal(document.querySelector("#batch-form"), null, `${tab} 标签页不得渲染批量操作`);
    assert.equal(document.querySelector(".node-batch-bar"), null, `${tab} 标签页不得保留批量操作栏`);
  }
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
