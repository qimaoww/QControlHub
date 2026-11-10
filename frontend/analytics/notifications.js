import { node, button } from './ui.js';

// A manual popover enters the browser top layer. Hosting it in the newest
// modal keeps its dismiss button usable while the rest of the page is inert.
export function createNotifications(scope) {
  const region=node('div',{className:'a-toast-region',popover:'manual',role:'region','aria-label':'通知'});
  const entries=new Map();
  function position() {
    const target=[...document.querySelectorAll('dialog.a-dialog[open]')].at(-1)||document.body;
    try { if(region.matches(':popover-open'))region.hidePopover(); } catch { /* Native fallback. */ }
    if(region.parentElement!==target)target.append(region);
    if(entries.size)try { region.showPopover(); } catch { /* Fixed placement remains available. */ }
  }
  function dismiss(card, animate = false) {
    const entry=entries.get(card);if(!entry)return;
    scope.clearTimer(entry.timer);entries.delete(card);if(animate)scope.motion?.notificationExit(card);card.remove();
    if(!entries.size)try{region.hidePopover();}catch{/* Native fallback. */}
  }
  function clear() { for(const card of [...entries.keys()])dismiss(card); }
  function show(message,tone='ok',onDetails) {
    if(!scope.current()||!message)return;
    if(tone==='ok')for(const card of [...entries.keys()])if(card.dataset.tone==='ok')dismiss(card);
    const previous=[...entries.keys()].find(card=>card.dataset.message===String(message)&&card.dataset.tone===tone);
    if(previous)dismiss(previous);
    while(entries.size>=2)dismiss(entries.keys().next().value);
    const card=node('div',{className:`a-notice a-toast ${tone}`,role:tone==='error'?'alert':'status','data-tone':tone,'data-message':String(message)},
      node('span',{className:'a-toast-message'},message));
    if(onDetails)card.querySelector('.a-toast-message').append(node('br'),button('查看详情',onDetails));
    card.append(button('',()=>dismiss(card,true),{glyph:'close',title:'关闭通知','aria-label':'关闭通知',className:'a-icon-button a-toast-close'}));
    const entry={timer:null};entries.set(card,entry);region.append(card);position();
    const arm=()=>{
      scope.clearTimer(entry.timer);
      if(!card.matches(':hover')&&!card.contains(document.activeElement))entry.timer=scope.timer(()=>dismiss(card,true),tone==='error'?9000:5000);
    };
    const pause=()=>scope.clearTimer(entry.timer);
    card.addEventListener('pointerenter',pause);card.addEventListener('pointerleave',arm);
    card.addEventListener('focusin',pause);card.addEventListener('focusout',()=>queueMicrotask(()=>{if(entries.has(card)&&scope.current())arm();}));
    arm();
    return card;
  }
  scope.own(()=>{clear();region.remove();});
  return {show,clear,position};
}
