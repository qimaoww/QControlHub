import { assert, delay, waitFor } from "./assertions.mjs";

export async function testConfigLayoutRuntime({ testAPI }) {
  const styles = document.createElement("link"); styles.rel = "stylesheet"; styles.href = "/assets/analytics/theme.css";
  const styled = new Promise((resolve, reject) => { styles.onload = resolve; styles.onerror = reject; }); document.head.append(styles); await styled;
  const session = await (await fetch("/api/v1/auth/session")).json();
  (await import("../analytics/app.js")).startAnalytics({ app: document.querySelector("#app"), session });
  const root = () => document.querySelector(".a-main");
  const target = () => root()?.querySelector(".a-target-form");
  const editor = () => root()?.querySelector('textarea[name="content"]');
  const configForm = () => editor()?.form;
  const button = (text, within = root()) => [...within.querySelectorAll("button")].find(control => control.textContent === text);
  const settled = async () => { await waitFor(() => target() && editor() && !document.querySelector('#app[aria-busy=true],.a-main [aria-busy=true]'), "配置页面未就绪"); await delay(30); };
  const change = (control, value) => { control.focus(); control.value = value; control.dispatchEvent(new Event("change", { bubbles: true })); };
  const input = (control, value) => { control.value = value; control.dispatchEvent(new Event("input", { bubbles: true })); };
  const confirm = async label => { const dialog = await waitFor(() => [...document.querySelectorAll("dialog[open]")].find(box => box.querySelector("h2")?.textContent === label), `缺少确认：${label}`); return dialog; };
  const close = async dialog => { dialog.querySelector('[aria-label="关闭"]').click(); await waitFor(() => !dialog.open, "弹窗未关闭"); await delay(30); };
  const cancel = async label => { const box = await confirm(label); button("取消", box).click(); await waitFor(() => !box.open, "确认未取消"); await delay(30); };
  const accepts = async label => { const box = await confirm(label); button(label, box).click(); await waitFor(() => !box.open, "确认未接受"); await settled(); };
  await settled();
  assert.equal(target().elements.engine.options.length, 4, "缺少已安装内核");
  assert.equal(document.querySelectorAll('.a-rail a[href="#agents"]').length, 0, "仍存在重复预设导航");
  if (!new URLSearchParams(location.search).has("preview")) {
    for (const [hash, agent, engine] of [
      ["#agents", "alpha", "xray"], ["#preset-node-bravo", "bravo", "xray"],
      ["#agent-config?agent=charlie&engine=sing-box", "charlie", "sing-box"],
      ["#live-config?agent=alpha&engine=xray", "alpha", "xray"],
    ]) {
      const previousTarget = target();
      location.hash = hash;
      await waitFor(() => target() && target() !== previousTarget && target().elements.agent.value === agent && target().elements.engine.value === engine && editor(), `旧链接目标未保留：${hash}`); await settled();
      assert.equal(editor().value, testAPI.layoutConfigs.get(`${agent}-${engine}`).content, "源码与链接目标不符");
      if (testAPI.agents.find(item => item.id === agent).status === "offline") {
        assert.ok(root().textContent.includes("离线"), "离线节点无状态提示");
        assert.ok(configForm().querySelector("[type=submit]").disabled, "离线仍允许部署");
      }
    }
    change(target().elements.engine, "sing-box"); await waitFor(() => target()?.elements.engine.value === "sing-box" && editor(), "切换内核失败"); await settled();
    assert.equal(document.activeElement, target().elements.engine, "切换内核丢失选择框焦点");
    assert.ok(location.hash.includes("engine=sing-box"), "内核未写入安全链接");
    const source = editor(), original = source.value, draft = `${original}\n`;
    assert.ok(!source.readOnly && !root().querySelector("[data-config-preview]"), "完整源码不能直接编辑");
    input(source, draft); source.focus(); source.setSelectionRange(1, 3);
    for (const tool of [button("通用配置项"), button("添加入站")]) {
      assert.ok(tool, "缺少通用配置或入站工具"); tool.click(); await cancel("打开编辑工具");
      assert.equal(source.value, draft, "取消编辑工具丢失源码草稿"); assert.equal(source.selectionStart, 1); assert.equal(source.selectionEnd, 3);
      assert.equal(editor(), source, "打开工具重建源码编辑器");
      assert.equal(source.closest(".a-editor").getAnimations().length, 0, "工具操作重复播放源码动画");
    }
    const accessReads = testAPI.calls.filter(call => call.path === "/access-controls").length;
    button("访问限制").click(); await delay(40);
    assert.equal(testAPI.calls.filter(call => call.path === "/access-controls").length, accessReads, "未保存源码仍读取可提交的访问限制");
    assert.equal(source.value, draft, "访问限制覆盖源码草稿");
    button("重置修改").click(); assert.equal(source.value, original); assert.ok(button("重置修改").disabled, "重置后仍显示待重置");
    input(source, draft); input(source, original); assert.ok(button("重置修改").disabled, "回到原值仍标记未保存");
    input(source, draft);
    change(target().elements.agent, "bravo"); await cancel("离开页面");
    assert.equal(editor(), source); assert.equal(source.value, draft); assert.equal(target().elements.agent.value, "alpha", "取消节点切换显示错误目标");
    change(target().elements.engine, "mihomo"); await cancel("离开页面");
    assert.equal(target().elements.engine.value, "sing-box"); assert.equal(source.value, draft, "取消内核切换丢失草稿");
    change(target().elements.engine, "mihomo"); await accepts("离开页面");
    assert.equal(target().elements.engine.value, "mihomo"); assert.equal(editor().value, testAPI.layoutConfigs.get("alpha-mihomo").content);
    change(target().elements.engine, "xray"); await settled(); await waitFor(() => target().elements.engine.value === "xray", "无法返回 Xray");
    button("通用配置项").focus(); button("通用配置项").click();
    const common = await confirm("通用配置项");
    change(common.querySelector('[name="mutation"]'), "modify");
    await waitFor(() => common.querySelector('[name="fragment"]').value.includes("1.1.1.1"), "通用配置未通过字段 API 读取");
    assert.equal(editor().value, testAPI.layoutConfigs.get("alpha-xray").content);
    common.dispatchEvent(new Event("cancel", { cancelable: true })); await waitFor(() => !common.open, "Escape 未关闭工具弹窗");
    assert.equal(document.activeElement, button("通用配置项"), "关闭工具未恢复焦点");
    button("保存历史").click(); const history = await confirm("节点配置历史");
    button("预览", history).click(); await waitFor(() => history.querySelector("pre")?.textContent.includes("inbounds"), "历史未从 API 读取预览"); await close(history);
    const form = configForm(), baseline = editor().value;
    input(form.elements.name, "源码保存后的名称"); input(form.elements.description, "保存后重置基线"); input(editor(), `${baseline}\n`);
    let finish; testAPI.layoutSaveGate = new Promise(resolve => { finish = resolve; });
    form.requestSubmit(); await waitFor(() => form.getAttribute("aria-busy") === "true", "保存未锁定表单");
    assert.ok(button("重置修改").disabled, "保存中可重置草稿");
    const submitting = editor().value; button("格式化 JSON").click(); assert.equal(editor().value, submitting, "保存中格式化改变待提交源码");
    const deployment = await confirm("部署并重启"); button("部署并重启", deployment).click();
    finish(); testAPI.layoutSaveGate = null; await settled();
    await close(await confirm("任务详情"));
    assert.ok(button("重置修改").disabled, "已保存仍显示待重置");
    input(form.elements.name, "丢弃此修改"); input(form.elements.description, "丢弃此说明"); input(editor(), "{}"); button("重置修改").click();
    assert.equal(form.elements.name.value, "源码保存后的名称"); assert.equal(form.elements.description.value, "保存后重置基线"); assert.equal(editor().value, submitting, "重置回到旧保存内容");
    // A clean source must leave without an unnecessary discard prompt.
    change(target().elements.engine, "sing-box"); await waitFor(() => target()?.elements.engine.value === "sing-box" && editor(), "保存并重置后仍阻止导航"); await settled();
  }
  const workspace = editor().closest(".a-card"), frame = editor().getBoundingClientRect(), panel = workspace.getBoundingClientRect();
  const inbound = root().querySelector('[aria-label="入站配置"]').getBoundingClientRect();
  const heading = workspace.querySelector("h2");
  const headingStyle = getComputedStyle(heading);
  const headingContentHeight = heading.clientHeight - parseFloat(headingStyle.paddingTop) - parseFloat(headingStyle.paddingBottom);
  assert.ok(headingContentHeight <= parseFloat(headingStyle.lineHeight) + 1, "源码标题被工具按钮挤压换行");
  assert.ok(inbound.bottom <= panel.top + 1, "入站工具不在源码之前");
  assert.ok(frame.width >= workspace.querySelector("form").clientWidth - 2, "源码被侧栏挤压");
  assert.ok(workspace.querySelector(".a-card-head").contains(button("重置修改")), "重置不在源码操作区");
  assert.ok(workspace.querySelector(".a-card-head").contains(button("格式化 JSON")), "格式化不在源码操作区");
  assert.ok(button("添加入站") && button("保存历史"), "缺少入站或历史入口");
  assert.equal(workspace.querySelectorAll('form > .a-actions button:not([type="submit"])').length, 0, "保存区混入源码工具");
  for (const control of [...target().querySelectorAll("select"), ...workspace.querySelectorAll(".a-card-head button")]) {
    const bounds = control.getBoundingClientRect(); assert.ok(bounds.width > 0 && bounds.left >= 0 && bounds.right <= innerWidth + 1, "选择器或源码工具溢出视口");
    assert.ok(bounds.height >= (matchMedia("(pointer: coarse)").matches ? 44 : control.tagName === "SELECT" ? 40 : 36), "配置操作触摸尺寸不足");
  }
  assert.ok(frame.left - panel.left >= 16 && panel.right - frame.right >= 16, "源码贴住卡片边界");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "实时配置横向溢出");
  if (new URLSearchParams(location.search).has("preview")) await new Promise(() => {});
}
