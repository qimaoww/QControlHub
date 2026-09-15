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
      fileButtons[1].click();
      assert.equal(fileButtons[1].getAttribute("aria-pressed"),"true","inbound selection missing");
      input.value += "\n"; input.dispatchEvent(new Event("input",{bubbles:true}));
      const inboundDraft = input.value;
      fileButtons[0].click();
      assert.equal(input.value,draft,"switch lost common draft");
      fileButtons[1].click();
      assert.equal(input.value,inboundDraft,"switch lost inbound draft");
      fileButtons[0].click();
      assert.equal(document.querySelector('optgroup[label="出站"]'),null,"legacy standalone exit group is still visible");
      document.querySelector("[data-config-preview]").click();
      assert.ok(input.readOnly,"merged preview must be readonly");
      document.querySelector("[data-config-preview]").click();
      assert.equal(input.value,draft,"preview lost file draft");
      assert.ok(!input.readOnly,"return from preview must restore editing");
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
