import { assert, waitFor } from "./browser/assertions.mjs";
const query = new URLSearchParams(location.search);
const username = query.get("user");
const agentID = query.get("agent");
const shared = username === "bob";
const formFor = engine => document.querySelector(`.service-${engine} .core-version-form`);
const buttonFor = (engine, selector) => document.querySelector(`.service-${engine} ${selector}`);
async function run() {
  const login = await waitFor(() => document.querySelector("#login-form"), "login did not render");
  login.elements.username.value = username;
  login.elements.token.value = "config scope password fixture";
  login.requestSubmit();
  await waitFor(() => document.querySelector('#logout'), "password login did not complete");
  const response = await fetch("/api/v1/auth/session");
  const session = await response.json();
  assert.equal(session.role, "user");
  assert.ok(session.permissions.includes("tasks.execute"));
  assert.ok(session.permissions.includes("tasks.read"));
  location.hash = `#settings-node-${agentID}`;
  await waitFor(() => document.querySelector(`.node-operations-workspace[data-agent-node="${agentID}"]`), "node did not render");
  if (shared) {
    assert.equal(document.querySelector("[data-core-uninstall], [data-service-action]"), null, "recipient received host lifecycle management");
    assert.equal(formFor("mihomo").querySelectorAll('input[name="release_channel"]').length, 1, "recipient received host version selection");
  }
  {
    const open = buttonFor("mihomo", "[data-open-version-form]");
    assert.equal(open.disabled, false);
    open.click();
    const form = formFor("mihomo");
    assert.equal(form.querySelector("button[type=submit]").disabled, false);
    await waitFor(() => {
      const bounds = form.getBoundingClientRect();
      const controls = [...form.querySelectorAll("button, .release-channel-options label")].filter(element => element.checkVisibility());
      return controls.length >= (shared ? 2 : 4) && controls.every(element => {
        const rect = element.getBoundingClientRect();
        return rect.width > 0 && rect.left >= bounds.left && rect.right <= bounds.right + 1;
      });
    }, "installation form clips a control");
    form.requestSubmit();
    const dialog = await waitFor(() => document.querySelector('[data-confirm-dialog][open]'), "install confirmation missing");
    [...dialog.querySelectorAll("button")].find(button => button.textContent === "提交任务").click();
    await waitFor(() => document.body.textContent.includes("fixture download failure"), "installation failure was not visible");
    const failure = formFor("mihomo").querySelector("[data-core-install-status]");
    assert.ok(failure.scrollWidth <= failure.clientWidth + 1, "long installation error overflows");
    await waitFor(() => formFor("mihomo")?.querySelector("button[type=submit]")?.disabled === false, "failed install cannot retry");
    formFor("mihomo").closest("details").open = true;
    formFor("mihomo").requestSubmit();
    const retry = await waitFor(() => document.querySelector('[data-confirm-dialog][open]'), "retry confirmation missing");
    [...retry.querySelectorAll("button")].find(button => button.textContent === "提交任务").click();
    await waitFor(() => buttonFor("mihomo", "[data-core-version]")?.textContent.includes("1.19.30"), "runtime version did not refresh");
    location.hash = `#live-config?agent=${agentID}&engine=xray`;
  }
  const add = await waitFor(() => document.querySelector('[data-inbound-action="add"]'), "add inbound action missing");
  add.click();
  const plan = await waitFor(() => document.querySelector("#server-plan-form"), "preset form missing");
  assert.ok(document.body.textContent.includes(shared ? "受控准备" : "自动安装"));
  const deploy = plan.querySelector('[data-plan-intent="deploy"]');
  assert.equal(deploy.disabled, false, "first install blocked deployment");
  plan.requestSubmit(deploy);
  await waitFor(() => document.querySelector("[data-preset-status]")?.textContent.includes("部署成功"), "automatic preparation did not continue deployment");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "page overflows viewport");
  const status = document.createElement("pre");
  status.id = "browser-smoke-result";
  status.textContent = `${username}: real password session, manual failure/retry/refresh and ${shared ? "private" : "owner"} automatic preparation, exact revision deployment, viewport ${innerWidth}: passed`;
  document.body.append(status);
  document.documentElement.dataset.browserSmoke = "passed";
}
run().catch(error => {
  const status = document.createElement("pre"); status.id="browser-smoke-result"; status.textContent=String(error.stack || error); document.body.append(status); document.documentElement.dataset.browserSmoke="failed";
});
