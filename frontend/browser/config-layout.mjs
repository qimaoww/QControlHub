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
      document.querySelector('[data-live-engine="sing-box"]').click();
      await waitFor(()=>document.querySelector('#live-config-form[data-engine="sing-box"]'),"top engine switch failed");
      assert.ok(location.hash.includes("engine=sing-box"),"engine switch not reflected in safe deep link");
      const input=document.querySelector("[data-code-input]");input.value+="\n";input.dispatchEvent(new Event("input",{bubbles:true}));
      const draft = input.value;
      const fileButtons = document.querySelectorAll("[data-config-file]");
      assert.ok(fileButtons.length >= 2,"shared/inbound buttons missing");
      assert.ok(fileButtons[0].classList.contains("is-dirty"),"edited common file is not marked unsaved");
      assert.ok(document.querySelector(".config-draft-summary").textContent.includes("1 个未保存"),"draft summary did not count the edited file");
      fileButtons[1].click();
      assert.equal(fileButtons[1].getAttribute("aria-pressed"),"true","inbound selection missing");
      assert.ok(document.querySelector("[data-code-reset]").disabled,"a clean file can be reset because another file is dirty");
      input.value += "\n"; input.dispatchEvent(new Event("input",{bubbles:true}));
      const inboundDraft = input.value;
      assert.ok(document.querySelector(".config-draft-summary").textContent.includes("2 个未保存"),"multiple drafts are not counted");
      fileButtons[0].click();
      assert.equal(input.value,draft,"switch lost common draft");
      fileButtons[1].click();
      assert.equal(input.value,inboundDraft,"switch lost inbound draft");
      fileButtons[0].click();
      assert.equal(document.querySelector('optgroup[label="出站"]'),null,"legacy standalone exit group is still visible");
      document.querySelector("[data-config-preview]").click();
      assert.ok(input.readOnly,"merged preview must be readonly");
      assert.ok(document.querySelector(".config-draft-summary").textContent.includes("2 个未保存"),"merged preview hid pending drafts");
      document.querySelector("[data-config-preview]").click();
      assert.equal(input.value,draft,"preview lost file draft");
      assert.ok(!input.readOnly,"return from preview must restore editing");
      fileButtons[1].click();
      document.querySelector("[data-code-reset]").click();
      assert.ok(!fileButtons[1].classList.contains("is-dirty") && fileButtons[0].classList.contains("is-dirty"),"reset changed drafts outside the current file");
      assert.ok(document.querySelector(".config-draft-summary").textContent.includes("1 个未保存"),"reset did not update the draft summary");
      assert.ok(document.querySelector("[data-code-reset]").disabled,"reset remains enabled for a restored file");
      fileButtons[0].click();
      assert.equal(input.value,draft,"resetting an inbound lost the common-file draft");
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
    assert.ok(document.querySelector('[data-inbound-action="history"]').closest(".config-tools-menu"),"secondary tools are not grouped in the header");
    const more = document.querySelector(".config-tools-menu");
    more.querySelector("summary").focus();
    more.dispatchEvent(new KeyboardEvent("keydown",{key:"ArrowDown",bubbles:true}));
    assert.ok(more.open && document.activeElement.matches('[role="menuitem"]'),"tools menu cannot be opened by keyboard");
    more.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true}));
    assert.ok(!more.open && document.activeElement === more.querySelector("summary"),"tools menu did not restore focus after Escape");
    document.querySelectorAll(".live-engine-tab").forEach(tab => {
      assert.equal(tab.offsetWidth,140,"engine buttons must have fixed width");
      assert.equal(tab.offsetHeight,40,"engine buttons must have fixed height");
    });
    if (innerWidth <= 820) {
      const bar = document.querySelector(".live-engine-bar").getBoundingClientRect();
      const tab = document.querySelector(".live-engine-tab").getBoundingClientRect();
      assert.ok(tab.top-bar.top >= 15.5 && bar.bottom-tab.bottom >= 15.5,"mobile tabs touch section dividers");
      const toolbar = document.querySelector(".code-editor-toolbar").getBoundingClientRect();
      const file = document.querySelector(".code-file-meta").getBoundingClientRect();
      const meta = document.querySelector(".code-editor-meta").getBoundingClientRect();
      assert.ok(file.top-toolbar.top >= 15.5 && toolbar.bottom-meta.bottom >= 15.5,`mobile file controls touch section dividers: ${file.top-toolbar.top}/${toolbar.bottom-meta.bottom}`);
      const footer = document.querySelector(".code-workspace>footer").getBoundingClientRect();
      const actions = document.querySelector(".code-workspace>footer>div").getBoundingClientRect();
      assert.ok(actions.top-footer.top >= 15.5 && footer.bottom-actions.bottom >= 15.5,"mobile actions touch section dividers");
    }
}
