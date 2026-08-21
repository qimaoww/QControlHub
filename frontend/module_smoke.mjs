import { installAgents } from "./modules/agents.js";
import { installClientAccess } from "./modules/client-access.js";
import { installConfigPages } from "./modules/configs.js";
import { installCoreLogs } from "./modules/core-logs.js";
import { installDashboard } from "./modules/dashboard.js";
import { installSettings } from "./modules/settings.js";
import { installTasks } from "./modules/tasks.js";
import { installTraffic } from "./modules/traffic.js";

const state = { data: {}, session: { role: "admin" } };
const noop = () => {};
const ctx = new Proxy(
  { state, engines: [], actions: [] },
  { get: (target, key) => target[key] ?? noop },
);

for (const install of [
  installAgents,
  installClientAccess,
  installConfigPages,
  installCoreLogs,
  installDashboard,
  installSettings,
  installTasks,
  installTraffic,
]) {
  const page = install(ctx);
  if (typeof page !== "function" && (typeof page !== "object" || !page)) {
    throw new TypeError(`${install.name} returned an invalid page module`);
  }
}
