import { assert, waitFor } from "./assertions.mjs";

export async function testConfigLayoutRuntime({ testAPI }) {
    await waitFor(()=>document.querySelector("#live-config-form"),"manual editor did not load");
    assert.equal(document.querySelectorAll(".live-engine-bar [data-live-engine]").length,4,"top bar must expose all installed engines");
    assert.equal(document.querySelectorAll(".context-sidebar [data-live-engine]").length,0,"sidebar must not duplicate engine navigation");
    assert.equal(document.querySelectorAll('.dock-nav a[href="#agents"], .mobile-account-menu a[href="#agents"]').length,0,"independent preset navigation remains");
    if (!new URLSearchParams(location.search).has("preview")) {
      for (const [hash, node, engine] of [
        ["#agents", "alpha", "xray"],
        ["#preset-node-bravo", "bravo", "xray"],
        ["#agent-config?agent=charlie&engine=sing-box", "charlie", "sing-box"],
        ["#live-config?agent=alpha&engine=xray", "alpha", "xray"],
      ]) {
        location.hash = hash;
        await waitFor(()=>{
          const workspace = document.querySelector(`[data-refresh-key^="live-config-content-${node}-${engine}-"]`);
          return location.hash.startsWith("#live-config") && workspace &&
            workspace.querySelector("#live-config-form");
        }, "legacy config link did not preserve node/core: " + hash);
      }
      const engineLauncher = document.querySelector('[data-live-engine="sing-box"]');
      engineLauncher.focus(); engineLauncher.click();
      await waitFor(()=>document.querySelector('#live-config-form[data-engine="sing-box"]'),"top engine switch failed");
      assert.equal(document.activeElement, document.querySelector('[data-live-engine="sing-box"]'), "replaced engine editor must retain selection launcher focus");
      assert.ok(location.hash.includes("engine=sing-box"),"engine switch not reflected in safe deep link");
      document.querySelector('[data-config-file="0"]').click();
      const input = document.querySelector("[data-code-input]");
      input.focus(); input.setSelectionRange(1, 3);
      const fileButtons = document.querySelectorAll("[data-config-file]");
      fileButtons[0].click();
      assert.equal(input.selectionStart, 1, "same object changed the caret");
      assert.equal(input.selectionEnd, 3);
      input.value = input.value.replace("{", '{"future_workspace_counter":18446744073709551615,');
      input.dispatchEvent(new Event("input", {bubbles:true}));
      assert.equal(document.querySelector(".code-workspace .config-draft-summary").dataset.dirty, "1");
      fileButtons[1].click();
      assert.equal(fileButtons[1].getAttribute("aria-current"), "true");
      assert.ok(document.querySelector("[data-code-reset]").disabled, "clean object inherited another object's dirty state");
      const inboundBaseline = input.value;
      input.value = input.value.replace(/("listen_port"\s*:\s*)(\d+)/, (_match, prefix, port) => prefix + (Number(port) + 1));
      input.dispatchEvent(new Event("input", {bubbles:true}));
      const inboundDraft = input.value;
      fileButtons[0].click();
      assert.ok(input.value.includes("18446744073709551615"), "common draft was lost");
      fileButtons[1].click();
      assert.equal(input.value, inboundDraft, "inbound draft was lost");
      document.querySelector("[data-config-preview]").click();
      assert.ok(input.readOnly && input.value.includes("18446744073709551615"));
      assert.ok(document.querySelector("[data-code-format]").disabled, "read-only preview exposes an enabled format action");
      document.querySelector("[data-config-source]").click();
      assert.ok(!input.readOnly && input.value.includes("18446744073709551615"), "complete source is not an editable merged document");
      fileButtons[1].click();
      document.querySelector("[data-code-reset]").click();
      assert.equal(input.value, inboundBaseline, "reset changed a different object");
      assert.ok(document.querySelector("[data-code-reset]").disabled);
      fileButtons[0].click();
      const draft = input.value;
      assert.ok(draft.includes("18446744073709551615"), "reset discarded another object's custom field");
      document.querySelectorAll("[data-live-agent]")[1].click();
      await waitFor(()=>document.querySelector("[data-confirm-dialog][open]"),"dirty node switch did not prompt");
      document.querySelector("[data-confirm-cancel]").click();
      assert.equal(input.value,draft,"cancel node switch lost draft");
      document.querySelector('[data-live-engine="mihomo"]').click();
      await waitFor(()=>document.querySelector("[data-confirm-dialog][open]"),"dirty engine switch did not prompt");
      document.querySelector("[data-confirm-cancel]").click();
      assert.ok(document.querySelector('#live-config-form[data-engine="sing-box"]'),"cancel discarded active engine");
      document.querySelector('[data-live-engine="mihomo"]').click();
      await waitFor(()=>document.querySelector("[data-confirm-dialog][open]"),"second switch did not prompt");
      document.querySelector("[data-confirm-accept]").click();
      await waitFor(()=>document.querySelector('#live-config-form[data-engine="mihomo"]'),"confirmed engine switch failed");
    }
    assert.ok(document.documentElement.scrollWidth<=innerWidth,"manual page overflows viewport");
    const tools = document.querySelector(".config-workspace-tools").getBoundingClientRect();
    const editor = document.querySelector("#live-config-form").getBoundingClientRect();
    assert.ok(tools.bottom <= editor.top + 1,"configuration tools remain below the source editor");
    const workspace = document.querySelector(".live-config-workspace");
    const rail = workspace.querySelector(".field-rail").getBoundingClientRect();
    const frame = workspace.querySelector(".code-editor-frame").getBoundingClientRect();
    if (innerWidth > 1100) {
      assert.ok(rail.right <= frame.left + 1 && rail.width >= 200, "desktop object rail did not leave a clear editing workspace");
      assert.ok(frame.width > 400, "desktop source editor is too narrow");
    } else assert.ok(rail.bottom <= frame.top + 1, "mobile navigation did not precede editing");
    assert.equal(document.querySelectorAll("[data-config-objects]").length, 1, "object navigation is duplicated");
    assert.ok(workspace.querySelector('.config-file-actions [data-inbound-action="add"]'), "stable add action is missing");
    for (const button of workspace.querySelectorAll(".config-file-actions button")) {
      assert.ok(button.scrollWidth <= button.clientWidth + 1, "mobile action label is squeezed");
    }
    assert.ok(workspace.querySelector('.code-editor-toolbar [data-code-reset]'),"reset must stay with current-file tools");
    assert.ok(workspace.querySelector('.code-editor-toolbar [data-code-format]'),"format must stay with current-file tools");
    assert.ok([...workspace.querySelectorAll('.code-workspace>footer button')].every(button => button.type === "submit"),"submission footer mixes file tools with save actions");
    const details = workspace.querySelector(".live-config-details").getBoundingClientRect();
    assert.ok(details.bottom <= workspace.querySelector(".live-engine-bar").getBoundingClientRect().top + 1,"node metadata must stay with the heading");
    assert.ok(document.querySelector('[data-inbound-action="history"]').closest(".config-tools-menu"),"secondary tools are not grouped in the header");
    const more = document.querySelector(".config-tools-menu");
    more.querySelector("summary").focus();
    more.dispatchEvent(new KeyboardEvent("keydown",{key:"ArrowDown",bubbles:true}));
    assert.ok(more.open && document.activeElement.matches('[role="menuitem"]'),"tools menu cannot be opened by keyboard");
    more.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true}));
    assert.ok(!more.open && document.activeElement === more.querySelector("summary"),"tools menu did not restore focus after Escape");
    const engineTabs = [...document.querySelectorAll(".live-engine-tab")];
    engineTabs.forEach(tab => {
      assert.ok(Math.abs(tab.offsetWidth-engineTabs[0].offsetWidth) <= 1,"engine buttons must have equal widths");
      assert.ok(tab.offsetHeight >= (innerWidth <= 820 ? 44 : 40),"engine buttons lost their minimum target height");
      const bounds = tab.getBoundingClientRect();
      assert.ok(bounds.left >= 0 && bounds.right <= innerWidth,"engine choice is hidden outside the viewport");
    });
    if (innerWidth <= 600) {
      assert.equal(engineTabs[0].offsetTop,engineTabs[1].offsetTop,"first engine pair must share a row");
      assert.ok(engineTabs[2].offsetTop > engineTabs[0].offsetTop,"mobile engines must form two rows");
      assert.equal(engineTabs[2].offsetTop,engineTabs[3].offsetTop,"second engine pair must share a row");
    }
    if (innerWidth <= 820) {
      const bar = document.querySelector(".live-engine-bar").getBoundingClientRect();
      const tab = document.querySelector(".live-engine-tab").getBoundingClientRect();
      assert.ok(tab.left-bar.left >= 15.5,"mobile engine tabs lost their horizontal inset");
      const toolbar = document.querySelector(".code-editor-toolbar").getBoundingClientRect();
      const file = document.querySelector(".code-file-meta").getBoundingClientRect();
      const meta = document.querySelector(".code-editor-meta").getBoundingClientRect();
      assert.ok(file.top-toolbar.top >= 15.5 && toolbar.bottom-meta.bottom >= 15.5,`mobile file controls touch section dividers: ${file.top-toolbar.top}/${toolbar.bottom-meta.bottom}`);
      const footer = document.querySelector(".code-workspace>footer").getBoundingClientRect();
      const actions = document.querySelector(".code-workspace>footer>div").getBoundingClientRect();
      assert.ok(actions.top-footer.top >= 15.5 && footer.bottom-actions.bottom >= 15.5,"mobile actions touch section dividers");
    }
}
