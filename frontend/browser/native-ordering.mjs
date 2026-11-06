import { assert, delay, waitFor } from './assertions.mjs';
import { startNativeNodes } from './agent-detail-actions.mjs';
import { accountStorage } from '../modules/account-storage.js';
export const settled = async () => { await delay(20); await waitFor(() => !document.getAnimations().some(a=>a.id.startsWith('analytics-')), '排序动画未结束'); };
export const route = async (hash, selector) => {
  location.hash=hash;
  await waitFor(()=>document.querySelector('.a-nav-link[aria-current=page]')?.hash===hash && !document.querySelector('.a-main > [aria-busy=true]') && document.querySelector(selector), `页面未打开 ${hash}`);
  await settled();
  return document.querySelector(selector);
};
const key=(grip,key)=>grip.dispatchEvent(new KeyboardEvent('keydown',{key,bubbles:true,cancelable:true}));
const equal=(a,b,message)=>assert.equal(JSON.stringify(a),JSON.stringify(b),message);
const frames=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
export async function viewportScale(scale) {
  window.__cardMoveViewport = { action: "viewport", scale };
  await waitFor(() => window.__cardMoveViewport === null, "native viewport setup did not finish");
}

export async function gesture(start, points, release, inspect = async () => {}, waitForLanding = true) {
  const input = new AbortController();
  window.__cardMoveStep = -1;
  window.__cardMoveAck = -1;
  window.__cardMoveInput = { pressed: false, ended: false, cancelled: false, point: null };
  for (const type of ["pointerdown", "pointermove", "pointerup", "pointercancel"])
    document.addEventListener(type, event => {
      const delivered = window.__cardMoveInput;
      if (type === "pointerdown") delivered.pressed = true;
      if (type === "pointerup" || type === "pointercancel") delivered.ended = true;
      if (type === "pointercancel") delivered.cancelled = true;
      else delivered.point = { x: event.clientX, y: event.clientY };
    }, { capture: true, signal: input.signal });
  window.__cardMoveGesture = { start, points, release };
  try {
    for (let index = 0; index < points.length; index++) {
      await waitFor(() => window.__cardMoveStep === index, "native input step missing");
      await inspect(index, points[index]);
      window.__cardMoveAck = index;
    }
    await waitFor(() => window.__cardMoveGesture === null, "native input did not finish");
    if (waitForLanding) await settled();
  } finally {
    input.abort();
  }
}

export async function testNativeOrdering({ mode, testAPI }, preview=false, coordinates=false) {
  const mobile=mode.includes('mobile'), reduced=mode.endsWith('reduced');
  assert.equal(matchMedia('(prefers-reduced-motion:reduce)').matches,reduced);
  if(mobile){assert.equal(innerWidth,390);assert.ok(matchMedia('(pointer:coarse)').matches);}
  await startNativeNodes();
  const button=(text,root=document)=>[...root.querySelectorAll('button')].find(b=>b.textContent===text);
  const measurements=[];
  for(const type of ['nodes','nodes-list','traffic','traffic-cards']){
    const isNode = type.startsWith('nodes');
    const root=await route(isNode?'#node-settings':'#traffic','.a-main');
    if (isNode) {
      button(type === 'nodes-list' ? '列表' : '卡片', root).click();
      await waitFor(() => root.querySelector(type === 'nodes-list' ? '.a-probe-list' : '.a-probe-grid'), '节点视图未切换');
      await settled();
    } else {
      button(type==='traffic'?'列表':'卡片').click();
    }
    const grips=()=>[...root.querySelectorAll('[data-node-drag]')];
    const ids=()=>grips().map(g=>g.dataset.nodeDrag);
    const storageKey=`qcontrolhub:${isNode?'node':'traffic'}-card-order`;
    const row=grip=>grip.closest('article[data-agent-id],article[data-traffic-key],tr');
    const original=ids();assert.ok(original.length>=4,'排序夹具缺少多节点或端口');
    const grip=grips()[0], first=row(grip);
    assert.ok(grip.querySelector('svg') && !grip.textContent.trim(), '拖拽手柄必须只有图标');
    assert.ok(grip.parentElement.lastElementChild === grip, '手柄必须放在操作组最后');
    if(type.endsWith('list') || type === 'traffic') assert.equal(grip.closest('td'), first.lastElementChild, '列表手柄必须放在最后一列');
    assert.ok(![...root.querySelectorAll('button')].some(control=>['调整顺序','完成排序','按位置调整','排序','拖动排序'].includes(control.textContent.trim())), '页面仍有排序按钮');
    grip.focus();key(grip,'ArrowDown');key(grip,'ArrowDown');key(grip,'ArrowUp');
    const moved=[original[1],original[0],...original.slice(2)];
    await waitFor(()=>JSON.stringify(ids())===JSON.stringify(moved),`连续方向键未串行保存 ${type}: ${ids()}`);
    assert.equal(document.activeElement,grip,'键盘排序丢失焦点');
    key(grip,'ArrowUp');await waitFor(()=>JSON.stringify(ids())===JSON.stringify(original),'反向排序未恢复');
    await delay(25);
    const saved=accountStorage.getItem(storageKey);
    key(grip,' ');key(grip,'ArrowDown');key(grip,'ArrowDown');
    assert.equal(accountStorage.getItem(storageKey),saved,'键盘预览提前保存');
    key(grip,'Escape');await delay(25);equal(ids(),original,'取消键盘预览改变顺序');
    key(grip,' ');key(grip,'ArrowDown');key(grip,'Enter');
    await waitFor(()=>JSON.stringify(ids())===JSON.stringify(moved),'键盘确认未保存');
    key(grip,'ArrowUp');await waitFor(()=>JSON.stringify(ids())===JSON.stringify(original),'键盘恢复失败');
    await delay(25);
    const position=async()=>{
      await settled();
      grip.scrollIntoView({block:'center',behavior:'instant'});await frames();
      const destinationBefore = row(grips()[1]).getBoundingClientRect();
      if (destinationBefore.top > innerHeight - 180) {
        document.scrollingElement.scrollBy({ top: destinationBefore.top - (innerHeight - 180), behavior: 'instant' });
        await frames();
      }
      const source=grip.getBoundingClientRect(), destination=row(grips()[1]).getBoundingClientRect();
      const start={x:source.left+source.width/2,y:source.top+source.height/2};
      const end={x:Math.min(innerWidth-20,Math.max(20,destination.left+destination.width/2)),y:Math.max(destination.top+24,Math.min(destination.bottom-24,start.y))};
      assert.ok(start.y>0&&start.y<innerHeight&&end.y>0&&end.y<innerHeight,`手柄/目标不在视口 ${type} ${JSON.stringify({start,end})}`);
      return {start,end};
    };
    for(const release of coordinates?['drop','escape','drop','escape']:['drop','escape']){
      // Restore the first two slots before each independent native gesture.
      if(ids()[0]!==original[0]){key(grip,'ArrowUp');await waitFor(()=>ids()[0]===original[0],'手势前恢复失败');await delay(25);}
      const {start,end}=await position(), before=ids(), stored=accountStorage.getItem(storageKey);
      let polls=0;
      const path=isNode?'/agents':'/traffic-policies';
      await gesture(start,[{x:start.x+2,y:start.y},end,start,end],release,async(index,point)=>{
        assert.equal(row(grip),first,'拖动重建正在阅读的记录');
        const previewOrder = index === 1 || index === 3 ? [before[1], before[0], ...before.slice(2)] : before;
        equal(ids(),previewOrder,`拖动没有实时让位或返回原位置 ${JSON.stringify({index,point,start,end,scroll:scrollY,viewport:[innerWidth,innerHeight]})}`);
        if (index > 0) {
          const floating = document.querySelector('[data-drag-preview] > *');
          assert.ok(floating, '拖动时没有显示跟随的卡片或列表行');
          const translation = getComputedStyle(floating).translate.split(' ').map(Number.parseFloat);
          translation[1] ||= 0;
          assert.ok(Math.abs(translation[0] - (row(grip).tagName === 'TR' ? 0 : point.x - start.x)) < 1 && Math.abs(translation[1] - (point.y - start.y)) < 1, `拖动表面没有跟随指针 ${JSON.stringify({translation, point, start})}`);
        }
        assert.equal(accountStorage.getItem(storageKey),stored,'拖动预览提前写入偏好');
        assert.equal(document.querySelector('.card-move-ghost,.card-move-layer'),null,'新主题不得嵌入旧主题拖动图层');
        if(index===1){
          assert.equal(grips().find(item => item.dataset.nodeDrag === before[1]).getAttribute('aria-pressed'),'true','目标位置未标记');
          const reads=testAPI.calls.filter(c=>c.path===path).length;
          await waitFor(()=>testAPI.calls.filter(c=>c.path===path).length>reads,'拖动期间轮询未运行');polls++;
          equal(ids(),previewOrder,'轮询打断拖动预览');
        }
        measurements.push({type,release,index,point,target:grips().find(g=>g.getAttribute('aria-pressed')==='true')?.dataset.nodeDrag,polls});
      });
      const expected=release==='drop'?[before[1],before[0],...before.slice(2)]:before;
      await waitFor(()=>JSON.stringify(ids())===JSON.stringify(expected),'真实输入未提交目标位置或取消失败');
      assert.ok(grips().every(g=>g.getAttribute('aria-pressed')==='false'),'排序留下活动手柄');
      assert.equal(document.activeElement,grip,'手势结束未恢复同一手柄焦点');
    }
    if(ids()[0]!==original[0]){key(grip,'ArrowUp');await waitFor(()=>ids()[0]===original[0],'筛选前恢复失败');await delay(25);}
    if(isNode){
      document.querySelectorAll('.a-toast-close').forEach(button => button.click());
      await settled();
      testAPI.failNodeOrder=true;key(grip,'ArrowDown');
      await waitFor(()=>[...document.querySelectorAll('.a-toast.error')].some(item=>/fixture save failed|服务暂不可用/.test(item.textContent)),'保存失败未反馈');
      equal(ids(),original,'保存失败提交了本地虚假顺序');
      testAPI.failNodeOrder=false;key(grip,'ArrowDown');await waitFor(()=>ids()[1]===original[0],'失败后无法重试');
      await delay(25);key(grip,'ArrowUp');await waitFor(()=>ids()[0]===original[0],'重试后无法恢复');await delay(25);
      const sort=document.querySelector('[name=node_sort]');sort.value='name';sort.dispatchEvent(new Event('change',{bubbles:true}));
      assert.ok(grips().every(g=>g.disabled),'按名称排序仍允许覆盖保存顺序');
      sort.value='saved';sort.dispatchEvent(new Event('change',{bubbles:true}));
    } else {
      document.querySelectorAll('.a-toast-close').forEach(button => button.click());
      await settled();
      testAPI.failTrafficOrder = true;
      const beforeFailure = ids(); key(grip, 'ArrowDown');
      await waitFor(() => [...document.querySelectorAll('.a-toast.error')].some(item => /fixture save failed|服务暂不可用/.test(item.textContent)), '账号端口排序保存失败未反馈');
      equal(ids(), beforeFailure, '保存失败提交虚假端口顺序');
      testAPI.failTrafficOrder = false;
      const writeStorage = accountStorage.setItem;
      accountStorage.setItem = (name, value) => { if (name !== storageKey) writeStorage(name, value); };
      try {
        key(grip, 'ArrowDown'); await waitFor(() => ids()[1] === original[0], '浏览器存储失败阻止账号排序保存');
        assert.ok(testAPI.trafficOrder.ids.length > 0, '端口顺序未通过 API 保存');
        key(grip, 'ArrowUp'); await waitFor(() => ids()[0] === original[0], '账号端口顺序无法恢复');
      } finally { accountStorage.setItem = writeStorage; }
      await delay(25);
      // Discovered listeners retain independent sortable keys without a policy.
      assert.ok(grips().some(g=>!g.dataset.nodeDrag.startsWith('policy:')),'发现的监听端口无法排序');
      const search=document.querySelector('[name=search]');search.value='alpha';search.dispatchEvent(new Event('input',{bubbles:true}));
      const subset=ids(), full=JSON.parse(accountStorage.getItem(storageKey));
      const identities=grips().map(g=>{ const r=row(g),value=testAPI.trafficPolicies.find(p=>g.dataset.nodeDrag===`policy:${p.id}`); if(value)return `${value.agent_id}:${value.port}`; const parts=g.dataset.nodeDrag.split(':'); return `${parts[2]}:${parts[4]}`; });
      const hidden=full.filter(id=>!identities.includes(id));
      assert.ok(subset.length>1,'筛选夹具没有可排序子集');
      key(grips()[0],'ArrowDown');await waitFor(()=>ids()[1]===subset[0],'筛选后的端口不能排序');await delay(25);
      const latest=JSON.parse(accountStorage.getItem(storageKey));equal(latest.filter(id=>!identities.includes(id)),hidden,'筛选排序丢失隐藏端口');
      search.value='';search.dispatchEvent(new Event('input',{bubbles:true}));assert.equal(ids().length,original.length,'清除筛选丢失记录');
      const stored=accountStorage.getItem(storageKey);
      button('统计详情',root).click();const box=await waitFor(()=>document.querySelector('dialog[data-traffic-details][open]'),'详情未打开');
      const title=box.querySelector('h2'),rect=title.getBoundingClientRect();
      await gesture({x:rect.left+8,y:rect.top+8},[{x:rect.left+28,y:rect.top+8}],'drop',()=>assert.ok(box.open,'阅读详情启动了拖动'));
      assert.equal(accountStorage.getItem(storageKey),stored,'详情文本选择写入排序');box.querySelector('[aria-label=关闭]').click();
    }
    assert.ok(document.documentElement.scrollWidth<=innerWidth+1,'排序页面横向溢出');
    if(preview){document.documentElement.dataset.orderingPreview='ready';await new Promise(()=>{});}
  }
  // Navigation cancels in-progress native input without persisting it.
  const root=await route('#node-settings','.a-probe-toolbar');
  button('卡片').click();
  await waitFor(() => document.querySelector('.a-probe-grid'), '退出验证的卡片视图未打开');
  await settled();
  const grip=document.querySelector('.a-probe-grid [data-node-drag]');
  grip.scrollIntoView({block:'center',behavior:'instant'});await frames();
  const bounds=grip.getBoundingClientRect(),start={x:bounds.left+bounds.width/2,y:bounds.top+bounds.height/2};
  const before=accountStorage.getItem('qcontrolhub:node-card-order');
  const scroller=document.scrollingElement,scrollBefore=scroller.scrollTop;
  await gesture(start,[{x:start.x,y:innerHeight-12}],'escape',async()=>{
    await waitFor(()=>scroller.scrollTop>scrollBefore+20,'拖动到视口边缘没有自动滚动');
  });
  assert.equal(accountStorage.getItem('qcontrolhub:node-card-order'),before,'取消边缘滚动保存了排序');
  grip.scrollIntoView({block:'center',behavior:'instant'});await frames();
  const next=grip.getBoundingClientRect();start.x=next.left+next.width/2;start.y=next.top+next.height/2;
  await gesture(start,[{x:start.x+20,y:start.y-20}],'leave');
  assert.equal(accountStorage.getItem('qcontrolhub:node-card-order'),before,'离开页面保存了未完成拖动');
  assert.equal(document.querySelector('[data-node-drag][aria-pressed=true]'),null,'离开页面遗留拖动状态');
  window.cardCoordinateMeasurements={mobile,reduced,dpr:devicePixelRatio,viewport:[innerWidth,innerHeight],measurements};
  window.cardMovementResult=`native ${mobile?'touch':'mouse'}; ${measurements.length} samples; serial keys/preview/cancel/poll/failure/filter/listener/dialog/leave`;
}
