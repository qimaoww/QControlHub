// Theme identity is independent of the classic shell's light/dark mode.
// Keep routes explicit: a theme must own every renderer before being offered.
export const themes = Object.freeze([
  Object.freeze({ id: "classic", name: "经典", description: "QControlHub 原有界面", available: true }),
  Object.freeze({ id: "analytics", name: "简析", description: "以数据卡片和表格为主的独立界面", available: true }),
]);

export function themeByID(id) {
  return themes.find(theme => theme.id === id) || null;
}

export function effectiveTheme(id) {
  const theme = themeByID(id);
  return theme?.available ? theme.id : "classic";
}

// Anonymous presentation uses the public deployment default. Account choices
// are read only after authentication and never come from a business URL.
export function loginTheme(defaultID = "analytics") {
  return effectiveTheme(defaultID);
}

// This loader is also usable by independently built frontends. A full document
// navigation is the lifecycle boundary: no old timers, listeners, dialogs or
// global CSS survive a successful theme change. No account preference is cached.
export function createThemePreference({ api, isCurrent = () => true }) {
  let confirmed = null;
  let pending = null;
  let epoch = 0;
  let readSequence = 0;
  return {
    get current() { return confirmed; },
    reset() { epoch++; readSequence++; confirmed = null; pending = null; },
    async read() {
      if (pending) return pending;
      const requestEpoch = epoch;
      const sequence = ++readSequence;
      const value = await api("/preferences/theme");
      if (requestEpoch !== epoch || sequence !== readSequence || !isCurrent()) throw new DOMException("会话或请求已切换", "AbortError");
      confirmed = value;
      return value;
    },
    save(id) {
      if (pending) return Promise.reject(new Error("正在保存主题选择"));
      if (!themeByID(id)?.available) return Promise.reject(new Error("该主题暂不可用"));
      if (!confirmed) return Promise.reject(new Error("请先读取当前主题选择"));
      const requestEpoch = epoch;
      readSequence++;
      const revision = confirmed.revision;
      const request = Promise.resolve().then(() => api("/preferences/theme", {
        method: "PUT", body: JSON.stringify({ theme_id: id, revision }),
      })).then(value => {
        if (requestEpoch !== epoch || !isCurrent()) throw new DOMException("会话已切换", "AbortError");
        confirmed = value;
        return value;
      }).finally(() => { if (pending === request) pending = null; });
      pending = request;
      return request;
    },
  };
}
