import { disposeTheme } from "./modules/theme-lifecycle.js";
import { accountRequest } from "./modules/account-request.js";
import { effectiveTheme, loginTheme } from "./modules/theme-registry.js";

const app = document.querySelector("#app");
const startup = app.querySelector(".qch-startup");
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
  let session = null;
  let selected = "classic";
  try {
    session = await accountRequest("/auth/session", { signal: timed() });
    if (session) {
      const preference = await accountRequest("/preferences/theme", { session, signal: timed() });
      selected = effectiveTheme(preference.theme_id);
    }
  } catch (error) {
    if (error.status === 401) session = null;
  }

  if (!session) selected = loginTheme(document.querySelector('meta[name="qch-login-theme"]')?.content);

  if (selected === "analytics") {
    const sheet = style("./analytics/theme.css", "analytics");
    try {
      const [runtime] = await Promise.all([import(asset("./analytics/app.js")), sheet.ready]);
      runtime.startAnalytics({ app, session });
      return;
    } catch {
      disposeTheme();
      sheet.link.remove();
      // Retain the public entry while the fallback theme loads.
      if (startup) app.replaceChildren(startup);
      document.body.removeAttribute("class");
      app.dataset.themeRecovery = "analytics";
    }
  }
  const sheet = style("./app.css", "classic");
  await sheet.ready;
  await import(asset("./app.js"));
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
