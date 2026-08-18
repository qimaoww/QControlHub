package webui

import "net/http"

const colorThemeScript = `
(() => {
  const storageKey = 'qcontrolhub-color-theme';
  const media = window.matchMedia('(prefers-color-scheme: light)');
  const storedTheme = () => {
    try {
      const value = window.localStorage.getItem(storageKey);
      return value === 'light' || value === 'dark' ? value : '';
    } catch (_) {
      return '';
    }
  };
  const systemTheme = () => media.matches ? 'light' : 'dark';
  const applyTheme = (theme) => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
  };
  const refreshControls = () => {
    const current = document.documentElement.dataset.theme;
    const nextLabel = current === 'light' ? '切换为深色主题' : '切换为浅色主题';
    document.querySelectorAll('[data-theme-toggle]').forEach((button) => {
      button.setAttribute('aria-label', nextLabel);
      button.setAttribute('title', nextLabel);
      const icon = button.querySelector('[data-theme-icon]');
      if (icon) icon.textContent = current === 'light' ? '☾' : '☀';
    });
  };

  applyTheme(storedTheme() || systemTheme());
  document.addEventListener('DOMContentLoaded', () => {
    refreshControls();
    document.querySelectorAll('[data-theme-toggle]').forEach((button) => {
      button.addEventListener('click', () => {
        const next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
        try { window.localStorage.setItem(storageKey, next); } catch (_) {}
        applyTheme(next);
        refreshControls();
      });
    });
  });
  media.addEventListener('change', () => {
    if (!storedTheme()) {
      applyTheme(systemTheme());
      refreshControls();
    }
  });
})();
`

func (s *Server) themeScript(w http.ResponseWriter, r *http.Request) {
	s.serveAsset(w, r, "text/javascript; charset=utf-8", s.themeJSAsset)
}
