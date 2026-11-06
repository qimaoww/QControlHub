import { tcpButton, tcpClose, tcpConfirmation, tcpAccept, tcpCancel, tcpField, tcpSelected, tcpError, tcpSelectedKeys, tcpSettle } from "./system-tcp-controls.mjs";
import { assert, delay, waitFor } from "./assertions.mjs";

const bbrSettings = {
  "net.core.default_qdisc": "fq",
  "net.ipv4.tcp_congestion_control": "bbr",
  "net.core.rmem_max": "33554432",
  "net.core.wmem_max": "33554432",
  "net.ipv4.tcp_rmem": "4096 65536 33554432",
  "net.ipv4.tcp_wmem": "4096 65536 33554432",
  "net.ipv4.tcp_mtu_probing": "1",
  "net.ipv4.tcp_window_scaling": "1",
};

function assertDraft(editor, expected) {
  const selected = tcpSelectedKeys(editor);
  assert.equal(selected.join(","), Object.keys(expected).sort().join(","), "BBR 预设选择了错误的参数");
  for (const [key, value] of Object.entries(expected))
    assert.equal(tcpField(editor, key).value, value, `BBR 预设取值错误：${key}`);
}

export async function testSystemTCPPresets({ card, editor, openEditor, refresh, testAPI, mode }) {
  const preset = () => editor().querySelector('[data-tcp-preset="bbr-32m"]');
  const field = (key) => tcpField(editor(), key);
  const select = (key) => tcpSelected(editor(), key);
  const edit = (key, value) => {
    field(key).value = value;
    field(key).dispatchEvent(new Event("input", { bubbles: true }));
  };
  const before = testAPI.tcpMutations.length;
  if (editor().open) tcpClose(editor());
  if (mode === "bbr-mobile") {
    assert.ok(matchMedia("(pointer: coarse)").matches, "移动 BBR 测试未启用触摸指针");
    assert.ok(innerWidth <= 420, "移动 BBR 测试未使用窄视口");
  }
  openEditor();
  assert.ok(preset() && !preset().disabled, "正常节点缺少可用的 BBR 预设");
  assert.equal(preset().type, "button", "BBR 预设会触发表单提交");
  edit("net.ipv4.tcp_rmem", "4096 262144 67108864");
  edit("net.ipv4.tcp_ecn", "0");
  const callsBefore = testAPI.calls.length;
  preset().click();
  const merged = { ...bbrSettings, "net.ipv4.tcp_ecn": "0" };
  assertDraft(editor(), merged);
  assert.ok(editor().matches(":modal"), "填入预设关闭了参数编辑器");
  assert.equal(tcpConfirmation(), null, "填入 BBR 预设进入了确认流程");
  assert.equal(testAPI.tcpMutations.length, before, "填入预设直接修改了节点");
  assert.equal(testAPI.calls.length, callsBefore, "填入预设触发了 API 请求");
  assert.equal(document.querySelector('[data-refresh-key="bravo"] input[type=checkbox]:checked'), null, "预设污染了其他节点的草稿");
  tcpClose(editor());
  assert.ok(card().textContent.includes("有草稿"), "预设草稿没有显示在节点入口");
  openEditor();
  assertDraft(editor(), merged);
  await refresh();
  assertDraft(editor(), merged);
  assert.ok(editor().matches(":modal"), "刷新丢失预设编辑弹窗");

  const agent = testAPI.agents.find((entry) => entry.id === "alpha");
  for (const key of Object.keys(bbrSettings)) {
    const actual = agent.metrics.bbr.parameters[key];
    delete agent.metrics.bbr.parameters[key];
    await refresh();
    assert.ok(preset().disabled, `节点未上报 ${key} 时仍允许填入预设`);
    preset().click();
    assertDraft(editor(), merged);
    assert.equal(testAPI.tcpMutations.length, before, "禁用的预设提交了任务");
    agent.metrics.bbr.parameters[key] = actual;
  }
  await refresh();
  assert.ok(!preset().disabled, "节点恢复完整参数后预设仍被禁用");
  const collected = agent.metrics.bbr.collected_at;
  agent.metrics.bbr.collected_at = new Date(Date.now() - 120000).toISOString();
  await refresh();
  assert.ok(preset().disabled, "采集数据过期时仍允许填入预设");
  agent.metrics.bbr.collected_at = collected;
  await refresh();
  assert.ok(!preset().disabled, "采集数据恢复后预设未解锁");

  edit("net.ipv4.tcp_mtu_probing", "2");
  assert.equal(field("net.ipv4.tcp_mtu_probing").value, "2", "预设参数无法继续编辑");
  select("net.ipv4.tcp_ecn").checked = false;
  select("net.ipv4.tcp_ecn").dispatchEvent(new Event("change", { bubbles: true }));
  preset().click();
  assertDraft(editor(), bbrSettings);
  editor().querySelector("form").requestSubmit();
  const confirmation = await waitFor(() => tcpConfirmation(), "预设提交没有请求确认");
  for (const [key, value] of Object.entries(bbrSettings))
    assert.ok(confirmation.textContent.includes(`${key}: ${agent.metrics.bbr.parameters[key]} → ${value}`), `预设确认缺少参数差异：${key}`);
  assert.ok(preset().disabled, "确认期间仍可改写预设草稿");
  await refresh();
  assert.ok(preset().disabled, "后台刷新解除了确认期间的预设锁定");
  tcpCancel(confirmation);
  await waitFor(() => !preset().disabled, "取消应用后预设未解锁");
  assert.equal(testAPI.tcpMutations.length, before, "取消预设确认仍提交了任务");
  assertDraft(editor(), bbrSettings);
  assert.ok(editor().matches(":modal"), "取消预设确认丢失了编辑弹窗");
  await refresh();
  assertDraft(editor(), bbrSettings);

  editor().querySelector("form").requestSubmit();
  tcpAccept(await waitFor(tcpConfirmation, "预设重试没有确认弹窗"));
  await waitFor(() => testAPI.tcpMutations.length === before + 1, "预设任务未提交");
  await waitFor(() => !editor().open, "预设提交成功未关闭编辑器");
  const payload = testAPI.tcpMutations.at(-1);
  assert.equal(payload.agent_id, "alpha");
  assert.equal(payload.action, "configure-tcp", "预设未使用自定义 TCP 任务");
  assert.equal(payload.engine, "");
  assert.equal(Object.keys(payload.tcp_settings).sort().join(","), Object.keys(bbrSettings).sort().join(","), "预设任务包含多余或遗漏的参数");
  for (const [key, value] of Object.entries(bbrSettings))
    assert.equal(payload.tcp_settings[key], value, `预设任务参数错误：${key}`);
  assert.ok(tcpButton(card(), "自定义参数").disabled, "等待执行时仍允许编辑参数");
  await tcpSettle();
  testAPI.tcpTasks[0].status = "succeeded";
  await refresh();
  openEditor();
  assertDraft(editor(), {});
  assert.ok(!preset().disabled, "任务完成后预设没有解锁");
  testAPI.tcpTasks = [];
  await refresh();
  await delay(50);
}
