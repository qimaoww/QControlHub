import assert from "node:assert/strict";

export async function checkAnalyticsMotion({ page, base, mode, call, evaluate, wait, screenshot, record, api, identity }) {
  await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "no-preference" }] }, page);
  await evaluate(page, `(() => {
    window.__analyticsMotion=[];window.__nativeAnimate=Element.prototype.animate;
    Element.prototype.animate=function(...args) {
      const animation=window.__nativeAnimate.apply(this,args),element=this;
      const sample={id:'',paint:[]};window.__analyticsMotion.push(sample);
      function frame(){sample.id=animation.id;if(element.isConnected)sample.paint.push(Number(getComputedStyle(element).opacity));
        if(sample.paint.length<40 && ['running','pending','paused'].includes(animation.playState))requestAnimationFrame(frame)}
      animation.addEventListener('finish',()=>{if(element.isConnected)sample.paint.push(Number(getComputedStyle(element).opacity))},{once:true});
      requestAnimationFrame(frame);return animation;
    };
  })()`);
  const navigate = async route => {
    await evaluate(page, `location.hash=${JSON.stringify("#" + route)}`);
    await wait(page, `!document.querySelector('[aria-busy=true]') && location.hash===${JSON.stringify("#" + route)}`);
  };
  const seeded = [];
  try {
    for (let index=0; index<13; index++) seeded.push(await api(page,identity,"POST","/configs", {name:`动画列表验证 ${index}`,engine:"mihomo",content:"mixed-port: 7890\nmode: rule\n"},201));
    await evaluate(page, "[...document.querySelectorAll('.a-top button')].find(item=>item.textContent.includes('刷新')).click()");
    await wait(page, "!document.querySelector('[aria-busy=true]') && !!document.querySelector('.a-pagination')");
    await evaluate(page, "[...document.querySelectorAll('.a-pagination button')].find(item=>item.textContent==='下一页').click()");
    await wait(page, "document.getAnimations().some(a=>a.id==='analytics-list')");
    assert.ok(await evaluate(page,"document.querySelector('.a-pagination').textContent.includes('第 2 / 2 页')"));
  } finally {
    for (const config of seeded) await api(page,identity,"DELETE",`/configs/${config.id}`,undefined,204);
  }
  await navigate("archive-config");
  await evaluate(page, "[...document.querySelectorAll('.a-top button')].find(item=>item.textContent.includes('刷新')).click()");
  await wait(page, "!document.querySelector('[aria-busy=true]')");
  const mainWidth = await evaluate(page, "document.querySelector('.a-main').getBoundingClientRect().width");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='新建配置').click()");
  await wait(page, "document.getAnimations().some(a=>a.id==='analytics-dialog')");
  assert.ok(await evaluate(page, "!!document.activeElement.closest('dialog[open]')"));
  assert.equal(await evaluate(page, "document.documentElement.clientWidth"), await evaluate(page, "innerWidth"), "modal backdrop covers the viewport including the reserved scrollbar gutter");
  assert.ok(Math.abs(await evaluate(page, "document.querySelector('.a-main').getBoundingClientRect().width") - mainWidth) < 1, "opening a modal preserves page width");
  // Escape commits closure/focus immediately, before its inert visual tail.
  await call("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, page);
  await call("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, page);
  await wait(page, "!document.querySelector('dialog[open]')");
  assert.ok(await evaluate(page, `(() => {const tail=document.querySelector('.a-modal-tail');return !tail || getComputedStyle(tail).opacity==='1' && getComputedStyle(tail.firstElementChild).backgroundColor==='rgb(255, 255, 255)'})()`), "closing forms retain an opaque surface rather than blending live table text through them");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='新建配置').click()");
  await wait(page, "!!document.querySelector('dialog[open]')");
  assert.equal(await evaluate(page, "document.querySelectorAll('.a-motion-tail').length"), 0, "reopening clears old paint");
  await wait(page, "document.getAnimations().some(a=>a.id==='analytics-dialog')");
  await wait(page, "!document.getAnimations().some(a=>a.id.startsWith('analytics-'))");
  assert.ok(await evaluate(page, "window.__analyticsMotion.some(a=>a.id==='analytics-dialog' && a.paint.some(value=>value<.98) && a.paint.some(value=>value>.98))"), "actual modal playback traverses intermediate and final paint");
  await evaluate(page, `(() => {
    document.querySelector('dialog[open]').close();
    const animations=document.getAnimations().filter(a=>a.id.startsWith('analytics-'));
    animations.forEach(a=>{a.pause();a.currentTime=55});
    const host=document.querySelector('.a-modal-tail'),surface=host?.firstElementChild;
    if(!surface || getComputedStyle(surface).opacity!=='1' || getComputedStyle(surface).backgroundColor!=='rgb(255, 255, 255)')throw Error('editor text blends through the closing surface');
    animations.forEach(a=>a.play());
  })()`);
  await wait(page, "!document.querySelector('dialog[open],.a-motion-tail')");
  assert.ok(Math.abs(await evaluate(page, "document.querySelector('.a-main').getBoundingClientRect().width") - mainWidth) < 1, "closing a modal restores page width");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='新建配置').click()");
  await wait(page, "!!document.querySelector('dialog[open] form')");
  await evaluate(page, "document.querySelector('dialog[open] form').requestSubmit()");
  await wait(page, "!!document.querySelector('.a-validation')");
  assert.ok(await evaluate(page, "document.activeElement.matches('[aria-invalid=true]')"));
  await evaluate(page, "(() => {const input=document.querySelector('dialog[open] [name=name]');input.value='动画验证';input.dispatchEvent(new Event('input',{bubbles:true}))})()");
  await screenshot(page, "analytics-motion-dialog");
  await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await wait(page, "[...document.querySelectorAll('dialog[open] button')].some(item=>item.textContent==='放弃更改')");
  await evaluate(page, "[...document.querySelectorAll('dialog[open] button')].find(item=>item.textContent==='放弃更改').click()");
  await wait(page, "!document.querySelector('dialog[open],.a-motion-tail')");
  await navigate("certificates");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='申请证书').click()");
  await wait(page, "!!document.querySelector('dialog[open] [name=method]')");
  await wait(page, "!document.getAnimations().some(a=>a.id.startsWith('analytics-'))");
  await evaluate(page, `(() => {const control=document.querySelector('dialog[open] [name=method]');
    control.value='http-01';control.dispatchEvent(new Event('change',{bubbles:true}));
    if(!document.querySelector('[name=dns_account_id]').closest('.a-field').hidden)throw Error('visibility waited for animation');
    control.value='dns-api';control.dispatchEvent(new Event('change',{bubbles:true}));
    if(document.querySelector('[name=dns_account_id]').closest('.a-field').hidden)throw Error('reversal lost final field state');
  })()`);
  await wait(page, "!document.querySelector('.a-motion-tail') && !document.getAnimations().some(a=>a.id.startsWith('analytics-'))");
  assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"));
  await screenshot(page, "analytics-motion-fields");
  await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').focus()");
  await wait(page, "!!document.querySelector('.a-tooltip')");
  await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').blur()");
  await wait(page, "!document.querySelector('.a-tooltip')");
  await navigate("tasks");
  await wait(page, "!document.getAnimations().some(a=>a.id.startsWith('analytics-'))");
  if (mode === "mobile") {
    await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').click()");
    await wait(page, "document.getAnimations().some(a=>a.id==='analytics-menu')");
    await evaluate(page, "document.querySelector('.a-drawer [title=关闭导航]').click();document.querySelector('.a-top [aria-label=切换导航]').click()");
    await wait(page, "!!document.querySelector('.a-drawer[open]') && !document.querySelector('.a-motion-tail')");
    assert.equal(await evaluate(page, "document.querySelector('.a-top [aria-label=切换导航]').getAttribute('aria-expanded')"), "true", "a retired drawer cannot reset its replacement's state");
    await wait(page, "!document.getAnimations().some(a=>a.id==='analytics-menu')");
    await screenshot(page, "analytics-motion-navigation");
    await evaluate(page, "document.querySelector('.a-drawer [title=关闭导航]').click()");
    await wait(page, "!document.querySelector('dialog[open],.a-motion-tail')");
    assert.ok(await evaluate(page, "document.activeElement.matches('.a-top [aria-label=切换导航]')"), "drawer close restores navigation focus even after interruption");
  }
  const count = await evaluate(page, "window.__analyticsMotion.filter(a=>a.id==='analytics-page').length");
  await evaluate(page, "[...document.querySelectorAll('.a-top button')].find(item=>item.textContent.includes('刷新')).click()");
  await wait(page, "!document.querySelector('[aria-busy=true]')");
  assert.equal(await evaluate(page, "window.__analyticsMotion.filter(a=>a.id==='analytics-page').length"), count, "refresh does not replay page entrance");
  await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] }, page);
  await wait(page, "document.documentElement.dataset.themeMotion==='off'");
  await navigate("archive-config");
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='新建配置').click()");
  assert.ok(await evaluate(page, "!!document.activeElement.closest('dialog[open]')"));
  assert.equal(await evaluate(page, "document.getAnimations().filter(a=>a.id.startsWith('analytics-')).length"), 0);
  await screenshot(page, "analytics-motion-reduced");
  await evaluate(page, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await evaluate(page, "Element.prototype.animate=window.__nativeAnimate");
  await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "no-preference" }] }, page);
  // Exercise theme disposal in the actual page with a live modal and queued
  // presentation work. Business runtime tests resume in a fresh document.
  await evaluate(page, "[...document.querySelectorAll('button')].find(item=>item.textContent==='新建配置').click()");
  await wait(page, "document.getAnimations().some(a=>a.id==='analytics-dialog')");
  await evaluate(page, `(async () => {
    const lifecycle=await import('/assets/modules/theme-lifecycle.js');const old=lifecycle.currentTheme('analytics');
    old.frame(()=>{document.body.dataset.staleMotion='frame'});old.timer(()=>{document.body.dataset.staleMotion='timer'},40);
    const replacement=lifecycle.mountTheme('classic',()=>({}));
    if(old.current())throw Error('old theme retained ownership');
    if(document.querySelector('.a-motion-tail,.a-tooltip'))throw Error('old temporary paint survived');
    if(document.getAnimations().some(a=>a.id.startsWith('analytics-')))throw Error('old animation survived');
    if(!document.querySelector('link[href*=\\"analytics/theme.css\\"]').disabled)throw Error('old stylesheet remained active');
    await new Promise(resolve=>setTimeout(resolve,80));
    if(document.body.dataset.staleMotion)throw Error('old queued callback ran');
    replacement.dispose();
  })()`);
  await call("Page.navigate", { url: `${base}/assets/testdata/analytics-runtime.html#archive-config` }, page);
  await wait(page, "document.querySelector('.a-top h1')?.textContent==='配置档案' && !document.querySelector('[aria-busy=true]')");
  record(`${mode}: actual analytics modal playback, interrupted close/reopen, field reversal, stable refresh, reduced motion, queued callback cancellation and stylesheet disposal passed`);
}
