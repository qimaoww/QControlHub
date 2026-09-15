import { bindEvent } from "./refresh.js";

export function compactPresetPage() {
  document.querySelector("#enrollment")?.remove();
  document.querySelector("#batch-form")?.remove();
  document.querySelectorAll(".preset-node-workspace").forEach((item) => {
    item.querySelector(".machine-resource-summary")?.remove();
    item.querySelector(".machine-state")?.remove();
    item.querySelector(".node-inspector")?.remove();
    item.querySelector(".machine-footer")?.remove();
    item.querySelectorAll(".service-management-unavailable, [data-upgrade-agent]").forEach((element) => element.remove());
    item.querySelectorAll("[data-batch-checkbox]").forEach((element) => element.closest("label")?.remove());
  });
}


export function bindAgentWorkspace({ state, can }, agentsByID, { loadMetricHistory, updateAgentMetrics }) {
  document
    .querySelectorAll(
      ".preset-node-workspace, .machine-workspace, .node-operations-workspace",
    )
    .forEach((item) => {
      const agent = agentsByID.get(item.dataset.agentNode);
      const installedCount = (agent?.capabilities || []).filter(
        (engine) => agent.runtime?.[engine]?.installed,
      ).length;
      const serviceCount = item.querySelector(
        ".service-canvas-head > span, [data-installed-summary]",
      );
      if (serviceCount) {
        const compact = serviceCount.hasAttribute("data-installed-summary");
        serviceCount.textContent = installedCount
          ? compact
            ? `${installedCount} 个已安装`
            : `${installedCount} 个已安装 · ${(agent?.capabilities || []).length} 个可用`
          : "尚未安装内核";
      }
      const machineFooter = item.querySelector(".machine-footer");
      item.querySelectorAll("[data-upgrade-agent]").forEach((button) => {
        const supported = (agent?.features || []).includes(
          "agent-self-upgrade-v1",
        );
        if (!supported) {
          button.disabled = true;
          button.title =
            "当前 Agent 不支持远程升级，请重新执行一次添加节点命令";
          button.textContent = "需重新安装 Agent";
        }
      });
      machineFooter?.remove();
      if (agent) updateAgentMetrics(agent);
      if (item instanceof HTMLDetailsElement) {
        bindEvent(item, "toggle", () => {
          if (item.open) {
            state.data.selectedAgent = item.dataset.agentNode;
            if (can("metrics.read")) loadMetricHistory(state.data.selectedAgent);
          }
        });
        if (item.open && can("metrics.read"))
          loadMetricHistory(item.dataset.agentNode);
      } else if (
        can("metrics.read") &&
        item.querySelector('[data-node-panel="metrics"]:not([hidden])')
      ) {
        loadMetricHistory(item.dataset.agentNode);
      }
    });
  document.querySelectorAll("[data-node-tab]").forEach((button) => {
    button.onclick = () => {
      const workspace = button.closest(".node-operations-workspace");
      if (!workspace) return;
      const tab = button.dataset.nodeTab;
      state.data.nodeSettingsTab = tab;
      workspace.querySelectorAll("[data-node-tab]").forEach((candidate) => {
        const selected = candidate === button;
        candidate.setAttribute("aria-selected", String(selected));
        candidate.tabIndex = selected ? 0 : -1;
      });
      workspace.querySelectorAll("[data-node-panel]").forEach((panel) => {
        panel.hidden = panel.dataset.nodePanel !== tab;
      });
      if (tab === "metrics" && can("metrics.read"))
        loadMetricHistory(workspace.dataset.agentNode);
    };
    button.onkeydown = (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
        return;
      event.preventDefault();
      const tabs = [...button.closest("[role=tablist]").querySelectorAll("[role=tab]")];
      const current = tabs.indexOf(button);
      const next =
        event.key === "Home"
          ? 0
          : event.key === "End"
            ? tabs.length - 1
            : (current + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
              tabs.length;
      tabs[next].focus();
      tabs[next].click();
    };
  });
  document.querySelectorAll("[data-config]").forEach((button) => {
    button.onclick = () => {
      state.data.agentId = button.dataset.config;
      state.data.engine = button.dataset.engine;
      // A preset card can be opened after editing another engine. The
      // protocol and inbound are engine-specific; retaining them can render
      // an empty editor or submit the wrong generated plan.
      state.data.protocol = "";
      state.data.inboundTag = "";
      state.data.configField = "";
      state.data.configInboundField = "";
      state.data.liveAgent = state.data.agentId;
      state.data.liveEngine = state.data.engine;
      state.data.liveConfigSource = "";
      location.hash = `#live-config?${new URLSearchParams({agent:state.data.agentId, engine:state.data.engine})}`;
    };
  });
  document.querySelectorAll("[data-client-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.accessAgent = link.dataset.clientAgent;
      state.data.accessEngine = link.dataset.clientEngine;
    };
  });

}
