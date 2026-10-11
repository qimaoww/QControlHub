// Re-evaluate the full scroll chain each frame: responsive changes can switch
// between workspace scrolling, a nested panel and document scrolling mid-drag.
export function createCardMoveScroll(grid, update) {
  let frame = null, point = null, previous = null;
  const tick = time => {
    frame = null;
    if (!point || !grid.isConnected) return;
    const chain = [];
    for (let node = grid; node && node !== document.scrollingElement; node = node.parentElement) {
      if (/(auto|scroll)/.test(`${getComputedStyle(node).overflowY} ${getComputedStyle(node).overflowX}`)) chain.push(node);
    }
    chain.push(document.scrollingElement);
    const view = window.visualViewport;
    const dock = document.querySelector(".app-dock"), bar = document.querySelector(".workspace-topbar");
    const clip = { left: view?.offsetLeft || 0, top: view?.offsetTop || 0,
      right: (view?.offsetLeft || 0) + (view?.width || innerWidth), bottom: (view?.offsetTop || 0) + (view?.height || innerHeight) };
    if (dock && getComputedStyle(dock).position === "fixed" && dock.getBoundingClientRect().width > innerWidth / 2) clip.bottom = Math.min(clip.bottom, dock.getBoundingClientRect().top);
    if (bar && /sticky|fixed/.test(getComputedStyle(bar).position)) clip.top = Math.max(clip.top, bar.getBoundingClientRect().bottom);
    const elapsed = Math.min(32, previous == null ? 16 : time - previous);
    previous = time;
    for (const scroller of chain) {
      const box = scroller === document.scrollingElement ? clip : scroller.getBoundingClientRect();
      const left = Math.max(clip.left, box.left), right = Math.min(clip.right, box.right);
      const top = Math.max(clip.top, box.top), bottom = Math.min(clip.bottom, box.bottom);
      if (point.x < left - 24 || point.x > right + 24 || point.y < top - 56 || point.y > bottom + 56) continue;
      const speed = (value, low, high) => value < low + 56 ? -Math.min(1, (low + 56 - value) / 56)
        : value > high - 56 ? Math.min(1, (value - high + 56) / 56) : 0;
      const beforeX = scroller.scrollLeft, beforeY = scroller.scrollTop;
      const dx = scroller.scrollWidth > scroller.clientWidth ? speed(point.x, left, right) * elapsed * .75 : 0;
      const dy = scroller.scrollHeight > scroller.clientHeight ? speed(point.y, top, bottom) * elapsed * .75 : 0;
      // The page uses scroll-behavior:smooth. Repeated smooth scrollTop writes
      // restart that animation; drag scrolling already has its own frame clock.
      if (dx || dy) scroller.scrollTo({ left: beforeX + dx, top: beforeY + dy, behavior: "instant" });
      if (scroller.scrollLeft !== beforeX || scroller.scrollTop !== beforeY) break;
    }
    // Also catches wheel/ancestor scroll, sidebar reflow, resize and zoom while
    // the pointer is stationary; hit geometry is read after scrolling.
    update();
    if (point) frame = requestAnimationFrame(tick);
  };
  return {
    move(value) { point = value; if (frame == null) frame = requestAnimationFrame(tick); },
    stop() { if (frame != null) cancelAnimationFrame(frame); frame = null; point = previous = null; },
  };
}
