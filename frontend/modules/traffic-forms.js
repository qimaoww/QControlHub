import { bindEvent } from "./refresh.js";

import { trafficEndpointKey } from "./traffic-model.js";
import { requestFromForm, resetTrafficCreateForm } from "./traffic-form-model.js";
export function createTrafficForms({ api, state, notify, confirmAction }, { filters, renderTraffic, traffic, sync, engineOptions }) {
  const { openTrafficSync } = sync;
  function bindTrafficForms(agents, endpoints) {
    document.querySelectorAll("[data-traffic-filter]").forEach((control) => {
      control.onchange = () => {
        filters()[control.dataset.trafficFilter] = control.value;
        renderTraffic(state.data.agents || [], state.data.trafficPolicies || [], state.data.trafficEndpoints || []);
      };
    });
    const reset = document.querySelector("[data-traffic-filter-reset]");
    if (reset) reset.onclick = () => {
      state.data.trafficFilters = {};
      renderTraffic(state.data.agents || [], state.data.trafficPolicies || [], state.data.trafficEndpoints || []);
    };
    const trafficSync = document.querySelector("[data-traffic-sync]");
    if (trafficSync) trafficSync.disabled = sync.isPending();
    if (trafficSync) trafficSync.onclick = openTrafficSync;
    document.querySelectorAll('a[href="#traffic-new"]').forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        const create = document.querySelector("#traffic-new");
        resetTrafficCreateForm(create?.querySelector("form"), agents, engineOptions);
        create?.showModal();
      };
    });
    document.querySelectorAll("[data-traffic-configure]").forEach((button) => {
      button.onclick = () => {
        const endpoint = endpoints.find((candidate) => trafficEndpointKey(candidate) === button.dataset.trafficConfigure);
        const form = document.querySelector("#traffic-policy-form");
        const dialog = document.querySelector("#traffic-new");
        if (!endpoint || !form || !dialog) return;
        resetTrafficCreateForm(form, agents, engineOptions);
        const agentSelect = form.querySelector("[name=agent_id]");
        const engineSelect = form.querySelector("[name=engine]");
        if (agentSelect) agentSelect.value = endpoint.agent_id;
        const agent = agents.find((candidate) => candidate.id === endpoint.agent_id);
        if (engineSelect) {
          engineSelect.innerHTML = engineOptions(agent, endpoint.engine);
          engineSelect.value = endpoint.engine;
        }
        const values = { name: endpoint.name, port: endpoint.port, protocol: endpoint.protocol };
        Object.entries(values).forEach(([name, value]) => {
          const field = form.querySelector(`[name="${name}"]`);
          if (field) field.value = value;
        });
        dialog.showModal();
      };
    });
    const createDialog = document.querySelector("#traffic-new");
    if (createDialog) {
      createDialog.querySelectorAll("[data-traffic-create-close]").forEach((button) => {
        button.onclick = () => createDialog.close();
      });
      createDialog.onclick = (event) => {
        if (event.target === createDialog) createDialog.close();
      };
    }
    document.querySelectorAll("[data-traffic-agent-select]").forEach((select) => {
      select.onchange = () => {
        const engineSelect = select.closest("form")?.querySelector("[name=engine]");
        const agent = agents.find((item) => item.id === select.value);
        if (engineSelect) engineSelect.innerHTML = engineOptions(agent);
      };
    });
    document.querySelectorAll("[data-traffic-edit-open]").forEach((button) => {
      button.onclick = () => {
        const dialog = document.querySelector(`[data-traffic-edit-dialog="${CSS.escape(button.dataset.trafficEditOpen)}"]`);
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-traffic-edit-dialog]").forEach((dialog) => {
      dialog.querySelectorAll("[data-traffic-edit-close]").forEach((button) => {
        button.onclick = () => dialog.close();
      });
      dialog.onclick = (event) => {
        if (event.target === dialog) dialog.close();
      };
    });
    bindEvent(document.querySelector("#traffic-policy-form"), "submit", async (event) => {
      event.preventDefault();
      try {
        await api("/traffic-policies", { method: "POST", body: JSON.stringify(requestFromForm(event.currentTarget)) });
        notify("端口流量配额已创建，正在同步到 Agent");
        await traffic({ resetCreate: true });
      } catch (error) { notify(error.message, "error"); }
    });
    document.querySelectorAll("[data-traffic-edit-form]").forEach((form) => {
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        try {
          await api(`/traffic-policies/${encodeURIComponent(form.dataset.trafficEditForm)}`, { method: "PUT", body: JSON.stringify(requestFromForm(form)) });
          notify("端口流量配额已更新");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      });
    });
    document.querySelectorAll("[data-traffic-reset]").forEach((button) => {
      button.onclick = async () => {
        if (!(await confirmAction("确定立即清零这个端口的当前周期流量并解除封禁？", "清零并解封"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficReset)}/reset`, { method: "POST" });
          notify("当前周期流量已清零，正在同步解封");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
    document.querySelectorAll("[data-traffic-delete]").forEach((button) => {
      button.onclick = async () => {
        if (!(await confirmAction("取消配额后会解除自动封禁，但该端口仍会继续统计流量。确定继续？", "取消配额"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficDelete)}`, { method: "DELETE" });
          notify("配额已取消，端口流量继续统计");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
    document.querySelectorAll("[data-traffic-monitor-delete]").forEach((button) => {
      button.onclick = async () => {
        if (!(await confirmAction("确定停止监控这个端口，并永久删除当前流量记录和每日历史？以后重新设置配额可再次启用。", "删除流量记录"))) return;
        try {
          await api(`/traffic-policies/${encodeURIComponent(button.dataset.trafficMonitorDelete)}/monitoring`, { method: "DELETE" });
          notify("流量记录已删除");
          await traffic();
        } catch (error) { notify(error.message, "error"); }
      };
    });
  }

  return bindTrafficForms;
}
