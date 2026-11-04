import { disposeTheme } from "./modules/theme-lifecycle.js";
import { accountRequest } from "./modules/account-request.js";
import { effectiveTheme, loginTheme } from "./modules/theme-registry.js";

const app = document.querySelector("#app");
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

  if (!session) selected = loginTheme(location.search,
    document.querySelector('meta[name="qch-login-theme"]')?.content);

  if (selected === "analytics") {
    const sheet = style("./analytics/theme.css", "analytics");
    try {
      const [runtime] = await Promise.all([import(asset("./analytics/app.js")), sheet.ready]);
      runtime.startAnalytics({ app, session });
      return;
    } catch {
      disposeTheme();
      sheet.link.remove();
      app.replaceChildren();
      document.body.removeAttribute("class");
      app.dataset.themeRecovery = "analytics";
    }
  }
  const sheet = style("./app.css", "classic");
  await sheet.ready;
  await import(asset("./app.js"));
}

start().catch(() => {
  const message = document.createElement("p");
  message.setAttribute("role", "alert");
  message.textContent = "控制台资源加载失败。";
  const retry = document.createElement("button");
  retry.type = "button";
  retry.textContent = "重新加载";
  retry.onclick = () => location.reload();
  app.replaceChildren(message, retry);
});
