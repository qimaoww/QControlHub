import { assert, waitFor } from "./assertions.mjs";

export async function testAgentTimeZoneLayout({ testAPI, mode }) {
  location.hash = "#settings-node-alpha";
  await waitFor(() => document.querySelector('.node-operations-workspace[data-agent-node="alpha"]'), "Agent 详情没有完成渲染");
  document.querySelector('[data-node-tab="agent"]').click();
  const form = await waitFor(() => document.querySelector('[data-agent-timezone-form="alpha"]'), "Agent 时区设置未渲染");
  const section = form.closest("[data-agent-timezone]");
  const auto = form.querySelector('[data-timezone-mode][value="auto"]');
  const custom = form.querySelector('[data-timezone-mode][value="custom"]');
  const input = form.querySelector("[data-timezone-input]");
  const submit = form.querySelector("[data-timezone-submit]");
  assert.ok(section && auto && custom && input && submit,
    `Agent 时区控件不完整：${JSON.stringify({ section: Boolean(section), auto: Boolean(auto), custom: Boolean(custom), input: Boolean(input), submit: Boolean(submit), markup: form.outerHTML.slice(0, 1200) })}`);
  assert.equal(section.querySelector("header b").textContent, "Agent 时区");
  assert.equal(section.querySelector("header small").textContent, "仅影响 Agent 进程，应用时会短暂重连");
  assert.equal(section.querySelector("[data-clock-sync-status]").textContent, "已同步", "系统时钟同步状态未显示");
  assert.equal(section.querySelector("[data-clock-sync-offset]").textContent, "-1.375 ms", "NTP 时钟偏差的正负毫秒未显示");
  assert.equal(section.querySelector("label[for]") === null && input.closest("label") !== null, true, "时区输入缺少可访问标签");
  assert.equal(custom.checked, true, "已保存的自定义模式未恢复");
  assert.equal(input.disabled, false, "自定义时区输入不可编辑");
  input.focus();
  assert.equal(document.activeElement, input, "时区输入无法键盘聚焦");

  assert.equal(custom.checked, true, "键盘操作改变了尚未提交的模式");
  assert.equal(input.disabled, false, "键盘切换到自定义模式后输入未启用");
  auto.click();
  assert.equal(input.disabled, true, "自动模式仍允许编辑自定义时区");
  custom.click();
  assert.equal(input.disabled, false, "自定义模式没有启用时区输入");

  input.value = "Pacific/Apia";
  input.focus();
  const update = await import("../modules/agent-time-zone.js");
  update.updateAgentTimeZone(document, {
    id: "alpha", status: "online",
    metrics: { clock_sync: { status: "unsynchronized", offset_milliseconds: 0.25 } },
    time_zone: { mode: "custom", time_zone: "Pacific/Apia", applied_time_zone: "America/New_York", status: "applying", revision: 3 },
  });
  assert.equal(document.activeElement, input, "轮询状态更新导致键盘焦点丢失");
  assert.equal(input.value, "Pacific/Apia", "状态轮询覆盖了用户输入");
  assert.equal(section.querySelector("[data-timezone-applied]").textContent, "America/New_York");
  assert.equal(section.querySelector("[data-clock-sync-status]").textContent, "未同步");
  assert.equal(section.querySelector("[data-clock-sync-offset]").textContent, "+0.250 ms");

  form.requestSubmit(submit);
  await waitFor(() => testAPI.timeZoneSaves.length === 1 && !submit.disabled, "自定义时区保存没有完成");
  assert.equal(JSON.stringify(testAPI.timeZoneSaves[0]), JSON.stringify({ agentID: "alpha", mode: "custom", time_zone: "Pacific/Apia", revision: 2, retry: false }));
  assert.equal(section.querySelector("[data-timezone-configured]").textContent, "Pacific/Apia");
  assert.equal(section.querySelector("[data-timezone-status]").textContent, "正在同步时区到 Agent 进程");

  testAPI.timeZoneSaveFailure = true;
  input.value = "Asia/Tokyo";
  input.focus();
  assert.equal(submit.disabled, false, `保存失败测试开始时按钮不可用：busy=${form.dataset.busy} connected=${form.isConnected}`);
  form.requestSubmit(submit);
  await waitFor(() => !submit.disabled && section.querySelector("[data-timezone-status]").textContent.includes("服务暂不可用"), "失败反馈没有显示");
  assert.equal(input.value, "Asia/Tokyo", "保存失败丢失了输入");
  assert.equal(document.activeElement, input, "保存失败后焦点丢失");

  const panel = section.closest("[data-node-panel]");
  const rect = section.getBoundingClientRect();
  const metrics = {
    mode, cssViewport: { width: innerWidth, height: innerHeight },
    visualViewport: { width: visualViewport.width, height: visualViewport.height, scale: visualViewport.scale },
    outerWindow: { width: outerWidth, height: outerHeight }, devicePixelRatio,
    documentWidth: document.documentElement.scrollWidth,
    panel: { left: panel.getBoundingClientRect().left, right: panel.getBoundingClientRect().right, scrollWidth: panel.scrollWidth, clientWidth: panel.clientWidth },
    section: { left: rect.left, right: rect.right, scrollWidth: section.scrollWidth, clientWidth: section.clientWidth },
  };
  assert.ok(metrics.documentWidth <= innerWidth + 1, `页面横向溢出：${JSON.stringify(metrics)}`);
  assert.ok(metrics.panel.left >= -1 && metrics.panel.right <= innerWidth + 1 && metrics.panel.scrollWidth <= metrics.panel.clientWidth + 1, `侧栏占用后设置面板溢出：${JSON.stringify(metrics)}`);
  assert.ok(metrics.section.left >= -1 && metrics.section.right <= innerWidth + 1 && metrics.section.scrollWidth <= metrics.section.clientWidth + 1, `时区区域溢出：${JSON.stringify(metrics)}`);
  if (mode.includes("mobile")) assert.ok(matchMedia("(pointer: coarse)").matches, "手机视口未启用触摸模拟");
  window.agentTimeZoneMeasurements = metrics;
}
