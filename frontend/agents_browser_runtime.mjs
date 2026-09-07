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
const tcpRules = [
  { key: "net.ipv4.tcp_congestion_control", label: "拥塞控制算法", choices: ["bbr", "bbr2", "bbr3", "cubic", "reno"] },
  { key: "net.core.default_qdisc", label: "默认队列算法", choices: ["fq", "fq_codel", "fq_pie", "pfifo_fast", "sfq", "cake"] },
  { key: "net.ipv4.tcp_rmem", label: "TCP 接收缓冲区：最小 / 默认 / 最大（字节）", tuple: true, min: 1, max: 1073741824 },
  { key: "net.ipv4.tcp_wmem", label: "TCP 发送缓冲区：最小 / 默认 / 最大（字节）", tuple: true, min: 1, max: 1073741824 },
  { key: "net.core.rmem_max", label: "接收缓冲区上限（字节）", min: 4096, max: 1073741824 },
  { key: "net.core.wmem_max", label: "发送缓冲区上限（字节）", min: 4096, max: 1073741824 },
  { key: "net.core.somaxconn", label: "监听连接队列上限", min: 128, max: 65535 },
  { key: "net.core.netdev_max_backlog", label: "网卡接收积压队列上限", min: 64, max: 1000000 },
  { key: "net.ipv4.tcp_max_syn_backlog", label: "TCP SYN 队列上限", min: 128, max: 1000000 },
  { key: "net.ipv4.tcp_mtu_probing", label: "MTU 探测（0 / 1 / 2）", max: 2 },
  { key: "net.ipv4.tcp_ecn", label: "ECN（0 / 1 / 2）", max: 2 },
  { key: "net.ipv4.tcp_fastopen", label: "TCP Fast Open（0 / 1 / 2 / 3）", max: 3 },
  { key: "net.ipv4.tcp_sack", label: "SACK（0 / 1）", max: 1 },
  { key: "net.ipv4.tcp_window_scaling", label: "窗口缩放（0 / 1）", max: 1 },
];
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
      public_ipv4: "8.8.8.8",
      public_ipv4_source: "agent-config",
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
  renameFailure: false,
  renameGate: null,
  profileNames: {},
  profileSaves: [],
  profileSaveFailure: false,
  profileSaveGate: null,
  deployments: [],
  savedConfigs: [],
  agentsFailure: false,
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
if (mode.startsWith("bbr")) {
  testAPI.tcpTasks = [];
  testAPI.tcpMutations = [];
  testAPI.agents = populatedAgents.map((agent, index) => ({
    ...agent, features: index === 3 ? [] : ["system-bbr-v1"],
    metrics: { bbr: {
      available: true, collected_at: new Date().toISOString(), kernel_release: "6.12.46-amd64",
      congestion_control: index === 1 ? "cubic" : "bbr", default_qdisc: "fq_codel",
      available_algorithms: ["reno", "cubic", "bbr"], persistence: "unmanaged",
      parameters: Object.fromEntries(tcpRules.map((rule) => [rule.key,
        rule.key === "net.ipv4.tcp_congestion_control" ? (index === 1 ? "cubic" : "bbr") :
        rule.key === "net.core.default_qdisc" ? "fq_codel" : rule.tuple ? "4096 131072 16777216" :
        rule.max < 4 ? "1" : "4096",
      ])),
      qdiscs: [{ device: "eth0", kind: "mq", root: true }, { device: "eth0", kind: "fq_codel", parent: "1:1", handle: "0:" }],
    } },
  }));
  location.hash = "#system-bbr";
}

const json = (value, status = 200) =>
  new Response(value === null ? null : JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });

window.fetch = async (input, options = {}) => {
  const url = new URL(input instanceof Request ? input.url : input, location.href);
  const path = url.pathname.replace(/^\/api\/v1/, "");
  const method = String(options.method || (input instanceof Request ? input.method : "GET")).toUpperCase();
  testAPI.calls.push({ method, path, query: url.search });
  if (!["GET", "HEAD", "OPTIONS"].includes(method))
    assert.equal(
      new Headers(options.headers).get("X-QControlHub-CSRF"),
      "browser-test-csrf",
      `mutation ${method} ${path} 缺少 CSRF 头`,
    );
  if (method === "GET" && path === "/auth/session")
    return json(mode === "bbr-writeonly"
      ? { role: "user", permissions: ["agents.read", "agents.manage", "tasks.execute"], csrf_token: "browser-test-csrf" }
      : { role: mode === "readonly" || mode === "bbr-readonly" ? "readonly" : "admin", csrf_token: "browser-test-csrf" });
  if (method === "GET" && path === "/system-tcp/parameters") return json(tcpRules);
  if (method === "GET" && path === "/system-tcp/tasks") {
    const latest = new Map();
    for (const task of testAPI.tcpTasks || []) {
      if (url.searchParams.get("agent_id") && task.agent_id !== url.searchParams.get("agent_id")) continue;
      if (!latest.has(task.agent_id) || task.created_at >= latest.get(task.agent_id).created_at) latest.set(task.agent_id, task);
    }
    return json([...latest.values()]);
  }
  if (mode.startsWith("bbr") && path === "/tasks") {
    if (method === "GET") return json(testAPI.tcpTasks.filter((task) => task.action === url.searchParams.get("action") && (!url.searchParams.get("agent_id") || task.agent_id === url.searchParams.get("agent_id"))));
    if (method === "POST") {
      const payload = JSON.parse(options.body);
      testAPI.tcpMutations.push(payload);
      if (testAPI.tcpFailure) return json({ error: "TCP test failure" }, 503);
      const task = { ...payload, id: `tcp-${testAPI.tcpTasks.length}`, created_at: new Date().toISOString(), status: "pending" };
      testAPI.tcpTasks.push(task);
      return json(task, 201);
    }
  }
  if (method === "GET" && path === "/overview")
    return json({ agents: mode === "empty" ? 0 : populatedAgents.length, agents_online: mode === "empty" ? 0 : 3 });
  if (method === "GET" && path === "/settings")
    return json({ panel_name: "QControlHub Browser Smoke" });
  if (method === "GET" && path === "/agents" && testAPI.agentsFailure) return json({error:"temporary runtime failure"},503);
  if (method === "GET" && path === "/agents") {
    if (testAPI.agentsGate) await testAPI.agentsGate;
    return json(mode === "empty" ? [] : testAPI.agents);
  }
  if (method === "GET" && path === "/core-logs" && mode === "logs") {
    const limit = Number(url.searchParams.get("limit") || 1000);
    const agent = url.searchParams.get("agent_id") || "alpha";
    if (testAPI.logGates?.[`${agent}:${limit}`]) await testAPI.logGates[`${agent}:${limit}`];
    if (options.signal?.aborted) throw new DOMException("Aborted", "AbortError");
    return json(["mihomo", "xray", "sing-box", "ss-rust"].flatMap((engine, engineIndex) =>
      Array.from({ length: limit }, (_, index) => ({
        id: engineIndex * 10000 + index + 1, agent_id: agent, engine, level: "warning",
        message: `${agent} ${engine} pressure entry ${index}`, logged_at: "2026-09-06T00:00:00Z",
      }))));
  }
  if (method === "GET" && path === "/deployments") return json(testAPI.deployments);
  if (method === "GET" && path === "/client-access" && ["ports","readonly"].includes(mode)) {
    const profiles = (address) => [20001,20002].map((port,index) => {
      const tag = `ss-rust-${index+1}`;
      const name = testAPI.profileNames[port] || tag;
      return {tag,port,client_name:testAPI.profileNames[port] || "",protocol:"Shadowsocks",profile:{format:"Shadowsocks SIP002 URI",uri:`ss://example@${address}:${port}#${encodeURIComponent(name)}`,fields:[]}};
    });
    return json([{agent_id:"alpha",agent_name:"ALPHA",engine:"ss-rust",address:"edge.example.com",source:"test",address_mode:"auto",profiles:profiles("edge.example.com"),address_options:[
      {address:"edge.example.com",family:"ipv4",source:"test",profiles:profiles("edge.example.com")},
      {address:"2001:db8::1",family:"ipv6",source:"test",profiles:profiles("[2001:db8::1]")},
    ]}]);
  }
  if (method === "PUT" && path === "/agents/alpha/client-address") {
    const payload = JSON.parse(options.body);
    testAPI.profileSaves.push(payload);
    if (testAPI.profileSaveFailure) return json({error:"temporary profile save failure"},503);
    if (testAPI.profileSaveGate) await testAPI.profileSaveGate;
    if (payload.profile) testAPI.profileNames[payload.profile.port] = payload.name;
    return json(payload);
  }
  if (method === "PUT" && /^\/agents\/[^/]+\/name$/.test(path)) {
    if (testAPI.renameFailure) return json({ error: "temporary rename failure" }, 503);
    const agentID = decodeURIComponent(path.split("/")[2]);
    const { name } = JSON.parse(String(options.body || "{}"));
    if (testAPI.renameGate) await testAPI.renameGate;
    testAPI.agents = testAPI.agents.map((agent) => agent.id === agentID ? { ...agent, name } : agent);
    return json({ name });
  }
  if (method === "GET" && path === "/agents/alpha/region")
    return json({ ip: "8.8.8.8", country_code: "TW", country: "Taiwan" });
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
  if (method === "GET" && /^\/agents\/[^/]+\/configs$/.test(path)) return json(testAPI.savedConfigs.filter((item) => path.includes(item.agent_id)));
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
    () => {
      const inline = document.querySelector('[data-komari-link="alpha"]');
      return inline?.querySelector("[data-komari-traffic]")?.textContent ===
        "4.0 GB / 10.0 GB"
        ? inline
        : null;
    },
    "Komari 流量没有载入网络资源格",
  );
  assert.equal(komariInline.closest(".node-card-network") !== null, true);
  assert.equal(
    komariInline.querySelector("[data-komari-traffic]").textContent,
    "4.0 GB / 10.0 GB",
  );
  assert.match(
    komariInline.querySelector("[data-komari-cycle]").textContent,
    /\d{1,2}\.\d{1,2}–\d{1,2}\.\d{1,2}/,
  );
  assert.equal(Number(komariInline.querySelector("[data-komari-progress]").value), 40);
  const alphaAvatar = document.querySelector('[data-agent-node="alpha"] [data-region-avatar]');
  const alphaFlag = await waitFor(
    () => alphaAvatar.querySelector('img[src="/api/v1/region-flags/cn"]'),
    "节点地区没有渲染统一 SVG 旗帜",
  );
  await waitFor(() => alphaFlag.complete && alphaFlag.naturalWidth > 0, "节点地区 SVG 旗帜没有载入");
  assert.equal(alphaAvatar.textContent, "");
  assert.equal(alphaAvatar.classList.contains("has-region"), true);
  assert.equal(alphaAvatar.title, "中国台湾 (TW)");
  assert.equal(alphaAvatar.getAttribute("aria-label"), "中国台湾");
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
  assert.match(document.body.textContent, /temporary rename failure/);
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
  location.hash = "#settings-node-alpha";
  const renameForm = await waitFor(() => document.querySelector('[data-agent-name-form="alpha"]'), "只读节点详情未完成渲染");
  assert.equal(renameForm.querySelector("input").disabled, true, "只读用户可编辑节点名称");
  assert.equal(renameForm.querySelector("button").disabled, true, "只读用户可提交改名");
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
}

async function testPortNamesAndRuntimeRefresh() {
  location.hash = "#client-access";
  const row = (port) => document.querySelector(`[data-client-profile-port="${port}"]`)?.closest(".client-profile-row");
  await waitFor(() => row(20002), "两个端口没有渲染");
  const open = (port) => {row(port).querySelector("[data-client-display-open]").click(); return row(port).querySelector("dialog.client-display-dialog form");};
  let form = open(20001);
  assert.equal(form.dataset.clientProfileTag,"ss-rust-1");
  form.elements.name.value = "香港 & ATT <edge>";
  testAPI.profileSaveFailure = true;
  form.requestSubmit();
  await waitFor(() => !form.querySelector('[type="submit"]').disabled,"失败保存按钮未恢复");
  assert.equal(form.closest("dialog").open,true,"保存失败关闭了弹窗");
  assert.equal(form.elements.name.value,"香港 & ATT <edge>","失败丢失草稿");
  testAPI.profileSaveFailure = false;
  let release;
  testAPI.profileSaveGate = new Promise((resolve) => {release=resolve;});
  const calls = testAPI.profileSaves.length;
  form.requestSubmit();form.requestSubmit();
  await waitFor(() => testAPI.profileSaves.length===calls+1,"未提交保存");
  assert.equal(testAPI.profileSaves.length,calls+1,"重复提交产生并行请求");
  release();testAPI.profileSaveGate=null;
  await waitFor(() => row(20001).querySelector("header b").textContent === "香港 & ATT <edge>","首端口名称未更新");
  assert.equal(row(20001).querySelector("header edge"),null,"名称未转义");
  assert.equal(row(20002).querySelector("header b").textContent,"ss-rust-2","改名影响第二端口");
  assert.equal("address" in testAPI.profileSaves.at(-1),false,"仅改名称意外固定了自动连接地址");
  assert.equal("address_mode" in testAPI.profileSaves.at(-1),false,"仅改名称意外覆盖协议栈");
  form=open(20002);form.elements.name.value="Tokyo 第二端口";form.requestSubmit();
  await waitFor(() => row(20002).querySelector("header b").textContent === "Tokyo 第二端口","第二端口名称未更新");
  document.querySelector("[data-refresh-client-access]").click();
  await waitFor(() => !document.querySelector("[data-refresh-client-access]").disabled,"刷新未完成");
  assert.equal(row(20001).querySelector("header b").textContent,"香港 & ATT <edge>","刷新串名");
  assert.equal(new URL(row(20002).querySelector(".client-share-control input").value).hash,"#Tokyo%20%E7%AC%AC%E4%BA%8C%E7%AB%AF%E5%8F%A3","分享链接未使用独立名称");
  form=open(20001);form.elements.name.value="";form.requestSubmit();
  await waitFor(() => row(20001).querySelector("header b").textContent === "ss-rust-1","清空未恢复入站标签");
  assert.equal(row(20002).querySelector("header b").textContent,"Tokyo 第二端口","清空影响其他端口");

  location.hash="#settings-node-alpha";
  await waitFor(() => document.querySelector(".node-operations-workspace"),"节点详情未渲染");
  const runtime = (installed,version,service_status="running") => {
    testAPI.agents = testAPI.agents.map((agent) => agent.id!=="alpha"?agent:{...agent,runtime:{...agent.runtime,"sing-box":{installed,version,service_status}}});
  };
  document.querySelector('[data-node-tab="agent"]').click();
  const draft = document.querySelector('[data-agent-name-form="alpha"] input');draft.value="尚未保存的节点名称";
  runtime(true,"1.13.0");
  await waitFor(() => document.querySelector('.service-sing-box[data-core-installed="1"]'),"安装完成后详情没有自动刷新");
  assert.match(document.querySelector('[data-core-version="sing-box"]').textContent,/1\.13\.0/);
  assert.equal(document.querySelector('[data-agent-name-form="alpha"] input').value,"尚未保存的节点名称","刷新覆盖节点名称草稿");
  assert.equal(document.querySelector('[data-task-engine="sing-box"][data-task-action="restart"]').disabled,false,"安装后操作按钮未启用");
  runtime(false,"");
  await waitFor(() => document.querySelector('.service-sing-box[data-core-installed="0"]'),"卸载状态未自动刷新");

  location.hash="#preset-node-alpha";
  await waitFor(() => document.querySelector(".preset-node-workspace"),"预设页未渲染");
  const card = () => document.querySelector(".service-sing-box");
  card().querySelector("[data-open-version-form]").click();
  card().querySelector('[name="release_channel"][value="custom"]').click();
  card().querySelector('[name="custom_version"]').value="1.14.0-draft";
  runtime(true,"1.13.1");
  await waitFor(() => card().dataset.coreInstalled==="1","预设页安装状态未自动刷新");
  assert.match(card().querySelector('[data-core-version]').textContent,/1\.13\.1/);
  assert.equal(card().querySelector('[name="custom_version"]').value,"1.14.0-draft","安装刷新覆盖版本草稿");
  assert.equal(card().querySelector(".version-drawer").open,true,"刷新关闭版本抽屉");
  runtime(true,"1.13.2","inactive");
  await waitFor(() => card().querySelector('[data-core-version]').textContent.includes("1.13.2"),"版本切换未自动刷新");
  assert.equal(card().querySelector('[data-core-service]').textContent,"已停止");
  testAPI.savedConfigs=[{id:"cfg-preset",agent_id:"alpha",engine:"sing-box",name:"preset",version:2,content:'{"inbounds":[]}'}];
  testAPI.deployments=[{agent_id:"alpha",engine:"sing-box",config_id:"cfg-preset",config_version:2}];
  await waitFor(() => card().querySelector(".service-facts").textContent.includes("v2"),"部署/保存版本未自动刷新");
  assert.equal(card().querySelector('[name="custom_version"]').value,"1.14.0-draft","部署刷新覆盖草稿");
  testAPI.agentsFailure=true;
  await delay(2200);
  assert.match(card().querySelector('[data-core-version]').textContent,/1\.13\.2/,"获取失败丢失最后状态");
  testAPI.agentsFailure=false;
  runtime(false,"");
  await waitFor(() => card().dataset.coreInstalled==="0","失败后没有恢复轮询");
  location.hash="#client-access";
  await waitFor(() => row(20001),"离开预设页失败");
  await delay(2200);
  const before = testAPI.calls.filter((call) => call.path==="/agents").length;
  await delay(2200);
  assert.equal(testAPI.calls.filter((call) => call.path==="/agents").length,before,"离开预设页仍后台轮询");
}

async function testSystemTCPRuntime() {
  await waitFor(() => document.querySelector(".bbr-card"), "TCP 页面未加载");
  assert.equal(document.querySelector(".bbr-intro"), null, "不应恢复冗余的顶部说明卡");
  if (mode !== "bbr-writeonly")
    assert.notEqual(document.querySelector('.dock-nav a[href="#system-bbr"] svg').innerHTML, document.querySelector('.dock-nav a[href="#traffic"] svg').innerHTML, "TCP 调优和流量侧栏图标重复");
  const card = () => document.querySelector('[data-refresh-key="bbr-alpha"]');
  assert.match(card().textContent, /BBR 已启用/);
  assert.match(card().textContent, /未由 QControlHub 管理/);
  assert.match(card().textContent, /fq_codel/);
  if (mode === "bbr-readonly") {
    assert.equal(document.querySelector("[data-tcp-form]"), null);
    assert.equal(document.querySelector("[data-bbr-action]"), null);
    return;
  }
  assert.ok(document.querySelector('[data-bbr-agent="charlie"]').disabled, "离线节点允许提交");
  assert.ok(document.querySelector('[data-bbr-agent="delta"]').disabled, "旧 Agent 允许提交");
  if (mode === "bbr-writeonly") {
    card().querySelector('[data-bbr-action="enable-bbr"]').click();
    (await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "无任务读权限时未显示确认框")).querySelector("[data-confirm-accept]").click();
    await waitFor(() => card().textContent.includes("已提交（无任务查看权限）"), "无任务读权限时状态卡在等待执行");
    assert.equal(testAPI.calls.filter((call) => call.path === "/system-tcp/tasks").length, 0, "越权读取任务列表");
    assert.equal(card().querySelector('.bbr-task a[href="#tasks"]'), null, "展示了无权访问的任务链接");
    return;
  }
  const editor = () => card().querySelector(".bbr-editor");
  editor().open = true;
  assert.ok(editor().querySelector('[data-tcp-value="net.core.default_qdisc"] option[value="fq_pie"]'), "缺少 FQ-PIE 自定义选项");
  const field = () => editor().querySelector('[data-tcp-value="net.ipv4.tcp_rmem"]');
  field().value = "4096 262144 33554432";
  field().dispatchEvent(new Event("input", { bubbles: true }));
  const refresh = async () => {
    const before = testAPI.calls.filter((call) => call.path === "/system-tcp/tasks").length;
    document.querySelector("[data-bbr-refresh]").click();
    await waitFor(() => testAPI.calls.filter((call) => call.path === "/system-tcp/tasks").length > before, "TCP 刷新未请求");
    await delay(120);
  };
  await refresh();
  assert.equal(field().value, "4096 262144 33554432", "刷新覆盖了 TCP 草稿");
  assert.equal(editor().open, true, "刷新折叠了 TCP 编辑器");
  const originalTheme = document.documentElement.dataset.theme;
  document.querySelector("#theme-toggle").click();
  assert.notEqual(document.documentElement.dataset.theme, originalTheme);
  assert.equal(field().value, "4096 262144 33554432");
  editor().querySelector("form").requestSubmit();
  const dialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "TCP 没有使用共享确认弹窗");
  assert.match(dialog.textContent, /4096 262144 33554432/);
  assert.match(dialog.textContent, /\/etc\/sysctl.d\/90-qcontrolhub-bbr.conf/);
  testAPI.agentsFailure = true;
  dialog.querySelector("[data-confirm-cancel]").click();
  await delay(200);
  assert.equal(testAPI.tcpMutations.length, 0, "取消仍提交任务");
  assert.equal(field().value, "4096 262144 33554432");
  assert.ok(!field().disabled, "取消后刷新失败导致编辑器锁死");
  assert.ok(!card().querySelector("[data-bbr-action]").disabled, "取消后刷新失败导致按钮锁死");
  testAPI.tcpFailure = true;
  editor().querySelector("form").requestSubmit();
  (await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "失败测试没有确认弹窗")).querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.tcpMutations.length === 1, "TCP 任务未发出");
  await delay(200);
  assert.equal(field().value, "4096 262144 33554432", "失败丢失草稿");
  assert.ok(!field().disabled, "提交失败且刷新失败导致编辑器锁死");
  testAPI.agentsFailure = false;
  testAPI.tcpFailure = false;
  editor().querySelector("form").requestSubmit();
  (await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "重试没有确认弹窗")).querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.tcpTasks.length === 1, "TCP 任务未创建");
  await delay(200);
  const payload = testAPI.tcpMutations.at(-1);
  assert.equal(payload.action, "configure-tcp");
  assert.equal(payload.engine, "");
  assert.equal(Object.keys(payload.tcp_settings).length, 1, "提交了未勾选的参数");
  assert.equal(payload.tcp_settings["net.ipv4.tcp_rmem"], "4096 262144 33554432");
  assert.ok(card().querySelector("[data-bbr-action]").disabled, "进行中任务未禁止重复提交");
  assert.match(card().textContent, /等待执行/);
  testAPI.tcpTasks[0].status = "succeeded";
  testAPI.agents[0].metrics.bbr.parameters["net.ipv4.tcp_rmem"] = "4096 262144 33554432";
  testAPI.agents[0].metrics.bbr.persistence = "managed";
  testAPI.agents[0].metrics.bbr.configured_parameters = { "net.ipv4.tcp_rmem": "4096 262144 33554432" };
  await refresh();
  assert.match(card().textContent, /执行成功/);
  assert.ok(!card().querySelector("[data-bbr-action]").disabled);
  field().value = "4096 524288 67108864";
  field().dispatchEvent(new Event("input", { bubbles: true }));
  testAPI.agentsFailure = true;
  editor().querySelector("[data-tcp-reset]").click();
  (await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "清空草稿未确认")).querySelector("[data-confirm-accept]").click();
  await delay(200);
  assert.equal(editor().querySelector('[data-tcp-selected="net.ipv4.tcp_rmem"]').checked, false, "清空后仍勾选参数");
  assert.equal(field().value, "4096 262144 33554432", "清空后未恢复当前值");
  assert.ok(!field().disabled, "清空草稿依赖网络成功才能解锁");
  await refresh();
  assert.match(document.querySelector("[data-bbr-refresh-status]").textContent, /刷新失败/);
  assert.ok(card(), "刷新失败清空了状态");
  testAPI.agentsFailure = false;
  testAPI.tcpTasks[0].status = "pending";
  await refresh();
  assert.ok(card().querySelector("[data-bbr-action]").disabled);
  testAPI.tcpTasks = [];
  await refresh();
  assert.ok(!card().querySelector("[data-bbr-action]").disabled, "已被服务端移除的任务仍永久阻塞操作");
  assert.equal(card().querySelector(".bbr-task"), null, "仍显示被清理的任务历史");
  card().querySelector('[data-bbr-action="enable-bbr"]').click();
  await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "节点状态变化测试未显示弹窗");
  const beforeOffline = testAPI.tcpMutations.length;
  testAPI.agents[0].status = "offline";
  await refresh();
  document.querySelector("[data-confirm-dialog][open] [data-confirm-accept]").click();
  await delay(200);
  assert.equal(testAPI.tcpMutations.length, beforeOffline, "确认框打开期间节点离线仍然提交");
  testAPI.agents[0].status = "online";
  await refresh();
  location.hash = "#system-bbr-agent-bravo";
  await waitFor(() => document.querySelectorAll(".bbr-card").length === 1 && document.querySelector('[data-refresh-key="bbr-bravo"]'), "节点筛选失败");
  location.hash = "#node-settings";
  await waitFor(() => document.querySelector(".node-card"), "离开 TCP 页面失败");
  const calls = testAPI.calls.filter((call) => call.path === "/system-tcp/tasks").length;
  await delay(5200);
  assert.equal(testAPI.calls.filter((call) => call.path === "/system-tcp/tasks").length, calls, "离开 TCP 页面后继续轮询");
  assert.equal(testAPI.calls.filter((call) => call.path === "/system-tcp/parameters").length, 1, "每次轮询都重复读取不变的参数规则");
}

async function testLargeLogRuntime() {
  await waitFor(() => document.querySelector(".desktop-app"), "initial shell missing");
  let releaseAgents;
  testAPI.agentsGate = new Promise((resolve) => { releaseAgents = resolve; });
  const began = performance.now();
  location.hash = "#core-logs";
  await waitFor(() => document.querySelectorAll(".core-log-row").length === 200, "default log page missing");
  assert.match(document.querySelector(".core-log-status").textContent, /已加载 4000 条/);
  const initial = performance.now() - began;
  testAPI.agentsGate = null;
  releaseAgents();
  document.querySelector("[data-toggle-core-log-refresh]").click();
  const selectedAt = performance.now();
  const limit = document.querySelector('#core-log-filters select[name="limit"]');
  limit.value = "2000";
  limit.dispatchEvent(new Event("change", { bubbles: true }));
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 8000 条"), "2000/engine result missing");
  const expanded = performance.now() - selectedAt;
  assert.equal(document.querySelectorAll(".core-log-row").length, 200, "8000 entries must not create 8000 DOM rows");
  assert.equal(document.querySelector(".core-log-stream .core-log-pagination"), null, "pagination must not split headings and log rows");
  assert.equal(document.querySelectorAll(".core-log-result-toolbar .core-log-pagination, .core-log-result-footer .core-log-pagination").length, 2);
  const toolbarBox = document.querySelector(".core-log-result-toolbar .core-log-pagination").getBoundingClientRect();
  const streamBox = document.querySelector(".core-log-stream").getBoundingClientRect();
  assert.ok(toolbarBox.bottom <= streamBox.top && Math.abs(toolbarBox.right - streamBox.right) < 10, "top pagination must sit outside and align with the table's right edge");
  const reads = testAPI.calls.filter((call) => call.path === "/core-logs").length;
  const pageAt = performance.now();
  document.querySelector('[data-core-log-page-index="1"]').click();
  await waitFor(() => document.querySelector(".core-log-pagination")?.textContent.includes("第 2 / 40 页"), "second page missing");
  assert.match(document.querySelector(".core-log-row pre").textContent, /entry 200/);
  const pageTime = performance.now() - pageAt;
  const filterAt = performance.now();
  const search = document.querySelector('#core-log-filters input[name="q"]');
  search.value = "pressure entry 1999";
  search.dispatchEvent(new Event("input", { bubbles: true }));
  await waitFor(() => document.querySelectorAll(".core-log-row").length === 4, "filter must search beyond the displayed page");
  const filterTime = performance.now() - filterAt;
  assert.equal(testAPI.calls.filter((call) => call.path === "/core-logs").length, reads, "local pagination/filtering must not query the remote database");
  document.querySelector("[data-reset-core-logs]").click();
  const gated = () => {
    let resolve;
    const promise = new Promise((yes) => { resolve = yes; });
    return { promise, resolve };
  };
  const preview = gated(), full = gated();
  testAPI.logGates = { "bravo:200": preview.promise, "bravo:2000": full.promise };
  const switchAt = performance.now();
  document.querySelector('[data-core-log-agent="bravo"]').click();
  assert.ok(document.querySelector('[data-core-log-agent="bravo"]').classList.contains("active"), "sidebar selection must update before network completion");
  assert.equal(document.querySelectorAll(".core-log-row").length, 0, "old node's logs must disappear immediately");
  const acknowledgement = performance.now() - switchAt;
  const previewAt = performance.now();
  preview.resolve();
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 800 条"), "node preview did not render before full window");
  const previewTime = performance.now() - previewAt;
  assert.match(document.querySelector(".core-log-row pre").textContent, /bravo/);
  assert.match(document.querySelector("[data-core-log-refresh-label]").textContent, /正在补齐/);
  full.resolve();
  await waitFor(() => document.querySelector(".core-log-status")?.textContent.includes("已加载 8000 条"), "node's requested window was truncated");
  document.querySelector('[data-core-log-agent=""]').click();
  await waitFor(() => document.querySelector(".core-log-row pre")?.textContent.includes("alpha"), "return to all logs failed");
  const refreshGate = gated();
  testAPI.logGates["bravo:2000"] = refreshGate.promise;
  const cachedAt = performance.now();
  document.querySelector('[data-core-log-agent="bravo"]').click();
  assert.match(document.querySelector(".core-log-row pre").textContent, /bravo/, "cached node must appear synchronously with a pending network refresh");
  assert.match(document.querySelector("[data-core-log-refresh-label]").textContent, /已显示缓存/);
  const cachedTime = performance.now() - cachedAt;
  refreshGate.resolve();
  await waitFor(() => document.querySelector("[data-core-log-refresh-label]")?.textContent === "自动更新已暂停", "cache revalidation did not settle");
  assert.ok(initial < 5000 && expanded < 5000 && pageTime < 2000 && filterTime < 2000, "large log UI exceeded smoke responsiveness budget");
  assert.ok(acknowledgement < 500 && previewTime < 1000 && cachedTime < 500, "node switch exceeded local rendering budget");
  window.logPressureResult = { loaded: 8000, domRows: 200, initialMs: Math.round(initial), expandedMs: Math.round(expanded), pageMs: Math.round(pageTime), filterMs: Math.round(filterTime), switchAckMs: Math.round(acknowledgement), previewRenderMs: Math.round(previewTime), cachedSwitchMs: Math.round(cachedTime) };
}

try {
  await import("./app.js");
  if (mode === "bbr-preview") await new Promise(() => {});
  else if (mode.startsWith("bbr")) await testSystemTCPRuntime();
  else if (mode === "admin") await testAdminRuntime();
  else if (mode === "ports") await testPortNamesAndRuntimeRefresh();
  else if (mode === "empty") await testEmptyRuntime();
  else if (mode === "logs") await testLargeLogRuntime();
  else await testReadonlyRuntime();
  document.documentElement.dataset.browserSmoke = "passed";
  if (!new URLSearchParams(location.search).has("preview"))
    document.body.innerHTML = `<pre id="browser-smoke-result">PASS ${mode}${window.logPressureResult ? " " + JSON.stringify(window.logPressureResult) : ""}</pre>`;
} catch (error) {
  document.documentElement.dataset.browserSmoke = "failed";
  document.body.innerHTML = `<pre id="browser-smoke-result"></pre>`;
  document.querySelector("#browser-smoke-result").textContent = String(error?.stack || error);
  console.error(error);
}
