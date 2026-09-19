import { assert, pause, waitFor, createConfigFixture } from "./config-fixture.mjs";

// Exercise the real catalog at a constrained width, including options that are
// normally collapsed. A public-key hint must not stretch its adjacent actions.
export async function testPresetFieldLayoutRuntime() {
  const entries = await (await fetch("/assets/preset-plans.json")).json();
  const root = document.documentElement;
  const originalScale = root.style.getPropertyValue("--ui-font-scale");
  const originalTheme = root.dataset.theme;
  try {
    for (const entry of entries) {
      const fixture = await createConfigFixture(entry.engine, { protocol: entry.protocol.key });
      try {
        fixture.select("second");
        fixture.click("modify");
        await waitFor(() => document.querySelector("#server-plan-form"), "preset form did not open");
        const form = document.querySelector("#server-plan-form");
        const dialog = form.closest("dialog");
        dialog.style.width = "min(780px, calc(100vw - 16px))";
        for (const tab of form.querySelectorAll("[data-builder-step]")) {
          tab.click();
          await pause();
          form.querySelectorAll(".builder-section:not([hidden]) .preset-option-panel")
            .forEach(panel => { panel.open = true; });
          for (const [theme, scale] of [["light", "1"], ["dark", "1.35"]]) {
            root.dataset.theme = theme;
            root.style.setProperty("--ui-font-scale", scale);
            const context = `${entry.engine}/${entry.protocol.key}/${tab.dataset.builderStep}/${scale}`;
            assert(dialog.scrollWidth <= dialog.clientWidth + 1, `${context}: dialog overflows`);
            if (entry.protocol.key === "mieru") {
              assert(document.documentElement.scrollWidth <= innerWidth, `${context}: page overflows`);
              const dialogBounds = dialog.getBoundingClientRect();
              assert(dialogBounds.left >= -1 && dialogBounds.right <= innerWidth + 1, `${context}: dialog escapes viewport`);
              for (const element of form.querySelectorAll(".preset-protocol-options, .preset-option-panel, .preset-option-panel summary, .preset-option-panel label, .preset-option-panel small")) {
                if (!element.checkVisibility()) continue;
                const bounds = element.getBoundingClientRect();
                assert(element.scrollWidth <= element.clientWidth + 1, `${context}: ${element.tagName} content overflows`);
                assert(bounds.left >= dialogBounds.left && bounds.right <= dialogBounds.right, `${context}: option escapes dialog`);
              }
            }
            for (const grid of form.querySelectorAll(".plan-fields")) {
              if (!grid.checkVisibility()) continue;
              assert(grid.scrollWidth <= grid.clientWidth + 1, `${context}: fields overflow`);
              for (const input of grid.querySelectorAll("input, select, textarea")) {
                if (["hidden", "checkbox", "radio"].includes(input.type) || !input.checkVisibility()) continue;
                assert(input.getBoundingClientRect().width >= 48, `${context}: ${input.name} is too narrow to use`);
              }
            }
            for (const group of form.querySelectorAll(".secret-value-control, .generated-input-control")) {
              if (!group.checkVisibility()) continue;
              const input = group.querySelector("input");
              const bounds = input.getBoundingClientRect();
              const groupBounds = group.getBoundingClientRect();
              for (const control of [group, ...group.querySelectorAll("button:not([hidden])")]) {
                const box = control.getBoundingClientRect();
                assert(Math.abs(box.top - bounds.top) <= 1 && Math.abs(box.bottom - bounds.bottom) <= 1,
                  `${context}: ${input.name} input and actions are misaligned`);
                assert(box.left >= groupBounds.left - 1 && box.right <= groupBounds.right + 1,
                  `${context}: ${input.name} action overflows`);
              }
              const help = group.parentElement.querySelector("small");
              assert(!help || help.getBoundingClientRect().top >= bounds.bottom,
                `${context}: ${input.name} help overlaps its controls`);
            }
          }
        }
      } finally {
        fixture.dispose();
      }
    }
  } finally {
    if (originalScale) root.style.setProperty("--ui-font-scale", originalScale);
    else root.style.removeProperty("--ui-font-scale");
    if (originalTheme) root.dataset.theme = originalTheme;
    else delete root.dataset.theme;
  }
}
