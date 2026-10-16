import { setPanelVisible } from "./workspace-motion.js";
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
        if (!installedCount) {
          serviceCount.textContent = "尚未安装内核";
        } else if (compact) {
          const sharedTotals = (agent?.capabilities || []).reduce(
            (acc, engine) => {
              const list = Array.isArray(agent?.runtime?.[engine]?.shared_instances)
                ? agent.runtime[engine].shared_instances
                : [];
              acc.total += list.length;
              acc.running += list.filter((entry) => entry?.running || ["active", "running"].includes(entry?.service_status)).length;
              return acc;
            },
            { total: 0, running: 0 },
          );
          serviceCount.textContent = sharedTotals.total > 0
            ? `${installedCount} 个已安装 · 多用户实例 ${sharedTotals.running}/${sharedTotals.total} 运行`
            : `${installedCount} 个已安装`;
        } else {
          serviceCount.textContent = `${installedCount} 个已安装 · ${(agent?.capabilities || []).length} 个可用`;
        }
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
      }
    });
  document.querySelectorAll("[data-node-subtab]").forEach((button) => {
    button.onclick = () => {
      const panel = button.closest(".node-tab-panel");
      if (!panel) return;
      const [group, subpage] = String(button.dataset.nodeSubtab || "").split(":");
      if (group === "cores") state.data.nodeCoresSubpage = subpage;
      if (group === "agent") state.data.nodeAgentSubpage = subpage;
      panel
        .querySelectorAll(`[data-node-subtab^="${group}:"]`)
        .forEach((peer) => {
          const active = peer === button;
          peer.setAttribute("aria-selected", String(active));
          peer.tabIndex = active ? 0 : -1;
        });
      panel
        .querySelectorAll(`[data-node-subpanel^="${group}:"]`)
        .forEach((subpanel) => {
          const active = subpanel.dataset.nodeSubpanel === `${group}:${subpage}`;
          subpanel.classList.toggle("is-active", active);
        });
    };
    button.onkeydown = (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      const nav = button.closest(".node-subpage-nav");
      if (!nav) return;
      const tabs = [...nav.querySelectorAll("[data-node-subtab]")];
      const current = tabs.indexOf(button);
      if (current < 0) return;
      event.preventDefault();
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
  document.querySelectorAll("[data-node-tab]").forEach((button) => {
    const activate = () => {
      const workspace = button.closest(".node-operations-workspace");
      if (!workspace) return;
      const target = button.dataset.nodeTab;
      state.data.nodeSettingsTab = target;
      workspace
        .querySelectorAll("[data-node-tab]")
        .forEach((peer) => {
          const active = peer === button;
          peer.setAttribute("aria-selected", String(active));
          peer.tabIndex = active ? 0 : -1;
        });
      workspace
        .querySelectorAll("[data-node-panel]")
        .forEach((panel) => {
          const active = panel.dataset.nodePanel === target;
          setPanelVisible(panel, active, { id: "qch-tab-panel" });
        });
    };
    button.onclick = activate;
    button.onkeydown = (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      const tabs = [
        ...button
          .closest(".node-settings-tabs")
          .querySelectorAll("[data-node-tab]"),
      ];
      const current = tabs.indexOf(button);
      event.preventDefault();
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
}
