import { assert, pause, waitFor, createConfigFixture } from "./config-fixture.mjs";

// Exercise the real catalog at a constrained width, including options that are
// normally collapsed. A public-key hint must not stretch its adjacent actions.
export async function testPresetFieldLayoutRuntime() {
  const entries = await (await fetch("/assets/preset-plans.json")).json();
  const root = document.documentElement;
  const originalScale = root.style.fontSize;
  try {
    for (const entry of entries) {
      const fixture = await createConfigFixture(entry.engine, { protocol: entry.protocol.key });
      try {
        fixture.select("second");
        fixture.click("modify");
        await waitFor(() => document.querySelector("dialog[open] form:has([name=protocol])"), "preset form did not open");
        const form = document.querySelector("dialog[open] form:has([name=protocol])");
        const dialog = form.closest("dialog");
        if (entry.protocol.key === "mieru") {
          const mode = form.elements.namedItem("mieru_traffic_pattern");
          mode.value = "custom";
          mode.dispatchEvent(new Event("input", { bubbles: true }));
        }
        const tabs = [];
        for (const tab of tabs.length ? tabs : [null]) {
          tab?.click();
          await pause();
          form.querySelectorAll("details")
            .forEach(panel => { panel.open = true; });
          for (const scale of ["90%", "100%", "135%"]) {
            root.style.fontSize = scale;
            const context = `${entry.engine}/${entry.protocol.key}/${tab?.dataset.builderStep || "connection"}/${scale}`;
            assert(dialog.scrollWidth <= dialog.clientWidth + 1, `${context}: dialog overflows`);
            if (entry.protocol.key === "mieru") {
              assert(document.documentElement.scrollWidth <= innerWidth, `${context}: page overflows`);
              const dialogBounds = dialog.getBoundingClientRect();
              assert(dialogBounds.left >= -1 && dialogBounds.right <= innerWidth + 1, `${context}: dialog escapes viewport`);
              for (const element of form.querySelectorAll(".a-disclosure, .a-disclosure summary, .a-disclosure label, .a-muted")) {
                if (!element.checkVisibility()) continue;
                const bounds = element.getBoundingClientRect();
                assert(element.scrollWidth <= element.clientWidth + 1, `${context}: ${element.tagName} content overflows`);
                assert(bounds.left >= dialogBounds.left && bounds.right <= dialogBounds.right, `${context}: option escapes dialog`);
              }
            }
            for (const grid of form.querySelectorAll(".a-form-grid")) {
              if (!grid.checkVisibility()) continue;
              assert(grid.scrollWidth <= grid.clientWidth + 1, `${context}: fields overflow (${grid.scrollWidth}/${grid.clientWidth}; ${grid.className}; ${getComputedStyle(grid).translate}; ${getComputedStyle(grid).transform}; ${grid.getAnimations({subtree:true}).map(a => a.id).join(',')})`);
              for (const input of grid.querySelectorAll("input, select, textarea")) {
                if (["hidden", "checkbox", "radio"].includes(input.type) || !input.checkVisibility()) continue;
                assert(input.getBoundingClientRect().width >= 48, `${context}: ${input.name} is too narrow to use`);
              }
            }
            for (const group of form.querySelectorAll(".a-field:has(.a-actions)")) {
              if (!group.checkVisibility()) continue;
              const input = group.querySelector("input");
              if (!input) continue;
              const bounds = input.getBoundingClientRect();
              const groupBounds = group.getBoundingClientRect();
              for (const control of [group, ...group.querySelectorAll("button:not([hidden])")]) {
                const box = control.getBoundingClientRect();
                assert(control === group || box.top >= bounds.bottom - 1,
                  `${context}: ${input.name} input and actions are misaligned`);
                assert(box.left >= groupBounds.left - 1 && box.right <= groupBounds.right + 1,
                  `${context}: ${input.name} action overflows`);
              }
              const help = group.querySelector("small");
              assert(!help || help.getBoundingClientRect().top >= bounds.bottom,
                `${context}: ${input.name} help overlaps its controls`);
            }
          }
        }
      } finally {
        fixture.dispose();
      }
    }
    // Optional Reality controls may start collapsed, but invalid values must
    // still reveal the field and prevent a write through the normal submit.
    const fixture = await createConfigFixture("xray", { protocol: "vless" });
    try {
      fixture.click("add");
      await waitFor(() => document.querySelector("dialog[open] form:has([name=protocol])"), "Reality add form did not open");
      const protocol = document.querySelector("dialog[open] [name=protocol]");
      protocol.value = "vless";
      protocol.dispatchEvent(new Event("change", { bubbles: true }));
      await waitFor(() => document.querySelector('[name="reality_min_client_ver"]'), "Reality fields did not load");
      const form = document.querySelector("dialog[open] form:has([name=protocol])");
      form.elements.namedItem("reality_server_name").value = "reality.example.test";
      const minimum = form.elements.namedItem("reality_min_client_ver");
      const advanced = minimum.closest("details");
      assert(advanced, "Reality fields have no containing section");
      advanced.open = false;
      minimum.value = "invalid";
      minimum.dispatchEvent(new Event("input", { bubbles: true }));
      fixture.submit("deploy");
      await waitFor(() => advanced.open && document.activeElement === minimum,
        `invalid advanced Reality field was not revealed and focused (${[...form.elements].filter(control=>control.willValidate&&!control.validity.valid).map(control=>control.name).join(',')}; active=${document.activeElement?.name}; open=${advanced.open})`);
      assert(fixture.writes.length === 0, "invalid collapsed Reality value was submitted");
      assert(form.textContent.includes('请检查此项的格式或取值范围。'), 'native constraint error did not use Chinese inline feedback');
    } finally {
      fixture.dispose();
    }
  } finally {
    root.style.fontSize = originalScale;
  }
}
