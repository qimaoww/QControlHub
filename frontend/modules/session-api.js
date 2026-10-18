import { configureNodeOrder, refreshNodeOrder } from "./node-order.js";
import { setStorageAccount } from "./account-storage.js";
import { createScopedAPI } from "./requests.js";
import { createPanelReads } from "./panel-reads.js";
import { requestJSON } from "./errors.js";

function combineAbortSignals(...values) {
  const signals = [...new Set(values.filter(Boolean))];
  if (signals.length < 2)
    return { signal: signals[0], release: () => {} };
  const controller = new AbortController();
  const abort = () => controller.abort();
  signals.forEach((signal) => {
    if (signal.aborted) abort();
    else signal.addEventListener("abort", abort, { once: true });
  });
  return {
    signal: controller.signal,
    release: () =>
      signals.forEach((signal) => signal.removeEventListener("abort", abort)),
  };
}

// Own the transport and render-local read scope, not route or DOM lifecycle.
// The login callback runs only after a request; construction is inert.
export function createSessionAPI({ state, renderLogin }) {
const scopedAPI = createScopedAPI(sendAPI);
configureNodeOrder(sendAPI);
const api = (path, options) => scopedAPI.request(path, options);

// The shell renders on every navigation, but its overview counters and panel
// settings change slowly. Refetching both for every route costs two round trips
// where one read already takes most of a second, so the account keeps a short
// cache instead. Pages that display a value live still read through it.
const panelReadPaths = { overview: "/overview", settings: "/settings" };
const panelReads = createPanelReads((key) => api(panelReadPaths[key]));

function readPanelData(key, { live = false } = {}) {
  return panelReads.get(key, {
    scope: state.session?.csrf_token || "",
    live,
  });
}

function invalidatePanelReads(...keys) {
  panelReads.invalidate(...keys);
}

async function sendAPI(path, options = {}) {
  const session = state.session;
  const method = String(options.method || "GET").toUpperCase();
  const headers = {
    Accept: "application/json",
    ...(options.body ? { "Content-Type": "application/json" } : {}),
    ...(options.headers || {}),
  };
  // Bind reads as well as writes to the page's session. A different tab can
  // replace the shared cookie without changing this page's in-memory account.
  if (session?.csrf_token)
    headers["X-QControlHub-CSRF"] = session.csrf_token;
  // Account ordering is shared across routes. A navigation must not cancel a
  // background read that the next page is about to reuse, or a save readback.
  const routeSignal = path !== "/preferences/node-order" && ["GET", "HEAD", "OPTIONS"].includes(method)
    ? state.routeSignal
    : null;
  const request = combineAbortSignals(options.signal, routeSignal);
  try {
    // Roster reads must keep their independent permission/removal lifecycle.
    // Navigation already loads order before rendering; polls refresh it in
    // parallel without holding up revocation when another page read fails.
    if (path === "/agents" && method === "GET" && session) void refreshNodeOrder()?.catch(() => {});
    if (path === "/client-access" && method === "GET" && session) await refreshNodeOrder();
    const result = await requestJSON(`/api/v1${path}`, {
      ...options,
      signal: request.signal,
      headers,
      credentials: "same-origin",
    }, {
      isLogin: path === "/auth/login",
      isCurrent: () => state.session === session,
      onUnauthorized(message) {
        state.session = null;
        setStorageAccount(null);
        state.data = {};
        renderLogin(message);
      },
    });
    return result;
  } finally {
    request.release();
  }
}

async function optionalAPI(path) {
  try {
    return await api(path);
  } catch (error) {
    if (error.status === 404) return null;
    throw error;
  }
}

async function ensureSession() {
  try {
    state.session = await api("/auth/session");
    setStorageAccount(state.session);
    return true;
  } catch {
    state.session = null;
    setStorageAccount(null);
    state.data = {};
    return false;
  }
}

  return { scopedAPI, api, optionalAPI, ensureSession, readPanelData, invalidatePanelReads };
}
