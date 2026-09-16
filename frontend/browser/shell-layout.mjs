import { assert, waitFor } from "./assertions.mjs";

export async function testShellLayoutRuntime({ mode }) {
  const mobile = mode.endsWith("-mobile");
  assert.ok(matchMedia(mobile ? "(pointer:coarse)" : "(pointer:fine)").matches,
    "layout checks must use the intended input device");
  assert.ok(innerWidth <= 820, "shell regression must run in a narrow viewport");
  const root = document.documentElement;
  const originalScale = root.style.getPropertyValue("--ui-font-scale");
  const originalTheme = root.dataset.theme;
  try {
    for (const [hash, selector] of [
      ["#node-settings", ".node-card-grid"],
      ["#settings-node-alpha", ".node-operations-workspace"],
      ["#client-access", ".client-profile-row"],
      ["#settings-engines", "#settings-form"],
      ["#archive-config", "#archive-form"],
    ]) {
      location.hash = hash;
      await waitFor(() => document.querySelector(selector), `missing layout route: ${hash}`);
      for (const [theme, scale] of [["light", "1"], ["dark", "1.35"]]) {
        root.dataset.theme = theme;
        root.style.setProperty("--ui-font-scale", scale);
        for (const animation of document.getAnimations()) {
          if (Number.isFinite(animation.effect?.getComputedTiming().endTime)) animation.finish();
        }
        assert.ok(root.scrollWidth <= innerWidth + 1, `${hash}/${scale}: page overflows`);
        const panels = document.querySelectorAll(
          ".desktop-app, .workspace-main, .node-card, .node-operations-workspace, .client-access-node-card, .settings-section, .config-inspector",
        );
        for (const panel of panels) {
          if (!panel.checkVisibility()) continue;
          assert.ok(panel.scrollWidth <= panel.clientWidth + 1,
            `${hash}/${scale}: ${panel.className} clips horizontal content`);
        }
      }
    }
  } finally {
    if (originalScale) root.style.setProperty("--ui-font-scale", originalScale);
    else root.style.removeProperty("--ui-font-scale");
    if (originalTheme) root.dataset.theme = originalTheme;
    else delete root.dataset.theme;
  }
}
