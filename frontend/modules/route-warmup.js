import { routeForHash, routeModuleNames } from "./routes.js";

export function createRouteWarmup(routeModules) {
function preloadNavigationRoute(event) {
  const link = event.target.closest?.('a[href^="#"]');
  if (!link) return;
  if (event.type === "pointerover" && link.contains(event.relatedTarget)) return;
  const route = routeForHash(link.getAttribute("href"));
  void routeModules.preload(routeModuleNames[route] || "dashboard");
}

function bindNavigationPreload() {
document.addEventListener("pointerover", preloadNavigationRoute, {
  passive: true,
});
document.addEventListener("pointerdown", preloadNavigationRoute, {
  passive: true,
});
document.addEventListener("focusin", preloadNavigationRoute);
}

const nextRouteModules = Object.freeze({
  dashboard: "agents",
  agents: "configs",
  configs: "client-access",
  "client-access": "substore-sync",
  "substore-sync": "traffic",
  traffic: "core-logs",
  "core-logs": "tasks",
  tasks: "settings",
  settings: "users",
});
let cancelRouteWarmup = () => {};

function stopRouteWarmup() {
  cancelRouteWarmup();
  cancelRouteWarmup = () => {};
}

function scheduleRouteWarmup(route) {
  stopRouteWarmup();
  const current = routeModuleNames[route] || "dashboard";
  const next = nextRouteModules[current];
  const connection = navigator.connection;
  if (
    !next ||
    routeModules.peek(next) ||
    document.hidden ||
    connection?.saveData ||
    connection?.effectiveType?.includes("2g")
  )
    return;
  const warm = () => {
    cancelRouteWarmup = () => {};
    void routeModules.preload(next);
  };
  if (typeof window.requestIdleCallback === "function") {
    const handle = window.requestIdleCallback(warm, { timeout: 1500 });
    cancelRouteWarmup =
      typeof window.cancelIdleCallback === "function"
        ? () => window.cancelIdleCallback(handle)
        : () => {};
  } else {
    const handle = window.setTimeout(warm, 600);
    cancelRouteWarmup = () => window.clearTimeout(handle);
  }
}

  return { bindNavigationPreload, scheduleRouteWarmup, stopRouteWarmup };
}
