import { disposeTheme } from "./modules/theme-lifecycle.js";
import { accountRequest } from "./modules/account-request.js";
import { bindSiteLogo, refreshSiteBranding } from "./analytics/site-brand.js";

const app = document.querySelector("#app");
const startup = app.querySelector(".qch-startup");
const startupMark = startup?.querySelector(".qch-startup-mark");
if (startupMark) bindSiteLogo(startupMark);
void refreshSiteBranding();
const startupStyle = document.querySelector("#qch-startup-style");
const startupObserver = new MutationObserver(() => {
  if (!startup?.isConnected) finishStartup();
});
const pauseStartup = () => startup?.toggleAttribute("data-paused", document.hidden);
function finishStartup(removeStyle = true) {
  startupObserver.disconnect();
  document.removeEventListener("visibilitychange", pauseStartup);
  if (removeStyle) startupStyle?.remove();
}
if (startup) {
  startupObserver.observe(app, { childList: true });
  document.addEventListener("visibilitychange", pauseStartup);
  pauseStartup();
}
const params = new URL(import.meta.url).searchParams;
const version = params.get("v");
const timed = () => AbortSignal.timeout(8000);
const asset = (path, key) => {
  const url = new URL(path, import.meta.url);
  const value = key ? params.get(key) : version;
  if (value) url.searchParams.set("v", value);
  return url.href;
};

function style(path, key) {
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = asset(path, key);
  const ready = new Promise((resolve, reject) => {
    link.onload = resolve;
    link.onerror = () => reject(new Error("样式加载失败"));
  });
  document.head.append(link);
  return { link, ready };
}

async function start() {
  const entryURL = new URL(location.href);
  if (entryURL.searchParams.has("theme")) {
    entryURL.searchParams.delete("theme");
    history.replaceState(null, "", entryURL);
  }
  // Load the one shipped runtime and its required styles while identity is
  // checked. Theme preference is a page setting, never a startup dependency.
  const sheet = style("./analytics/theme.css", "analytics");
  const runtimeReady = Promise.all([import(asset("./analytics/app.js")), sheet.ready]);
  void runtimeReady.catch(() => {});
  let session = null;
  try {
    session = await accountRequest("/auth/session", { signal: timed() });
  } catch (error) {
    if (error.status === 401) session = null;
  }

  try {
    const [runtime] = await runtimeReady;
    runtime.startAnalytics({ app, session });
  } catch (error) {
    disposeTheme();
    sheet.link.remove();
    if (startup) app.replaceChildren(startup);
    document.body.removeAttribute("class");
    throw error;
  }
}

start().catch(() => {
  finishStartup(false);
  if (startupStyle && !startupStyle.isConnected) document.head.append(startupStyle);
  const screen = startup || document.createElement("div");
  screen.className = "qch-startup";
  screen.removeAttribute("aria-busy");
  screen.setAttribute("data-failed", "");
  const content = screen.querySelector(".qch-startup-content") || screen;
  const message = screen.querySelector(".qch-startup-status") || document.createElement("p");
  message.className = "qch-startup-status";
  message.setAttribute("role", "alert");
  message.textContent = "控制台加载失败，请重试。";
  if (!screen.contains(message)) content.append(message);
  const retry = document.createElement("button");
  retry.type = "button";
  retry.className = "qch-startup-retry";
  retry.textContent = "重试";
  retry.onclick = () => location.reload();
  content.append(retry);
  app.replaceChildren(screen);
});
