import { assert, delay } from "./browser/assertions.mjs";
const query = new URLSearchParams(location.search);
const username = query.get("user"), agentID = query.get("agent");
const shared = username === "bob";
const button = (name, root = document) => [...root.querySelectorAll("button")].find(control => control.textContent === name);
const dialog = title => [...document.querySelectorAll("dialog[open]")].find(box => box.querySelector("h2")?.textContent === title);
const core = () => document.querySelector('dialog[open] [data-refresh-key=mihomo]');
async function waitFor(predicate, message) {
  for (let attempt = 0; attempt < 1200; attempt++) {
    const value = predicate(); if (value) return value;
    await delay(10);
  }
  assert.fail(message);
}
async function confirmation(title) {
  const box = await waitFor(() => dialog(title), `${title}: confirmation missing`);
  button(title, box).click();
}
async function run() {
  const login = await waitFor(() => document.querySelector('.a-login form'), "native login did not render");
  login.elements.username.value = username;
  login.elements.token.value = "config scope password fixture";
  login.requestSubmit();
  await waitFor(() => document.querySelector('button[title="退出登录"]') && !document.querySelector('#app[aria-busy=true]'), "password login did not complete");
  const response = await fetch("/api/v1/auth/session"), session = await response.json();
  assert.equal(session.role, "user");
  assert.ok(session.permissions.includes("tasks.execute"));
  assert.ok(session.permissions.includes("tasks.read"));
  location.hash = `#settings-node-${agentID}`;
  await waitFor(core, "native node settings did not render");
  const settings = core().closest('dialog');
  const nodePanel = settings.querySelector('[data-settings-panel=node]'), corePanel = settings.querySelector('[data-settings-panel=core]');
  assert.ok(!nodePanel.hidden && corePanel.hidden, 'node deep link did not open node settings');
  assert.ok(!nodePanel.querySelector('[data-core-installed],.a-probe-network,.a-probe-meters,[data-ip-family]'), 'node settings repeats the overview or exposes core controls');
  assert.equal(Boolean(nodePanel.querySelector('[name=name]')), !shared, 'node editing escaped its owner scope');
  button('内核设置', settings).click();
  assert.ok(nodePanel.hidden && !corePanel.hidden, 'core settings did not open independently');
  assert.ok(core().checkVisibility(), 'core operations remain hidden');
  assert.equal(Boolean(core().querySelector('[data-engine-capability]')), !shared, 'recipient received host capability controls');
  if (shared) {
    assert.equal(button("卸载内核", core()), undefined, "recipient received host uninstall control");
    for (const name of ["启动", "停止", "重启"]) assert.ok(!button(name, core()) || button(name, core()).disabled, "unprepared recipient received host lifecycle management");
  }
  const open = button(shared ? "准备内核" : "安装内核", core());
  assert.ok(open && !open.disabled, "manual first installation is blocked"); open.click();
  const installBox = await waitFor(() => dialog("Browser install · mihomo"), "native installation dialog missing");
  const form = installBox.querySelector("form"), submit = form.querySelector('[type=submit]');
  assert.ok(!submit.disabled, "manual installation is disabled");
  assert.equal([...form.elements.release_channel.options].map(option => option.value).join(','), shared ? "stable" : "stable,development,custom", "host release selection escaped its scope");
  const bounds = form.getBoundingClientRect();
  for (const control of form.querySelectorAll("button,input,select")) {
    if (!control.checkVisibility() || control.closest('[hidden]')) continue;
    const rect = control.getBoundingClientRect();
    assert.ok(rect.width > 0 && rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1, "installation form clips a control");
  }
  form.requestSubmit(); await confirmation("提交安装");
  const failed = await waitFor(() => dialog("任务详情")?.querySelector('.a-notice')?.textContent.includes("fixture download failure") && dialog("任务详情"), "installation failure was not visible");
  const failure = failed.querySelector('.a-notice');
  assert.ok(failure.scrollWidth <= failure.clientWidth + 1, "long installation error overflows");
  failed.querySelector('[aria-label=关闭]').click();
  await waitFor(() => !submit.disabled, "failed install cannot retry");
  form.requestSubmit(); await confirmation("提交安装");
  const success = await waitFor(() => dialog("任务详情")?.textContent.includes("成功") && dialog("任务详情"), "installation retry did not succeed");
  success.querySelector('[aria-label=关闭]').click();
  await waitFor(() => core()?.textContent.includes("1.19.30"), "runtime version did not refresh");
  assert.ok(!corePanel.hidden && core().checkVisibility(), 'runtime refresh changed the settings section');
  installBox.querySelector('[aria-label=关闭]').click();
  document.querySelector('dialog[open] [data-refresh-key=mihomo]').closest('dialog').querySelector('[aria-label=关闭]').click();
  location.hash = `#live-config?agent=${agentID}&engine=xray`;
  const add = await waitFor(() => button("添加入站"), "native add inbound action missing"); add.click();
  const plan = await waitFor(() => document.querySelector('dialog[open] form:has([name=protocol]) [name=port]')?.form, "native preset form missing");
  const deploy = plan.querySelector('[type=submit]');
  assert.equal(deploy.textContent, "保存并部署");
  assert.equal(deploy.disabled, false, "first install blocked deployment");
  plan.requestSubmit(); await confirmation("部署并重启");
  const prepare = await waitFor(() => dialog("安装并部署"), "automatic preparation confirmation missing");
  assert.ok(prepare.textContent.includes("此内核尚未安装") && prepare.textContent.includes("稳定版"), "automatic first-install intent is not visible");
  button("安装并部署", prepare).click();
  await waitFor(() => dialog("任务详情")?.textContent.includes("成功"), "automatic preparation did not continue exact-revision deployment");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "page overflows viewport");
  const status = document.createElement("pre"); status.id = "browser-smoke-result";
  status.textContent = `${username}: native real password session, manual failure/retry/refresh and ${shared ? "private" : "owner"} automatic preparation, exact revision deployment, viewport ${innerWidth}: passed`;
  document.body.append(status); document.documentElement.dataset.browserSmoke = "passed";
}
run().catch(error => {
  const status = document.createElement("pre"); status.id="browser-smoke-result"; status.textContent=String(error.stack || error); document.body.append(status); document.documentElement.dataset.browserSmoke="failed";
});
