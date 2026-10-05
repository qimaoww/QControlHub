import { assert, waitFor } from "./assertions.mjs";

async function testDockNavigation(mobile) {
  const dock = await waitFor(() => document.querySelector(".app-dock"), "missing sidebar");
  const link = dock.querySelector('[href="#client-access"]');
  const previousMain = document.querySelector(".workspace-main");
  const menu = dock.querySelector(".mobile-account-menu");
  if (mobile) {
    menu.open = true;
  } else {
    const bounds = dock.getBoundingClientRect();
    for (const item of dock.querySelectorAll(".dock-nav a,.dock-tools a,.dock-tools button")) {
      const box = item.getBoundingClientRect();
      const icon = item.querySelector("svg").getBoundingClientRect();
      assert.ok(box.left > bounds.left && box.right < bounds.right,
        `${item.textContent}: collapsed highlight must fit inside the rail`);
      assert.ok(Math.abs((icon.left + icon.right - box.left - box.right) / 2) <= 1,
        `${item.textContent}: collapsed icon must remain centered`);
    }
    link.focus();
    await waitFor(() => dock.getBoundingClientRect().width >= 211, "keyboard focus did not expand the sidebar");
  }
  link.click();
  await waitFor(() => document.body.classList.contains("page-client-access")
    && document.querySelector(".client-profile-row"), "sidebar navigation did not load clients");
  assert.equal(document.querySelector(".app-dock"), dock, "navigation must keep the mounted sidebar");
  assert.equal(dock.querySelector('[href="#client-access"]'), link, "navigation must keep the clicked link");
  assert.equal(link.getAttribute("aria-current"), "page", "navigation must update the selected entry");
  assert.equal(previousMain.isConnected, false, "navigation must replace the previous workspace");
  if (mobile) {
    assert.equal(menu.open, false, "mobile navigation must close the More menu");
  } else {
    assert.equal(document.activeElement, link, "keyboard navigation must retain focus");
    assert.ok(dock.getBoundingClientRect().width >= 211, "navigation must keep the sidebar expanded");
    link.blur();
    await waitFor(() => dock.getBoundingClientRect().width <= 59, "sidebar did not collapse after focus left");
  }
}

export async function testShellLayoutRuntime({ mode }) {
  const mobile = mode.endsWith("-mobile");
  assert.ok(matchMedia(mobile ? "(pointer:coarse)" : "(pointer:fine)").matches,
    "layout checks must use the intended input device");
  assert.ok(innerWidth <= 820, "shell regression must run in a narrow viewport");
  const root = document.documentElement;
  const originalScale = root.style.getPropertyValue("--ui-font-scale");
  const originalTheme = root.dataset.theme;
  try {
    await testDockNavigation(mobile);
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
