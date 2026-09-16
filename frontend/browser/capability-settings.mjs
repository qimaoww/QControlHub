import { assert, waitFor } from "./assertions.mjs";

export async function testCapabilitySettingsRuntime({ mode, testAPI }) {
const form = await waitFor(() => document.querySelector("#settings-form"), "系统设置未加载");
  const inputs = [...form.querySelectorAll('#settings-engines input[role="switch"]')];
  assert.equal(inputs.length, 4, "全局设置应显示四个内核开关");
  assert.equal(form.querySelector("#settings-engines .settings-toggle"), null, "不再使用大块复选框卡片");
  assert.ok(inputs[0].checked && !inputs[1].checked, "默认能力状态错误");
  assert.equal(getComputedStyle(inputs[0]).appearance, "none", "开关不应使用浏览器默认复选框样式");
  for (const link of document.querySelectorAll('[aria-label="设置目录"] a')) {
    assert.equal(link.querySelector("span").textContent, document.querySelector(`${link.hash} .settings-section-number`).textContent, "目录与设置分区编号须一致");
  }
  assert.equal(form.querySelectorAll("[data-settings-state]").length, 1, "设置页只保留一处保存状态");
  assert.equal(form.querySelector("header p:empty, .settings-toggle small:empty"), null, "省略说明后不应留下空文案行");
  if (!mode.endsWith("-readonly")) {
    assert.ok(form.querySelector("[data-settings-state]").getBoundingClientRect().height > 0,
      "手机和桌面都应显示保存状态");
  }
  if (new URLSearchParams(location.search).has("preview")) return;
  if (mode.endsWith("-readonly")) {
    assert.ok(inputs.every(input => input.disabled), "只读用户不得修改全局能力");
    assert.equal(form.querySelector("[data-save-settings]"), null, "只读页不得提供保存入口");
    return;
  }
  const ipv4 = form.querySelector('[name="cnip_ipv4_url"]'), ipv6 = form.querySelector('[name="cnip_ipv6_url"]');
  assert.ok(ipv4 && ipv6 && !form.querySelector('[name="cnip_format"]'), "CN IP sources must use automatic format and both families");
  const save = form.querySelector("[data-save-settings]");
  assert.ok(save.disabled, "初始状态不应需要保存");
  ipv4.value = "https://example.com/cn.mmdb";
  ipv6.value = "https://example.com/cn6.srs";
  inputs[1].click();
  assert.ok(!save.disabled, "切换应标记待保存");
  assert.notEqual(getComputedStyle(inputs[1].closest("label").querySelector(".when-enabled")).display, "none", "状态文字须随开关即时更新");
  testAPI.settingsFailure = true;
  form.requestSubmit(save);
  await waitFor(() => !save.disabled && document.body.textContent.includes("数据已发生变化或存在冲突"), "保存失败未恢复操作或未显示中文提示");
  assert.ok(inputs[1].checked, "保存失败须保留选择");
  testAPI.settingsFailure = false;
  for (const input of inputs) if (input.checked) input.click();
  form.requestSubmit(save);
  await waitFor(() => testAPI.settings.revision === 2, "全局能力未保存");
  assert.equal(testAPI.settings.default_agent_engines.length, 0, "必须允许全部关闭");
  assert.equal(testAPI.settings.cnip_source.ipv4_url, ipv4.value, "IPv4 source missing from save");
  assert.equal(testAPI.settings.cnip_source.ipv6_url, ipv6.value, "IPv6 source missing from save");
  assert.equal(testAPI.settings.cnip_source.format, "auto", "format must be automatic");
  assert.ok(save.disabled, "保存成功后应清除待保存状态");
}
