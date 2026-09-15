export const routeModuleNames = Object.freeze({
  dashboard: "dashboard",
  agents: "agents",
  "node-settings": "agents",
  "client-access": "client-access",
  "substore-sync": "substore-sync",
  "agent-config": "configs",
  "live-config": "configs",
  "archive-config": "configs",
  tasks: "tasks",
  "core-logs": "core-logs",
  traffic: "traffic",
  "access-control": "access-control",
  "system-bbr": "system-bbr",
  settings: "settings",
  users: "users",
  "my-quota": "users",
});

const supportedRoutes = new Set(Object.keys(routeModuleNames));
const routeAliases = Object.freeze({
  summary: "dashboard",
  "panel-host": "dashboard",
  "traffic-usage": "dashboard",
  fleet: "dashboard",
  activity: "dashboard",
  enrollment: "node-settings",
  "settings-engines": "settings",
  "settings-basic": "settings",
  "settings-runtime": "settings",
  "settings-data": "settings",
  "settings-notify": "settings",
  "settings-komari": "settings",
  "settings-cnip": "settings",
  "settings-deployment": "settings",
  "preset-node": "live-config",
  "settings-node": "node-settings",
  "new-config": "archive-config",
  templates: "archive-config",
  archive: "archive-config",
  "traffic-new": "traffic",
  "traffic-all": "traffic",
  "access-control-all": "access-control",
});

export function routeForHash(value) {
  const hash = String(value || "")
    .replace(/^#/, "")
    .split("?", 1)[0];
  if (supportedRoutes.has(hash)) return hash;
  if (hash.startsWith("system-bbr-agent-")) return "system-bbr";
  if (routeAliases[hash]) return routeAliases[hash];
  if (hash.startsWith("preset-node-")) return "live-config";
  if (hash.startsWith("settings-node-") || hash.startsWith("node-"))
    return "node-settings";
  if (hash.startsWith("traffic-agent-")) return "traffic";
  if (hash.startsWith("access-control-agent-")) return "access-control";
  if (hash.startsWith("config-")) return "archive-config";
  return "dashboard";
}
