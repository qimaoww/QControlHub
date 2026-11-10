import { assert, delay } from "./browser/assertions.mjs";
const query = new URLSearchParams(location.search), agentID = query.get("agent");
const button = (name, root = document) => [...root.querySelectorAll("button")].find(item => item.textContent === name);
const dialog = title => [...document.querySelectorAll("dialog[open]")].find(box => box.querySelector("h2")?.textContent === title);
async function waitFor(predicate, message) {
  for (let attempt = 0; attempt < 1500; attempt++) {
    const value = await predicate();
    if (value) return value;
    await delay(10);
  }
  assert.fail(message);
}
const editor = () => document.querySelector('form textarea[name=content]');
async function run() {
  const login = await waitFor(() => document.querySelector('.a-login form'), "login missing");
  login.elements.username.value = "alice";
  login.elements.token.value = "config scope password fixture";
  login.requestSubmit();
  await waitFor(() => document.querySelector('button[title="退出登录"]') && !document.querySelector('.a-login') && !document.querySelector('#app[aria-busy=true]'), "password login incomplete");
  location.hash = `#live-config?agent=${agentID}&engine=xray`;
  const content = await waitFor(editor, "manual editor missing");
  const original = `{"inbounds":[{"tag":"manual","listen":"127.0.0.1","port":21000,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}],"opaque":{"large":9007199254740993}}`;
  content.value = original;
  content.dispatchEvent(new Event("input", {bubbles:true}));
  assert.equal(content.readOnly, false, "external discovery made manual editor read-only");
  assert.equal(button("保存并部署").disabled, false, "external discovery blocked managed deployment");
  assert.ok(document.body.textContent.includes("系统配置导入不可用"), "source failure disclosure missing");
  if (query.has("preview")) {
    window.importDiagnosticsPreview = {agentID, original};
    return;
  }
  content.form.requestSubmit();
  const deployConfirmation = await waitFor(() => dialog("部署并重启"), "deployment confirmation missing");
  button("部署并重启", deployConfirmation).click();
  const deployed = await waitFor(() => dialog("任务详情")?.textContent.includes("成功") && dialog("任务详情"), "manual deployment failed");
  deployed.querySelector('[aria-label=关闭]').click();
  await waitFor(() => !document.querySelector('#app[aria-busy=true]'), "deployment did not release UI");
  const session = await (await fetch("/api/v1/auth/session")).json();
  const api = async (path, body) => {
    const response = await fetch(`/api/v1${path}`, body === undefined ? {} : {method:"POST",headers:{"Content-Type":"application/json","X-QControlHub-CSRF":session.csrf_token},body:JSON.stringify(body)});
    const result = await response.json();
    assert.ok(response.ok, `API ${path}: ${JSON.stringify(result)}`);
    return result;
  };
  const base = `/agents/${agentID}/configs/xray`, saved = (await api(`${base}/workspace`)).config;
  assert.ok(saved.content.includes("9007199254740993"), "large integer changed");
  const history = await api(`/configs/${saved.id}/revisions`);
  location.hash = `#live-config?agent=${agentID}&engine=xray&source=import`;
  await waitFor(() => editor()?.readOnly && button("读取系统配置"), "independent import view missing");
  button("读取系统配置").click();
  await waitFor(() => editor()?.value.includes("external-source") && !button("读取系统配置").disabled, "protected source preview failed");
  assert.equal((await api(`${base}/workspace`)).config.content, saved.content, "preview replaced manual configuration");
  assert.equal((await api(`/configs/${saved.id}/revisions`)).length, history.length, "preview wrote history");
  location.hash = `#live-config?agent=${agentID}&engine=xray`;
  await waitFor(() => editor() && !editor().readOnly, "cancel did not restore ordinary editor");
  assert.equal(editor().value, saved.content, "cancel replaced ordinary content");
  location.hash = `#live-config?agent=${agentID}&engine=xray&source=import`;
  await waitFor(() => editor()?.readOnly && button("读取系统配置"), "second import view missing");
  button("读取系统配置").click();
  await waitFor(() => editor()?.value.includes("external-source") && !button("读取系统配置").disabled, "second preview failed");
  editor().form.requestSubmit();
  const confirmation = await waitFor(() => dialog("导入并迁移"), "explicit migration confirmation missing");
  button("导入并迁移", confirmation).click();
  const failed = await waitFor(() => dialog("任务详情")?.textContent.includes("DIAGNOSTIC_END") && dialog("任务详情"), "complete diagnostic tail missing");
  assert.ok(failed.textContent.includes("OUTPUT_END") && failed.textContent.includes("fixture nested cause"), "original cause or output lost");
  assert.ok(failed.textContent.includes("导入现有服务") && failed.textContent.includes("xray"), "task target heading missing");
  const output = failed.querySelector("pre");
  assert.ok(output.textContent.length > 65536, "diagnostics stayed at summary size");
  output.scrollTop = 50;
  await delay(2200);
  assert.equal(output.scrollTop, 50, "poll reset diagnostic scroll");
  assert.ok(document.documentElement.scrollWidth <= innerWidth+1, "page overflows viewport");
  assert.ok(failed.scrollWidth <= failed.clientWidth+1, "long diagnostic overflows dialog");
  assert.equal((await api(`${base}/workspace`)).config.content, saved.content, "failed import replaced saved configuration");
  assert.equal((await api(`/configs/${saved.id}/revisions`)).length, history.length, "failed import wrote history");
  const writeText = navigator.clipboard.writeText, execCommand = document.execCommand;
  const notices = new Set(document.querySelectorAll('.a-toast-region .a-toast'));
  try {
    navigator.clipboard.writeText = async () => { throw new DOMException("fixture unexpected clipboard interruption", "AbortError"); };
    document.execCommand = () => { throw new DOMException("fixture unexpected fallback interruption", "AbortError"); };
    button("复制诊断", failed).click();
    const notification = await waitFor(() => {
      if (document.querySelector('#app[aria-busy=true]')) return;
      return [...document.querySelectorAll('.a-toast-region .a-toast')].find(card => !notices.has(card) && card.dataset.tone === 'error');
    }, "unexpected native AbortError was swallowed or left controls busy");
    const records = await api('/tasks?action=panel-failure&limit=100');
    let retained = false;
    for (const task of records) {
      if (!task.error.includes("fixture unexpected fallback interruption")) continue;
      const detail = await api(`/tasks/${task.id}/diagnostics`);
      retained ||= detail.error.includes("fixture unexpected fallback interruption");
    }
    assert.ok(retained, "unexpected native AbortError was not retained in task diagnostics");
    button("查看详情", notification).click();
    const detail = await waitFor(() => {
      const box = [...document.querySelectorAll('dialog[open]')].at(-1);
      return box?.querySelector('pre')?.textContent.includes("fixture unexpected fallback interruption") && box;
    }, "native failure task detail did not open");
    assert.ok(detail.querySelector('.a-notice.error')?.textContent.startsWith("面板操作失败："), "panel failure was mislabeled as a node operation");
  } finally {
    navigator.clipboard.writeText = writeText;
    document.execCommand = execCommand;
  }
  const status = document.createElement("pre"); status.id = "browser-smoke-result";
  status.textContent = `Native session, external-source failure/manual deployment, preview/cancel/migration failure, immutable history, long diagnostic tails and stable scroll: passed; CSS viewport ${innerWidth}x${innerHeight}, DPR ${devicePixelRatio}`;
  document.body.append(status); document.documentElement.dataset.browserSmoke = "passed";
}
run().catch(error => {
  const status = document.createElement("pre"); status.id = "browser-smoke-result"; status.textContent = String(error.stack || error);
  document.body.append(status); document.documentElement.dataset.browserSmoke = "failed";
});
