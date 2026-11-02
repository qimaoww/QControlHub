import { assert } from "./assertions.mjs";

// A geometry probe, not a password-manager emulator. Injected fixed-position
// children must be able to use viewport coordinates inside either modal type.
export async function checkPopupAnchor(surface, field) {
  const probe = document.createElement("div");
  probe.style.cssText = "position:fixed;width:8px;height:8px;pointer-events:none";
  surface.append(probe);
  const check = () => {
    const anchor = field.getBoundingClientRect();
    probe.style.left = `${anchor.left}px`;
    probe.style.top = `${anchor.bottom}px`;
    const actual = probe.getBoundingClientRect();
    assert.ok(Math.abs(actual.left - anchor.left) < 1 && Math.abs(actual.top - anchor.bottom) < 1,
      `fixed popup anchor drift: expected ${anchor.left},${anchor.bottom}; got ${actual.left},${actual.top}`);
  };
  try {
    // Check every painted frame, including entrance and the settled state.
    const until = performance.now() + 420;
    do {
      await new Promise(requestAnimationFrame);
      check();
    } while (performance.now() < until);
    const scroller = field.closest(".traffic-edit-body") || surface;
    const previous = scroller.scrollTop;
    scroller.scrollTop = scroller.scrollHeight;
    check();
    scroller.scrollTop = previous;
    check();
  } finally { probe.remove(); }
}
