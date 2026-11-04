import { checkAnalyticsMotion } from "./analytics_motion_browser.mjs";
import { checkAnalyticsWorkflows } from "./analytics_workflows_browser.mjs";
import assert from "node:assert/strict";

export async function runAnalyticsBrowser({ base, mode, fixture, createPage, login, call, evaluate, wait, screenshot, api, record }) {
  const anonymous = await createPage();
  await call("Page.navigate", { url: `${base}/assets/testdata/analytics-runtime.html#tasks` }, anonymous);
  await wait(anonymous, "!!document.querySelector('.a-login form')");
  assert.equal(await evaluate(anonymous, "document.querySelectorAll('.login-shell,link[href*=\"app.css\"]').length"), 0);
  await screenshot(anonymous, "analytics-login");
  await evaluate(anonymous, `(() => { const form=document.querySelector('.a-login form');form.elements.username.value='analytics-admin';form.elements.token.value='wrong-preview-password';form.requestSubmit(); })()`);
  await wait(anonymous, "!!document.querySelector('.a-login [role=alert]')?.textContent && !document.querySelector('[aria-busy=true]')");
  assert.equal(await evaluate(anonymous, "document.querySelector('.a-login [name=username]').value"), "analytics-admin");
  await evaluate(anonymous, `(() => { const form=document.querySelector('.a-login form');form.elements.token.value='config scope password fixture';form.requestSubmit(); })()`);
  await wait(anonymous, "!!document.querySelector('#logout') && location.hash==='#tasks'");
  assert.equal(await evaluate(anonymous, "document.querySelectorAll('.a-layout,.a-login').length"), 0, "authenticated classic preference overrides public analytics login hint");
  const page = await createPage();
  const capture = screenshot;
  screenshot = async (target, label) => {
    await wait(target, "!document.getAnimations().some(a=>a.id.startsWith('analytics-'))");
    await capture(target, label);
  };
  const identity = await login(page, "analytics-admin");
  await call("Page.navigate", { url: `${base}/assets/testdata/analytics-runtime.html#dashboard` }, page);
  await wait(page, "document.querySelector('.a-top h1')?.textContent==='仪表盘' && !document.querySelector('[aria-busy=true]')");
  assert.equal(await evaluate(page, `document.querySelectorAll('link[href*="app.css"],.workspace-main,.app-dock').length`), 0);
  assert.equal(await evaluate(page, "document.querySelectorAll('.a-stat').length"), 4);
  assert.ok(await evaluate(page, "document.querySelector('.a-chart')?.getAttribute('aria-label').includes('流量')"));
  await evaluate(page, "document.fonts.ready.then(()=>true)");
  assert.ok(await evaluate(page, `document.fonts.check('500 14px "Analytics Sans"')`));
  await call("DOM.enable", {}, page);
  await call("CSS.enable", {}, page);
  const { root } = await call("DOM.getDocument", {}, page);
  const { nodeId } = await call("DOM.querySelector", { nodeId: root.nodeId, selector: ".a-top h1" }, page);
  const { fonts } = await call("CSS.getPlatformFontsForNode", { nodeId }, page);
  assert.ok(fonts.length && fonts.every(font => font.isCustomFont), "Chinese heading uses the bundled font");
  await screenshot(page, "analytics-dashboard");
  if (mode === "desktop") {
    await call("Emulation.setDeviceMetricsOverride", { width:1440,height:720,deviceScaleFactor:1,mobile:false }, page);
    await evaluate(page, `(() => {
      window.__stableRail=document.querySelector('.a-rail');window.__stableTop=document.querySelector('.a-top');
      const nav=window.__stableRail.querySelector('nav'),link=nav.querySelector('[href="#tasks"]');
      link.focus();nav.scrollTop=100;link.click();window.__railScroll=nav.scrollTop;
    })()`);
    await wait(page, "document.querySelector('.a-top h1')?.textContent==='任务' && !document.querySelector('[aria-busy=true]')");
    assert.ok(await evaluate(page, `window.__stableRail===document.querySelector('.a-rail') && window.__stableTop===document.querySelector('.a-top') && Math.abs(document.querySelector('.a-rail nav').scrollTop-window.__railScroll)<1 && document.activeElement.matches('[href="#tasks"]')`), "route changes preserve rail identity, scroll and keyboard focus");
    await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').click();location.hash='#archive-config'");
    await wait(page, "document.querySelector('.a-top h1')?.textContent==='配置档案' && !document.querySelector('[aria-busy=true]')");
    assert.ok(await evaluate(page, "document.querySelector('.a-layout').classList.contains('a-collapsed')"));
    await evaluate(page, "[...document.querySelectorAll('.a-top button')].find(item=>item.textContent.includes('刷新')).click()");
    await wait(page, "!document.querySelector('[aria-busy=true]')");
    assert.ok(await evaluate(page, "window.__stableTop===document.querySelector('.a-top') && document.querySelector('.a-layout').classList.contains('a-collapsed')"));
    await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').click();location.hash='#dashboard'");
    await wait(page, "document.querySelector('.a-top h1')?.textContent==='仪表盘' && !document.querySelector('[aria-busy=true]')");
  }
  const widths = mode === "mobile" ? [[390, 844], [844, 390], [768, 1024]] : [[1920, 1080], [3840, 2160], [1280, 900], [960, 800]];
  for (const [width, height] of widths) {
    await call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: mode === "mobile" }, page);
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"), `analytics ${width}x${height}: no page overflow`);
    if (width === 1920 || width === 3840 || width === 844) await screenshot(page, `analytics-dashboard-${width}x${height}`);
  }
  await call("Emulation.setDeviceMetricsOverride", { width: mode === "mobile" ? 390 : 1440, height: mode === "mobile" ? 844 : 1000, deviceScaleFactor: 1, mobile: mode === "mobile" }, page);
  for (const [route, title] of [["settings", "面板设置"], ["users", "用户管理"], ["traffic", "流量"], ["ip-quality", "IP 质量"], ["certificates", "证书"], ["node-settings", "节点"], ["live-config", "实时配置"], ["access-control", "访问控制"], ["system-bbr", "TCP 调优"], ["substore-sync", "订阅同步"], ["tasks", "任务"], ["core-logs", "日志"], ["client-connections", "客户端连接"], ["client-access", "客户端接入"], ["my-quota", "共享与额度"], ["themes", "主题"], ["archive-config", "配置档案"]]) {
    await evaluate(page, `location.hash=${JSON.stringify("#" + route)}`);
    await wait(page, `document.querySelector('.a-top h1')?.textContent===${JSON.stringify(title)} && !document.querySelector('[aria-busy=true]')`);
    assert.equal(await evaluate(page, "[...document.querySelectorAll('.a-main [role=alert]')].filter(item=>item.textContent.trim()).length"), 0, `${route}: real API did not fail`);
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"), `${route}: no page overflow`);
    assert.equal(await evaluate(page, "document.querySelectorAll('.a-main > .a-heading, .a-main h1').length"), 0, `${route}: page title appears only in the top bar`);
    if (route === "core-logs" && mode === "desktop") {
      assert.ok(await evaluate(page, `(() => {
        const controls=[...document.querySelectorAll('.a-filter-form .a-field input,.a-filter-form .a-field select,.a-filter-form button[type=submit]')].map(el=>el.getBoundingClientRect());
        return controls.length===5 && Math.max(...controls.map(rect=>rect.top))-Math.min(...controls.map(rect=>rect.top))<2;
      })()`), "log filters and submit share one compact desktop row");
    }
    await screenshot(page, `analytics-${route}`);
    if (route === "settings") {
      const formWidths = mode === "mobile" ? [[390, 844], [768, 1024]] : [[1440, 1000], [1280, 900], [960, 800], [3840, 2160]];
      for (const [width, height] of formWidths) {
        await call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: mode === "mobile" }, page);
        await evaluate(page, "new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))");
        assert.ok(await evaluate(page, `(() => {
          const card=document.querySelector('.a-card');
          const fields=[...card.querySelectorAll('.a-field')];
          const controls=fields.map(field=>field.querySelector('input,select').getBoundingClientRect());
          const labels=fields.map(field=>field.querySelector('.a-field-label').getBoundingClientRect());
          const rows=new Map();
          controls.forEach((rect,index)=>{const y=Math.round(labels[index].top); const row=rows.get(y)||[];row.push(rect.top);rows.set(y,row)});
          return [...rows.values()].every(row=>Math.max(...row)-Math.min(...row)<1)
            && Math.abs(fields[0].getBoundingClientRect().left-card.querySelector('h2').getBoundingClientRect().left)<1
            && Math.abs(document.querySelector('.a-top-inner').getBoundingClientRect().left-document.querySelector('.a-main').getBoundingClientRect().left)<1
            && document.documentElement.scrollWidth<=innerWidth+1;
        })()`), `${width}x${height}: aligned form controls, card gutter and centered header`);
        if (width === 3840 || width === 1280 || width === 768) await screenshot(page, `analytics-settings-${width}x${height}`);
      }
      await call("Emulation.setDeviceMetricsOverride", { width: mode === "mobile" ? 390 : 1440, height: mode === "mobile" ? 844 : 1000, deviceScaleFactor: 1, mobile: mode === "mobile" }, page);
      const chooseScale = async value => {
        await evaluate(page, `(() => {
          const control=document.querySelector('[name=ui_font_scale]');
          control.value=String(${value});
          control.dispatchEvent(new Event('input',{bubbles:true}));
          control.form.requestSubmit();
        })()`);
        await wait(page, `getComputedStyle(document.documentElement).fontSize==="${16 * value / 100}px" && !document.querySelector('[aria-busy=true]')`);
        assert.equal((await api(page, identity, "GET", "/settings")).ui_font_scale, value);
      };
      await chooseScale(110);
      await call("Page.navigate", { url: `${base}/assets/testdata/analytics-runtime.html#settings` }, page);
      await wait(page, "document.querySelector('.a-top h1')?.textContent==='面板设置' && !document.querySelector('[aria-busy=true]') && getComputedStyle(document.documentElement).fontSize==='17.6px'");
      await evaluate(page, "document.fonts.ready.then(()=>true)");
      await screenshot(page, "analytics-settings-large-font");
      await chooseScale(100);
    }
  }
  await checkAnalyticsWorkflows({ page, mode, fixture, evaluate, wait, screenshot, api, identity, record });
  await checkAnalyticsMotion({ page, base, mode, call, evaluate, wait, screenshot, record, api, identity });
  await evaluate(page, "window.__animationBeforeFailure=Element.prototype.animate;Element.prototype.animate=function(){throw new Error('injected presentation failure')}");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='新建配置').click()");
  await wait(page, "!!document.querySelector('dialog[open] form')");
  await evaluate(page, `(() => { const form=document.querySelector('dialog[open] form');form.elements.name.value='独立主题验证配置';form.elements.content.value='mixed-port: 7890\\nmode: rule\\n';form.elements.content.dispatchEvent(new Event('input',{bubbles:true}));form.requestSubmit();form.requestSubmit(); })()`);
  await wait(page, "document.querySelector('dialog[open]')?.textContent.includes('已保存') && !document.querySelector('[aria-busy=true]')");
  const saved = (await api(page, identity, "GET", "/configs")).find(config => config.name === "独立主题验证配置");
  assert.ok(saved?.id);
  assert.equal(saved.version, 1);
  await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await wait(page, "!document.querySelector('dialog[open]')");
  await evaluate(page, "[...document.querySelectorAll('tbody tr')].find(row=>row.textContent.includes('独立主题验证配置')).querySelectorAll('button')[1].click()");
  await wait(page, "document.querySelector('dialog[open]')?.textContent.includes('保存历史')");
  await evaluate(page, "[...document.querySelectorAll('dialog[open] button')].find(item=>item.textContent==='预览').click()");
  await wait(page, "document.querySelector('dialog[open] pre')?.textContent.includes('mixed-port')");
  assert.ok(await evaluate(page, "!document.querySelector('dialog[open]').textContent.includes('版本 1')"));
  await screenshot(page, "analytics-archive-history");
  // Native modal focus must stay within the dialog while navigating by keyboard.
  await call("Input.dispatchKeyEvent", { type: "keyDown", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 }, page);
  await call("Input.dispatchKeyEvent", { type: "keyUp", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 }, page);
  assert.ok(await evaluate(page, "!!document.activeElement.closest('dialog[open]')"));
  await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await api(page, identity, "DELETE", `/configs/${saved.id}`, undefined, 204);
  await evaluate(page, "Element.prototype.animate=window.__animationBeforeFailure");
  if (mode === "mobile") {
    await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').click()");
    await wait(page, "document.getAnimations().some(a=>a.id==='analytics-menu')");
    await wait(page, "!!document.querySelector('dialog.a-drawer[open]')");
    assert.ok(await evaluate(page, `document.querySelector('dialog.a-drawer a[href="#themes"]').getBoundingClientRect().height>=44`));
    await screenshot(page, "analytics-mobile-navigation");
    await call("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, page);
    await call("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, page);
    await wait(page, "!document.querySelector('dialog.a-drawer[open]')");
  }
  await evaluate(page, "location.hash='#themes'");
  await wait(page, "[...document.querySelectorAll('button')].some(item=>item.dataset.themeID==='classic') && !document.querySelector('[aria-busy=true]')");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.dataset.themeID==='classic').click()");
  // The test entry replaced its URL with the production route, so the
  // confirmed save reloads through the real gateway.
  await wait(page, "!!document.querySelector('[data-select-theme=classic]')");
  assert.equal(await evaluate(page, `document.querySelectorAll('link[href*="analytics/theme.css"],.a-layout').length`), 0);
  assert.equal((await api(page, identity, "GET", "/preferences/theme")).theme_id, "classic");
  await screenshot(page, "analytics-switched-classic");
  await evaluate(page, `(async () => {
    const lifecycle=await import('/assets/modules/theme-lifecycle.js');
    const classic=await import('/assets/modules/motion.js');
    const {createAnalyticsMotion}=await import('/assets/analytics/motion.js');
    const old=lifecycle.currentTheme('classic');
    classic.enterSurface(document.querySelector('.theme-picker-card'));
    old.frame(()=>{document.body.dataset.staleClassic='frame'});
    old.timer(()=>{document.body.dataset.staleClassic='timer'},40);
    const replacement=lifecycle.mountTheme('analytics',createAnalyticsMotion);
    if(old.current() || document.getAnimations().length)throw Error('classic animation retained ownership');
    if(!document.querySelector('link[href*=\\"app.css\\"]').disabled)throw Error('classic stylesheet remained active');
    await new Promise(resolve=>setTimeout(resolve,80));
    if(document.body.dataset.staleClassic || document.querySelector('.interaction-tooltip,[data-motion-isolation]'))throw Error('classic presentation survived disposal');
    replacement.dispose();
  })()`);
  record(`${mode}: independent analytics runtime against real API; eighteen routes, archive save/history, native modal keyboard focus, responsive viewports, document/CSS isolation; registry remains unavailable until full parity`);
}
