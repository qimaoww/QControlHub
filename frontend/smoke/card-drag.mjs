import assert from "node:assert/strict";

import { animateNodeCardDrop, clearNodeCardDragState, nodeCardDropIndex } from "../modules/agents.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const cardSlots = [
  { left: 0, right: 100, top: 0, bottom: 160 },
  { left: 120, right: 220, top: 0, bottom: 160 },
  { left: 240, right: 340, top: 0, bottom: 160 },
  { left: 0, right: 100, top: 180, bottom: 340 },
  { left: 120, right: 220, top: 180, bottom: 340 },
];
const upperRightGripOffset = { x: 40, y: -60 };
const dropAt = (x, y) =>
  nodeCardDropIndex(cardSlots, { x, y }, upperRightGripOffset);

assert.equal(dropAt(90, 20), 0, "natural upper-half drop selects first slot");
assert.equal(dropAt(210, 20), 1, "natural upper-half drop selects middle slot");
assert.equal(dropAt(330, 20), 2, "natural upper-half drop selects last slot");
assert.equal(dropAt(90, 200), 3, "drop selection follows the actual grid row");
assert.equal(dropAt(210, 200), 4, "drop selection follows row direction");

const fakeCard = (left) => {
  const listeners = new Map();
  const classes = new Set();
  return {
    left,
    style: { transition: "", transform: "" },
    classList: {
      add: (...names) => names.forEach((name) => classes.add(name)),
      remove: (...names) => names.forEach((name) => classes.delete(name)),
      contains: (name) => classes.has(name),
    },
    forcedLayouts: 0,
    get offsetWidth() {
      this.forcedLayouts += 1;
      return 100;
    },
    getBoundingClientRect() {
      return { left: this.left, top: 0 };
    },
    addEventListener(type, listener) {
      listeners.set(type, listener);
    },
    removeEventListener(type, listener) {
      if (listeners.get(type) === listener) listeners.delete(type);
    },
    dispatchTransitionEnd() {
      listeners.get("transitionend")?.({
        target: this,
        propertyName: "transform",
      });
    },
    hasTransitionListener() {
      return listeners.has("transitionend");
    },
  };
};
const pending = new Map();
const animate = (card, frames, { onSettled }) => {
  assert.match(frames[0].transform, /translate/);
  let active = true;
  const settle = () => { if (active) { active = false; pending.delete(card); onSettled(); } };
  pending.set(card, settle);
  return settle;
};
const first = fakeCard(120), second = fakeCard(240);
let settlements = 0;
const cancel = animateNodeCardDrop([first, second], new Map([
  [first, { left: 20, top: 0 }], [second, { left: 120, top: 0 }],
]), { animate, onSettled: () => settlements++ });
assert.equal(first.style.transform, "", "native landing keeps inline geometry clean");
assert.equal(first.forcedLayouts, 0, "landing does not force per-card layout");
pending.get(first)();
assert.equal(settlements, 0, "all moving cards settle before releasing the gate");
cancel(); cancel();
assert.equal(settlements, 1, "interruption releases the gate exactly once");
assert.equal(pending.size, 0);
let immediate = 0;
animateNodeCardDrop([first], new Map(), { animate, onSettled: () => immediate++ });
assert.equal(immediate, 1, "unchanged layout releases immediately");
animateNodeCardDrop([first], new Map([[first, { left: 0, top: 0 }]]), {
  animate: (_card, _frames, { onSettled }) => { onSettled(); return () => {}; },
  onSettled: () => immediate++,
});
assert.equal(immediate, 2, "reduced motion can settle synchronously");

const releaseCard = fakeCard(0);
const releaseTarget = fakeCard(120);
releaseCard.classList.add("dragging");
releaseTarget.classList.add("drop-target");
releaseCard.style.order = "1";
releaseCard.style.transition = "none";
releaseCard.style.transform = "translate(-100px, 0px)";
let ghostRemoved = false;
const dragBody = fakeCard(0);
dragBody.classList.add("node-card-dragging");
const dragGrid = {
  querySelectorAll: () => [releaseCard, releaseTarget],
};
clearNodeCardDragState(
  dragGrid,
  { card: releaseCard, ghost: { remove: () => (ghostRemoved = true) } },
  { clearAnimationStyles: false, body: dragBody },
);
assert.equal(releaseCard.classList.contains("dragging"), false);
assert.equal(releaseTarget.classList.contains("drop-target"), false);
assert.equal(dragBody.classList.contains("node-card-dragging"), false);
assert.equal(ghostRemoved, true);
assert.equal(releaseCard.style.order, "");
assert.equal(releaseCard.style.transition, "none");
assert.equal(releaseCard.style.transform, "translate(-100px, 0px)");
clearNodeCardDragState(
  dragGrid,
  { card: releaseCard },
  { clearAnimationStyles: true, body: dragBody },
);
assert.equal(releaseCard.style.transition, "");
assert.equal(releaseCard.style.transform, "");

}
