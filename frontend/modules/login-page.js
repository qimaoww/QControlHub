import { setStorageAccount } from "./account-storage.js";

// Session/route cleanup stays at the application boundary. A failed login
// re-enters that boundary before displaying the error.
export function createLoginPage({
  app, state, esc, api, applyUIFontScale, applyTheme, toggleTheme, render, renderLogin,
}) {
  return function renderLoginPage(message = "") {
  document.body.className = "login-body";
  document.title = "登录 · QControlHub";
  app.style.display = "contents";
  app.innerHTML = `<button class="theme-toggle login-theme-toggle" type="button" data-theme-toggle aria-label="切换颜色主题"><span data-theme-icon aria-hidden="true">☀</span></button><main class="login-shell compact-login"><section class="login-card"><a class="brand login-card-brand" href="#dashboard"><span class="brand-mark large">QH</span><strong>QControlHub</strong></a><div class="login-card-head"><h1>登录</h1></div>${message ? `<div class="alert error">${esc(message)}</div>` : ""}<form id="login-form" class="stack-form"><label>用户名<input name="username" type="text" autocomplete="username" autofocus required maxlength="64" pattern="[A-Za-z0-9._\\-]+"></label><label>密码 / 管理令牌<input name="token" type="password" autocomplete="current-password" required minlength="12"></label><button class="button primary" type="submit">登录</button></form></section></main>`;
  applyUIFontScale(100);
  applyTheme();
  document.querySelector("[data-theme-toggle]").onclick = toggleTheme;
  document
    .querySelector("#login-form")
    .addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      const token = form.get("token");
      const button = event.currentTarget.querySelector("button");
      button.disabled = true;
      try {
        const session = await api("/auth/login", {
          method: "POST",
          body: JSON.stringify({ username: form.get("username"), token }),
        });
        state.data = {};
        state.session = session;
        setStorageAccount(session);
        location.hash = "#dashboard";
        await render();
      } catch (error) {
        renderLogin(error.message);
      }
    });
}
}
