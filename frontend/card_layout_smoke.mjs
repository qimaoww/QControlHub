import assert from "node:assert/strict";
import { createCardMasonry } from "./modules/card-masonry.js";

const names = ["document", "getComputedStyle", "requestAnimationFrame", "ResizeObserver"];
const originals = new Map(names.map(name => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
try {
  const card = { style: {}, getBoundingClientRect: () => ({ height: 119 }) };
  const grid = { querySelectorAll: () => [card] };
  let rowSize = "auto", observations = 0, disconnections = 0;
  Object.assign(globalThis, {
    document: { querySelector: () => grid },
    getComputedStyle: () => ({ gridAutoRows: rowSize, rowGap: "12px" }),
    requestAnimationFrame: callback => callback(),
    ResizeObserver: class {
      observe() { observations++; }
      disconnect() { disconnections++; }
    },
  });
  const layout = createCardMasonry(".grid", ".card");
  layout.bind();
  assert.equal(card.style.gridRowEnd, undefined, "auto rows must not receive synthetic masonry spans");
  assert.equal(observations, 0, "normal card grids must not observe and remeasure their own layout");
  layout.disconnect();

  rowSize = "1px";
  layout.bind();
  assert.equal(card.style.gridRowEnd, "span 11", "explicit masonry rows still cover the full card height");
  assert.equal(observations, 1);
  layout.disconnect();
  assert.equal(disconnections, 1, "route cleanup disconnects its observer");
} finally {
  for (const [name, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, name, descriptor);
    else delete globalThis[name];
  }
}
console.log("CSS card rows and optional masonry lifecycle passed");
