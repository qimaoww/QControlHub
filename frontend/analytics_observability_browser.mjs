import assert from "node:assert/strict";

// Runs against the production theme and real authenticated fixture APIs.
export async function checkAnalyticsObservability({ page, mode, evaluate, wait, screenshot, record, call }) {
  const visit = async (route, title) => {
    await evaluate(page, `location.hash=${JSON.stringify(route)}`);
    await wait(page, `document.querySelector('.a-top h1')?.textContent===${JSON.stringify(title)} && !document.querySelector('[aria-busy=true]')`);
  };
  const sourceDialog = async marker => {
    await evaluate(page, `document.querySelector('[data-source-status]').focus();document.querySelector('[data-source-status]').click()`);
    await wait(page, `!!document.querySelector('dialog[${marker}][open]')`);
    assert.ok(await evaluate(page, `(() => {
      const dialog=document.querySelector('dialog[${marker}]');
      return !dialog.querySelector('summary') && [...dialog.querySelectorAll('[data-source-expand]')].every(button =>
        button.tagName==='BUTTON' && button.textContent===(button.getAttribute('aria-expanded')==='true'?'收起':'展开'));
    })()`), "collection sources use explicit, accessible text controls without disclosure arrows");
    await evaluate(page, `(() => {
      window.__sourceDialog=document.querySelector('dialog[${marker}]');
      const toggle=window.__sourceDialog.querySelector('[data-source-expand]');
      if(toggle){if(toggle.getAttribute('aria-expanded')!=='true')toggle.click();toggle.focus();}
      window.__sourceFocus=document.activeElement;
      window.__obsRequests=0;
      [...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click();
    })()`);
    await wait(page, "window.__obsRequests>0 && window.__obsPending===0");
    assert.ok(await evaluate(page, `window.__sourceDialog===document.querySelector('dialog[${marker}]') && window.__sourceDialog.open && window.__sourceFocus===document.activeElement && (!window.__sourceDialog.querySelector('[data-source-expand]') || window.__sourceDialog.querySelector('[data-source-expand]').getAttribute('aria-expanded')==='true')`), "status readback retains dialog, keyboard focus and expanded node");
    assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1 && window.__sourceDialog.scrollWidth<=window.__sourceDialog.clientWidth+1"), "status dialog fits the viewport");
    assert.ok(await evaluate(page, "[...window.__sourceDialog.querySelectorAll('.a-table-scroll')].every(table=>table.scrollWidth<=table.clientWidth+1)"), "source status and observation times remain visible without horizontal scrolling");
    await evaluate(page, "window.__sourceDialog.querySelector('[aria-label=关闭]').click();delete window.__sourceDialog;delete window.__sourceFocus");
    assert.ok(await evaluate(page, "document.activeElement.matches('[data-source-status]')"), "closing status restores focus to its entry button");
  };
  await visit("#core-logs", "日志");
  await call('Page.bringToFront', {}, page);
  await wait(page, '!document.hidden');
  // Earlier workflows can persist a paused log view for this account.
  await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='自动更新')?.click()");
  await evaluate(page, `window.__obsFetch=window.fetch;window.__obsRequests=0;window.__obsPending=0;window.fetch=async (...args)=>{const tracked=['core-log','client-connections','preferences/node-order'].some(part=>String(args[0]).includes(part));if(tracked){window.__obsRequests++;window.__obsPending++;}try{return await window.__obsFetch(...args);}finally{if(tracked)window.__obsPending--;}}`);
  assert.ok(await evaluate(page, "['agent_id','instance_id','engine','level','q','limit'].every(name=>document.querySelector('.a-main [name='+name+']'))"));
  await evaluate(page, `(() => {
    const input=document.querySelector('.a-main [name=q]');input.value='observation stability';input.dispatchEvent(new Event('input',{bubbles:true}));input.focus();input.setSelectionRange(2,7);
    window.__logForm=input.form;window.__logInput=input;
    window.__logView=document.querySelector('.a-main [aria-label="内核日志"]');
    [...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click();
  })()`);
  await wait(page, "window.__obsRequests>0 && window.__obsPending===0 && document.querySelector('.a-main').textContent.includes('正在实时更新')");
  assert.ok(await evaluate(page, "window.__logInput===document.querySelector('.a-main [name=q]') && document.activeElement===window.__logInput && window.__logInput.selectionStart===2 && window.__logInput.selectionEnd===7 && window.__logForm===window.__logInput.form && window.__logView===document.querySelector('.a-main [aria-label=内核日志]')"), "log refresh retains query focus, text selection, form and scroll region");
  await evaluate(page, "[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='暂停更新').click()");
  assert.ok(await evaluate(page, "document.querySelector('.a-main').textContent.includes('自动更新已暂停')"));
  await sourceDialog("data-log-sources");
  await screenshot(page, "analytics-logs-paused");
  await visit("#client-connections", "客户端连接");
  assert.ok(await evaluate(page, "['agent_id','date','period','client_ip','engine','include_non_public'].every(name=>document.querySelector('.a-main [name='+name+']'))"));
  assert.ok(await evaluate(page, "document.querySelector('.a-main').textContent.includes('独立 IP') && document.querySelector('.a-main').textContent.includes('观测连接') && document.querySelector('.a-main').textContent.includes('不代表当前在线')"));
  await evaluate(page, `(() => {
    window.__obsRequests=0;const form=document.querySelector('.a-main form');window.__connectionForm=form;
    window.__connectionTable=document.querySelector('.a-main [aria-label="客户端来源 IP 明细"]');
    form.elements.client_ip.focus();
    [...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='刷新').click();
  })()`);
  await wait(page, "window.__obsRequests>0 && window.__obsPending===0 && document.querySelector('.a-main').textContent.includes('已读取')");
  assert.ok(await evaluate(page, "window.__connectionForm===document.querySelector('.a-main form') && document.activeElement===window.__connectionForm.elements.client_ip && window.__connectionTable===document.querySelector('.a-main [aria-label=\"客户端来源 IP 明细\"]')"));
  await evaluate(page, "window.__obsRequests=0;[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='本月').click()");
  await wait(page, "window.__obsRequests>0 && window.__obsPending===0 && document.querySelector('.a-main [name=period]').value==='month' && document.querySelector('.a-main').textContent.includes('已读取')");
  assert.ok(await evaluate(page, "document.documentElement.scrollWidth<=innerWidth+1"));
  await sourceDialog("data-connection-sources");
  await screenshot(page, "analytics-connections-month");
  await evaluate(page, "window.fetch=window.__obsFetch;delete window.__obsFetch;delete window.__obsRequests;delete window.__obsPending;delete window.__logForm;delete window.__logInput;delete window.__logView;delete window.__connectionForm;delete window.__connectionTable");
  record(`${mode}: analytics log scope/filter controls, pause and stable refresh focus/selection; connection date/month, statistics, stable form and scroll region passed`);
}
