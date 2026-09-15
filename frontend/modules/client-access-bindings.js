import { bindEvent } from "./refresh.js";
import { createRegionDisplay } from "./regions.js";

import { createClientAccessProfiles } from "./client-access-profiles.js";
export function createClientAccessBindings(ctx, { clientAccess, renderClientAccess, masonry }) {
  const { state, notify } = ctx;
  const loadRegionDisplay = createRegionDisplay(ctx);
  const bindClientProfiles = createClientAccessProfiles(ctx, { clientAccess });
  function bindClientAccessPage() {
    masonry.bind();
    document.querySelectorAll(".client-access-node-card").forEach((card) => {
      const id = card.querySelector("[data-region-avatar]")?.dataset.regionAvatar;
      const agent = (state.data.agents || []).find((item) => item.id === id);
      if (agent) loadRegionDisplay(agent, card);
    });
    document.querySelectorAll("[data-access-agent]").forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessAgent = button.dataset.accessAgent;
        renderClientAccess();
      };
    });
    document.querySelectorAll("[data-filter-engine]").forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessEngine = button.dataset.filterEngine;
        renderClientAccess();
      };
    });
    bindEvent(document.querySelector("#client-search"), "submit", (event) => {
      event.preventDefault();
      state.data.accessQuery = String(
        new FormData(event.currentTarget).get("q") || "",
      ).trim();
      renderClientAccess();
    });
    bindEvent(document.querySelector("[data-clear-search]"), "click", () => {
      state.data.accessQuery = "";
      const input = document.querySelector("#client-search [name=q]");
      if (input) {
        input.value = "";
        input.defaultValue = "";
      }
      renderClientAccess();
    });
    bindEvent(
      document.querySelector("[data-clear-client-filters]"),
      "click",
      () => {
        state.data.accessEngine = "";
        state.data.accessQuery = "";
        const input = document.querySelector("#client-search [name=q]");
        if (input) {
          input.value = "";
          input.defaultValue = "";
        }
        renderClientAccess();
      },
    );
    bindEvent(
      document.querySelector("[data-refresh-client-access]"),
      "click",
      async (event) => {
        const button = event.currentTarget;
        button.disabled = true;
        button.setAttribute("aria-busy", "true");
        try {
          const refreshed = await clientAccess();
          if (refreshed) notify("客户端配置已刷新");
          else if (button.isConnected) {
            button.disabled = false;
            button.removeAttribute("aria-busy");
          }
        } catch (error) {
          notify(error.message, "error");
          button.disabled = false;
          button.removeAttribute("aria-busy");
        }
      },
    );

    bindClientProfiles();
  }
  return bindClientAccessPage;
}
