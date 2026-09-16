import { assert } from "./assertions.mjs";

export function testSubStoreCardLayout() {
  const grid = document.querySelector(".substore-agent-grid");
  const root = document.documentElement;
  const originalStyle = grid.getAttribute("style");
  const originalScale = root.style.getPropertyValue("--ui-font-scale");
  const originalTheme = root.dataset.theme;
  try {
    grid.style.gridTemplateColumns = "minmax(0,1fr)";
    for (const width of [320, 390, 470, 560, 740]) {
      grid.style.width = `min(100%, ${width}px)`;
      for (const [theme, scale] of [["light", "1"], ["dark", "1.35"]]) {
        root.dataset.theme = theme;
        root.style.setProperty("--ui-font-scale", scale);
        for (const animation of document.getAnimations()) {
          if (Number.isFinite(animation.effect?.getComputedTiming().endTime)) animation.finish();
        }
        for (const card of grid.querySelectorAll(".substore-agent-card")) {
          const label = `${width}/${scale}`;
          assert.ok(card.scrollWidth <= card.clientWidth + 1, `${label}: Sub-Store card clips content`);
          for (const row of card.querySelectorAll(".substore-node-row, .substore-node-settings-row")) {
            assert.ok(row.scrollWidth <= row.clientWidth + 1, `${label}: Sub-Store row overflows`);
            const bounds = row.getBoundingClientRect();
            for (const control of row.querySelectorAll("input:not([type=checkbox]), select, button")) {
              const box = control.getBoundingClientRect();
              assert.ok(box.left >= bounds.left - 1 && box.right <= bounds.right + 1,
                `${label}: Sub-Store control extends outside its row`);
              if (control.matches("input, select"))
                assert.ok(box.width >= 48, `${label}: Sub-Store input is too narrow`);
            }
          }
        }
      }
    }
  } finally {
    if (originalStyle === null) grid.removeAttribute("style");
    else grid.setAttribute("style", originalStyle);
    if (originalScale) root.style.setProperty("--ui-font-scale", originalScale);
    else root.style.removeProperty("--ui-font-scale");
    if (originalTheme) root.dataset.theme = originalTheme;
    else delete root.dataset.theme;
  }
}
