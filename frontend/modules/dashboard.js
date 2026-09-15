import { utcMonth } from "./dashboard-model.js";
import { createDashboardView } from "./dashboard-view.js";
import { createDashboardBindings } from "./dashboard-bindings.js";
export { dashboardTrafficMonthDays, aggregateDashboardTrafficDays } from "./dashboard-model.js";

import { installPanelMetrics } from "./panel-metrics.js";

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
  async function dashboard({ overview: preloadedOverview } = {}) {
    const trafficMonth = state.data.dashboardTrafficMonth || utcMonth();
    const [overview, agents, tasks, trafficUsage] = await Promise.all([
      preloadedOverview || api("/overview"),
      can("agents.read") ? api("/agents") : Promise.resolve([]),
      can("tasks.read") ? api("/tasks?limit=7") : Promise.resolve([]),
      can("traffic.read")
        ? api(`/traffic-usage?month=${encodeURIComponent(trafficMonth)}`)
        : Promise.resolve({ month: trafficMonth, timezone: "UTC", days: [] }),
    ]);
    state.data.overview = overview;
    state.data.agents = agents;
    state.data.dashboardTrafficMonth = trafficMonth;
    state.data.dashboardTrafficUsage = trafficUsage;
    const { trafficYear } = render({ overview, agents, tasks, trafficUsage, trafficMonth });
    bind({ trafficYear, trafficMonth });
  }
  return dashboard;
}
