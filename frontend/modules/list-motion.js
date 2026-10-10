import { animateMotion, cancelMotion, reducedMotion, retireSurface } from "./motion.js";
import { isolateMotionSurface } from "./motion-isolation.js";

const cards = ".node-card,.traffic-policy-card,.user-account-card,.service-card,.client-access-node-card,.access-control-card,.bbr-card,.substore-agent-card,.settings-version-card,.template-card";
export function captureListMotion(current, fresh, key) {
  if (!current.isConnected || reducedMotion() || !current.children?.length ||
      ![...current.children].some(child => child.matches(cards))) return null;
  const before = [...current.children], after = [...fresh.children];
  if (before.length === after.length && before.every((child, index) => key(child) === key(after[index]))) return null;
  // Editing telemetry or polling the same members performs no geometry reads.
  const rects = new Map(before.filter(child => child.matches(cards)).map(child => [child, child.getBoundingClientRect()]));
  const nextKeys = new Set(after.map(key));
  const departing = [...rects].filter(([child]) => key(child) && !nextKeys.has(key(child)))
    .filter(([, rect]) => rect.width && rect.height && rect.bottom > 0 && rect.top < innerHeight)
    .slice(0, 8).map(([child, rect]) => ({ clone: child.cloneNode(true), rect, opacity: getComputedStyle(child).opacity }));
  return { rects, departing };
}

export function finishListMotion(current, snapshot) {
  if (!snapshot) return;
  snapshot.departing.forEach(({ clone, rect, opacity }) => {
    const frame = document.createElement("div");
    frame.className = document.body.className;
    clone.style.cssText = `position:fixed;left:${rect.left}px;top:${rect.top}px;width:${rect.width}px;height:${rect.height}px;margin:0;opacity:${opacity};pointer-events:none`;
    frame.append(clone);
    const host = isolateMotionSurface(frame);
    if (host) retireSurface(host, { surface: clone });
  });
  requestAnimationFrame(() => {
    if (!current.isConnected || current.closest(".is-route-departing")) return;
    const survivors = [...snapshot.rects].filter(([child]) => child.isConnected && child.parentElement === current);
    // The previous landing affects getBoundingClientRect. Release all owners
    // before reading destination geometry so a reversal starts at captured paint.
    survivors.forEach(([child]) => cancelMotion(child));
    const moves = survivors.map(([child, from]) => ({ child, from, to: child.getBoundingClientRect() }));
    moves.forEach(({ child, from, to }) => {
      const dx = from.left - to.left, dy = from.top - to.top;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;
      animateMotion(child, [{ transform: `translate(${dx}px,${dy}px)` }, { transform: "none" }], {
        id: "qch-list", token: "--motion-reorder", retarget: false,
      });
    });
  });
}
