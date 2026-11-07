import { routeForHash, routeModuleNames } from "./routes.js";

export function createRouteWarmup(routeModules, { moduleName = route => routeModuleNames[route] || 'dashboard', signal } = {}) {
function preloadNavigationRoute(event) {
  const link = event.target.closest?.('a[href^="#"]');
  if (!link) return;
  if (event.type === "pointerover" && link.contains(event.relatedTarget)) return;
  const route = routeForHash(link.getAttribute("href"));
  void routeModules.preload(moduleName(route));
}

function bindNavigationPreload() {
document.addEventListener("pointerover", preloadNavigationRoute, {
  passive: true, signal,
});
document.addEventListener("pointerdown", preloadNavigationRoute, {
  passive: true, signal,
});
document.addEventListener("focusin", preloadNavigationRoute, { signal });
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
  const current = moduleName(route);
  const next = nextRouteModules[current];
  const connection = navigator.connection;
  if (
    signal?.aborted || !next ||
    routeModules.peek(next) ||
    document.hidden ||
    connection?.saveData ||
    connection?.effectiveType?.includes("2g")
  )
    return;
  const warm = () => {
    cancelRouteWarmup = () => {};
    if (!signal?.aborted && !document.hidden) void routeModules.preload(next);
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

  signal?.addEventListener('abort', stopRouteWarmup, { once: true });
  return { bindNavigationPreload, scheduleRouteWarmup, stopRouteWarmup };
}
