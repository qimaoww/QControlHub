import { assert, waitFor } from "./assertions.mjs";

export async function testAgentTimeZoneLayout({ testAPI, mode }) {
  location.hash = "#settings-node-alpha";
  await waitFor(() => document.querySelector('.node-operations-workspace[data-agent-node="alpha"]'), "Agent 详情没有完成渲染");
  document.querySelector('[data-node-tab="agent"]').click();
  const form = await waitFor(() => document.querySelector('[data-agent-timezone-form="alpha"]'), "Agent 时区设置未渲染");
  const section = form.closest("[data-agent-timezone]");
  const modeInput = form.querySelector("[data-timezone-mode]");
  const input = form.querySelector("[data-timezone-input]");
  const submit = form.querySelector("[data-timezone-submit]");
  assert.ok(section && modeInput && input && submit,
    `Agent 时区控件不完整：${JSON.stringify({ section: Boolean(section), modeInput: Boolean(modeInput), input: Boolean(input), submit: Boolean(submit), markup: form.outerHTML.slice(0, 1200) })}`);
  assert.equal(section.querySelector("header b").textContent, "Agent 进程时区");
  assert.equal(section.querySelector("[data-timezone-status]").textContent, "已生效");
  assert.equal(section.querySelector("[data-timezone-applied]").hidden, true, "已生效时不应重复显示时区");
  assert.equal(section.querySelector("[data-clock-sync-status]").textContent, "已同步", "系统时钟同步状态未显示");
  assert.equal(section.querySelector("[data-clock-sync-offset]").textContent, "-1.375 ms", "NTP 时钟偏差的正负毫秒未显示");
  assert.equal(input.getAttribute("aria-label"), "IANA 时区", "时区输入缺少可访问标签");
  assert.equal(modeInput.closest("label").querySelector("span").textContent, "模式", "模式选择缺少可见标签");
  assert.equal(modeInput.value, "custom", "已保存的自定义模式未恢复");
  const update = await import("../modules/agent-time-zone.js");
  const agent = testAPI.agents.find((item) => item.id === "alpha");
  if (mode.endsWith("-readonly")) {
    update.updateAgentTimeZone(document, agent);
    assert.ok(modeInput.disabled && input.disabled && submit.disabled, "只读时区控件可写");
    form.requestSubmit(submit);
    assert.equal(testAPI.timeZoneSaves.length, 0, "只读表单提交了时区修改");
    return;
  }
  assert.equal(input.disabled, false, "自定义时区输入不可编辑");
  input.focus();
  assert.equal(document.activeElement, input, "时区输入无法键盘聚焦");
  agent.time_zone = { ...agent.time_zone, time_zone: "Europe/Paris", applied_time_zone: "Europe/Paris", revision: 3 };
  update.updateAgentTimeZone(document, agent);
  assert.equal(input.value, "Europe/Paris", "未编辑的输入未显示远端保存的时区");
  assert.equal(document.activeElement, input, "远端更新导致焦点丢失");
  assert.equal(form.dataset.timezoneRevision, "3", "远端更新未同步提交版本");

  assert.equal(modeInput.value, "custom", "键盘操作改变了尚未提交的模式");
  assert.equal(input.disabled, false, "键盘切换到自定义模式后输入未启用");
  modeInput.value = "auto";
  modeInput.dispatchEvent(new Event("change", { bubbles: true }));
  assert.equal(input.disabled, true, "自动模式仍允许编辑自定义时区");
  modeInput.value = "custom";
  modeInput.dispatchEvent(new Event("change", { bubbles: true }));
  assert.equal(input.disabled, false, "自定义模式没有启用时区输入");

  input.value = "Pacific/Apia";
  input.focus();
  update.updateAgentTimeZone(document, {
    id: "alpha", status: "online",
    metrics: { clock_sync: { status: "unsynchronized", offset_milliseconds: 0.25 } },
    time_zone: { mode: "custom", time_zone: "Pacific/Apia", applied_time_zone: "America/New_York", status: "applying", revision: 3 },
  });
  assert.equal(document.activeElement, input, "轮询状态更新导致键盘焦点丢失");
  assert.equal(input.value, "Pacific/Apia", "状态轮询覆盖了用户输入");
  assert.equal(section.querySelector("[data-timezone-applied]").textContent, "当前生效：America/New_York");
  assert.equal(section.querySelector("[data-timezone-applied]").hidden, false);
  assert.equal(section.querySelector("[data-clock-sync-status]").textContent, "未同步");
  assert.equal(section.querySelector("[data-clock-sync-offset]").textContent, "+0.250 ms");

  form.requestSubmit(submit);
  await waitFor(() => testAPI.timeZoneSaves.length === 1 && !submit.disabled, "自定义时区保存没有完成");
  assert.equal(JSON.stringify(testAPI.timeZoneSaves[0]), JSON.stringify({ agentID: "alpha", mode: "custom", time_zone: "Pacific/Apia", revision: 3, retry: false }));
  assert.equal(input.value, "Pacific/Apia");
  assert.equal(section.querySelector("[data-timezone-status]").textContent, "应用中");
  assert.equal(section.querySelector("[data-clock-sync-status]").textContent, "已同步", "保存后丢失了时钟同步状态");
  assert.equal(section.querySelector("[data-clock-sync-offset]").textContent, "-1.375 ms");

  modeInput.focus();
  form.requestSubmit(submit);
  await waitFor(() => testAPI.timeZoneSaves.length === 2 && !submit.disabled, "键盘提交没有完成");
  assert.equal(document.activeElement, modeInput, "保存后模式控件的焦点丢失");

  testAPI.timeZoneSaveFailure = true;
  input.value = "Asia/Tokyo";
  input.focus();
  assert.equal(submit.disabled, false, `保存失败测试开始时按钮不可用：busy=${form.dataset.busy} connected=${form.isConnected}`);
  form.requestSubmit(submit);
  await waitFor(() => !submit.disabled && section.querySelector("[data-timezone-status]").textContent.includes("服务暂不可用"), "失败反馈没有显示");
  assert.equal(input.value, "Asia/Tokyo", "保存失败丢失了输入");
  assert.equal(document.activeElement, input, "保存失败后焦点丢失");

  testAPI.timeZoneSaveFailure = false;
  modeInput.value = "auto";
  modeInput.dispatchEvent(new Event("change", { bubbles: true }));
  form.requestSubmit(submit);
  await waitFor(() => testAPI.timeZoneSaves.length === 3 && !submit.disabled, "自动模式保存没有完成");
  assert.equal(testAPI.timeZoneSaves[2].mode, "auto");
  assert.equal(testAPI.timeZoneSaves[2].time_zone, "");
  assert.equal(testAPI.timeZoneSaves[2].retry, true);
  const previous = { ...agent.time_zone };
  agent.time_zone = { ...previous, time_zone: "Asia/Tokyo", applied_time_zone: "Asia/Tokyo", status: "applied", revision: previous.revision + 1 };
  update.updateAgentTimeZone(document, agent);
  assert.equal(input.value, "Asia/Tokyo", "公网 IP 变化后自动时区未更新");
  assert.equal(input.disabled, true, "自动模式更新后允许编辑时区");
  assert.equal(submit.textContent, "重新检测");
  update.updateAgentTimeZone(document, { ...agent, time_zone: previous });
  assert.equal(input.value, "Asia/Tokyo", "旧轮询回退了已确认的时区");
  assert.equal(section.querySelector("[data-timezone-status]").textContent, "已生效");

  modeInput.value = "custom";
  modeInput.dispatchEvent(new Event("change", { bubbles: true }));
  input.value = "Australia/Sydney";
  input.focus();
  agent.time_zone = { ...agent.time_zone, time_zone: "Europe/London", applied_time_zone: "Europe/London", revision: agent.time_zone.revision + 1 };
  update.updateAgentTimeZone(document, agent);
  assert.equal(modeInput.value, "custom", "自动检测覆盖了未保存的自定义模式");
  assert.equal(input.value, "Australia/Sydney", "自动检测覆盖了未保存的自定义时区");
  form.requestSubmit(submit);
  await waitFor(() => !submit.disabled && section.querySelector("[data-timezone-status]").textContent.includes("冲突"), "并发修改未显示冲突");
  assert.equal(input.value, "Australia/Sydney", "并发冲突丢失了输入");
  assert.equal(document.activeElement, input, "并发冲突导致焦点丢失");
  assert.equal(testAPI.timeZoneSaves.length, 3, "并发修改覆盖了较新设置");

  const panel = section.closest("[data-node-panel]");
  const rect = section.getBoundingClientRect();
  const inputRect = input.getBoundingClientRect();
  const submitRect = submit.getBoundingClientRect();
  assert.ok(inputRect.width >= 100 && (inputRect.right <= submitRect.left || inputRect.bottom <= submitRect.top), "时区输入与按钮重叠或不可用");
  const modeRect = modeInput.getBoundingClientRect();
  assert.ok(modeRect.left >= rect.left && modeRect.right <= rect.right, "模式控件超出时区区域");
  assert.ok(Math.abs(modeRect.height - inputRect.height) <= 1, "模式选择与时区输入的高度不一致");
  assert.ok(Math.abs(inputRect.bottom - submitRect.bottom) <= 1, "应用按钮未与时区输入对齐");
  assert.ok(Math.abs(inputRect.height - submitRect.height) <= 1, "输入和应用按钮的高度不一致");
  if (innerWidth > 600) assert.ok(Math.abs(modeRect.top - inputRect.top) <= 1, `桌面模式选择与时区输入未对齐：${JSON.stringify({ mode: modeRect, input: inputRect })}`);
  if (matchMedia("(pointer: coarse)").matches) {
    for (const control of [modeInput, input, submit]) assert.ok(parseFloat(getComputedStyle(control).height) >= 44, "触摸控件高度不足 44 像素");
  }
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
