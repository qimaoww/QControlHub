import { utcMonth } from "./dashboard-model.js";
import { createDashboardView } from "./dashboard-view.js";
import { createDashboardBindings } from "./dashboard-bindings.js";
export { dashboardTrafficMonthDays, aggregateDashboardTrafficDays } from "./dashboard-model.js";

import { installPanelMetrics } from "./panel-metrics.js";
import { createRefreshChannel } from "./refresh.js";

export function installDashboard(ctx) {
  const {
    api, state, esc,
    can = () => false,
    bytes = (value) => `${Number(value || 0)} B`,
    rate = (value) => `${Number(value || 0)} B/s`,
  } = ctx;
  const panelMetrics = installPanelMetrics({ api, state, can, esc, bytes, rate });
  const render = createDashboardView(ctx, panelMetrics);
  const bind = createDashboardBindings(ctx, { panelMetrics, dashboard });
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "dashboard",
    getScope: () => state.navigationEpoch,
  });
  async function dashboard({ overview: preloadedOverview } = {}) {
    const data = state.data;
    const trafficMonth = state.data.dashboardTrafficMonth || utcMonth();
    return refresh.run(signal => Promise.all([
      preloadedOverview || api("/overview", { signal }),
      can("agents.read") ? api("/agents", { signal }) : Promise.resolve([]),
      can("tasks.read") ? api("/tasks?limit=7", { signal }) : Promise.resolve([]),
      can("traffic.read")
        ? api(`/traffic-usage?month=${encodeURIComponent(trafficMonth)}`, { signal })
        : Promise.resolve({ month: trafficMonth, timezone: "UTC", days: [] }),
    ]), ([overview, agents, tasks, trafficUsage]) => {
      if (data !== state.data) return;
      state.data.overview = overview;
      state.data.agents = agents;
      state.data.dashboardTrafficMonth = trafficMonth;
      state.data.dashboardTrafficUsage = trafficUsage;
      const { trafficYear } = render({ overview, agents, tasks, trafficUsage, trafficMonth });
      bind({ trafficYear, trafficMonth });
    });
  }
  dashboard.dispose = () => refresh.invalidate();
  return dashboard;
}
