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
            (testAPI.agents.find(agent=>agent.id === node).status === "offline"
              ? workspace.querySelector(".node-config-source")?.textContent.includes("节点离线")
              : workspace.querySelector("#live-config-form"));
        }, "legacy config link did not preserve node/core: " + hash);
      }
      const engineLauncher = document.querySelector('[data-live-engine="sing-box"]');
      engineLauncher.focus(); engineLauncher.click();
      await waitFor(()=>document.querySelector('#live-config-form[data-engine="sing-box"]'),"top engine switch failed");
      assert.equal(document.activeElement, document.querySelector('[data-live-engine="sing-box"]'), "replaced engine editor must retain selection launcher focus");
      assert.ok(location.hash.includes("engine=sing-box"),"engine switch not reflected in safe deep link");
      const input=document.querySelector("[data-code-input]");input.value+="\n";input.dispatchEvent(new Event("input",{bubbles:true}));
      const draft = input.value;
      const common = document.querySelector("[data-config-common]");
      const inboundButtons = document.querySelectorAll("[data-access-inbound]");
      assert.ok(common && inboundButtons.length, "complete source lost inbound tools");
      input.focus(); input.setSelectionRange(1,3); common.click();
      assert.equal(input.selectionStart,1,"selection changed the complete-source caret");
      assert.equal(input.selectionEnd,3);
      assert.equal(input.closest(".code-editor-frame").getAnimations().length,0,"selection replayed the source animation");
      inboundButtons[0].click();
      assert.equal(inboundButtons[0].getAttribute("aria-pressed"),"true","inbound selection missing");
      assert.equal(input.value,draft,"inbound selection replaced the source draft");
      assert.ok(!input.readOnly && !document.querySelector("[data-config-preview]"),"single source retains a merged-preview workflow");
      common.click();
      assert.equal(input.value,draft,"returning to full source lost draft");
      document.querySelector("[data-code-reset]").click();
      assert.ok(document.querySelector("[data-code-reset]").disabled,"reset did not restore the full source baseline");
      input.value=draft;input.dispatchEvent(new Event("input",{bubbles:true}));
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
    const nav = workspace.querySelector(".config-file-buttons").getBoundingClientRect();
    const frame = workspace.querySelector(".code-editor-frame").getBoundingClientRect();
    assert.ok(nav.bottom <= frame.top + 1,"file navigation must stay above the full-width source");
    assert.ok(frame.width >= workspace.getBoundingClientRect().width - 3,"source editor lost width to an extra rail");
    assert.equal(document.querySelectorAll(".config-file-navigation").length,0,"redundant toolbar still separates selection from source");
    assert.ok(workspace.querySelector('.config-file-buttons [data-inbound-action="add"]'),"add action must stay with file navigation");
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
