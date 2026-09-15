import assert from "node:assert/strict";

import {
  aggregateDashboardTrafficDays,
  dashboardTrafficMonthDays,
  installDashboard,
} from "../modules/dashboard.js";

import { installTasks } from "../modules/tasks.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run({ noop }) {
const routeDataDocument = globalThis.document;
globalThis.document = {
  querySelector: () => null,
  querySelectorAll: () => [],
};
try {
  const dashboardCalls = [];
  let dashboardMarkup = "";
  const dashboardState = {
    route: "dashboard",
    data: { dashboardTrafficMonth: "2026-08" },
  };
  const renderDashboard = installDashboard(
    new Proxy(
      {
        state: dashboardState,
        api: async (path) => {
          dashboardCalls.push(path);
          if (path === "/agents" || path.startsWith("/tasks?")) return [];
          if (path === "/traffic-usage?month=2026-08")
            return { days: [{ day: "2026-08-27", received_bytes: 6, sent_bytes: 4, used_bytes: 10, peak_receive_bps: 2, peak_send_bps: 1 }] };
          assert.fail(`unexpected dashboard preload API path ${path}`);
        },
        can: (capability) => ["traffic.read", "agents.read", "tasks.read"].includes(capability),
        esc: (value) => String(value ?? ""),
        bytes: (value) => `${value || 0} B`,
        rate: (value) => `${value || 0} B/s`,
        shell: (markup) => { dashboardMarkup = markup; },
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await renderDashboard({
    overview: { agents: 3, agents_online: 3, tasks_pending: 0 },
  });
  assert.deepEqual(
    dashboardCalls,
    ["/agents", "/tasks?limit=7", "/traffic-usage?month=2026-08"],
    "dashboard reuses the route bootstrap overview",
  );
  assert.equal(dashboardMarkup.includes('id="traffic-usage"'), true, "dashboard owns the monthly traffic chart");
  assert.equal(dashboardMarkup.includes('data-dashboard-traffic-month'), true, "dashboard traffic history can change month");
  assert.equal(dashboardMarkup.includes('data-dashboard-traffic-details'), true, "dashboard daily traffic opens from a dedicated action");
  assert.equal(dashboardMarkup.includes('data-dashboard-traffic-dialog'), true, "dashboard daily traffic is rendered in a modal dialog");
  assert.equal(dashboardMarkup.includes('class="dashboard-traffic-axis"'), true, "dashboard chart keeps dates on a stable external axis");
  assert.doesNotMatch(dashboardMarkup, /\sstyle=/, "dashboard markup must respect the production CSP");
  assert.equal(dashboardMarkup.includes("31日"), true, "dashboard chart labels natural days explicitly");
  assert.equal(dashboardMarkup.includes('class="dashboard-month-picker"'), true, "dashboard uses a theme-native month picker");
  assert.equal(dashboardMarkup.includes('type="month"'), false, "dashboard does not open the browser-native month panel");
  assert.equal(dashboardMarkup.includes("<details class=\"traffic-daily\""), false, "dashboard no longer expands daily history inline");
  assert.equal(dashboardMarkup.includes("2026-08-27"), true, "dashboard renders persisted daily traffic details");
  assert.equal(dashboardTrafficMonthDays("2024-02").length, 29, "dashboard traffic month helper observes leap years");
  assert.deepEqual(
    aggregateDashboardTrafficDays([
      { day: "2026-08-01", received_bytes: 4, sent_bytes: 3, used_bytes: 7, peak_receive_bps: 2 },
      { day: "2026-08-01", received_bytes: 5, sent_bytes: 2, used_bytes: 7, peak_receive_bps: 8 },
    ], "2026-08")[0],
    { day: "2026-08-01", received_bytes: 9, sent_bytes: 5, used_bytes: 14, peak_receive_bps: 8, peak_send_bps: 0 },
    "same-day policy rows are aggregated for the dashboard chart",
  );

  const taskCalls = [];
  const taskTimers = new Map();
  let nextTaskTimer = 1;
  let taskNow = 1_000;
  const taskState = { route: "tasks", data: {} };
  const renderTasks = installTasks(
    new Proxy(
      {
        state: taskState,
        actions: [],
        api: async (path) => {
          taskCalls.push(path);
          if (path === "/agents" || path.startsWith("/tasks?")) return [];
          if (path === "/settings") return { task_poll_interval_ms: 600 };
          assert.fail(`unexpected task polling API path ${path}`);
        },
        shell: noop,
        setTimer: (callback) => {
          const id = nextTaskTimer++;
          taskTimers.set(id, callback);
          return id;
        },
        clearTimer: (id) => taskTimers.delete(id),
        now: () => taskNow,
      },
      { get: (target, key) => target[key] ?? noop },
    ),
  );
  await renderTasks({ settings: { task_poll_interval_ms: 600 } });
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    0,
    "initial task render reuses bootstrap settings",
  );
  assert.equal(taskTimers.size, 1, "task page schedules one polling timer");
  const poll = [...taskTimers.values()][0];
  taskTimers.clear();
  await poll();
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    0,
    "background task polling does not refetch unchanged settings",
  );
  assert.equal(
    taskCalls.filter((path) => path.startsWith("/tasks?")).length,
    2,
    "each task refresh issues one task request",
  );
  assert.equal(
    taskCalls.filter((path) => path === "/agents").length,
    1,
    "background task polling reuses the cached node list",
  );
  assert.equal(taskTimers.size, 1, "task polling keeps a single timer");
  const expiredPoll = [...taskTimers.values()][0];
  taskTimers.clear();
  taskNow += 30_001;
  await expiredPoll();
  assert.equal(
    taskCalls.filter((path) => path === "/settings").length,
    1,
    "task polling refreshes cached settings after the bounded interval",
  );
  assert.equal(
    taskCalls.filter((path) => path === "/agents").length,
    2,
    "task polling refreshes the cached node list after the bounded interval",
  );
  assert.equal(taskTimers.size, 1, "expired settings refresh keeps one timer");
  taskTimers.clear();
} finally {
  if (routeDataDocument === undefined) delete globalThis.document;
  else globalThis.document = routeDataDocument;
}

}
